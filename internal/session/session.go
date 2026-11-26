// Package session reads the existing Loop server and browser-session configuration.
package session

import (
	"bufio"
	"context"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func configured() (server, command string) {
	config, err := os.UserConfigDir()
	if err != nil {
		return "", ""
	}

	file, err := os.Open(filepath.Join(config, "matterhorn", "config.ini"))
	if err != nil {
		return "", ""
	}

	defer func() { _ = file.Close() }()

	section := ""

	scan := bufio.NewScanner(file)
	for scan.Scan() {
		line := strings.TrimSpace(scan.Text())
		if strings.HasPrefix(line, "[") {
			section = line
			continue
		}

		if section != "[mattermost]" {
			continue
		}

		separator := strings.IndexAny(line, "=:")
		if separator < 0 {
			continue
		}

		key, value := line[:separator], line[separator+1:]
		switch strings.TrimSpace(key) {
		case "host":
			server = "https://" + strings.TrimSpace(value)
		case "tokencmd":
			command = strings.TrimSpace(value)
		}
	}

	return server, command
}

// Server validates an explicit URL, environment URL, or existing SSO host.
func Server(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		raw = os.Getenv("LOOP_SERVER")
	}

	if strings.TrimSpace(raw) == "" {
		raw, _ = configured()
	}

	raw = strings.TrimRight(strings.TrimSpace(raw), "/")

	address, err := url.Parse(raw)
	if err != nil || address.Scheme != "https" || address.Hostname() == "" || address.User != nil || address.RawQuery != "" || address.Fragment != "" || address.ForceQuery {
		return "", errors.New("set LOOP_SERVER or --server to an HTTPS URL without credentials, query or fragment")
	}

	return address.String(), nil
}

// Token reuses an existing local SSO command only for its configured server.
func Token(ctx context.Context, server string, explicit bool) (string, error) {
	if token := strings.TrimSpace(os.Getenv("LOOP_TOKEN")); token != "" {
		return token, nil
	}

	configuredServer, command := configured()
	if explicit || command == "" || strings.TrimRight(configuredServer, "/") != server {
		return "", nil
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// tokencmd is the user's existing trusted local command, not server-provided data.
	token, err := exec.CommandContext(ctx, "sh", "-c", command).Output()
	if err != nil {
		return "", errors.New("loop browser session is unavailable; run loop --login and sign in again")
	}

	return strings.TrimSpace(string(token)), nil
}
