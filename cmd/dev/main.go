// Command dev runs the project's development tasks: the local stack, the
// database, code generation, lint and tests. It needs only Go and Docker, so
// the same commands work in PowerShell, cmd and Git Bash on Windows, and on
// macOS and Linux.
//
// Run it from anywhere inside the repository:
//
//	go run ./cmd/dev help
//	go run ./cmd/dev up
//
// Scripts that need a POSIX shell, such as scripts/db-check.sh, run inside the
// Linux containers, never on the host. Lint tools run from pinned Docker images
// when they aren't installed, so their versions match CI.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
)

// Pinned tool versions. CI uses the same ones, so change them together.
const (
	oapiCodegen   = "github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.2"
	golangciLint  = "2.15.0"
	golangciImage = "golangci/golangci-lint:v" + golangciLint
	redoclyImage  = "redocly/cli:2.77.0"
)

// Connection strings used inside the Postgres container, over its local socket.
const (
	ownerDSN = "postgresql:///claims?user=claims_owner"
	appDSN   = "postgresql:///claims?user=claims_app"
)

const (
	groupStack = "Local stack"
	groupDB    = "Database"
	groupGo    = "Go code"
	groupApp   = "Service containers"
)

type runFunc func(ctx context.Context, args []string) error

type task struct {
	name  string
	args  string // the argument synopsis for help; empty means the task takes none
	group string
	help  string
	run   runFunc
}

func tasks() []task {
	return []task{
		{name: "up", group: groupStack, run: compose("up", "--detach", "--wait"),
			help: "Start Postgres, SeaweedFS, Temporal and Grafana LGTM, then wait until they're healthy"},
		{name: "down", group: groupStack, run: compose("down"),
			help: "Stop the stack and keep its data"},
		{name: "reset", group: groupStack, run: compose("down", "--volumes", "--remove-orphans"),
			help: "Stop the stack and delete all local data"},
		{name: "ps", group: groupStack, run: compose("ps"),
			help: "Show the state of each service"},
		{name: "logs", args: "[service...]", group: groupStack, run: compose("logs", "--follow"),
			help: "Follow the logs of every service, or of the ones named"},

		{name: "migrate", args: "[up|down N|version]", group: groupDB, run: migrate,
			help: "Run golang-migrate as claims_owner; with no arguments, apply every migration"},
		{name: "seed", group: groupDB, run: seed,
			help: "Load the synthetic development data (safe to re-run)"},
		{name: "db-check", group: groupDB, run: dbCheck,
			help: "Check the database guarantees: grants, the audit chain and constraints"},
		{name: "psql", group: groupDB, run: psql,
			help: "Open psql as claims_app, with exactly the services' privileges"},

		{name: "tools", group: groupGo, run: goTool("get", "-tool", oapiCodegen),
			help: "Add oapi-codegen to go.mod as a tool (run once)"},
		{name: "gen", group: groupGo, run: goTool("generate", "./internal/api/apigen"),
			help: "Regenerate internal/api/apigen from api/openapi.yaml"},
		{name: "build", group: groupGo, run: goTool("build", "-trimpath", "-o", "bin/", "./cmd/..."),
			help: "Build every command into bin/"},
		{name: "vet", group: groupGo, run: goTool("vet", "./..."),
			help: "Run go vet"},
		{name: "test", args: "[packages]", group: groupGo, run: test,
			help: "Run the tests, with the race detector where this machine supports it"},
		{name: "lint", group: groupGo, run: inOrder(lintGo, lintAPI),
			help: "Run golangci-lint and the OpenAPI lint"},
		{name: "lint-go", group: groupGo, run: lintGo,
			help: "Run golangci-lint v" + golangciLint},
		{name: "lint-api", group: groupGo, run: lintAPI,
			help: "Lint api/openapi.yaml with Redocly"},
		{name: "fmt", group: groupGo, run: fmtGo,
			help: "Format the Go code with gofmt and goimports"},
		{name: "check", group: groupGo, run: inOrder(goTool("vet", "./..."), lintGo, lintAPI, test),
			help: "Run what CI runs on the code: vet, lint and test"},

		{name: "images", group: groupApp, run: compose("--profile", "app", "build"),
			help: "Build the api, relay and worker images"},
		{name: "app", group: groupApp, run: compose("--profile", "app", "up", "--build", "--detach"),
			help: "Build and start the api, relay and worker containers"},
		{name: "token", args: "[role] [subject]", group: groupApp, run: token,
			help: "Print a 12-hour bearer token for calling the API (role: submitter, reviewer or admin)"},

		{name: "help", run: help, help: "List the tasks"},
	}
}

func main() {
	if err := mainErr(os.Args[1:]); err != nil {
		var code exitCode
		if errors.As(err, &code) {
			os.Exit(int(code))
		}
		fmt.Fprintln(os.Stderr, "dev:", err)
		os.Exit(1)
	}
}

