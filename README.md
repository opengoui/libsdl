# libsdl

`github.com/opengoui/libsdl` 把 **SDL3 官方发行版的共享库**以 gzip 压缩后用 `go:embed` 嵌入 Go 模块，
按 `GOOS/GOARCH` 选择，首次使用时解压到用户缓存目录，返回路径供 purego 系绑定
（例如同级模块 [puresdl](../puresdl)）加载。无需 cgo，无需用户预装 SDL3。

当前固定版本：**SDL 3.4.18**（上游标签 `release-3.4.18`），见 `libsdl.Version` 与 `assets/MANIFEST.json`。

## 使用

```go
import (
    _ "github.com/opengoui/libsdl/auto" // 必须排在 puresdl 之前
    "github.com/opengoui/puresdl/sdl"
)
```

puresdl 在 `import` 时就会自动调用 `Load("")`（搜索 `SDL3_LIBRARY` 与系统默认路径），之后的 `sdl.Load(path)` 是空操作，
并且 `SDL3_LIBRARY` 加载失败时会静默回退到系统库（例如 Homebrew）。因此**不能**在 `main` 里先 `libsdl.Path()` 再 `sdl.Load(path)`：
那样实际映射的仍是系统库。`libsdl/auto` 在自己的 `init` 中把 `SDL3_LIBRARY` 设为 `libsdl.Path()` 的结果（已设置则不覆盖），
所以必须先于 puresdl 被导入；也可以在进程启动前自行设置该环境变量。`testdata/probe` 通过 `/proc/self/maps` 或
`_dyld_get_image_name` 验证实际映射的文件。只需要路径时仍可直接调用 `libsdl.Path()`
（不受支持的平台上 `errors.Is(err, errors.ErrUnsupported)`）。

`libsdl.Info()` 不做 I/O，返回当前平台库的名称、大小与 SHA-256。

### 解压策略

- 目标：`os.UserCacheDir()/libsdl/<版本>-<sha256 前 16 位>/<库名>`，库名为 `libSDL3.so.0`、`libSDL3.dylib`、`SDL3.dll`。
- 已存在的文件只有在**属主为当前用户、其他用户不可写，且大小与 SHA-256 都匹配**时才复用（目录同样必须私有，否则尝试 chmod 0700；哈希校验不防同用户的恶意进程），否则替换；先写临时文件再 `rename`，并发进程不会看到半成品，
  也不会覆盖已被他人加载的库（Windows 上很重要）。
- 缓存目录不可用时回退到固定的 `os.TempDir()/libsdl-<uid>-<版本>-<哈希前缀>`（跨运行复用，不会自动删除）。
- 内嵌数据与记录的哈希不符时直接报错，不回退。

## 平台与来源

| 平台 | 来源 | 说明 |
|---|---|---|
| windows/amd64、windows/arm64 | **官方发行版** `SDL3-<ver>-win32-{x64,arm64}.zip` 中的 `SDL3.dll` | 按 GitHub 公布的 asset digest 校验 zip；PE machine 校验；上游 DLL 无 Authenticode 签名；只依赖系统 DLL |
| darwin/amd64、darwin/arm64 | **官方发行版** `SDL3-<ver>.dmg`（`hdiutil` 挂载，`lipo -thin` 取出 xcframework 中的 macOS 切片，`codesign --force -s -` 重新 ad-hoc 签名） | 最低系统：arm64 为 macOS 11.0，x86_64 为 10.13（Go 本身要求 macOS 12+）；ad-hoc 签名对 `dlopen` 足够，arm64 切片**必须保留签名**，否则内核拒绝加载 |
| linux/amd64、linux/arm64 | **自行用 zig 从源码构建**（官方不发布 Linux 二进制） | glibc 下限 **2.17**；SONAME `libSDL3.so.0`；仅 NEEDED libc/libm/libdl/libpthread，所有后端（X11、Wayland、KMSDRM、libdecor、ALSA、PulseAudio、PipeWire、JACK、sndio、udev、dbus 等）运行时 `dlopen`；已 strip，无 RPATH |

其它平台（`linux/386`、`js/wasm`、`android`、`ios`、`freebsd` 等）能编译，但 `Path()` 返回 `errors.ErrUnsupported`。

