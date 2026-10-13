package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/opengoui/libsdl/internal/manifest"
)

// Overridable for tests.
var (
	apiBase      = "https://api.github.com/repos/libsdl-org/SDL"
	downloadBase = "https://github.com/libsdl-org/SDL/releases/download"
	httpClient   = &http.Client{Timeout: 10 * time.Minute}
)

type asset struct {
	Name   string `json:"name"`
	Digest string `json:"digest"` // "sha256:<hex>"
}

type release struct {
	Tag    string  `json:"tag_name"`
	Assets []asset `json:"assets"`

	cache map[string][]byte // verified downloads by asset name
}

func get(url string) (io.ReadCloser, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "libsdl-update")
	if tok := os.Getenv("GITHUB_TOKEN"); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok) // dropped by net/http on cross-host redirects
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return resp.Body, nil
}

func getRelease(url string) (*release, error) {
	body, err := get(url)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	var r release
	if err := json.NewDecoder(body).Decode(&r); err != nil {
		return nil, fmt.Errorf("%s: %w", url, err)
	}
	return &r, nil
}

func latestTag() (string, error) {
	r, err := getRelease(apiBase + "/releases/latest")
	if err != nil {
		return "", err
	}
	if r.Tag == "" {
		return "", fmt.Errorf("latest release has no tag")
	}
	return r.Tag, nil
}

func fetchRelease(tag string) (*release, error) {
	r, err := getRelease(apiBase + "/releases/tags/" + tag)
	if err != nil {
		return nil, err
	}
	return r, nil
}

// download fetches a release asset into memory and verifies it against the
// sha256 digest GitHub publishes for it. It returns the data and its URL.
func (r *release) download(tag, name string) (data []byte, url string, sum string, err error) {
	url = downloadBase + "/" + tag + "/" + name
	if b, ok := r.cache[name]; ok {
		return b, url, manifest.SHA256Hex(b), nil
	}
	var want string
	for _, a := range r.Assets {
		if a.Name == name {
			want = strings.TrimPrefix(a.Digest, "sha256:")
		}
	}
	if len(want) != 64 {
		return nil, "", "", fmt.Errorf("release %s: no sha256 digest published for %s", tag, name)
	}
	body, err := get(url)
	if err != nil {
		return nil, "", "", err
	}
	defer body.Close()
	data, err = io.ReadAll(body)
	if err != nil {
		return nil, "", "", err
	}
	sum = manifest.SHA256Hex(data)
	if sum != want {
		return nil, "", "", fmt.Errorf("%s: sha256 %s, published digest %s", name, sum, want)
	}
	if r.cache == nil {
		r.cache = map[string][]byte{}
	}
	r.cache[name] = data
	return data, url, sum, nil
}

// newerVersion reports whether dotted-numeric a is greater than b.
func newerVersion(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			return x > y
		}
	}
	return false
}
