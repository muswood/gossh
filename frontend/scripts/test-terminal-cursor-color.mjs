// owner: muswood | Email: mumu920@outlook.com
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";

const files = Object.fromEntries(await Promise.all([
  ["stores", "src/lib/stores.ts"],
  ["settings", "src/lib/components/SettingsPage.svelte"],
  ["terminal", "src/lib/components/TerminalPanel.svelte"],
  ["serial", "src/lib/components/SerialTerminal.svelte"],
].map(async ([name, path]) => [name, await readFile(path, "utf8")])));

assert.match(files.stores, /terminalCursorColor = persistedWritable<string>\(/);
assert.match(files.settings, /terminalCursorColor/);
assert.match(files.settings, /type="color"/);
assert.match(files.terminal, /cursor:\s*\$terminalCursorColor/);
assert.match(files.serial, /cursor:\s*\$terminalCursorColor/);
console.log("Terminal cursor color regression checks passed");
