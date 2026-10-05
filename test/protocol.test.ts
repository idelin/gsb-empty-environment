import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { Center } from "../src/center.ts";
import { EdgeNode } from "../src/node.ts";
import { Ledger } from "../src/ledger.ts";
import type { Grant, Message } from "../src/types.ts";

function tmp(): string {
  return mkdtempSync(join(tmpdir(), "quota-test-"));
}

function makeCenter(quota = 100, windowLength = 10): { center: Center; path: string } {
  const path = join(tmp(), "center.ledger.jsonl");
  return { center: new Center({ windowQuota: quota, windowLength, ledger: new Ledger(path) }), path };
}

function makeNode(nodeId = "A", windowLength = 10): { node: EdgeNode; path: string } {
  const path = join(tmp(), `node-${nodeId}.ledger.jsonl`);
  return { node: new EdgeNode({ nodeId, windowLength, ledger: new Ledger(path) }), path };
}

function grantFor(nodeId: string, amount: number, expiryTick: number, grantId = "g0"): Grant {
  return { grantId, windowId: 0, nodeId, amount, issuedAtTick: 0, expiryTick };
}

test("center never issues beyond window quota", () => {
  const { center } = makeCenter(100, 10);
  const m1 = center.receive({ kind: "BorrowRequest", requestId: "r1", nodeId: "A", amount: 60 }, 0);
  assert.equal(m1[0].kind, "GrantMsg");
  const m2 = center.receive({ kind: "BorrowRequest", requestId: "r2", nodeId: "B", amount: 50 }, 1);
  assert.deepEqual(m2[0], { kind: "BorrowReject", requestId: "r2", nodeId: "B", reason: "window_quota_exhausted" });
  assert.equal(center.spent(0), 60);
});

test("returned quota is reissuable only after center confirms the return", () => {
  const { center } = makeCenter(100, 10);
  center.receive({ kind: "BorrowRequest", requestId: "r1", nodeId: "A", amount: 80 }, 0);
  // No return yet: even much later in the window, quota stays locked.
  const rejected = center.receive({ kind: "BorrowRequest", requestId: "r2", nodeId: "B", amount: 30 }, 8);
  assert.equal(rejected[0].kind, "BorrowReject");
  // Confirm a return of 50, then 30 fits again.
  const ack = center.receive({ kind: "ReturnReport", returnId: "A-r0", nodeId: "A", grantId: "g0", amount: 50 }, 8);
  assert.deepEqual(ack[0], { kind: "ReturnAck", returnId: "A-r0", nodeId: "A", grantId: "g0", acceptedAmount: 50 });
  assert.equal(center.spent(0), 30);
  const granted = center.receive({ kind: "BorrowRequest", requestId: "r3", nodeId: "B", amount: 30 }, 9);
  assert.equal(granted[0].kind, "GrantMsg");
  assert.equal(center.spent(0), 60);
});

test("center never reclaims quota on timeout", () => {
  const { center } = makeCenter(100, 10);
  center.receive({ kind: "BorrowRequest", requestId: "r1", nodeId: "A", amount: 90 }, 0);
  // Grant is lost / node silent. Ticks pass; center must not infer the quota is unused.
  for (let tick = 1; tick <= 9; tick++) {
    const out = center.receive({ kind: "BorrowRequest", requestId: `rx-${tick}`, nodeId: "B", amount: 20 }, tick);
    assert.equal(out[0].kind, "BorrowReject", `tick ${tick}`);
  }
  assert.equal(center.spent(0), 90);
});

