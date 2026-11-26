package notify

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

func readSettings(name string, out any) error {
	config, err := os.UserConfigDir()
	if err != nil {
		return err
	}

	data, err := os.ReadFile(filepath.Join(config, name, "settings.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}

	if err != nil {
		return err
	}

	return json.Unmarshal(data, out)
}

func writeSettings(name string, value any) error {
	config, err := os.UserConfigDir()
	if err != nil {
		return err
	}

	dir := filepath.Join(config, name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	data, err := json.Marshal(value)
	if err != nil {
		return err
	}

	staged, err := os.CreateTemp(dir, ".notifications-*")
	if err != nil {
		return err
	}

	defer func() { _ = staged.Close(); _ = os.Remove(staged.Name()) }()

	if _, err = staged.Write(data); err != nil {
		return err
	}

	if err = errors.Join(staged.Sync(), staged.Close()); err != nil {
		return err
	}

	return os.Rename(staged.Name(), filepath.Join(dir, "settings.json"))
}
