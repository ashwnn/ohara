[← Back to README](../README.md)

# Installation

## Quick Install (GitHub Releases)

Latest release:

```bash
curl -fsSL https://raw.githubusercontent.com/ashwnn/ohara/main/install.sh | sh
```

Pinned release:

```bash
OHARA_VERSION=v0.1.0 curl -fsSL https://raw.githubusercontent.com/ashwnn/ohara/main/install.sh | sh
```

The installer places `ohara` in `${OHARA_INSTALL_DIR:-$HOME/.local/bin}` by default.

## Optional Source Build Fallback

Use local source build when release artifacts are unavailable:

```bash
OHARA_FROM_SOURCE=1 curl -fsSL https://raw.githubusercontent.com/ashwnn/ohara/main/install.sh | sh
```

Required tools for source fallback:

- `git`
- `go` (1.24+)

## Manual Install

1. Open <https://github.com/ashwnn/ohara/releases>
2. Download `ohara-<tag>-<os>-<arch>.tar.gz` and `checksums.txt`
3. Verify checksum and extract
4. Copy binary to `~/.local/bin/ohara`

## Verify Install

```bash
ohara --version
ohara check
```

## Start Server

Ohara listens on loopback (`127.0.0.1`) by default and stores data in
`~/.local/share/ohara`.

```bash
ohara serve
```

## Uninstall

```bash
rm -f ~/.local/bin/ohara
```
