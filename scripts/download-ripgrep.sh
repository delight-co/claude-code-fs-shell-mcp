#!/usr/bin/env bash
# Download the pinned ripgrep release archives, verify their sha256
# checksums, and place the rg binaries under internal/tools/ripgrep_binaries/
# so that the //go:embed directives in internal/tools/ripgrep_embed_*.go can
# pick them up at build time.
#
# Run this once before the first build (and again whenever the pinned version
# is bumped). The downloaded binaries are gitignored.

set -euo pipefail

VERSION="15.1.0"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
DEST_DIR="$PROJECT_ROOT/internal/tools/ripgrep_binaries"

mkdir -p "$DEST_DIR"

PLATFORMS=(
  "linux_amd64:x86_64-unknown-linux-musl"
  "linux_arm64:aarch64-unknown-linux-gnu"
  "darwin_amd64:x86_64-apple-darwin"
  "darwin_arm64:aarch64-apple-darwin"
  "windows_amd64:x86_64-pc-windows-msvc"
)

for entry in "${PLATFORMS[@]}"; do
  go_platform="${entry%%:*}"
  rust_triple="${entry##*:}"

  if [[ "$rust_triple" == *windows* ]]; then
    archive="ripgrep-${VERSION}-${rust_triple}.zip"
    binary_in_archive="ripgrep-${VERSION}-${rust_triple}/rg.exe"
    dest_name="rg_${go_platform}.exe"
  else
    archive="ripgrep-${VERSION}-${rust_triple}.tar.gz"
    binary_in_archive="ripgrep-${VERSION}-${rust_triple}/rg"
    dest_name="rg_${go_platform}"
  fi

  url="https://github.com/BurntSushi/ripgrep/releases/download/${VERSION}/${archive}"
  sha256_url="${url}.sha256"
  tmp="$(mktemp -d)"

  echo "[${go_platform}] downloading ${archive}"
  curl -fsSL -o "${tmp}/${archive}" "$url"
  curl -fsSL -o "${tmp}/${archive}.sha256" "$sha256_url"

  # The Linux/macOS checksum files use `<hash>  <filename>`; the Windows
  # checksum file uses CertUtil's multi-line format. Extracting the first
  # 64-character lowercase hex sequence handles both shapes.
  expected=$(grep -oE '[a-f0-9]{64}' "${tmp}/${archive}.sha256" | head -1)
  # sha256sum is the GNU coreutils name (Linux); shasum -a 256 is the
  # equivalent shipped with macOS by default. Pick whichever is available
  # so the script runs on both CI runners.
  if command -v sha256sum >/dev/null 2>&1; then
    actual=$(sha256sum "${tmp}/${archive}" | awk '{print $1}')
  else
    actual=$(shasum -a 256 "${tmp}/${archive}" | awk '{print $1}')
  fi
  if [[ "$expected" != "$actual" ]]; then
    echo "sha256 mismatch for ${archive}: expected ${expected}, got ${actual}" >&2
    rm -rf "$tmp"
    exit 1
  fi

  if [[ "$archive" == *.zip ]]; then
    unzip -q -j "${tmp}/${archive}" "${binary_in_archive}" -d "$tmp"
    mv "${tmp}/rg.exe" "${DEST_DIR}/${dest_name}"
  else
    tar -xzf "${tmp}/${archive}" -C "$tmp" "${binary_in_archive}"
    mv "${tmp}/${binary_in_archive}" "${DEST_DIR}/${dest_name}"
  fi
  chmod +x "${DEST_DIR}/${dest_name}"

  rm -rf "$tmp"
  echo "[${go_platform}] placed ${dest_name}"
done

echo
echo "All ripgrep ${VERSION} binaries placed under ${DEST_DIR}"
