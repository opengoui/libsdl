# libsdl

`github.com/opengoui/libsdl` 把 **SDL3 官方发行版的共享库**以 gzip 压缩后用 `go:embed` 嵌入 Go 模块，
按 `GOOS/GOARCH` 选择，需要时**释放到磁盘**（自定义目录或系统临时目录），返回路径供 purego 系绑定
（例如同级模块 [puresdl](../puresdl)）加载。无需 cgo，无需用户预装 SDL3。根模块没有任何依赖。

当前固定版本：**SDL 3.4.18**（上游标签 `release-3.4.18`），见 `libsdl.Version` 与 `assets/MANIFEST.json`。

## 使用：三种方式

### 1. `load.Load`（推荐）

嵌套模块 `github.com/opengoui/libsdl/load`（位于 `load/`，有自己的 `go.mod`，依赖 libsdl 与 puresdl）：

```go
import "github.com/opengoui/libsdl/load"

path, err := load.Load("/opt/myapp/lib") // 自定义目录
path, err := load.Load()                 // 不带参数：LIBSDL_DIR，否则系统临时目录
```

`Load` 先 `libsdl.Extract(dir)` 释放库，再调用 puresdl 的 `sdl.Load(path)`；如果之后 `sdl.LoadedPath()` 不等于该路径
（实际绑定的不是刚释放的库）就返回错误。puresdl 在 `import` 时可能已经加载了系统库（如 Homebrew），
`sdl.Load(path)` 会切换到新路径并重新绑定全部函数，所以**不依赖包初始化顺序**；必须在使用 SDL 之前调用。
macOS 上若已经映射了另一份 SDL3，切换后两份镜像同时存在，Objective-C 运行时会打印
`Class ... is implemented in both ...` 警告；想避免就用方式 2，或在进程启动前设置 `SDL3_LIBRARY`。
Windows 上同名 `SDL3.dll` 若已加载，系统加载器可能直接返回已加载的模块，应让 `Load` 先于其它加载。

`load/go.mod` 里的 `replace github.com/opengoui/libsdl => ../` 与 `replace github.com/opengoui/puresdl => ../../puresdl`
只是**开发期**写法（三个仓库并排检出）；发布时应删除并改为引用带标签的版本。

### 2. `libsdl/auto`（仅靠环境变量）

```go
import (
    _ "github.com/opengoui/libsdl/auto" // 必须在 puresdl 之前初始化
    "github.com/opengoui/puresdl/sdl"
)
```

`auto` 在自己的 `init` 中把 `SDL3_LIBRARY` 设为 `libsdl.Path()`（已设置则不覆盖；`Path()` 遵循 `LIBSDL_DIR`），
puresdl 的 `init` 随后就直接加载内嵌库。**初始化顺序警告**：Go 按导入路径的字典序初始化互相无依赖的包，
`github.com/opengoui/libsdl/auto` 恰好排在 `github.com/opengoui/puresdl/sdl` 之前，这是巧合而非保证；
改了导入路径（例如 vendor、replace 到别的路径）就可能失效，此时 puresdl 会静默回退到系统库。
需要确定性时用方式 1。释放失败时 `auto` 不改环境，puresdl 回退系统库。

### 3. 手动 `Extract` + `sdl.Load`

```go
path, err := libsdl.Extract("/opt/myapp/lib") // dir == "" 用 LIBSDL_DIR 或临时目录；等价于 libsdl.Path()
if err != nil { return err }
err = sdl.Load(path)                          // 需要支持切换的 puresdl（见下）
```

不受支持的平台上 `errors.Is(err, errors.ErrUnsupported)`。`libsdl.Info()` 不做 I/O，返回当前平台库的名称、大小与 SHA-256。

### 释放策略

API：`Extract(dir string) (string, error)`（并发安全，每个目录记忆一次成功结果，错误不记忆）、
`Path()`（等价于 `Extract("")`）、`Info()`、`Version`、`Library`、常量 `DirEnv = "LIBSDL_DIR"`。
库名为 `libSDL3.so.0`、`libSDL3.dylib`、`SDL3.dll`。

**自定义目录**（`Extract(dir)` 的参数，或 `dir == ""` 时的环境变量 `LIBSDL_DIR`；显式参数优先）：

- 目录不存在则 `MkdirAll(0755)`；无法创建或不可写直接报错，**不会回退到别处**。相对路径会转为绝对路径。
- 目标 `<dir>/<库名>` 是**普通文件**且大小与 SHA-256 都等于内嵌库时原样复用（不改 inode 与 mtime）；
  缺失、截断、内容不同、符号链接等都会重写：先在同目录写临时文件（0755），再 `rename` 覆盖，因此读者不会看到半成品，
  符号链接被替换为普通文件而**不会写穿**到链接目标；目标是目录则报错。出错时不留临时文件，
  同目录里属于本库的、超过 1 小时的残留 `*.tmp` 会被清理，其它文件不动。
