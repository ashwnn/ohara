[← Back to README](../README.md)

# Installation

Ohara is **source-build only**. No prebuilt binaries or package managers.

- [Build from source (all platforms)](#build-from-source-all-platforms)
- [Requirements](#requirements)
- [Environment Variables](#environment-variables)
- [Windows Config Paths](#windows-config-paths)

---

## Build from Source (All Platforms)

### Prerequisites

- **Go 1.24+** (required to build)
- Git (to clone the repository)

### Clone and Build

```bash
git clone https://github.com/ashwnn/ohara.git
cd ohara
go build -o ohara ./cmd/ohara
```

Optional: build with version stamp:

```bash
go build -ldflags="-X main.version=local-$(git describe --tags --always)" -o ohara ./cmd/ohara
```

### Install to PATH

```bash
# Move to a directory in your PATH
mv ohara ~/.local/bin/
# Or on macOS with Homebrew prefix:
# mv ohara /usr/local/bin/
```

### Windows (PowerShell)

```powershell
git clone https://github.com/ashwnn/ohara.git
cd ohara
go build -o ohara.exe ./cmd/ohara

# Move to a directory in your PATH
Move-Item ohara.exe $env:USERPROFILE\bin\
```

---

## Requirements

- **Go 1.24+** (required to build from source)
- That's it. No runtime dependencies.

The binary includes SQLite (via [modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite) — pure Go, no CGO). Works natively on **macOS**, **Linux**, and **Windows** (x86_64 and ARM64).

---

## Environment Variables

| Variable | Description | Default |
|---|---|---|
| `OHARA_DATA_DIR` | Data directory | `~/.ohara` (Windows: `%USERPROFILE%\.ohara`) |
| `OHARA_PORT` | HTTP server port | `7437` |

---

## Windows Config Paths

When using `ohara setup opencode`, config files are written to platform-appropriate locations:

| Component | macOS / Linux | Windows |
|-----------|---------------|---------|
| OpenCode plugin | `~/.config/opencode/plugins/` | `%APPDATA%\opencode\plugins\` |
| OpenCode config | `~/.config/opencode/opencode.json` | `%APPDATA%\opencode\opencode.json` |
| Data directory | `~/.ohara/` | `%USERPROFILE%\.ohara\` |

---

## Systemd User Service (Linux)

For Linux systems with systemd, user service files are provided in the repository:

```bash
# Copy service files to user systemd directory
mkdir -p ~/.config/systemd/user/
cp systemd/ohara.service ~/.config/systemd/user/
cp systemd/ohara-maintain.timer ~/.config/systemd/user/
cp systemd/ohara-maintain.service ~/.config/systemd/user/

# Enable and start services
systemctl --user daemon-reload
systemctl --user enable --now ohara.service
systemctl --user enable --now ohara-maintain.timer
```

This configures:
- `ohara.service` — runs the memory server continuously with 512M memory limit
- `ohara-maintain.timer` — nightly maintenance at 02:00 (archive, backup, integrity check)

See the spec section 8 for full details on the maintenance schedule and resource limits.

