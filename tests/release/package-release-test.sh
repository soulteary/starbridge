#!/bin/sh
# Exercise the real packaging script using a small, explicit go-build fixture.
# No compilation, VM operation, tag push or release publication is performed.
set -eu

root=$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)
temporary=$(mktemp -d "${TMPDIR:-/tmp}/starbridge-package-test.XXXXXXXX")
trap 'rm -rf "$temporary"' EXIT HUP INT TERM
mkdir -p "$temporary/bin"
FAKE_GO_TOOLCHAIN="go$(cat "$root/.go-version")"
export FAKE_GO_TOOLCHAIN
cat > "$temporary/bin/go" <<'FAKE_GO'
#!/bin/sh
set -eu
case "${1:-}" in
  env)
    [ "$#" -eq 2 ] && [ "$2" = GOVERSION ]
    printf '%s\n' "$FAKE_GO_TOOLCHAIN"
    exit 0 ;;
  version)
    [ "$#" -eq 3 ] && [ "$2" = -m ] && [ -f "$3" ]
    printf '%s: %s\n' "$3" "${FAKE_GO_BINARY_VERSION:-$FAKE_GO_TOOLCHAIN}"
    exit 0 ;;
esac
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
        guest/windows/developer-bridge.ps1 guest/windows/developer/system.ps1 \
        tests/windows/desktop-acceptance.ps1 tests/windows/desktop-fixture.ps1 \
        tests/windows/guest-smoke.ps1 tests/windows/guest-performance.ps1 \
        tests/windows/parameter-contract.ps1 tests/windows/developer-contract.ps1 tests/fixtures/parameters.json; do
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
    git -C "$root" ls-files -- guest/windows > "$temporary/guest-files"
    while IFS= read -r guest_file; do
      cmp "$root/$guest_file" "$package/$guest_file"
    done < "$temporary/guest-files"
    cmp "$root/go.mod" "$package/go.mod"
    printf 'fake-macos-%s\n' "$architecture" > "$temporary/expected-binary"
    cmp "$temporary/expected-binary" "$package/starbridge"
    grep -Fx "version=$normalized" "$package/BUILD.txt" > /dev/null
    grep -Fx "arch=$architecture" "$package/BUILD.txt" > /dev/null
    grep -Fx "go_version=$FAKE_GO_TOOLCHAIN" "$package/BUILD.txt" > /dev/null
    grep -Fx 'developer_protocol=dev-1' "$package/BUILD.txt" > /dev/null
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

# Use a separate local repository so ignored/untracked fixtures and index edits
# cannot touch the caller's checkout. Seed from tracked working-tree contents,
# including the packaging changes under test rather than only committed HEAD.
git -C "$root" ls-files -z > "$temporary/fixture-files"
COPYFILE_DISABLE=1 tar -cf "$temporary/fixture.tar" -C "$root" --null --no-recursion -T "$temporary/fixture-files"
fixture="$temporary/source"
mkdir -p "$fixture"
tar -xf "$temporary/fixture.tar" -C "$fixture"
printf 'Will be removed from the index.\n' > "$fixture/docs/removed fixture.md"
mkdir -p "$fixture/guest/windows/developer/workers"
printf '# Tracked nested worker fixture.\n' > "$fixture/guest/windows/developer/workers/package-fixture.ps1"
git -C "$fixture" -c init.defaultBranch=package-test init -q
git -C "$fixture" add -A
git -C "$fixture" -c commit.gpgsign=false -c core.hooksPath=/dev/null \
  -c user.name='Package test' -c user.email='package-test@example.invalid' commit -qm fixture

# Refuse both an incorrectly selected toolchain and an incorrectly built binary.
# Check the real workflow contract without compiling or installing any Go version.
if FAKE_GO_TOOLCHAIN=go0.0.0 FAKE_GO_VERSION=1.2.6 FAKE_GO_LOG="$temporary/wrong-toolchain-builds" \
    STARBRIDGE_DIST_DIR="$temporary/wrong-toolchain-dist" "$fixture/scripts/package-release.sh" 1.2.6 > "$temporary/wrong-toolchain-output" 2>&1; then
  printf '%s\n' 'Wrong release toolchain was accepted.' >&2
  exit 1
