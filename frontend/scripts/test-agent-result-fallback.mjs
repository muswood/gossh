// owner: muswood | Email: mumu920@outlook.com
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";

const source = await readFile("src/lib/components/AIAssistant.svelte", "utf8");
assert.match(source, /else if \(task\.result\?\.trim\(\)\) finishAgentTaskWithReport\(task\.result/);
console.log("Agent task result fallback regression check passed");
