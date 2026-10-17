#!/bin/sh
# Build complete macOS distributions without requiring Go on the target machine.
set -eu

version=${1:-}
version=${version#v}
case "$version" in
  ''|*[!0-9A-Za-z.+-]*) printf '%s\n' 'Usage: scripts/package-release.sh VERSION' >&2; exit 1 ;;
esac
case "$version" in
  [0-9]*.[0-9]*.[0-9]*) ;;
  *) printf '%s\n' 'VERSION must have major.minor.patch components.' >&2; exit 1 ;;
esac
root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
cd "$root"
output=${STARBRIDGE_DIST_DIR:-"$root/dist"}
mkdir -p "$output"
output=$(CDPATH='' cd -- "$output" && pwd)
staging=$(mktemp -d "${TMPDIR:-/tmp}/starbridge-release.XXXXXXXX")
trap 'rm -rf "$staging"' EXIT HUP INT TERM
commit=$(git rev-parse HEAD)
dirty=false
if [ -n "$(git status --porcelain)" ]; then dirty=true; fi

for arch in arm64 amd64; do
  name="starbridge_${version}_darwin_${arch}"
  package="$staging/$name"
  mkdir -p "$package/guest/windows" "$package/docs"
  CGO_ENABLED=0 GOOS=darwin GOARCH="$arch" go build -trimpath -buildvcs=false \
    -ldflags "-s -w -X github.com/soulteary/starbridge/internal/version.Version=$version" \
    -o "$package/starbridge" ./cmd/starbridge
  cp guest/windows/desktop-bridge.ps1 "$package/guest/windows/desktop-bridge.ps1"
  cp guest/windows/README.md "$package/guest/windows/README.md"
  cp LICENSE THIRD_PARTY_NOTICES.md "$package/"
  cp docs/USAGE.md docs/ARCHITECTURE.md docs/ACCEPTANCE.md "$package/docs/"
  cp README.md "$package/README.md"
  cp -R examples "$package/examples"
  cat > "$package/INSTALL.txt" <<'INSTALL'
Starbridge for macOS

Verify the archive against checksums.txt before extracting it.
Run ./starbridge --version and ./starbridge doctor from this directory.
Optionally copy starbridge into a directory on your PATH.

This binary requires macOS and a licensed Parallels Desktop CLI at runtime.
No Go or Python runtime is required. See docs/USAGE.md for CLI and MCP usage;
replace source-build examples (./bin/starbridge) with ./starbridge.

Windows desktop queries also require Parallels Tools and the paired script:
  guest/windows/desktop-bridge.ps1
Install it in the guest using guest/windows/README.md. The binary does not
install files, start VMs or bypass macOS Gatekeeper automatically.
INSTALL
  printf 'version=%s\ncommit=%s\ndirty=%s\nos=darwin\narch=%s\nguest_protocol=1\n' \
    "$version" "$commit" "$dirty" "$arch" > "$package/BUILD.txt"
  tar -czf "$output/$name.tar.gz" -C "$staging" "$name"
done
(cd "$output" && shasum -a 256 "starbridge_${version}_darwin_arm64.tar.gz" "starbridge_${version}_darwin_amd64.tar.gz") > "$output/checksums.txt"
printf 'Created paired macOS packages and checksums in %s\n' "$output"
