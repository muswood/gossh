// owner: muswood | Email: mumu920@outlook.com
import assert from "node:assert/strict";
import { highlightTerminalOutput } from "../src/lib/terminalPrivacy.ts";

const rendered = highlightTerminalOutput("OK error running 192.168.1.10 42");
assert.match(rendered, /\x1b\[38;2;192;132;252m192\.168\.1\.10\x1b\[39m/);
assert.match(rendered, /\x1b\[38;2;250;204;21m42\x1b\[39m/);

const custom = highlightTerminalOutput("deploy", [
  { id: "custom", label: "自定义", color: "#00ff00", keywords: ["deploy"] },
]);
assert.match(custom, /\x1b\[38;2;0;255;0mdeploy\x1b\[39m/);

const caseInsensitive = highlightTerminalOutput("DEPLOY deploy", [
  { id: "custom", label: "自定义", color: "#00ff00", keywords: ["deploy"] },
]);
assert.equal((caseInsensitive.match(/\x1b\[38;2;0;255;0m/g) || []).length, 2);

const customOnNumericRule = highlightTerminalOutput("job 42 deploy", [
  { id: "number", label: "数字", color: "#facc15", kind: "number", keywords: ["deploy"] },
]);
assert.match(customOnNumericRule, /\x1b\[38;2;250;204;21mdeploy\x1b\[39m/);
console.log("Terminal highlighting regression checks passed");
