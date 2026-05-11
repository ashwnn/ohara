#!/bin/sh
set -eu

REPO="ashwnn/ohara"
BINARY_NAME="ohara"
INSTALL_DIR="${OHARA_INSTALL_DIR:-$HOME/.local/bin}"
VERSION="${OHARA_VERSION:-latest}"
FROM_SOURCE="${OHARA_FROM_SOURCE:-0}"

say() { printf '%s\n' "$*"; }
err() { printf 'error: %s\n' "$*" >&2; exit 1; }
need_cmd() { command -v "$1" >/dev/null 2>&1 || err "missing required command: $1"; }

os_arch() {
  os=$(uname -s 2>/dev/null | tr '[:upper:]' '[:lower:]')
  arch=$(uname -m 2>/dev/null)

  case "$os" in
    linux|darwin) ;;
    *) err "unsupported operating system: $os" ;;
  esac

  case "$arch" in
    x86_64|amd64) arch="amd64" ;;
    aarch64|arm64) arch="arm64" ;;
    *) err "unsupported architecture: $arch" ;;
  esac

  printf '%s %s\n' "$os" "$arch"
}

version_for_artifact() {
  if [ "$VERSION" = "latest" ]; then
    need_cmd curl
    curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
      | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' \
      | head -n 1
  else
    printf '%s\n' "$VERSION"
  fi
}

install_from_source() {
  need_cmd git
  need_cmd go
  mkdir -p "$INSTALL_DIR"
  tmpdir=$(mktemp -d)
  trap 'rm -rf "$tmpdir"' EXIT INT HUP TERM
  say "building ${BINARY_NAME} from source"
  git clone --depth 1 "https://github.com/${REPO}.git" "$tmpdir/ohara"
  if [ "$VERSION" != "latest" ]; then
    (cd "$tmpdir/ohara" && git fetch --depth 1 origin "refs/tags/${VERSION}:refs/tags/${VERSION}" && git checkout "tags/${VERSION}")
  fi
  (cd "$tmpdir/ohara" && go build -trimpath -o "$INSTALL_DIR/$BINARY_NAME" ./cmd/ohara)
  say "installed $BINARY_NAME to $INSTALL_DIR"
}

install_from_release() {
  need_cmd curl
  need_cmd tar
  need_cmd mktemp

  set -- $(os_arch)
  os="$1"
  arch="$2"
  tag=$(version_for_artifact)
  [ -n "$tag" ] || err "could not determine release version"

  artifact="ohara-${tag}-${os}-${arch}.tar.gz"
  base_url="https://github.com/${REPO}/releases/download/${tag}"

  tmpdir=$(mktemp -d)
  trap 'rm -rf "$tmpdir"' EXIT INT HUP TERM

  if ! curl -fsSL "$base_url/$artifact" -o "$tmpdir/$artifact"; then
    err "release artifact not found for ${os}/${arch} at ${tag}. Use OHARA_FROM_SOURCE=1 to build locally."
  fi

  if curl -fsSL "$base_url/checksums.txt" -o "$tmpdir/checksums.txt"; then
    if command -v sha256sum >/dev/null 2>&1; then
      (cd "$tmpdir" && sha256sum -c --ignore-missing checksums.txt)
    elif command -v shasum >/dev/null 2>&1; then
      expected=$(sed -n "s/^\([a-f0-9]*\)  ${artifact}$/\1/p" "$tmpdir/checksums.txt")
      [ -n "$expected" ] || err "checksum entry missing for $artifact"
      actual=$(shasum -a 256 "$tmpdir/$artifact" | awk '{print $1}')
      [ "$expected" = "$actual" ] || err "checksum verification failed for $artifact"
    else
      err "checksum file found but no sha256 tool available (need sha256sum or shasum)"
    fi
  fi

  mkdir -p "$INSTALL_DIR"
  tar -xzf "$tmpdir/$artifact" -C "$tmpdir"
  cp "$tmpdir/ohara-${tag}-${os}-${arch}/$BINARY_NAME" "$INSTALL_DIR/$BINARY_NAME"
  chmod 0755 "$INSTALL_DIR/$BINARY_NAME"

  say "installed $BINARY_NAME $tag to $INSTALL_DIR"
}

if [ "$FROM_SOURCE" = "1" ]; then
  install_from_source
else
  install_from_release
fi

case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *)
    say "warning: $INSTALL_DIR is not on PATH"
    say "add this to your shell profile: export PATH=\"$INSTALL_DIR:\$PATH\""
    ;;
esac
