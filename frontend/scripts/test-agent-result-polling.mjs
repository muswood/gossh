// owner: muswood | Email: mumu920@outlook.com
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";

const source = await readFile("src/lib/components/AIAssistant.svelte", "utf8");
assert.match(source, /async function pollActiveAgentTask\(\)/);
assert.match(source, /setInterval\(\(\) => void pollActiveAgentTask\(\), 1500\)/);
assert.match(source, /if \(!isAgentRunning\(task\.status\)\) await hydrateAgentTask\(task\)/);
console.log("Agent task polling regression check passed");
