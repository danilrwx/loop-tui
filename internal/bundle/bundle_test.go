package bundle

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/danilrwx/telega/updater"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func archiveFixture(t *testing.T, daemonVersion string, includeDaemon bool) []byte {
	t.Helper()

	var data bytes.Buffer

	gz := gzip.NewWriter(&data)
	archive := tar.NewWriter(gz)

	names := []string{"loop"}
	if includeDaemon {
		names = append(names, "loopd")
	}

	for _, name := range names {
		version := "v1.2.3"
		if name == "loopd" {
			version = daemonVersion
		}

		body := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' '%s %s'\n", name, version)
		if err := archive.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}

		if _, err := io.WriteString(archive, body); err != nil {
			t.Fatal(err)
		}
	}

	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}

	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}

	return data.Bytes()
}

func TestBundleValidatesBothFilesBeforeReplacingEither(t *testing.T) {
	for _, scenario := range []struct {
		name, daemonVersion                 string
		includeDaemon, badChecksum, success bool
	}{{"valid", "v1.2.3", true, false, true}, {"bad daemon version", "v0.0.0", true, false, false}, {"missing daemon", "v1.2.3", false, false, false}, {"bad checksum", "v1.2.3", true, true, false}} {
		t.Run(scenario.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, name := range []string{"loop", "loopd"} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte("old "+name), 0o755); err != nil {
					t.Fatal(err)
				}
			}

			data := archiveFixture(t, scenario.daemonVersion, scenario.includeDaemon)

			archive, err := archiveName("loop-tui", runtime.GOOS, runtime.GOARCH)
			if err != nil {
				t.Fatal(err)
			}

			sum := sha256.Sum256(data)
			if scenario.badChecksum {
				sum[0] ^= 255
			}

			sums := fmt.Sprintf("%x  %s\n", sum, archive)
			previous := http.DefaultClient

			t.Cleanup(func() { http.DefaultClient = previous })

			http.DefaultClient = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				body := data
				if strings.HasSuffix(r.URL.Path, "SHA256SUMS") {
					body = []byte(sums)
				}

				return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body)), Header: make(http.Header)}, nil
			})}

			err = Install(t.Context(), updater.Release{Version: "v1.2.3"}, filepath.Join(dir, "loop"))
			if (err == nil) != scenario.success {
				t.Fatalf("unexpected install result: %v", err)
			}

			for _, name := range []string{"loop", "loopd"} {
				body, err := os.ReadFile(filepath.Join(dir, name))
				if err != nil {
					t.Fatal(err)
				}

				if (string(body) == "old "+name) == scenario.success {
					t.Fatalf("partial bundle replacement: %s", name)
				}
			}
		})
	}
}
