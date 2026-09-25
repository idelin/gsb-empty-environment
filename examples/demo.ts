/**
 * Runnable scenario demo: `npm run demo` (Node >= 23.6, or 22.6+ with
 * --experimental-strip-types). Uses a ManualClock so the timeline is
 * fully deterministic.
 */
import { ManualClock } from "../src/clock.ts";
import { StatusAnnounceQueue } from "../src/queue.ts";
import { collectStatusText } from "../src/nodeTree.ts";
import { truncateToWidth } from "../src/metrics.ts";
import { fixtureStatusTree } from "../fixtures/statusTree.ts";
import { fixtureGlyphMetrics } from "../fixtures/glyphMetrics.ts";

const clock = new ManualClock(1_000);
const log: string[] = [];
const say = (line: string) => log.push(`[t=${clock.now()}] ${line}`);

const queue = new StatusAnnounceQueue({
  clock,
  urgentQuota: 2,
  announce: (e) => say(`ANNOUNCE ${e.message.priority.padEnd(6)} "${e.text}"${e.resumed ? " (resumed)" : ""}`),
  onAck: (ackId) => say(`ACK      ${ackId}`),
  onDrop: (m, reason) => say(`DROP     "${m.text}" (${reason})`),
});

// Status text comes from the fixture node tree; width is computed from
// fixture glyph metrics — no browser layout engine involved.
const statusText = truncateToWidth(collectStatusText(fixtureStatusTree), fixtureGlyphMetrics, 6_000);
say(`measured status text: "${statusText}"`);

// 1. Polite progress updates; same merge key + same progress coalesce.
queue.enqueue({ id: "p1", entityId: "upload", version: 1, ackId: "ack-p1", ownerId: "uploader", priority: "polite", text: statusText, mergeKey: "upload", progress: 42 });
queue.enqueue({ id: "p2", entityId: "upload", version: 1, ackId: "ack-p2", ownerId: "uploader", priority: "polite", text: "上传进度 43%", mergeKey: "upload", progress: 42 });

// 2. Urgent message interrupts the in-flight polite announcement.
queue.enqueue({ id: "u1", entityId: "alert", version: 1, ackId: "ack-u1", ownerId: "alerts", priority: "urgent", text: "下载失败" });
clock.advance(30);
queue.finishCurrent(); // urgent completes -> paused polite message resumes

// 3. Retry of an already-acked delivery is ignored (no double ack).
queue.enqueue({ id: "u1-retry", entityId: "alert", version: 1, ackId: "ack-u1", ownerId: "alerts", priority: "urgent", text: "下载失败" });
queue.finishCurrent(); // finishes the resumed polite message

// 4. Version correction: a newer entity version supersedes the queued older one.
queue.enqueue({ id: "v1", entityId: "sync", version: 1, ackId: "ack-v1", ownerId: "sync", priority: "polite", text: "同步 v1" });
queue.enqueue({ id: "v2", entityId: "sync", version: 2, ackId: "ack-v2", ownerId: "sync", priority: "polite", text: "同步 v2" });

// 5. Entity retraction invalidates queued messages of that entity.
queue.enqueue({ id: "r1", entityId: "print", version: 1, ackId: "ack-r1", ownerId: "printer", priority: "polite", text: "打印排队中" });
queue.retractEntity("print");

// 6. Expiry: this message goes stale while waiting behind the others.
queue.enqueue({ id: "x1", entityId: "promo", version: 1, ackId: "ack-x1", ownerId: "promo", priority: "polite", text: "限时提示", ttlMs: 50 });
clock.advance(100);

// 7. Component unmount cleans up its pending items.
queue.enqueue({ id: "w1", entityId: "widget", version: 1, ackId: "ack-w1", ownerId: "widget", priority: "polite", text: "小部件已更新" });
queue.unmount("widget");

// 8. Fairness: a burst of urgent traffic cannot starve the polite message.
queue.enqueue({ id: "tail", entityId: "tail", version: 1, ackId: "ack-tail", ownerId: "app", priority: "polite", text: "后台任务完成" });
for (let i = 1; i <= 4; i += 1) {
  queue.enqueue({ id: `ub${i}`, entityId: `burst${i}`, version: 1, ackId: `ack-ub${i}`, ownerId: "alerts", priority: "urgent", text: `紧急告警 ${i}` });
}

queue.runToCompletion();

say(`idle=${queue.idle} pending=${queue.pendingCount}`);
console.log(log.join("\n"));
