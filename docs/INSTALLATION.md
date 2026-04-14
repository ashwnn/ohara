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

