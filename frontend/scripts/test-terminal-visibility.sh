#!/usr/bin/env bash
# owner: muswood | Email: mumu920@outlook.com
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
panel="$root/src/lib/components/TerminalPanel.svelte"
app="$root/src/App.svelte"

rg -q 'visible=\{isSessionDisplayed\(tab.id\)\}' "$app"
rg -q 'visible = true' "$panel"
rg -q 'function fitVisibleTerminal\(' "$panel"
rg -q 'if \(!visible \|\| !containerEl\.clientWidth \|\| !containerEl\.clientHeight\) return;' "$panel"
rg -q 'if \(!visible\) return;' "$panel"
rg -q 'if \(!visible \|\| !term \|\| !sessionId \|\| pollInFlight\) return;' "$panel"
rg -q 'setInterval\(\(\) => void readPendingOutput\(\), 100\)' "$panel"
