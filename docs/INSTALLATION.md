[← Back to README](../README.md)

# Installation

Install the latest release:

```bash
curl -fsSL https://raw.githubusercontent.com/ashwnn/ohara/main/install.sh | sh
```

Install a pinned release:

```bash
OHARA_VERSION=v0.1.0 curl -fsSL https://raw.githubusercontent.com/ashwnn/ohara/main/install.sh | sh
```

Source-build fallback:

```bash
OHARA_FROM_SOURCE=1 curl -fsSL https://raw.githubusercontent.com/ashwnn/ohara/main/install.sh | sh
```

Required tools for source fallback:

- `git`
- `go` 1.24+

Next: [docs/OPERATIONS.md](OPERATIONS.md) for runtime commands and
[DOCS.md](../DOCS.md) for the consolidated reference.
