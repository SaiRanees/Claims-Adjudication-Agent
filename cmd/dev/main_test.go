package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/platform"
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

func writeEnv(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestMintTokenSignsForTheRoleAndSubject(t *testing.T) {
	//nolint:gosec // G101: a throwaway signing secret that exists only in this test.
	const secret = "dev-only-jwt-secret-for-the-test-0123456789"
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	envFile := writeEnv(t, "# local settings\r\nPOSTGRES_PASSWORD=x\r\nAUTH_JWT_SECRET="+secret+"\r\n")
	auth, err := platform.NewAuthenticator([]byte(secret), clock)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		args []string
		want platform.Principal
	}{
		{nil, platform.Principal{Subject: "dev-submitter", Role: platform.RoleSubmitter}},
		{[]string{"reviewer"}, platform.Principal{Subject: "dev-reviewer", Role: platform.RoleReviewer}},
		{[]string{"admin", "ops-1"}, platform.Principal{Subject: "ops-1", Role: platform.RoleAdmin}},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			var out strings.Builder
			if err := mintToken(envFile, tc.args, &out, clock); err != nil {
				t.Fatalf("mintToken: %v", err)
			}
			got, err := auth.Authenticate(strings.TrimSpace(out.String()))
			if err != nil || got != tc.want {
				t.Fatalf("the minted token authenticates as (%+v, %v), want %+v", got, err, tc.want)
			}
		})
	}
}

func TestMintTokenRefusesAnUnknownRole(t *testing.T) {
	envFile := writeEnv(t, "AUTH_JWT_SECRET=dev-only-jwt-secret-for-the-test-0123456789\n")
	var out strings.Builder
	if err := mintToken(envFile, []string{"superuser"}, &out, time.Now); err == nil {
		t.Fatalf("mintToken minted %q for an unknown role", out.String())
	}
}

func TestMintTokenNeedsTheSecret(t *testing.T) {
	envFile := writeEnv(t, "POSTGRES_PASSWORD=x\n")
	err := mintToken(envFile, nil, io.Discard, time.Now)
	if err == nil || !strings.Contains(err.Error(), "AUTH_JWT_SECRET") {
		t.Fatalf("err = %v, want one naming AUTH_JWT_SECRET", err)
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
	if err := os.WriteFile(".env", []byte("A=changed\nB=2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ensureEnv(); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(".env"); string(got) != "A=changed\nB=2\n" {
		t.Fatalf(".env was overwritten: %q", got)
	}
}

func TestEnsureEnvAddsVariablesNewInTheExample(t *testing.T) {
	t.Chdir(t.TempDir())
	example := "# Settings\r\nA=1\r\nB=2\r\n# C=a commented-out example\r\nD=4\r\n"
	if err := os.WriteFile(".env.example", []byte(example), 0o600); err != nil {
		t.Fatal(err)
	}
	// An .env made before B and D were added, with the developer's own value for A.
	if err := os.WriteFile(".env", []byte("A=mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	for range 2 { // a second run must not add them again
		if err := ensureEnv(); err != nil {
			t.Fatal(err)
		}
	}
	vars, err := readEnvFile(".env")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"A": "mine", "B": "2", "D": "4"}
	if len(vars) != len(want) || vars["A"] != "mine" || vars["B"] != "2" || vars["D"] != "4" {
		t.Fatalf(".env holds %v, want %v", vars, want)
	}
	raw, _ := os.ReadFile(".env")
	if n := strings.Count(string(raw), "D=4"); n != 1 {
		t.Fatalf(".env has D=4 %d times:\n%s", n, raw)
	}
	if strings.Contains(string(raw), "\r") {
		t.Fatalf(".env has CR line endings:\n%q", raw)
	}
}
