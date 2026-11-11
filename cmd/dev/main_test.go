package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTasksAreWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, task := range tasks() {
		if task.name == "" || task.help == "" || task.run == nil {
			t.Errorf("task %q needs a name, help text and a run function", task.name)
		}
		if seen[task.name] {
			t.Errorf("task %q is listed twice", task.name)
		}
		seen[task.name] = true
	}
	for _, name := range []string{"up", "migrate", "seed", "db-check", "gen", "test", "lint", "check"} {
		if !seen[name] {
			t.Errorf("task %q is missing", name)
		}
	}
}

func TestLookup(t *testing.T) {
	tests := []struct {
		name    string
		task    string
		args    []string
		wantErr string
	}{
		{name: "known task", task: "up"},
		{name: "task that takes arguments", task: "logs", args: []string{"temporal"}},
		{name: "arguments to a task that takes none", task: "seed", args: []string{"extra"}, wantErr: "takes no arguments"},
		{name: "unknown task", task: "deploy", wantErr: "unknown task"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := lookup(tt.task, tt.args)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("lookup(%q) error = %v, want one containing %q", tt.task, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("lookup(%q) error = %v", tt.task, err)
			}
			if got.name != tt.task {
				t.Fatalf("lookup(%q) = %q", tt.task, got.name)
			}
		})
	}
}

func TestFindRoot(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"go.mod", "compose.yaml"} {
		if err := os.WriteFile(filepath.Join(root, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	deep := filepath.Join(root, "internal", "api")
	if err := os.MkdirAll(deep, 0o750); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{root, deep} {
		got, err := findRoot(dir)
		if err != nil {
			t.Fatalf("findRoot(%q) error = %v", dir, err)
		}
		if got != root {
			t.Fatalf("findRoot(%q) = %q, want %q", dir, got, root)
		}
	}
}

func TestReadLFConvertsWindowsLineEndings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "script.sh")
	if err := os.WriteFile(path, []byte("set -eu\r\necho ok\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := readLF(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if want := "set -eu\necho ok\n"; string(got) != want {
		t.Fatalf("readLF = %q, want %q", got, want)
	}
}

func TestEnsureEnvCopiesTheExampleOnce(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.WriteFile(".env.example", []byte("A=1\r\nB=2\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ensureEnv(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(".env")
	if err != nil {
		t.Fatal(err)
	}
	if want := "A=1\nB=2\n"; string(got) != want {
		t.Fatalf(".env = %q, want %q", got, want)
	}

	// A second run keeps the developer's own edits.
	if err := os.WriteFile(".env", []byte("A=changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ensureEnv(); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(".env"); string(got) != "A=changed\n" {
		t.Fatalf(".env was overwritten: %q", got)
	}
}
