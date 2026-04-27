[← Back to README](../README.md)

# Installation

Ohara is **source-build only**. This project does not publish release binaries,
package-manager formulas, Docker images, or marketplace packages.

## Requirements

- Go 1.24+
- Git
- SQLite is embedded through [modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite), so no CGO or system SQLite package is required.

## Build From Source

```bash
git clone https://github.com/ashwnn/ohara
cd ohara
go build -trimpath -o ohara ./cmd/ohara
./ohara version
```

Optional install into `~/.local/bin`:

```bash
mkdir -p ~/.local/bin
go build -trimpath -o ~/.local/bin/ohara ./cmd/ohara
ohara version
```

## Verify Build

Before using a new checkout for real data:

```bash
go test ./...
go vet ./...
ohara validate
```

`ohara validate` checks the local database schema and exits non-zero on
validation failure. On a first install with no existing database, run `ohara
serve` or any command that opens the store once before validation.

## Start Server

Ohara listens on loopback (`127.0.0.1`) by default and stores data in
`~/.local/share/ohara`.

```bash
ohara serve
```

The `OHARA_HTTP_ADDR` setting currently supplies the port only; the server binds
to loopback. Use `OHARA_SOCKET` for local Unix socket mode.

## Agent Setup

```bash
ohara setup opencode
ohara setup --check
```

Supported setup targets are documented in [Usage](USAGE.md#setup).

## User Service

Systemd user units live in `systemd/`. Install manually if you want Ohara to run
in the background:

```bash
mkdir -p ~/.config/systemd/user
cp systemd/ohara.service ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now ohara.service
systemctl --user status ohara.service
```

The service expects the binary at `~/.local/bin/ohara`.
