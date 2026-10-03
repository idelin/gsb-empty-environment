import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { Scheduler } from "../src/scheduler.ts";
import { Transport } from "../src/transport.ts";
import { Coordinator } from "../src/coordinator.ts";
import { Participant } from "../src/participant.ts";
import { AuditLog } from "../src/audit.ts";
import { Wal } from "../src/wal.ts";
import type { WalRecord } from "../src/types.ts";

const OPTS = { prepareTimeout: 10, retryInterval: 5, maxRetries: 3 };

function makeWorld(participantIds: string[] = ["p1", "p2", "p3"]) {
  const dir = mkdtempSync(join(tmpdir(), "2pc-test-"));
  const scheduler = new Scheduler();
  const transport = new Transport(scheduler, 1);
  const audit = new AuditLog(join(dir, "audit.log"));
  const participants = new Map(
    participantIds.map((id) => [
      id,
      new Participant(id, transport, join(dir, `${id}.wal`)),
    ]),
  );
  const newCoordinator = () =>
    new Coordinator(
      "coord",
      transport,
      scheduler,
      join(dir, "coord.wal"),
      audit,
      OPTS,
    );
  const coord = newCoordinator();
  const walRecords = () => new Wal<WalRecord>(join(dir, "coord.wal")).replay();
  return { dir, scheduler, transport, audit, participants, coord, newCoordinator, walRecords };
}

test("happy path: all YES votes lead to COMMIT and END record", () => {
  const w = makeWorld();
  w.coord.beginTransaction("txn-1", ["p1", "p2", "p3"]);
  w.scheduler.run();

  assert.equal(w.coord.stateOf("txn-1"), "COMMITTED");
  for (const p of w.participants.values()) {
    assert.equal(p.statusOf("txn-1"), "COMMITTED");
  }
  const ops = w.walRecords().map((r) => r.op);
  assert.deepEqual(ops.filter((op) => op === "END"), ["END"]);
});

test("a single NO vote aborts the transaction everywhere", () => {
  const w = makeWorld();
  w.participants.set(
    "p2",
    new Participant("p2", w.transport, join(w.dir, "p2.wal"), () => "NO"),
  );
  w.coord.beginTransaction("txn-1", ["p1", "p2", "p3"]);
  w.scheduler.run();

  assert.equal(w.coord.stateOf("txn-1"), "ABORTED");
  assert.equal(w.participants.get("p1")!.statusOf("txn-1"), "ABORTED");
  assert.equal(w.participants.get("p2")!.statusOf("txn-1"), "ABORTED");
});

test("participant crash during prepare triggers timeout abort", () => {
  const w = makeWorld();
  const p3 = w.participants.get("p3")!;
  p3.crash();
  w.coord.beginTransaction("txn-1", ["p1", "p2", "p3"]);
  w.scheduler.run();

  // decision ABORT is durable and applied by online participants;
  // the txn stays pending (ABORTING) while p3 is unresponsive
  assert.equal(w.coord.stateOf("txn-1"), "ABORTING");
  assert.equal(w.participants.get("p1")!.statusOf("txn-1"), "ABORTED");
  const decision = w.walRecords().find((r) => r.op === "DECISION");
  assert.deepEqual(decision, { op: "DECISION", txnId: "txn-1", decision: "ABORT" });
  const pending = w.audit.events().filter((e) => e.event === "PENDING_PARTICIPANT");
  assert.equal(pending.length, 1);
  assert.equal(pending[0].participant, "p3");

  // once p3 is back, a recovery pass completes the abort
  p3.restart();
  w.coord.recover();
  w.scheduler.run();
  assert.equal(w.coord.stateOf("txn-1"), "ABORTED");
  assert.equal(p3.statusOf("txn-1"), "ABORTED");
});

test("duplicate votes and duplicate ACKs are applied only once", () => {
  const w = makeWorld();
  w.coord.beginTransaction("txn-1", ["p1", "p2"]);
  // inject a duplicate YES vote from p1 before its real one arrives
  w.coord.receive("p1", { type: "VOTE", txnId: "txn-1", vote: "YES" });
  w.coord.receive("p1", { type: "VOTE", txnId: "txn-1", vote: "YES" });
  w.scheduler.run();

  assert.equal(w.coord.stateOf("txn-1"), "COMMITTED");
  const votes = w.walRecords().filter(
    (r) => r.op === "VOTE" && r.participant === "p1",
  );
  assert.equal(votes.length, 1);
  const acks = w.walRecords().filter(
    (r) => r.op === "ACK" && r.participant === "p1",
  );
  assert.equal(acks.length, 1);
});