Linux 构建细节见 `tools/linux/build.sh`：zig 0.17（`-target <arch>-linux-gnu.2.17`）作交叉编译器；
头文件与 `.pc` 来自按 digest 固定的 `debian:trixie-slim` 镜像在 docker 中安装的 `-dev` 包（可选 `DEBIAN_SNAPSHOT` 冻结 apt）；
`wayland-scanner` 与 GNU strip 也在该镜像内运行；源码取自 `archive/refs/tags/<tag>.tar.gz`，其 sha256 写入 `MANIFEST.json`。
同一输入两次构建的结果逐位一致。库的 `SDL_GetRevision()` 为 `SDL-release-<ver> (libsdl zig build)`，可据此与官方构建区分。
未验证项：真实 X11/Wayland 会话、音频设备（仅用 dummy 驱动与驱动列表发现测试）。

## 文件布局

```
libsdl.go                 文档、Version 常量、Library/Info/Path
extract.go                解压与校验（原子写入、并发安全）
binary_<os>_<arch>.go     6 个，由 tools/update 生成，go:embed 各自的 assets/sdl_<os>_<arch>.gz
binary_unsupported.go     其它平台的占位
assets/sdl_*.gz           6 个压缩库
assets/MANIFEST.json      每个库的来源、SDL 版本、原始/压缩 sha256 与大小（不含日期，可复现）
LICENSE-SDL               SDL 的 zlib 许可证（取自发行版）
tools/update              更新程序（Go）
tools/linux/build.sh      Linux 库的 zig 构建脚本
internal/manifest         清单读写与校验（tools/update 与测试共用）
testdata/probe            集成测试用的探针程序（依赖 puresdl，不属于本模块构建）
```

## 更新到新版本

前置条件：

- 完整更新需要 **macOS 主机**（`hdiutil`、`lipo`、`codesign`）、`zig` 0.17、`cmake`、`ninja`、`docker`、`curl`/`tar`、Go。
- 仅 Windows 部分只需 Go 与网络；`GITHUB_TOKEN`（可选）用于提高 API 限额。

```sh
go run ./tools/update -latest                    # 固定到最新正式发行版（全部 6 个库）
go run ./tools/update -version release-3.4.18    # 或指定版本（"3.4.18" 亦可）
go run ./tools/update -newer                     # 若最新版本比当前新则打印标签（供 CI 使用）
go run ./tools/update -check                     # 离线校验 assets 与 MANIFEST.json
```

可用 `-os windows,darwin,linux` 只更新部分平台（CI 里 macOS 与 Linux 分开 job 运行），
`-linux-dir <目录>` 直接使用已有的 `SDL3_linux_{amd64,arm64}.so` 与 `build-report.txt` 而不重新构建。
部分更新会留下版本混合的清单，`-check` 会报错，直到所有平台都更新。
更新程序会重写 `assets/*.gz`、`assets/MANIFEST.json`、`binary_*.go`、`libsdl.go` 里的 `Version` 与 `LICENSE-SDL`。
更新后跑 `go test ./... -count=1` 并提交。

`.github/workflows/update.yml` 每周（及手动）检查新版本：macOS job 提取 Windows/macOS，Linux job 用 zig 构建，
最后一个 job 合并并通过 PR 提交。

## 验证

```sh
go run ./tools/update -check      # 离线：gzip/原始哈希、可执行头（ELF/Mach-O/PE 与架构）、生成文件与 Version 同步
go test ./... -count=1            # 单元测试，包含上面的校验
# 集成测试：用 puresdl 真正加载内嵌库并比较 SDL_GetVersion 与 libsdl.Version
LIBSDL_PURESDL_DIR=/path/to/puresdl go test . -run TestLoad -v
# 额外：在 docker 中运行 linux/amd64 与 linux/arm64，或在 Apple Silicon 上经 Rosetta 运行 darwin/amd64
LIBSDL_DOCKER=1 LIBSDL_ROSETTA=1 LIBSDL_PURESDL_DIR=/path/to/puresdl go test . -run TestLoad -v
```

## 已知限制

- Windows 库只做了静态验证（PE 头、digest），本仓库的作者环境无法运行；CI 的 windows 矩阵会运行集成测试。
- macOS 重新签名的结果取决于 `codesign` 版本，重新生成 darwin 资源可能得到不同哈希；以 `MANIFEST.json` 为准。
- Go 的 gzip 压缩率略低于 `gzip -9`（约 3% 至 5%），换来可复现且无外部依赖。
- 仓库每次升级会新增约 7 MB 二进制到 git 历史。

## 许可证

本模块代码见仓库许可；内嵌的 SDL 库遵循 zlib 许可证，见 `LICENSE-SDL`。
