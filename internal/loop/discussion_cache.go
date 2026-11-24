package loop

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type cachedDiscussion struct {
	followedThread
	Team   string
	Author string
	Own    bool
}

func (c *client) discussionCachePath() string {
	dir, err := os.UserCacheDir()
	if err != nil || c.me.ID == "" {
		return ""
	}

	key := sha256.Sum256([]byte(c.baseURL + "\x00" + c.me.ID))

	return filepath.Join(dir, "loop-tui", "discussions", fmt.Sprintf("%x.json", key))
}

func readDiscussionCache(path string) []followedThread {
	if path == "" {
		return nil
	}

	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return nil
	}

	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = file.Close() }()

	var saved []cachedDiscussion
	if err := json.NewDecoder(file).Decode(&saved); err != nil {
		return nil
	}

	items := make([]followedThread, 0, len(saved))
	for _, item := range saved {
		item.TeamID, item.Post.Author, item.Post.Own = item.Team, item.Author, item.Own
		items = append(items, item.followedThread)
	}

	return items
}

func saveDiscussionCache(path string, items []followedThread) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}

	file, err := os.CreateTemp(dir, ".discussions-*")
	if err != nil {
		return
	}
	defer func() { _ = os.Remove(file.Name()) }()

	saved := make([]cachedDiscussion, 0, len(items))
	for _, item := range items {
		saved = append(saved, cachedDiscussion{item, item.TeamID, item.Post.Author, item.Post.Own})
	}

	encodeErr := json.NewEncoder(file).Encode(saved)

	closeErr := file.Close()
	if encodeErr == nil && closeErr == nil {
		_ = os.Rename(file.Name(), path)
	}
}