fi
[ ! -e "$temporary/wrong-toolchain-builds" ]
grep -F 'Release builds require' "$temporary/wrong-toolchain-output" > /dev/null
if FAKE_GO_BINARY_VERSION=go0.0.0 FAKE_GO_VERSION=1.2.6 FAKE_GO_LOG="$temporary/wrong-binary-builds" \
    STARBRIDGE_DIST_DIR="$temporary/wrong-binary-dist" "$fixture/scripts/package-release.sh" 1.2.6 > "$temporary/wrong-binary-output" 2>&1; then
  printf '%s\n' 'Wrong binary toolchain metadata was accepted.' >&2
  exit 1
fi
grep -F 'release toolchain must be' "$temporary/wrong-binary-output" > /dev/null
[ ! -e "$temporary/wrong-binary-dist/starbridge_1.2.6_darwin_arm64.tar.gz" ]
for notice_part in summary component runtime; do
  awk -v part="$notice_part" '
    part == "summary" && /^本清单根据/ { sub(/Go [0-9]+\.[0-9]+\.[0-9]+/, "Go 0.0.0") }
    part == "component" && /^- Go runtime and standard library / { sub(/go[0-9]+\.[0-9]+\.[0-9]+/, "go0.0.0") }
    part == "runtime" && /^## Go runtime and standard library / { sub(/go[0-9]+\.[0-9]+\.[0-9]+/, "go0.0.0") }
    { print }
  ' "$root/THIRD_PARTY_NOTICES.md" > "$fixture/THIRD_PARTY_NOTICES.md"
  if FAKE_GO_VERSION=1.2.6 FAKE_GO_LOG="$temporary/wrong-notice-builds" STARBRIDGE_DIST_DIR="$temporary/wrong-notice-dist" \
      "$fixture/scripts/package-release.sh" 1.2.6 > "$temporary/wrong-notice-output" 2>&1; then
    printf 'Mismatched notice %s was accepted.\n' "$notice_part" >&2
    exit 1
  fi
  [ ! -e "$temporary/wrong-notice-builds" ]
  grep -F 'THIRD_PARTY_NOTICES.md must consistently describe' "$temporary/wrong-notice-output" > /dev/null
done
cp "$root/THIRD_PARTY_NOTICES.md" "$fixture/THIRD_PARTY_NOTICES.md"
[ -z "$(git -C "$fixture" status --porcelain)" ]
printf 'fixture-only ignored secret\n' > "$fixture/docs/.DS_Store"
printf '{"fixture-only-local-secret":true}\n' > "$fixture/examples/recycle-policy.local.json"
printf 'fixture-only ignored test data\n' > "$fixture/tests/windows/.DS_Store"
git -C "$fixture" check-ignore docs/.DS_Store examples/recycle-policy.local.json tests/windows/.DS_Store > /dev/null
[ -z "$(git -C "$fixture" status --porcelain)" ]

