// owner: muswood | Email: mumu920@outlook.com
import assert from "node:assert/strict";
import {
  remoteTargetKey,
  pruneConversationRecords,
  migrateLegacyConversations,
  summarizeConversationForAgent,
} from "../src/lib/aiConversations.ts";

const messages = [{ role: "user", content: "检查磁盘", timestamp: Date.now() }];
assert.equal(remoteTargetKey({ type: "ssh", host: "db", port: 22, username: "ops" }), "ssh:db:22:ops");
assert.notEqual(remoteTargetKey({ type: "ssh", host: "db", port: 22, username: "root" }), "ssh:db:22:ops");
const now = Date.now();
const expiredMessages = [{ ...messages[0], timestamp: now - 8 * 86400000 }];
const expired = { id: "old", title: "old", messages: expiredMessages, createdAt: now - 8 * 86400000, updatedAt: now - 8 * 86400000 };
const fresh = { id: "fresh", title: "fresh", messages, createdAt: now, updatedAt: now };
assert.equal(pruneConversationRecords([expired, fresh], 7, now).length, 1);
assert.deepEqual(migrateLegacyConversations({ "terminal:old": messages })[0].messages, messages);
const oldUserMessage = { role: "user", content: "旧目标：" + "x".repeat(3000), timestamp: now - 3000 };
const summary = summarizeConversationForAgent([
  oldUserMessage,
  { role: "assistant", content: "旧结论：" + "y".repeat(3000), timestamp: now - 2000 },
  { role: "user", content: "当前要继续检查磁盘", timestamp: now - 1000 },
]);
assert.equal(summary.length, 1);
assert.ok(summary[0].content.includes("会话历史摘要"));
assert.ok(summary[0].content.includes("当前要继续检查磁盘"));
assert.ok(summary[0].content.length < 8000);
assert.notEqual(summary[0].content, [oldUserMessage, { role: "assistant", content: "旧结论：" + "y".repeat(3000), timestamp: now - 2000 }, { role: "user", content: "当前要继续检查磁盘", timestamp: now - 1000 }].map(item => item.content).join("\n"));
console.log("AI conversation regression checks passed");
