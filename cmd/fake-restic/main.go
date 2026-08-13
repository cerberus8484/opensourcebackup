// fake-restic is a deterministic, test-only restic stand-in for local E2E gates.
// It implements only the commands exercised by the agent runner and must never be
// shipped with product artifacts.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

func main() {
	command := commandFromArgs(os.Args[1:])
	writeInvocation(command)
	if command != "backup" {
		return
	}
	if ms, err := strconv.Atoi(os.Getenv("FAKE_RESTIC_SLEEP_MS")); err == nil && ms > 0 {
		time.Sleep(time.Duration(ms) * time.Millisecond)
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
		"message_type":  "summary",
		"snapshot_id":   "fake-snapshot",
		"data_added":    42,
		"files_new":     1,
		"files_changed": 0,
	})
}

func commandFromArgs(args []string) string {
	for _, arg := range args {
		switch arg {
		case "init", "unlock", "backup", "check", "restore":
			return arg
		}
	}
	return "unknown"
}

func writeInvocation(command string) {
	path := os.Getenv("FAKE_RESTIC_LOG")
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	defer f.Close()
	_, _ = fmt.Fprintf(f, "%s %s\n", time.Now().UTC().Format(time.RFC3339Nano), command)
}