- **不做属主/权限检查**：用户选择的目录（例如可执行文件旁的共享安装目录）完全合法。代价是哈希校验只防损坏，
  不防能写该目录的恶意进程，请选择只有自己能写的目录。
- Windows：已加载的 DLL 无法被替换。若文件内容不同且正被使用，`rename` 失败，返回明确说明"可能被运行中的进程加载"的错误
  （不会等待、不会删除）；内容一致则照常复用，即使 DLL 正被本进程或其它进程加载。

**默认目录**（不指定、也没有 `LIBSDL_DIR`）：

- `os.TempDir()/libsdl-<uid>-<版本>-<sha256 前 16 位>/<库名>`（Windows 上 `<uid>` 为 `user`，临时目录本身是每用户的）。
  名称是确定的，所以多次运行与并发进程复用同一份。
- 沿用严格检查：目录必须是真实目录（符号链接被拒绝）、属主为当前用户（权限过宽时 chmod 0700），已有文件必须是属主为当前用户、
  其他用户不可写的普通文件且大小与哈希匹配，否则替换；属于他人的目录会报错。已不再使用 `os.UserCacheDir`。
- 临时目录**不会被自动清理**；每个版本最多留下一个约几 MB 的目录。

内嵌数据与记录的哈希不符时直接报错。

### puresdl 的 `Load` 切换

`sdl.Load(path)`（非空路径）在已加载另一份库时会打开新路径、重新绑定全部函数并更新 `sdl.LoadedPath()`；相同路径是空操作，
路径无效则返回错误且旧库继续可用。切换不能与 SDL 调用并发。

## 平台与来源

| 平台 | 来源 | 说明 |
|---|---|---|
| windows/amd64、windows/arm64 | **官方发行版** `SDL3-<ver>-win32-{x64,arm64}.zip` 中的 `SDL3.dll` | 按 GitHub 公布的 asset digest 校验 zip；PE machine 校验；上游 DLL 无 Authenticode 签名；只依赖系统 DLL |
| darwin/amd64、darwin/arm64 | **官方发行版** `SDL3-<ver>.dmg`（`hdiutil` 挂载，`lipo -thin` 取出 xcframework 中的 macOS 切片，`codesign --force -s -` 重新 ad-hoc 签名） | 最低系统：arm64 为 macOS 11.0，x86_64 为 10.13（Go 本身要求 macOS 12+）；ad-hoc 签名对 `dlopen` 足够，arm64 切片**必须保留签名**，否则内核拒绝加载 |
| linux/amd64、linux/arm64 | **自行用 zig 从源码构建**（官方不发布 Linux 二进制） | glibc 下限 **2.17**；SONAME `libSDL3.so.0`；仅 NEEDED libc/libm/libdl/libpthread，所有后端（X11、Wayland、KMSDRM、libdecor、ALSA、PulseAudio、PipeWire、JACK、sndio、udev、dbus 等）运行时 `dlopen`；已 strip，无 RPATH |

其它平台（`linux/386`、`js/wasm`、`android`、`ios`、`freebsd` 等）能编译，但 `Extract()`/`Path()` 返回 `errors.ErrUnsupported`。

Linux 构建细节见 `tools/linux/build.sh`：zig 0.17（`-target <arch>-linux-gnu.2.17`）作交叉编译器；
头文件与 `.pc` 来自按 digest 固定的 `debian:trixie-slim` 镜像在 docker 中安装的 `-dev` 包（可选 `DEBIAN_SNAPSHOT` 冻结 apt）；
`wayland-scanner` 与 GNU strip 也在该镜像内运行；源码取自 `archive/refs/tags/<tag>.tar.gz`，其 sha256 写入 `MANIFEST.json`。
同一输入两次构建的结果逐位一致。库的 `SDL_GetRevision()` 为 `SDL-release-<ver> (libsdl zig build)`，可据此与官方构建区分。
未验证项：真实 X11/Wayland 会话、音频设备（仅用 dummy 驱动与驱动列表发现测试）。

## 文件布局

```
libsdl.go                 文档、Version 常量、Library/Info/Extract/Path
extract.go                释放与校验（原子写入、并发安全）
auto/                     init 时设置 SDL3_LIBRARY 的便捷包（无依赖）
load/                     嵌套模块：Load(dir...) = Extract + puresdl 的 sdl.Load
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
go test ./... -count=1            # 单元测试（根模块，不含 load/）
(cd load && CGO_ENABLED=0 go test ./... -count=1)   # 嵌套模块：真实加载并核对 sdl.LoadedPath 与版本
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
