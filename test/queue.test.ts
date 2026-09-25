import { test } from "node:test";
import assert from "node:assert/strict";
import { ManualClock } from "../src/clock.ts";
import { StatusAnnounceQueue } from "../src/queue.ts";
import type { AnnounceEvent, DropReason, StatusMessage } from "../src/queue.ts";

function msg(partial: Partial<StatusMessage> & { id: string }): StatusMessage {
  return {
    entityId: "entity-1",
    version: 1,
    ackId: `ack-${partial.id}`,
    ownerId: "owner-1",
    priority: "polite",
    text: `text-${partial.id}`,
    ...partial,
  };
}

function harness(options: { urgentQuota?: number } = {}) {
  const clock = new ManualClock(1_000);
  const announced: AnnounceEvent[] = [];
  const acks: string[] = [];
  const drops: Array<{ id: string; reason: DropReason }> = [];
  const queue = new StatusAnnounceQueue({
    clock,
    announce: (event) => announced.push(event),
    onAck: (ackId) => acks.push(ackId),
    onDrop: (m, reason) => drops.push({ id: m.id, reason }),
    urgentQuota: options.urgentQuota,
  });
  const texts = () => announced.map((e) => e.message.id);
  return { clock, queue, announced, acks, drops, texts };
}

test("polite messages are announced FIFO and acked on completion", () => {
  const { queue, acks, texts } = harness();
  queue.enqueue(msg({ id: "a" }));
  queue.enqueue(msg({ id: "b" }));
  queue.enqueue(msg({ id: "c" }));
  assert.deepEqual(texts(), ["a"]); // first is announced immediately
  queue.finishCurrent();
  queue.finishCurrent();
  queue.finishCurrent();
  assert.deepEqual(texts(), ["a", "b", "c"]);
  assert.deepEqual(acks, ["ack-a", "ack-b", "ack-c"]);
  assert.ok(queue.idle);
});

test("urgent interrupts polite; paused polite is re-validated and resumed", () => {
  const { queue, announced, texts } = harness();
  queue.enqueue(msg({ id: "p" }));
  queue.enqueue(msg({ id: "u", priority: "urgent" }));
  assert.deepEqual(texts(), ["p", "u"]); // p paused, u announced
  queue.finishCurrent(); // u completes
  assert.deepEqual(texts(), ["p", "u", "p"]); // p resumes
  assert.equal(announced[2]!.resumed, true);
  assert.equal(announced[0]!.resumed, false);
  queue.finishCurrent();
  assert.ok(queue.idle);
});

test("paused polite message that expires during urgent interruption is not re-announced", () => {
  const { clock, queue, drops, texts } = harness();
  queue.enqueue(msg({ id: "p", ttlMs: 100 })); // expires at t=1100
  queue.enqueue(msg({ id: "u", priority: "urgent" }));
  clock.advance(200); // t=1200, p is now stale
  queue.finishCurrent(); // u completes; p must be dropped, not resumed
  assert.deepEqual(texts(), ["p", "u"]);
  assert.deepEqual(drops, [{ id: "p", reason: "expired" }]);
  assert.ok(queue.idle);
});

test("canceling a pending message prevents its announcement", () => {
  const { queue, drops, texts } = harness();
  queue.enqueue(msg({ id: "a" }));
  queue.enqueue(msg({ id: "b" }));
  assert.equal(queue.cancel("b"), true);
  queue.finishCurrent();
  assert.deepEqual(texts(), ["a"]);
  assert.deepEqual(drops, [{ id: "b", reason: "canceled" }]);
  assert.ok(queue.idle);
});

test("canceling the in-flight message stops it without ack", () => {
  const { queue, acks, drops } = harness();
  queue.enqueue(msg({ id: "a" }));
  queue.enqueue(msg({ id: "b" }));
  assert.equal(queue.cancel("ack-a"), true); // cancel by ackId
  assert.deepEqual(drops, [{ id: "a", reason: "canceled" }]);
  queue.finishCurrent(); // completes b (pumped after cancel)
  assert.deepEqual(acks, ["ack-b"]);
  assert.ok(queue.idle);
});

test("retry delivery never double-acks", () => {
  const { queue, acks, texts } = harness();
  const m = msg({ id: "m" });
  queue.enqueue(m);
  assert.equal(queue.enqueue(m), "duplicate-ack"); // retry while in-flight
  queue.finishCurrent();
  assert.equal(queue.enqueue(m), "duplicate-ack"); // retry after completion
  assert.equal(queue.finishCurrent(), false); // nothing left to finish
  assert.deepEqual(acks, ["ack-m"]);
  assert.deepEqual(texts(), ["m"]);
});

test("version correction supersedes older queued and in-flight versions", () => {
  const { queue, drops, texts } = harness();
  queue.enqueue(msg({ id: "v1a", version: 1 }));
  queue.enqueue(msg({ id: "v1b", version: 1 }));
  queue.enqueue(msg({ id: "v2", version: 2 }));
  assert.deepEqual(texts(), ["v1a", "v2"]);
  assert.deepEqual(drops, [
    { id: "v1a", reason: "superseded" },
    { id: "v1b", reason: "superseded" },
  ]);
  // an older version arriving late is stale and never queued
  assert.equal(queue.enqueue(msg({ id: "v1late", version: 1 })), "dropped-stale");
  queue.finishCurrent();
  assert.ok(queue.idle);
});