test("duplicate returns are idempotent and over-returns are clamped", () => {
  const { center } = makeCenter(100, 10);
  center.receive({ kind: "BorrowRequest", requestId: "r1", nodeId: "A", amount: 60 }, 0);
  const a1 = center.receive({ kind: "ReturnReport", returnId: "A-r0", nodeId: "A", grantId: "g0", amount: 25 }, 5);
  assert.equal((a1[0] as { acceptedAmount: number }).acceptedAmount, 25);
  assert.equal(center.spent(0), 35);
  // Exact duplicate: same ack, no state change.
  const a2 = center.receive({ kind: "ReturnReport", returnId: "A-r0", nodeId: "A", grantId: "g0", amount: 25 }, 6);
  assert.equal((a2[0] as { acceptedAmount: number }).acceptedAmount, 25);
  assert.equal(center.spent(0), 35);
  // Over-return beyond the outstanding amount is clamped.
  const a3 = center.receive({ kind: "ReturnReport", returnId: "A-r1", nodeId: "A", grantId: "g0", amount: 999 }, 7);
  assert.equal((a3[0] as { acceptedAmount: number }).acceptedAmount, 35);
  assert.equal(center.spent(0), 0);
});

test("borrow retries are idempotent: same request id yields the same grant", () => {
  const { center } = makeCenter(100, 10);
  const first = center.receive({ kind: "BorrowRequest", requestId: "r1", nodeId: "A", amount: 40 }, 0);
  const grantId = (first[0] as { grant: Grant }).grant.grantId;
  // GrantMsg lost; node retransmits the same borrow request.
  const retry = center.receive({ kind: "BorrowRequest", requestId: "r1", nodeId: "A", amount: 40 }, 3);
  assert.equal((retry[0] as { grant: Grant }).grant.grantId, grantId);
  assert.equal(center.spent(0), 40, "no double issue");
});

test("node consume is idempotent per request id; different cost is a conflict", () => {
  const { node } = makeNode();
  node.receive({ kind: "GrantMsg", grant: grantFor("A", 20, 9), borrowRequestId: "b0" }, 0);
  const first = node.consume("req-1", 5, 1);
  assert.equal(first.result.status, "accepted");
  assert.equal(node.available(1), 15);
  const replay = node.consume("req-1", 5, 2);
  assert.equal(replay.result.status, "accepted");
  assert.equal(replay.result.replay, true);
  assert.equal(node.available(2), 15, "idempotent replay must not deduct twice");
  const conflict = node.consume("req-1", 7, 3);
  assert.equal(conflict.result.status, "conflict");
  assert.equal(node.available(3), 15, "conflict must not deduct");
});

test("grant is spendable at its expiry tick but not one tick later", () => {
  const { node } = makeNode();
  node.receive({ kind: "GrantMsg", grant: grantFor("A", 10, 9), borrowRequestId: "b0" }, 0);
  const atDeadline = node.consume("req-d", 4, 9);
  assert.equal(atDeadline.result.status, "accepted");
  const afterDeadline = node.consume("req-e", 4, 10);
  assert.equal(afterDeadline.result.status, "rejected");
  assert.equal((afterDeadline.result as { reason: string }).reason, "insufficient_quota");
});

test("expiry sweep returns the unconsumed remainder and it stays unspendable", () => {
  const { node } = makeNode();
  node.receive({ kind: "GrantMsg", grant: grantFor("A", 10, 9), borrowRequestId: "b0" }, 0);
  node.consume("req-1", 6, 5);
  const { outbox } = node.sweep(10);
  assert.equal(outbox.length, 1);
  const report = outbox[0].msg;
  assert.equal(report.kind, "ReturnReport");
  assert.equal((report as { amount: number }).amount, 4, "only the unconsumed remainder is returned");
  const late = node.consume("req-2", 1, 10);
  assert.equal(late.result.status, "rejected");
});

test("late grant cannot revive expired budget and is returned in full", () => {
  const { node } = makeNode();
  const late = grantFor("A", 30, 5, "gL");
  const { outbox } = node.receive({ kind: "GrantMsg", grant: late, borrowRequestId: "b0" }, 8);
  assert.equal(node.available(8), 0, "late grant must not credit budget");
  assert.equal(outbox.length, 1);
  const report = outbox[0].msg;
  assert.equal(report.kind, "ReturnReport");
  assert.equal((report as { amount: number }).amount, 30, "full amount handed back");
  const spend = node.consume("req-1", 1, 8);
  assert.equal(spend.result.status, "rejected");
});