check_source_snapshot() {
  snapshot_version=$1
  snapshot_dirty=$2
  snapshot_distribution="$temporary/snapshot-$snapshot_version"
  FAKE_GO_VERSION="$snapshot_version" FAKE_GO_LOG="$temporary/snapshot-builds-$snapshot_version" \
    STARBRIDGE_DIST_DIR="$snapshot_distribution" "$fixture/scripts/package-release.sh" "$snapshot_version" > /dev/null
  for snapshot_arch in arm64 amd64; do
    snapshot_name="starbridge_${snapshot_version}_darwin_${snapshot_arch}"
    mkdir -p "$temporary/snapshot-unpacked-$snapshot_version-$snapshot_arch"
    tar -xzf "$snapshot_distribution/$snapshot_name.tar.gz" -C "$temporary/snapshot-unpacked-$snapshot_version-$snapshot_arch"
    snapshot_package="$temporary/snapshot-unpacked-$snapshot_version-$snapshot_arch/$snapshot_name"
    grep -Fx "dirty=$snapshot_dirty" "$snapshot_package/BUILD.txt" > /dev/null
    for local_file in docs/.DS_Store examples/recycle-policy.local.json tests/windows/.DS_Store \
        docs/local-secrets.json examples/local-notes.txt tests/windows/local-command.ps1 tests/fixtures/local.json; do
      if [ -e "$snapshot_package/$local_file" ]; then
        printf 'Local ignored/untracked file leaked into package: %s\n' "$local_file" >&2
        exit 1
      fi
    done
    cmp "$fixture/docs/USAGE.md" "$snapshot_package/docs/USAGE.md"
    cmp "$fixture/guest/windows/desktop-bridge.ps1" "$snapshot_package/guest/windows/desktop-bridge.ps1"
    cmp "$fixture/guest/windows/developer/workers/package-fixture.ps1" "$snapshot_package/guest/windows/developer/workers/package-fixture.ps1"
  done
}
check_source_snapshot 1.2.3 false
printf 'fixture-only untracked secret\n' > "$fixture/docs/local-secrets.json"
printf 'fixture-only untracked note\n' > "$fixture/examples/local-notes.txt"
printf 'fixture-only untracked command\n' > "$fixture/tests/windows/local-command.ps1"
printf 'fixture-only untracked data\n' > "$fixture/tests/fixtures/local.json"
printf '# Never package untracked guest extensions.\n' > "$fixture/guest/windows/developer/local.ps1"
check_source_snapshot 1.2.4 true
[ ! -e "$snapshot_package/guest/windows/developer/local.ps1" ]
printf '\nTracked working-tree documentation change.\n' >> "$fixture/docs/USAGE.md"
printf '\n# Tracked working-tree helper change.\n' >> "$fixture/guest/windows/desktop-bridge.ps1"
printf 'New indexed file.\n' > "$fixture/docs/indexed space.txt"
git -C "$fixture" add 'docs/indexed space.txt'
git -C "$fixture" rm -q 'docs/removed fixture.md'
check_source_snapshot 1.2.5 true
cmp "$fixture/docs/indexed space.txt" "$snapshot_package/docs/indexed space.txt"
[ ! -e "$snapshot_package/docs/removed fixture.md" ]

expect_snapshot_failure() {
  if FAKE_GO_VERSION=1.2.6 FAKE_GO_LOG="$temporary/unsafe-builds" STARBRIDGE_DIST_DIR="$temporary/unsafe-dist" \
      "$fixture/scripts/package-release.sh" 1.2.6 > "$temporary/unsafe-output" 2>&1; then
    printf '%s\n' 'Missing or non-regular tracked file was accepted.' >&2
    exit 1
  fi
  [ ! -e "$temporary/unsafe-builds" ]
}
rm "$fixture/docs/USAGE.md"
expect_snapshot_failure
printf 'fixture-only external secret\n' > "$temporary/external-secret"
ln -s "$temporary/external-secret" "$fixture/docs/USAGE.md"
expect_snapshot_failure
rm "$fixture/docs/USAGE.md"
mkfifo "$fixture/docs/USAGE.md"
expect_snapshot_failure

# Execute the actual workflow publish block with a fake gh, checking the full
# argument vector without credentials, network access or a GitHub release.
awk '
  /^      - name: Publish tested tag assets$/ { publish = 1; next }
  publish && /^        run: \|$/ { commands = 1; next }
  commands && /^          / { sub(/^          /, ""); print; next }
  commands { exit }
' "$root/.github/workflows/release.yml" > "$temporary/publish-step.sh"
[ -s "$temporary/publish-step.sh" ]
cat > "$temporary/bin/gh" <<'FAKE_GH'
#!/bin/sh
set -eu
printf '%s\n' "$@" > "$FAKE_GH_LOG"
printf '%s\n' called >> "$FAKE_GH_CALLS"
capture_notes=false
for argument do
  if [ "$capture_notes" = true ]; then
    cat "$argument" > "$FAKE_GH_NOTES"
    capture_notes=false
  elif [ "$argument" = --notes-file ]; then
    capture_notes=true
  fi
