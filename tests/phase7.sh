#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
"$root/tests/integration.sh"
"$root/tests/aliases.sh"
"$root/tests/retention.sh"
"$root/tests/administration.sh"
"$root/tests/browser-sequential.sh"
"$root/tests/browser-acceptance.sh"
"$root/tests/accessibility.sh"
"$root/tests/screenshots.sh"
echo 'phase 7 isolated tests passed'
