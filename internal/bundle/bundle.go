// Package bundle installs the verified loop and loopd release as a pair.
package bundle

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/danilrwx/telega/updater"
)

const maxDownload = 128 << 20

func download(ctx context.Context, url string, limit int64) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	request.Header.Set("User-Agent", "terminal-client-updater")

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}

	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download failed: HTTP %d", response.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err == nil && int64(len(data)) > limit {
		err = errors.New("release download exceeds the size limit")
	}

	return data, err
}

func archiveName(name, system, arch string) (string, error) {
	if arch == "arm" {
		arch = "armv7"
	}

	switch system + "/" + arch {
	case "linux/amd64", "linux/arm64", "linux/armv7", "openbsd/amd64", "openbsd/arm64", "openbsd/armv7", "darwin/amd64", "darwin/arm64":
		return name + "_" + system + "_" + arch + ".tar.gz", nil
	default:
		return "", errors.New("no release binary is available for this platform")
	}
}

// Install validates both executables before replacing either installed binary.
func Install(ctx context.Context, release updater.Release, target string) error {
	if !updater.IsRelease(release.Version) {
		return errors.New("invalid release version")
	}

	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	archive, err := archiveName("loop-tui", runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}

	base := "https://github.com/danilrwx/loop-tui/releases/download/" + release.Version + "/"

	sums, err := download(ctx, base+"SHA256SUMS", 64<<10)
	if err != nil {
		return err
	}

	var expected []byte

	for line := range strings.SplitSeq(string(sums), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == archive {
			if expected != nil {
				return errors.New("duplicate archive checksum")
			}

			expected, err = hex.DecodeString(fields[0])
			if err != nil || len(expected) != sha256.Size {
				return errors.New("invalid archive checksum")
			}
		}
	}

	if expected == nil {
		return errors.New("release checksum is missing")
	}

	data, err := download(ctx, base+archive, maxDownload)
	if err != nil {
		return err
	}

	actual := sha256.Sum256(data)
	if !bytes.Equal(actual[:], expected) {
		return errors.New("release checksum verification failed")
	}

	return installArchive(ctx, data, release.Version, target)
}

func installArchive(ctx context.Context, data []byte, version, target string) error {
	target, err := filepath.EvalSymlinks(target)
	if err != nil {
		return err
	}

	dir := filepath.Dir(target)

	type replacement struct{ path, staged, backup string }

	var files []replacement
	defer func() {
		for _, file := range files {
			_ = os.Remove(file.staged)
			if file.backup != "" {
				_ = os.Remove(file.backup)
			}
		}
	}()

	for _, name := range []string{"loop", "loopd"} {
		path := filepath.Join(dir, name)
		if name == "loop" {
			path = target
		}

		staged, err := os.CreateTemp(dir, "."+name+"-update-*")
		if err != nil {
			return err
		}

		files = append(files, replacement{path: path, staged: staged.Name()})
		if err = extractBinary(staged, data, name); err != nil {
			_ = staged.Close()
			return err
		}

		if err = errors.Join(staged.Chmod(0o755), staged.Sync(), staged.Close()); err != nil {
			return err
		}

		output, err := exec.CommandContext(ctx, staged.Name(), "--version").Output()
		if err != nil || strings.TrimSpace(string(output)) != name+" "+version {
			return fmt.Errorf("downloaded %s failed its version check", name)
		}

		if _, err := os.Lstat(path); err == nil {
			backup, err := os.CreateTemp(dir, "."+name+"-previous-*")
			if err != nil {
				return err
			}

			backupPath := backup.Name()
			_ = backup.Close()

			_ = os.Remove(backupPath)
			if err := os.Link(path, backupPath); err != nil {
				return err
			}

			files[len(files)-1].backup = backupPath
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}

	for i, file := range files {
		if err := os.Rename(file.staged, file.path); err != nil {
			var rollback error

			for _, old := range files[:i] {
				if old.backup != "" {
					rollback = errors.Join(rollback, os.Rename(old.backup, old.path))
				} else {
					rollback = errors.Join(rollback, os.Remove(old.path))
				}
			}

			return errors.Join(err, rollback)
		}
	}

	return nil
}

func extractBinary(out io.Writer, data []byte, name string) error {
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer func() { _ = reader.Close() }()

	archive := tar.NewReader(reader)
	found := false

	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return err
		}

		if header.Name != name {
			continue
		}

		if found || header.Typeflag != tar.TypeReg || header.Size < 1 || header.Size > maxDownload {
			return errors.New("invalid release binary")
		}

		if _, err := io.Copy(out, archive); err != nil {
			return err
		}

		found = true
	}

	if !found {
		return errors.New("release binary is missing from the archive")
	}

	return nil
}
