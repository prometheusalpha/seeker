// Package testenv prepares what tests of the app need from outside Go.
package testenv

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

const sqliteEnv = "MYGO_NATIVE_SQLITECORE"

var sqliteOnce = sync.OnceValue(prepareSQLite)

// SQLite makes the mygo sqlite plugin loadable under go test. With mygo
// replaced by the ../mygo fork, the module is local, so the runtime wants
// a build from its Zig sources and ignores the published library, which
// the CLI still uses for mygo dev and mygo build. This points the plugin at
// that published library, downloaded into the cache the CLI shares.
//
// It fails rather than skips: a store test that quietly skips is a false
// green.
func SQLite(tb testing.TB) {
	tb.Helper()
	if err := sqliteOnce(); err != nil {
		tb.Fatalf("sqlite plugin: %v", err)
	}
}

func prepareSQLite() error {
	if os.Getenv(sqliteEnv) != "" {
		return nil
	}
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/egoist/mygo").Output()
	if err != nil {
		return fmt.Errorf("locating mygo: %w", err)
	}
	manifest := filepath.Join(strings.TrimSpace(string(out)), "plugins/sqlite/internal/sqlitecore/mygo-plugin.json")
	data, err := os.ReadFile(manifest)
	if err != nil {
		return err
	}
	var m struct {
		Libraries []struct {
			Files map[string]struct {
				Name, URL, SHA256 string
			}
		}
	}
	if err := json.Unmarshal(data, &m); err != nil || len(m.Libraries) != 1 {
		return fmt.Errorf("reading %s: %v", manifest, err)
	}
	f, ok := m.Libraries[0].Files[runtime.GOOS+"-"+runtime.GOARCH]
	if !ok {
		return fmt.Errorf("no published sqlite library for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return err
	}
	// The layout of mygo's internal/nativelib.CachePath.
	path := filepath.Join(cache, "mygo", "natives", f.SHA256, f.Name)
	if sum, _ := fileSHA256(path); sum != f.SHA256 {
		if err := download(f.URL, path, f.SHA256); err != nil {
			return err
		}
	}
	return os.Setenv(sqliteEnv, path)
}

func download(url, path, want string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("downloading %s: %s", url, resp.Status)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "download-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(tmp, h), resp.Body)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return fmt.Errorf("downloading %s: sha256 %s, want %s", url, got, want)
	}
	return os.Rename(tmp.Name(), path)
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