done
FAKE_GH
chmod +x "$temporary/bin/gh"
publish_root="$temporary/publish with spaces"
mkdir -p "$publish_root/dist" "$publish_root/docs/releases"
touch "$publish_root/dist/amd64 fixture.tar.gz" "$publish_root/dist/arm64 fixture.tar.gz" \
  "$publish_root/dist/checksums.txt"
printf 'Other version notes must not be selected.\n' > "$publish_root/docs/releases/other-version.md"
cat > "$temporary/release-notes" <<'RELEASE_NOTES'
# Synthetic release notes

发布说明保留 Unicode、空格和原始换行。
Literal shell text: $(must-not-run) `must-not-run` "$not_an_environment_value".
RELEASE_NOTES
publish_checks=0
check_publish() {
  release_tag=$1
  release_kind=$2
  notes_kind=$3
  notes_path="docs/releases/$release_tag.md"
  case "$notes_kind" in
    missing) rm -f "$publish_root/$notes_path" ;;
    provided) cp "$temporary/release-notes" "$publish_root/$notes_path" ;;
    empty) : > "$publish_root/$notes_path" ;;
  esac
  rm -f "$temporary/publish-args" "$temporary/publish-calls" "$temporary/published-notes"
  (cd "$publish_root" && RELEASE_TAG="$release_tag" FAKE_GH_LOG="$temporary/publish-args" \
    FAKE_GH_CALLS="$temporary/publish-calls" FAKE_GH_NOTES="$temporary/published-notes" \
    sh "$temporary/publish-step.sh")
  {
    printf '%s\n' release create "$release_tag"
    if [ "$release_kind" = pre ]; then printf '%s\n' --prerelease; fi
    printf '%s\n' --verify-tag
    if [ "$notes_kind" = missing ]; then
      printf '%s\n' --generate-notes
    else
      printf '%s\n' --notes-file "$notes_path"
    fi
    printf '%s\n' --title "Starbridge $release_tag" \
      'dist/amd64 fixture.tar.gz' 'dist/arm64 fixture.tar.gz' dist/checksums.txt
  } > "$temporary/expected-publish-args"
  cmp "$temporary/expected-publish-args" "$temporary/publish-args"
  printf 'called\n' > "$temporary/expected-publish-calls"
  cmp "$temporary/expected-publish-calls" "$temporary/publish-calls"
  if [ "$notes_kind" = missing ]; then
    [ ! -e "$temporary/published-notes" ]
  else
    cmp "$publish_root/$notes_path" "$temporary/published-notes"
  fi
  publish_checks=$((publish_checks + 1))
}
for release_case in \
    v1.2.3:stable v1.2.3-rc.1:pre v1.2.3-alpha.beta.1:pre \
    v1.2.3-0:pre v1.2.3--alpha:pre v1.2.3+build.7:stable \
    v1.2.3+build-alpha:stable v1.2.3+-:stable v1.2.3-rc.1+build-alpha:pre; do
  for notes_kind in missing provided empty; do
    check_publish "${release_case%:*}" "${release_case##*:}" "$notes_kind"
  done
done
# Isolate shell quoting from the upstream SemVer/tag validation. This synthetic
# value is never sent to GitHub or accepted as a package version.
for notes_kind in missing provided empty; do
  check_publish 'synthetic tag with spaces' stable "$notes_kind"
done
printf 'Release packaging checks passed: %s version/argument cases; paired archives, checksums and documentation links verified.\n' "$checks"
printf '%s\n' 'Tracked-file packaging checks passed: ignored/untracked exclusion, dirty changes, index edits and unsafe-file rejection.'
printf '%s\n' 'Release toolchain checks passed: binary metadata and all three notice version declarations match; five mismatch cases rejected.'
printf 'Release publication checks passed: %s argument/notes cases using one workflow create call; exact, empty and absent notes and paths with spaces verified.\n' "$publish_checks"