test("node restart restores state from ledger and retransmits pending returns", () => {
  const { node, path } = makeNode();
  node.receive({ kind: "GrantMsg", grant: grantFor("A", 10, 9), borrowRequestId: "b0" }, 0);
  node.consume("req-1", 6, 5);
  node.sweep(10); // creates pending return of 4

  const restored = EdgeNode.restore(path, { nodeId: "A", windowLength: 10 });
  assert.equal(restored.available(10), 0);
  // Idempotency survives the restart.
  const replay = restored.consume("req-1", 6, 10);
  assert.equal(replay.result.status, "accepted");
  assert.equal(replay.result.replay, true);
  const conflict = restored.consume("req-1", 7, 10);
  assert.equal(conflict.result.status, "conflict");
  // Pending return is retransmitted after the restart.
  const { outbox } = restored.retransmit(16);
  const returns = outbox.filter((o) => o.msg.kind === "ReturnReport");
  assert.equal(returns.length, 1);
  assert.equal((returns[0].msg as { amount: number }).amount, 4);
  // Ack stops the retransmission.
  const ret = returns[0].msg as { returnId: string };
  restored.receive({ kind: "ReturnAck", returnId: ret.returnId, nodeId: "A", grantId: "g0", acceptedAmount: 4 }, 17);
  assert.equal(restored.retransmit(30).outbox.length, 0);
});

test("center restart restores spent accounting from ledger", () => {
  const { center, path } = makeCenter(100, 10);
  center.receive({ kind: "BorrowRequest", requestId: "r1", nodeId: "A", amount: 70 }, 0);
  center.receive({ kind: "ReturnReport", returnId: "A-r0", nodeId: "A", grantId: "g0", amount: 20 }, 3);

  const restored = Center.restore(path, { windowQuota: 100, windowLength: 10 });
  assert.equal(restored.spent(0), 50);
  // Borrow idempotency survives the restart.
  const retry = restored.receive({ kind: "BorrowRequest", requestId: "r1", nodeId: "A", amount: 70 }, 4);
  assert.equal((retry[0] as { grant: Grant }).grant.grantId, "g0");
  // Duplicate return dedup survives the restart.
  const dup = restored.receive({ kind: "ReturnReport", returnId: "A-r0", nodeId: "A", grantId: "g0", amount: 20 }, 5);
  assert.equal((dup[0] as { acceptedAmount: number }).acceptedAmount, 20);
  assert.equal(restored.spent(0), 50);
  // And the quota check still binds.
  const over = restored.receive({ kind: "BorrowRequest", requestId: "r2", nodeId: "B", amount: 60 }, 6);
  assert.equal(over[0].kind, "BorrowReject");
});

test("window switch: old grant expires, new window quota is independent", () => {
  const { center } = makeCenter(100, 10);
  const { node } = makeNode();
  const granted = center.receive({ kind: "BorrowRequest", requestId: "r1", nodeId: "A", amount: 80 }, 0);
  node.receive(granted[0] as Message & { kind: "GrantMsg" }, 0);
  node.consume("req-1", 30, 5);
  // Window 0 ends at tick 9; at tick 10 the remainder is returned.
  const { outbox } = node.sweep(10);
  const report = outbox[0].msg as { returnId: string; amount: number };
  assert.equal(report.amount, 50);
  center.receive(outbox[0].msg, 11);
  // New window: full quota available again regardless of window 0.
  const again = center.receive({ kind: "BorrowRequest", requestId: "r2", nodeId: "A", amount: 100 }, 11);
  assert.equal(again[0].kind, "GrantMsg");
  assert.equal(center.spent(0), 30, "window 0 keeps only the consumed part");
  assert.equal(center.spent(1), 100);
});
