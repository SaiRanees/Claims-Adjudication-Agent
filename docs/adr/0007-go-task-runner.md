# 0007. Run development tasks with a Go program, not Make

- Status: Accepted
- Date: 2026-11-11

## Context

Development happens natively on Windows, without WSL or a Linux distribution. The first scaffold used a Makefile and shell scripts, which need GNU Make and bash on the host. Windows has neither by default, and installing them, or moving the work into WSL, is friction this project doesn't need.

The tasks themselves are simple: run Docker Compose, the go command and a few pinned tools.

## Decision

Project tasks run through `cmd/dev`, a small Go program using only the standard library: `go run ./cmd/dev <task>`. It needs only Go and Docker, which the project needs anyway, and behaves the same in PowerShell, cmd, Git Bash, macOS and Linux.

- Anything that needs a POSIX shell runs inside a Linux container. `scripts/db-check.sh` is piped into the Postgres container, with CRLF line endings turned into LF on the way.
- The database roles are created by `db/init/01-roles.sql`, which reads its passwords with psql's `\getenv`, so no shell script is mounted into Postgres.
- golangci-lint and Redocly run from pinned Docker images when they aren't installed, so their versions match CI. On Windows, golangci-lint always runs from the image, which lints with the same Linux paths as CI.
- CI runs a Windows job that vets, builds and tests the code, and runs the dev tool.

## Consequences

- One set of commands everywhere, with no extra installs beyond Go and Docker.
- The task runner is a few hundred lines of Go to maintain, with its own tests, where a Makefile would be shorter.
- `go run` compiles the tool on first use; after that, Go's build cache makes it quick.
- Lint through Docker is slower than a native binary, and its first run downloads the Go modules into a cache volume.
- `go test -race` needs cgo and a C compiler, which Windows machines often lack. The tool runs the tests without `-race` there and says so; CI always runs them with it.

## Alternatives considered

- **Make.** Needs GNU Make and bash on Windows, and recipes written for bash.
- **Task (Taskfile).** Cross-platform and tidy, but one more tool to install and keep at the same version everywhere.
- **PowerShell scripts.** Native on Windows, but blocked by the default execution policy until it is changed, and a second scripting language for macOS and Linux users.
- **Develop inside WSL.** Works well, but requires a Linux setup, which is exactly what this project avoids.
