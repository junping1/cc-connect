package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"
)

func runArtifact(args []string) {
	if len(args) == 0 {
		printArtifactUsage()
		return
	}

	switch args[0] {
	case "allow":
		runArtifactAllow(args[1:])
	case "list":
		runArtifactList(args[1:])
	case "revoke":
		runArtifactRevoke(args[1:])
	case "--help", "-h", "help":
		printArtifactUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown artifact subcommand: %s\n", args[0])
		printArtifactUsage()
		os.Exit(1)
	}
}

func runArtifactAllow(args []string) {
	var dataDir string
	var ttl int
	var path string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--ttl", "-t":
			if i+1 < len(args) {
				i++
				n, err := strconv.Atoi(args[i])
				if err != nil {
					fmt.Fprintf(os.Stderr, "Error: invalid TTL %q\n", args[i])
					os.Exit(1)
				}
				ttl = n
			}
		case "--data-dir":
			if i+1 < len(args) {
				i++
				dataDir = args[i]
			}
		case "--help", "-h":
			printArtifactUsage()
			return
		default:
			if path == "" {
				path = args[i]
			}
		}
	}

	if path == "" {
		fmt.Fprintln(os.Stderr, "Error: file path is required")
		printArtifactUsage()
		os.Exit(1)
	}

	sockPath := resolveSocketPath(dataDir)
	if _, err := os.Stat(sockPath); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Error: cc-connect is not running (socket not found: %s)\n", sockPath)
		os.Exit(1)
	}

	payload, _ := json.Marshal(map[string]any{
		"path": path,
		"ttl":  ttl,
	})

	resp, err := artifactPost(sockPath, "/artifact/allow", payload)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "Error: %s\n", string(body))
		os.Exit(1)
	}

	var result map[string]string
	json.Unmarshal(body, &result)
	fmt.Println(result["url"])
}

func runArtifactList(args []string) {
	var dataDir string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--data-dir":
			if i+1 < len(args) {
				i++
				dataDir = args[i]
			}
		}
	}

	sockPath := resolveSocketPath(dataDir)
	if _, err := os.Stat(sockPath); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Error: cc-connect is not running (socket not found: %s)\n", sockPath)
		os.Exit(1)
	}

	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", sockPath)
			},
		},
	}

	resp, err := client.Get("http://unix/artifact/list")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "Error: %s\n", string(body))
		os.Exit(1)
	}

	var entries []struct {
		Token   string    `json:"token"`
		Path    string    `json:"path"`
		Expires time.Time `json:"expires"`
		URL     string    `json:"url"`
	}
	json.Unmarshal(body, &entries)

	if len(entries) == 0 {
		fmt.Println("No active artifacts.")
		return
	}

	fmt.Printf("Active artifacts (%d):\n\n", len(entries))
	for _, e := range entries {
		remaining := time.Until(e.Expires).Round(time.Second)
		fmt.Printf("  %s  %s  (expires in %s)\n", e.Token, e.URL, remaining)
		fmt.Printf("    path: %s\n", e.Path)
	}
}

func runArtifactRevoke(args []string) {
	var dataDir string
	var token string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--data-dir":
			if i+1 < len(args) {
				i++
				dataDir = args[i]
			}
		default:
			if token == "" {
				token = args[i]
			}
		}
	}

	if token == "" {
		fmt.Fprintln(os.Stderr, "Error: token is required")
		os.Exit(1)
	}

	sockPath := resolveSocketPath(dataDir)
	if _, err := os.Stat(sockPath); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Error: cc-connect is not running (socket not found: %s)\n", sockPath)
		os.Exit(1)
	}

	payload, _ := json.Marshal(map[string]string{"token": token})
	resp, err := artifactPost(sockPath, "/artifact/revoke", payload)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "Error: %s\n", string(body))
		os.Exit(1)
	}

	fmt.Printf("Artifact %s revoked.\n", token)
}

func artifactPost(sockPath, path string, payload []byte) (*http.Response, error) {
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", sockPath)
			},
		},
	}
	return client.Post("http://unix"+path, "application/json", bytes.NewReader(payload))
}

func printArtifactUsage() {
	fmt.Println(`Usage: cc-connect artifact <command> [options]

Commands:
  allow <path> [--ttl N]   Share a file and print its URL (default TTL: 3600s)
  list                      List active artifacts
  revoke <token>            Revoke an artifact token

Options:
  --ttl N          TTL in seconds (for allow)
  --data-dir path  Data directory (default: ~/.cc-connect)
  -h, --help       Show this help

Examples:
  cc-connect artifact allow /tmp/report.pdf --ttl 7200
  cc-connect artifact list
  cc-connect artifact revoke a1b2c3d4e5f6g7h8`)
}
