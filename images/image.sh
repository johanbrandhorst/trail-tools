#!/bin/sh
# Prints the image of a stage in images/Dockerfile by its name, or with
# --tag only its tag, e.g. the Terraform version. The Makefile and CI read
# the images here, as Dependabot can update them in a Dockerfile.
#
# Usage: images/image.sh [--tag] <stage>
set -eu

tag=false
if [ "${1:-}" = "--tag" ]; then
	tag=true
	shift
fi
if [ $# -ne 1 ]; then
	echo "usage: $0 [--tag] <stage>" >&2
	exit 2
fi

image=$(awk -v name="$1" '$1 == "FROM" && $NF == name { print $2 }' "$(dirname "$0")/Dockerfile")
if [ -z "$image" ]; then
	echo "$0: no stage named $1 in images/Dockerfile" >&2
	exit 1
fi
if $tag; then
	# Drop the digest, then the repository.
	image=${image%%@*}
	image=${image##*:}
fi
echo "$image"