func mainErr(argv []string) error {
	if len(argv) == 0 {
		_ = help(context.Background(), nil)
		return exitCode(2)
	}
	t, err := lookup(argv[0], argv[1:])
	if err != nil {
		return err
	}
	if t.name != "help" {
		if err := chdirRoot(); err != nil {
			return err
		}
	}
	// Ctrl+C reaches the child process straight from the terminal. Catching it
	// here keeps this process alive until the child has exited and passes its
	// exit code on.
	signal.Notify(make(chan os.Signal, 1), os.Interrupt)
	return t.run(context.Background(), argv[1:])
}

// lookup finds the named task and checks that it accepts the arguments given.
func lookup(name string, args []string) (task, error) {
	for _, t := range tasks() {
		if t.name != name {
			continue
		}
		if t.args == "" && len(args) > 0 {
			return task{}, fmt.Errorf("%s takes no arguments", name)
		}
		return t, nil
	}
	return task{}, fmt.Errorf("unknown task %q; run `go run ./cmd/dev help` to list them", name)
}

func help(_ context.Context, _ []string) error {
	w := os.Stdout
	fmt.Fprintln(w, "Usage: go run ./cmd/dev <task> [arguments]")
	group := ""
	for _, t := range tasks() {
		if t.group == "" {
			continue
		}
		if t.group != group {
			group = t.group
			fmt.Fprintf(w, "\n%s\n", group)
		}
		name := t.name
		if t.args != "" {
			name += " " + t.args
		}
		fmt.Fprintf(w, "  %-36s %s\n", name, t.help)
	}
	return nil
}

// compose returns a task that runs `docker compose` with these arguments,
// followed by any the developer adds.
func compose(args ...string) runFunc {
	return func(ctx context.Context, extra []string) error {
		return composeIn(ctx, nil, append(append([]string{}, args...), extra...)...)
	}
}

// composeIn runs `docker compose` with stdin connected to in. compose.yaml
// requires the values in .env, so every Compose task creates it first.
func composeIn(ctx context.Context, in io.Reader, args ...string) error {
	if err := ensureEnv(); err != nil {
		return err
	}
	return run(ctx, in, "docker", append([]string{"compose"}, args...)...)
}

func migrate(ctx context.Context, args []string) error {
	if len(args) == 0 {
		args = []string{"up"}
	}
	return composeIn(ctx, nil, append([]string{"run", "--rm", "migrate"}, args...)...)
}

func seed(ctx context.Context, _ []string) error {
	sql, err := readLF(filepath.Join("db", "seed", "dev.sql"))
	if err != nil {
		return err
	}
	return composeIn(ctx, sql, "exec", "-T", "postgres", "psql", "-X", "-q", "-v", "ON_ERROR_STOP=1", ownerDSN)
}

func dbCheck(ctx context.Context, _ []string) error {
	script, err := readLF(filepath.Join("scripts", "db-check.sh"))
	if err != nil {
		return err
	}
	return composeIn(ctx, script, "exec", "-T", "postgres", "sh", "-s")
}

func psql(ctx context.Context, _ []string) error {
	return composeIn(ctx, os.Stdin, "exec", "postgres", "psql", appDSN)
}

// goTool returns a task that runs the go command with these arguments.
func goTool(args ...string) runFunc {
	return func(ctx context.Context, _ []string) error {
		return run(ctx, nil, "go", args...)
	}
}

func test(ctx context.Context, args []string) error {
	goArgs := []string{"test", "-count=1"}
	if raceSupported(ctx) {
		goArgs = append(goArgs, "-race")
	} else {
		fmt.Fprintln(os.Stderr, "note: the race detector needs cgo and a C compiler, which this machine lacks, so the tests run without -race. CI runs them with it.")
	}
	if len(args) == 0 {
		args = []string{"./..."}
	}
	return run(ctx, nil, "go", append(goArgs, args...)...)
}

// raceSupported reports whether `go test -race` can work here: it needs cgo,
// and so a C compiler, which Windows machines often lack.
func raceSupported(ctx context.Context) bool {
	out, err := exec.CommandContext(ctx, "go", "env", "CGO_ENABLED", "CC").Output()
	if err != nil {
		return false
	}
	fields := strings.Fields(string(out))
	if len(fields) < 2 || fields[0] != "1" {
		return false
	}
	_, err = exec.LookPath(fields[1])
	return err == nil
}

func lintGo(ctx context.Context, _ []string) error { return golangci(ctx, "run") }

func fmtGo(ctx context.Context, _ []string) error { return golangci(ctx, "fmt") }

