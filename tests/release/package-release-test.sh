#!/bin/sh
# Exercise the real packaging script using a small, explicit go-build fixture.
# No compilation, VM operation, tag push or release publication is performed.
set -eu

root=$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)
temporary=$(mktemp -d "${TMPDIR:-/tmp}/starbridge-package-test.XXXXXXXX")
trap 'rm -rf "$temporary"' EXIT HUP INT TERM
mkdir -p "$temporary/bin"
cat > "$temporary/bin/go" <<'FAKE_GO'
#!/bin/sh
set -eu
[ "$1" = build ]
[ "$CGO_ENABLED" = 0 ]
[ "$GOOS" = darwin ]
case "$GOARCH" in arm64|amd64) ;; *) exit 1 ;; esac
output=''
flags=''
previous=''
for argument do
  if [ "$previous" = -o ]; then output=$argument; fi
  if [ "$previous" = -ldflags ]; then flags=$argument; fi
  previous=$argument
done
[ -n "$output" ]
[ "$flags" = "-s -w -X github.com/soulteary/starbridge/internal/version.Version=$FAKE_GO_VERSION" ]
printf '%s\n' "$GOARCH" >> "$FAKE_GO_LOG"
printf 'fake-macos-%s\n' "$GOARCH" > "$output"
chmod +x "$output"
FAKE_GO
chmod +x "$temporary/bin/go"
PATH="$temporary/bin:$PATH"
export PATH

checks=0
expect_invalid() {
  if STARBRIDGE_DIST_DIR="$temporary/invalid-dist" FAKE_GO_LOG="$temporary/invalid-builds" \
      "$root/scripts/package-release.sh" "$@" > "$temporary/invalid-output" 2>&1; then
    printf 'Invalid version was accepted: %s\n' "$*" >&2
    exit 1
  fi
  if [ -e "$temporary/invalid-builds" ] || [ -e "$temporary/invalid-dist" ]; then
    printf 'Invalid version reached build or output creation: %s\n' "$*" >&2
    exit 1
  fi
  checks=$((checks + 1))
}

expect_invalid
expect_invalid ''
expect_invalid 1.2.3 extra
for version in \
    v1..2.3 v1a.2.3 v1.2.3foo 1 1.2 1.2.3.4 \
    01.2.3 1.02.3 1.2.03 00.0.0 \
    1.2.3-01 1.2.3-alpha.01 1.2.3-00.1 \
    1.2.3- 1.2.3+ 1.2.3-. 1.2.3-a..b 1.2.3+a..b \
    1.2.3-a_b 1.2.3+build_1 1.2.3+build+extra \
    vv1.2.3 V1.2.3 -1.2.3 ' 1.2.3' '1.2.3 ' '1.2.3/extra' '１.2.3'; do
  expect_invalid "$version"
done
expect_invalid '1.2.3
malformed'
expect_invalid 'malformed
1.2.3'

for version in \
    0.0.0 v1.2.3 1.2.3-alpha 1.2.3-alpha.1 1.2.3-0.3.7 \
    1.2.3-x.7.z.92 1.2.3-x-y-z.-- 1.2.3-01alpha \
    1.2.3-rc.0+001 1.2.3+001.02 1.2.3-beta+exp.sha.5114f85 \
    1.2.3-A-B.0.0Z+build.00 9999999999999999999999999999.2.3; do
  normalized=${version#v}
  distribution="$temporary/dist-$checks"
  FAKE_GO_VERSION="$normalized" FAKE_GO_LOG="$temporary/builds-$checks" \
    STARBRIDGE_DIST_DIR="$distribution" "$root/scripts/package-release.sh" "$version" > /dev/null
  printf 'arm64\namd64\n' > "$temporary/expected-builds"
  cmp "$temporary/expected-builds" "$temporary/builds-$checks"
  (cd "$distribution" && shasum -a 256 -c checksums.txt > /dev/null)
  for architecture in arm64 amd64; do
    name="starbridge_${normalized}_darwin_${architecture}"
    extraction="$temporary/extracted-$checks-$architecture"
    mkdir -p "$extraction"
    tar -xzf "$distribution/$name.tar.gz" -C "$extraction"
    package="$extraction/$name"
    for required in starbridge INSTALL.txt BUILD.txt README.md CONTRIBUTING.md LICENSE THIRD_PARTY_NOTICES.md go.mod \
        guest/windows/README.md guest/windows/desktop-bridge.ps1 \
        tests/windows/desktop-acceptance.ps1 tests/windows/desktop-fixture.ps1 \
        tests/windows/guest-smoke.ps1 tests/windows/guest-performance.ps1 \
        tests/windows/parameter-contract.ps1 tests/fixtures/parameters.json; do
      if [ ! -f "$package/$required" ]; then
        printf 'Missing packaged file: %s\n' "$required" >&2
        exit 1
      fi
    done
    for document in "$root"/docs/*.md; do
      cmp "$document" "$package/docs/$(basename -- "$document")"
    done
    for excluded in .git .github cmd internal; do
      [ ! -e "$package/$excluded" ]
    done
    cmp "$root/guest/windows/desktop-bridge.ps1" "$package/guest/windows/desktop-bridge.ps1"
    cmp "$root/go.mod" "$package/go.mod"
    printf 'fake-macos-%s\n' "$architecture" > "$temporary/expected-binary"
    cmp "$temporary/expected-binary" "$package/starbridge"
    grep -Fx "version=$normalized" "$package/BUILD.txt" > /dev/null
    grep -Fx "arch=$architecture" "$package/BUILD.txt" > /dev/null
    "$root/scripts/check-package-docs.sh" "$package"
  done
  checks=$((checks + 1))
done

# A dangling root link must fail the same checker used before archiving; this
# reproduces the original missing go.mod rather than only checking a file list.
rm "$package/go.mod"
if "$root/scripts/check-package-docs.sh" "$package" > "$temporary/broken-link-output" 2>&1; then
  printf '%s\n' 'Documentation checker missed an absent go.mod link target.' >&2
  exit 1
fi
grep -F 'go.mod' "$temporary/broken-link-output" > /dev/null
printf 'Release packaging checks passed: %s version/argument cases; paired archives, checksums and documentation links verified.\n' "$checks"
