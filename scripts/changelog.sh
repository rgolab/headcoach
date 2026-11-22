#!/bin/sh
# Print the section of CHANGELOG.md for one version, without its heading: the notes of a release.
# Usage: scripts/changelog.sh v1.2.3 [CHANGELOG.md]
version="${1#v}"
file="${2:-CHANGELOG.md}"
[ -n "$version" ] || { echo "usage: $0 <version> [changelog]" >&2; exit 2; }
notes=$(awk -v v="$version" '
	/^## \[/ { if (found) exit; found = index($0, "## [" v "]") == 1; next }
	/^\[[^]]*\]: / { next }
	found
' "$file" | sed -e '/./,$!d')
[ -n "$notes" ] || { echo "no section for $version in $file" >&2; exit 1; }
printf '%s\n' "$notes"
