#!/bin/sh
# Build complete macOS distributions without requiring Go on the target machine.
set -eu

if [ "$#" -ne 1 ]; then
  printf '%s\n' 'Usage: scripts/package-release.sh VERSION' >&2
  exit 1
fi
version=$1
version=${version#v}
case "$version" in
  ''|*[!0-9A-Za-z.+-]*) printf '%s\n' 'Usage: scripts/package-release.sh VERSION' >&2; exit 1 ;;
esac
# SemVer 2.0.0: numeric core and prerelease identifiers have no leading zeroes;
# build identifiers may contain leading zeroes. The character check above also
# prevents grep accepting one valid line inside a multiline argument.
number='(0|[1-9][0-9]*)'
prerelease='(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)'
semver="^$number\\.$number\\.$number(-$prerelease(\\.$prerelease)*)?(\\+[0-9A-Za-z-]+(\\.[0-9A-Za-z-]+)*)?$"
if ! printf '%s\n' "$version" | LC_ALL=C grep -Eq "$semver"; then
  printf '%s\n' 'VERSION must follow SemVer 2.0.0 (optionally prefixed with v).' >&2
  exit 1
fi
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
  mkdir -p "$package/guest/windows" "$package/tests"
  CGO_ENABLED=0 GOOS=darwin GOARCH="$arch" go build -trimpath -buildvcs=false \
    -ldflags "-s -w -X github.com/soulteary/starbridge/internal/version.Version=$version" \
    -o "$package/starbridge" ./cmd/starbridge
  cp guest/windows/desktop-bridge.ps1 "$package/guest/windows/desktop-bridge.ps1"
  cp guest/windows/README.md "$package/guest/windows/README.md"
  cp README.md CONTRIBUTING.md LICENSE THIRD_PARTY_NOTICES.md go.mod "$package/"
  cp -R docs examples "$package/"
  # Keep documented Windows acceptance commands usable, including the smoke
  # script's parameter-contract fixture, without shipping the Go source tree.
  cp -R tests/windows tests/fixtures "$package/tests/"
  cat > "$package/INSTALL.txt" <<'INSTALL'
Starbridge for macOS

Verify the archive against checksums.txt before extracting it.
Run ./starbridge --version and ./starbridge doctor from this directory.
Optionally copy starbridge into a directory on your PATH.

This binary requires macOS and a licensed Parallels Desktop CLI at runtime.
No Go or Python runtime is required. See docs/USAGE.md for CLI and MCP usage;
replace source-build examples (./bin/starbridge) with ./starbridge.
README, CONTRIBUTING and RELEASING build/maintenance commands describe a source
checkout; this binary distribution intentionally omits the Go source and Git data.

Windows desktop queries also require Parallels Tools and the paired script:
  guest/windows/desktop-bridge.ps1
Install it in the guest using guest/windows/README.md. The binary does not
install files, start VMs or bypass macOS Gatekeeper automatically.
Optional Windows acceptance scripts and their fixtures are under tests/.
INSTALL
  printf 'version=%s\ncommit=%s\ndirty=%s\nos=darwin\narch=%s\nguest_protocol=1\n' \
    "$version" "$commit" "$dirty" "$arch" > "$package/BUILD.txt"
  "$root/scripts/check-package-docs.sh" "$package"
  tar -czf "$output/$name.tar.gz" -C "$staging" "$name"
done
(cd "$output" && shasum -a 256 "starbridge_${version}_darwin_arm64.tar.gz" "starbridge_${version}_darwin_amd64.tar.gz") > "$output/checksums.txt"
printf 'Created paired macOS packages and checksums in %s\n' "$output"