// golangci runs golangci-lint. It uses an installed binary of the pinned version
// on macOS and Linux, and the pinned Docker image otherwise. Windows always uses
// the image, which lints with the same Linux paths as CI.
func golangci(ctx context.Context, args ...string) error {
	if runtime.GOOS != "windows" && localGolangciMatches(ctx) {
		return run(ctx, nil, "golangci-lint", args...)
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	docker := []string{
		"run", "--rm",
		"--mount", "type=bind,source=" + root + ",target=/src",
		"--mount", "type=volume,source=claims-agent-go-mod,target=/go/pkg/mod",
		"--mount", "type=volume,source=claims-agent-go-cache,target=/root/.cache",
		"--workdir", "/src",
		"--env", "GOTOOLCHAIN=auto",
		golangciImage, "golangci-lint",
	}
	return run(ctx, nil, "docker", append(docker, args...)...)
}

func localGolangciMatches(ctx context.Context) bool {
	if _, err := exec.LookPath("golangci-lint"); err != nil {
		return false
	}
	out, err := exec.CommandContext(ctx, "golangci-lint", "version").Output()
	return err == nil && strings.Contains(string(out), "version "+golangciLint)
}

// lintAPI lints the contract with the pinned Redocly image, so Node.js isn't needed.
func lintAPI(ctx context.Context, _ []string) error {
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	return run(ctx, nil, "docker", "run", "--rm",
		"--mount", "type=bind,source="+root+",target=/spec,readonly",
		"--workdir", "/spec",
		redoclyImage, "lint", "api/openapi.yaml")
}

// inOrder returns a task that runs steps one after another and stops at the
// first failure.
func inOrder(steps ...runFunc) runFunc {
	return func(ctx context.Context, _ []string) error {
		for _, step := range steps {
			if err := step(ctx, nil); err != nil {
				return err
			}
		}
		return nil
	}
}

// exitCode carries a child process's exit status back to main.
type exitCode int

func (c exitCode) Error() string { return fmt.Sprintf("exit status %d", int(c)) }

// run executes a program with this process's output, printing the command line
// first.
func run(ctx context.Context, in io.Reader, name string, args ...string) error {
	fmt.Fprintf(os.Stderr, "+ %s %s\n", name, strings.Join(args, " "))
	//nolint:gosec // G204: each program is fixed in this file, and arguments come from the developer's own command line.
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = in, os.Stdout, os.Stderr
	err := cmd.Run()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &exitErr):
		return exitCode(exitErr.ExitCode())
	case errors.Is(err, exec.ErrNotFound):
		return fmt.Errorf("%s isn't installed or isn't on PATH", name)
	default:
		return err
	}
}

// ensureEnv creates .env from .env.example the first time, with LF line endings whatever
// the checkout uses. After that it appends any variable .env.example has gained since:
// Compose checks every variable compose.yaml requires, even for services it isn't starting.
// Values already in .env are never changed.
func ensureEnv() error {
	_, err := os.Stat(".env")
	switch {
	case errors.Is(err, fs.ErrNotExist):
		example, err := os.ReadFile(".env.example")
		if err != nil {
			return err
		}
		//nolint:gosec // G703: the path is the constant .env, and the content comes from this repository's .env.example.
		if err := os.WriteFile(".env", []byte(toLF(string(example))), 0o600); err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, "Created .env from .env.example. Its passwords are for local development only.")
		return nil
	case err != nil:
		return err
	}
	return addNewVariables()
}

// addNewVariables appends to .env each variable in .env.example that .env lacks.
func addNewVariables() error {
	example, err := os.ReadFile(".env.example")
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	current, err := os.ReadFile(".env")
	if err != nil {
		return err
	}
	have := parseEnv(string(current))
	var lines, names []string
	for _, line := range strings.Split(toLF(string(example)), "\n") {
		line = strings.TrimSpace(line)
		name, _, ok := strings.Cut(line, "=")
		if !ok || strings.HasPrefix(line, "#") {
			continue
		}
		if name = strings.TrimSpace(name); name == "" {
			continue
		}
		if _, ok := have[name]; ok {
			continue
		}
		have[name] = ""
		lines = append(lines, line)
		names = append(names, name)
	}
	if len(lines) == 0 {
		return nil
	}
	content := toLF(string(current))
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += "\n# Added from .env.example by the dev tool.\n" + strings.Join(lines, "\n") + "\n"
	//nolint:gosec // G703: the path is the constant .env, and the added lines come from this repository's .env.example.
	if err := os.WriteFile(".env", []byte(content), 0o600); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Added %s to .env from .env.example.\n", strings.Join(names, ", "))
	return nil
}

// readLF reads a file for a Linux container. A Windows checkout may have CRLF
// line endings, which would break a shell script, so they become LF.
func readLF(path string) (io.Reader, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return strings.NewReader(toLF(string(data))), nil
}

func toLF(s string) string { return strings.ReplaceAll(s, "\r\n", "\n") }

// chdirRoot moves to the repository root, so tasks work from any subfolder.
func chdirRoot() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	root, err := findRoot(dir)
	if err != nil {
		return err
	}
	return os.Chdir(root)
}

// findRoot returns the nearest folder, from dir upwards, that holds both
// go.mod and compose.yaml.
func findRoot(dir string) (string, error) {
	for {
		if isFile(filepath.Join(dir, "go.mod")) && isFile(filepath.Join(dir, "compose.yaml")) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("run this inside the claims-agent repository: no folder above here holds go.mod and compose.yaml")
		}
		dir = parent
	}
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
