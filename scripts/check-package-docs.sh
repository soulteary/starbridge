#!/bin/sh
# Check inline Markdown link destinations in a staged/extracted distribution.
# Remote URLs and same-file fragments do not require archive files.
set -eu

if [ "$#" -ne 1 ] || [ ! -d "$1" ]; then
  printf '%s\n' 'Usage: scripts/check-package-docs.sh PACKAGE_DIRECTORY' >&2
  exit 1
fi
package=$(CDPATH='' cd -- "$1" && pwd -P)

find "$package" -type f -name '*.md' -exec awk '
  {
    remaining = $0
    while (match(remaining, /\[[^]]*\]\([^)]*\)/)) {
      end = RSTART + RLENGTH
      target = substr(remaining, RSTART, RLENGTH)
      sub(/^[^]]*\]\(/, "", target)
      sub(/\)$/, "", target)
      if (substr(target, 1, 1) == "<") {
        sub(/^</, "", target)
        sub(/>.*/, "", target)
      } else {
        sub(/[[:space:]].*/, "", target)
      }
      if (target != "") print FILENAME "\t" target
      remaining = substr(remaining, end)
    }
  }
' {} + | while IFS="$(printf '\t')" read -r document target; do
  case "$target" in
    \#*|//*|[A-Za-z][A-Za-z0-9+.-]*:*) continue ;;
  esac
  target=${target%%\#*}
  target=${target%%\?*}
  [ -n "$target" ] || continue
  case "$target" in
    /*) destination="$package$target" ;;
    *) destination="$(dirname -- "$document")/$target" ;;
  esac
  if [ ! -e "$destination" ]; then
    printf 'Missing packaged documentation target: %s -> %s\n' "$document" "$target" >&2
    exit 1
  fi
  directory=$(CDPATH='' cd -- "$(dirname -- "$destination")" && pwd -P)
  case "$directory/" in
    "$package/"*) ;;
    *) printf 'Documentation target escapes package: %s -> %s\n' "$document" "$target" >&2; exit 1 ;;
  esac
done
