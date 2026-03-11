// Package artifact provides a self-contained HTTP file-sharing server for cc-connect.
// It manages file access tokens with TTL and serves files with correct Content-Type headers.
package artifact

import (
	"context"
	"fmt"
	"net"
	"net/http"
)

const (
	DefaultPort       = 17174
	DefaultTTL        = 3600 // seconds
	defaultPathPrefix = "/a/"
)

// Config holds configuration for the artifact server.
type Config struct {
	BaseURL    string // Public base URL, e.g. "https://example.com". Falls back to http://localhost:{Port} if empty.
	Port       int    // HTTP listen port (default 17174)
	DefaultTTL int    // Default token TTL in seconds (default 3600)
}

// Server is a self-contained HTTP artifact file server.
type Server struct {
	cfg        Config
	store      *store
	httpServer *http.Server
}

// New creates a new Server. dataDir is the cc-connect data directory (~/.cc-connect).
func New(cfg Config, dataDir string) *Server {
	if cfg.Port == 0 {
		cfg.Port = DefaultPort
	}
	if cfg.DefaultTTL == 0 {
		cfg.DefaultTTL = DefaultTTL
	}

	s := &Server{
		cfg:   cfg,
		store: newStore(dataDir),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/a/", s.handler)

	s.httpServer = &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Port),
		Handler: mux,
	}

	return s
}

// Start launches the HTTP server in a background goroutine.
func (s *Server) Start() error {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", s.cfg.Port))
	if err != nil {
		return fmt.Errorf("artifact server: listen on port %d: %w", s.cfg.Port, err)
	}
	go func() {
		if err := s.httpServer.Serve(ln); err != nil && err != http.ErrServerClosed {
			_ = err
		}
	}()
	return nil
}

// Stop gracefully shuts down the HTTP server.
func (s *Server) Stop() {
	_ = s.httpServer.Shutdown(context.Background())
}

// Allow registers a file for sharing and returns its public URL.
// If ttl <= 0, the server's DefaultTTL is used.
func (s *Server) Allow(path string, ttl int) (string, error) {
	if ttl <= 0 {
		ttl = s.cfg.DefaultTTL
	}
	token, err := s.store.add(path, ttl)
	if err != nil {
		return "", fmt.Errorf("artifact: generate token: %w", err)
	}
	return s.publicURL(token), nil
}

// List returns all currently active (unexpired) entries.
func (s *Server) List() []Entry {
	return s.store.list()
}

// Revoke removes a token, making the file immediately inaccessible.
func (s *Server) Revoke(token string) {
	s.store.revoke(token)
}

// publicURL builds the public URL for a token.
func (s *Server) publicURL(token string) string {
	base := s.cfg.BaseURL
	if base == "" {
		base = fmt.Sprintf("http://localhost:%d", s.cfg.Port)
	}
	return base + "/a/" + token
}

// PublicURL returns the public URL for a given token.
func (s *Server) PublicURL(token string) string {
	return s.publicURL(token)
}
