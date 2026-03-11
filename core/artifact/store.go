package artifact

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Entry represents a single shared file access token.
type Entry struct {
	Token   string    `json:"token"`
	Path    string    `json:"path"`
	Expires time.Time `json:"expires"`
	URL     string    `json:"url,omitempty"` // populated by Server.Allow; not persisted to disk
}

type store struct {
	mu      sync.RWMutex
	entries map[string]Entry
	dataDir string
}

func newStore(dataDir string) *store {
	s := &store{
		entries: make(map[string]Entry),
		dataDir: dataDir,
	}
	s.load()
	go s.cleanupLoop()
	return s
}

// add generates a crypto-random token, stores the entry, and persists.
func (s *store) add(path string, ttl int) (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b)

	entry := Entry{
		Token:   token,
		Path:    path,
		Expires: time.Now().Add(time.Duration(ttl) * time.Second),
	}

	s.mu.Lock()
	s.entries[token] = entry
	s.mu.Unlock()

	s.save()
	return token, nil
}

// get returns the entry only if it exists and is not expired.
func (s *store) get(token string) (Entry, bool) {
	s.mu.RLock()
	e, ok := s.entries[token]
	s.mu.RUnlock()
	if !ok {
		return Entry{}, false
	}
	if time.Now().After(e.Expires) {
		return Entry{}, false
	}
	return e, true
}

// revoke removes a token from the store and persists.
func (s *store) revoke(token string) {
	s.mu.Lock()
	delete(s.entries, token)
	s.mu.Unlock()
	s.save()
}

// list returns all unexpired entries.
func (s *store) list() []Entry {
	now := time.Now()
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []Entry
	for _, e := range s.entries {
		if now.Before(e.Expires) {
			result = append(result, e)
		}
	}
	return result
}

func (s *store) persistPath() string {
	return filepath.Join(s.dataDir, "artifacts.json")
}

func (s *store) save() {
	s.mu.RLock()
	entries := make([]Entry, 0, len(s.entries))
	for _, e := range s.entries {
		entries = append(entries, e)
	}
	s.mu.RUnlock()

	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		slog.Warn("artifact: failed to marshal store", "error", err)
		return
	}

	p := s.persistPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		slog.Warn("artifact: failed to create data dir", "error", err)
		return
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		slog.Warn("artifact: failed to save store", "error", err)
	}
}

func (s *store) load() {
	p := s.persistPath()
	data, err := os.ReadFile(p)
	if err != nil {
		// File may not exist yet; that's fine.
		return
	}
	var entries []Entry
	if err := json.Unmarshal(data, &entries); err != nil {
		slog.Warn("artifact: failed to parse store", "error", err)
		return
	}
	now := time.Now()
	s.mu.Lock()
	for _, e := range entries {
		if now.Before(e.Expires) {
			s.entries[e.Token] = e
		}
	}
	s.mu.Unlock()
}

// cleanupLoop removes expired entries every 5 minutes.
func (s *store) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		s.mu.Lock()
		changed := false
		for token, e := range s.entries {
			if now.After(e.Expires) {
				delete(s.entries, token)
				changed = true
			}
		}
		s.mu.Unlock()
		if changed {
			s.save()
		}
	}
}