test("entity retraction invalidates queued messages and blocks new ones", () => {
  const { queue, drops, texts } = harness();
  queue.enqueue(msg({ id: "r1", entityId: "file-1" }));
  queue.enqueue(msg({ id: "r2", entityId: "file-1" }));
  queue.enqueue(msg({ id: "ok", entityId: "file-2" }));
  assert.equal(queue.retractEntity("file-1"), 2);
  assert.deepEqual(drops, [
    { id: "r1", reason: "retracted" },
    { id: "r2", reason: "retracted" },
  ]);
  assert.equal(queue.enqueue(msg({ id: "r3", entityId: "file-1" })), "dropped-retracted");
  queue.runToCompletion();
  assert.deepEqual(texts(), ["r1", "ok"]);
});

test("same merge key and same progress coalesce, keeping the latest", () => {
  const { queue, drops, texts } = harness();
  queue.enqueue(msg({ id: "m1", mergeKey: "upload", progress: 50, text: "50%" }));
  assert.equal(
    queue.enqueue(msg({ id: "m2", mergeKey: "upload", progress: 50, text: "50% (new)" })),
    "merged",
  );
  assert.deepEqual(drops, [{ id: "m1", reason: "superseded" }]);
  // different progress does not merge
  assert.equal(
    queue.enqueue(msg({ id: "m3", mergeKey: "upload", progress: 75, text: "75%" })),
    "queued",
  );
  queue.runToCompletion();
  assert.deepEqual(texts(), ["m1", "m2", "m3"]);
});

test("urgent traffic cannot permanently starve polite messages", () => {
  const { queue, announced, texts } = harness({ urgentQuota: 2 });
  queue.enqueue(msg({ id: "p" }));
  for (let i = 1; i <= 5; i += 1) {
    queue.enqueue(msg({ id: `u${i}`, priority: "urgent", entityId: `e${i}` }));
  }
  queue.runToCompletion();
  // quota = 2: after two consecutive urgents the paused polite message resumes
  assert.deepEqual(texts(), ["p", "u1", "u2", "p", "u3", "u4", "u5"]);
  const resumeIndex = announced.findIndex((e) => e.message.id === "p" && e.resumed);
  assert.ok(resumeIndex > 0 && resumeIndex < announced.length - 1);
  assert.ok(queue.idle);
});

test("unmounting a component discards its pending announcements only", () => {
  const { queue, drops, texts } = harness();
  queue.enqueue(msg({ id: "a1", ownerId: "comp-a" }));
  queue.enqueue(msg({ id: "a2", ownerId: "comp-a" }));
  queue.enqueue(msg({ id: "b1", ownerId: "comp-b" }));
  assert.equal(queue.unmount("comp-a"), 2);
  assert.deepEqual(drops, [
    { id: "a1", reason: "unmounted" },
    { id: "a2", reason: "unmounted" },
  ]);
  queue.runToCompletion();
  assert.deepEqual(texts(), ["a1", "b1"]);
});

test("expired pending messages are dropped, never announced", () => {
  const { clock, queue, drops, texts } = harness();
  queue.enqueue(msg({ id: "a" }));
  queue.enqueue(msg({ id: "b", ttlMs: 100 })); // expires at t=1100
  clock.advance(200);
  queue.finishCurrent();
  assert.deepEqual(texts(), ["a"]);
  assert.deepEqual(drops, [{ id: "b", reason: "expired" }]);
  assert.ok(queue.idle);
});

test("liveness: every live message is eventually announced and acked under fair scheduling", () => {
  const { clock, queue, announced, acks, drops } = harness({ urgentQuota: 3 });
  const live: string[] = [];
  for (let i = 0; i < 6; i += 1) {
    const id = `u${i}`;
    queue.enqueue(msg({ id, priority: "urgent", entityId: `eu${i}` }));
    live.push(id);
  }
  for (let i = 0; i < 4; i += 1) {
    const id = `p${i}`;
    queue.enqueue(msg({ id, entityId: `ep${i}`, ttlMs: i === 3 ? 10 : undefined }));
    if (i < 3) live.push(id);
  }
  clock.advance(50); // p3 expires while queued
  queue.runToCompletion();
  const announcedIds = announced.map((e) => e.message.id);
  for (const id of live) {
    assert.equal(announcedIds.filter((x) => x === id).length, 1, `${id} announced exactly once`);
  }
  assert.equal(announcedIds.includes("p3"), false); // stale message never announced
  assert.deepEqual(drops, [{ id: "p3", reason: "expired" }]);
  assert.equal(new Set(acks).size, live.length);
  assert.equal(acks.length, live.length); // no duplicate acks
  assert.ok(queue.idle);
});