test("ACCEPTANCE: crash before publishing COMMIT, recovery replays WAL, queries participants and finishes", () => {
  const w = makeWorld();
  w.coord.beginTransaction("txn-1", ["p1", "p2", "p3"]);
  // votes arrive at t=2, decision messages would land at t=3: crash at t=2.5
  w.scheduler.schedule(2.5, () => w.coord.crash());
  w.scheduler.run();

  // decision is durable, but no participant received it
  assert.equal(w.coord.stateOf("txn-1"), "COMMITTING");
  for (const p of w.participants.values()) {
    assert.equal(p.statusOf("txn-1"), "PREPARED");
  }

  // restart as a fresh process over the same WAL file
  const coord2 = w.newCoordinator();
  coord2.recover();
  w.scheduler.run();

  assert.equal(coord2.stateOf("txn-1"), "COMMITTED");
  for (const p of w.participants.values()) {
    assert.equal(p.statusOf("txn-1"), "COMMITTED");
    assert.deepEqual(p.appliedLog, [{ txnId: "txn-1", decision: "COMMIT" }]);
  }
  // recovery completed the txn via QUERY/STATUS, not by re-running the protocol
  const queries = w.transport.delivered.filter(
    (d) => d.msg.type === "QUERY" && d.from === "coord",
  );
  assert.equal(queries.length, 3);
});

test("ACCEPTANCE: unresponsive participant exhausts retries and is audited as pending; later recovery resolves it", () => {
  const w = makeWorld();
  const p3 = w.participants.get("p3")!;
  w.coord.beginTransaction("txn-1", ["p1", "p2", "p3"]);
  // p3 votes YES at t=1, then crashes before the decision arrives at t=3
  w.scheduler.schedule(2.5, () => p3.crash());
  w.scheduler.run();

  assert.equal(w.coord.stateOf("txn-1"), "COMMITTING"); // pending, not forgotten
  const pending = w.audit.events().filter((e) => e.event === "PENDING_PARTICIPANT");
  assert.equal(pending.length, 1);
  assert.equal(pending[0].participant, "p3");
  assert.equal(pending[0].txnId, "txn-1");
  assert.equal(pending[0].retries, 3);

  // p3 comes back; a recovery pass finishes the pending txn (no orphan)
  p3.restart();
  w.coord.recover();
  w.scheduler.run();

  assert.equal(w.coord.stateOf("txn-1"), "COMMITTED");
  assert.equal(p3.statusOf("txn-1"), "COMMITTED");
  assert.deepEqual(p3.appliedLog, [{ txnId: "txn-1", decision: "COMMIT" }]);
  // no duplicate audit events for the same participant
  assert.equal(w.audit.events().length, 1);
});

test("crash during PREPARING recovers via presumptive abort", () => {
  const w = makeWorld();
  w.coord.beginTransaction("txn-1", ["p1", "p2"]);
  // PREPARE delivered at t=1, votes would arrive at t=2: crash at t=1.5
  w.scheduler.schedule(1.5, () => w.coord.crash());
  w.scheduler.run();

  const coord2 = w.newCoordinator();
  coord2.recover();
  w.scheduler.run();

  assert.equal(coord2.stateOf("txn-1"), "ABORTED");
  assert.equal(w.participants.get("p1")!.statusOf("txn-1"), "ABORTED");
  assert.equal(w.participants.get("p2")!.statusOf("txn-1"), "ABORTED");
});

test("participant crash/restart replays its own WAL and answers queries", () => {
  const w = makeWorld();
  const p1 = w.participants.get("p1")!;
  w.coord.beginTransaction("txn-1", ["p1", "p2"]);
  w.scheduler.schedule(2.5, () => {
    w.coord.crash();
    p1.crash();
  });
  w.scheduler.run();

  p1.restart(); // must remember it is PREPARED from its own WAL
  assert.equal(p1.statusOf("txn-1"), "PREPARED");

  const coord2 = w.newCoordinator();
  coord2.recover();
  w.scheduler.run();

  assert.equal(coord2.stateOf("txn-1"), "COMMITTED");
  assert.equal(p1.statusOf("txn-1"), "COMMITTED");
});
