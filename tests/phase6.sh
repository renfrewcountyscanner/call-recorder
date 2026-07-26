#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
"$root/tests/integration.sh"
"$root/tests/aliases.sh"
"$root/tests/retention.sh"
"$root/tests/administration.sh"
"$root/tests/browser-sequential.sh"
echo 'phase 6 isolated tests passed'
