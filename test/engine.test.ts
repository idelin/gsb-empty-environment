import { test, describe } from "node:test";
import assert from "node:assert/strict";
import { CepEngine } from "../src/index.ts";
import type { CepEvent, Notification } from "../src/index.ts";

const ev = (type: string, tMs: number): CepEvent => ({
  id: `${type}@${tMs}`,
  type,
  timestamp: tMs,
});

function makeEngine() {
  const engine = new CepEngine();
  engine.registerPattern({
    name: "A-then-B-no-C",
    sequence: ["A", "B"],
    forbid: ["C"],
    windowMs: 10_000,
  });
  const notifications: Notification[] = [];
  engine.subscribe((n) => notifications.push(n));
  return { engine, notifications };
}

describe("acceptance boundary", () => {
  test("A@0 then B@5 hits; late C@2 precisely retracts the hit and logs it", () => {
    const { engine, notifications } = makeEngine();

    engine.ingest(ev("A", 0));
    engine.ingest(ev("B", 5_000));

    assert.equal(notifications.length, 1);
    assert.equal(notifications[0].kind, "match");
    const hit = notifications[0] as Extract<Notification, { kind: "match" }>;
    assert.deepEqual(hit.events.map((e) => e.type), ["A", "B"]);

    // Out-of-order compensation: C@2s arrives after the hit was emitted.
    engine.ingest(ev("C", 2_000));

    assert.equal(notifications.length, 2);
    const retraction = notifications[1] as Extract<Notification, { kind: "retract" }>;
    assert.equal(retraction.kind, "retract");
    assert.equal(retraction.matchId, hit.matchId);
    assert.equal(retraction.cause.type, "C");
    assert.equal(retraction.cause.timestamp, 2_000);

    const retractLogs = engine.journal.filter((j) => j.kind === "retract");
    assert.equal(retractLogs.length, 1);
    assert.ok(retractLogs[0].message.includes(`match#${hit.matchId}`));
  });
});

describe("sliding time window", () => {
  test("B exactly at the 10s window edge still matches", () => {
    const { engine, notifications } = makeEngine();
    engine.ingest(ev("A", 0));
    engine.ingest(ev("B", 10_000));
    assert.equal(notifications.filter((n) => n.kind === "match").length, 1);
  });

  test("B beyond the 10s window does not match", () => {
    const { engine, notifications } = makeEngine();
    engine.ingest(ev("A", 0));
    engine.ingest(ev("B", 10_001));
    assert.equal(notifications.length, 0);
  });

  test("expired run is discarded as timeout and logged", () => {
    const { engine, notifications } = makeEngine();
    engine.ingest(ev("A", 0));
    engine.advanceTo(10_000);
    assert.equal(engine.journal.filter((j) => j.kind === "timeout").length, 0);
    engine.advanceTo(10_001);
    const timeouts = engine.journal.filter((j) => j.kind === "timeout");
    assert.equal(timeouts.length, 1);
    assert.ok(timeouts[0].message.includes("discarded"));
    // A B arriving after the timeout must not resurrect the run.
    engine.ingest(ev("B", 20_000));
    assert.equal(notifications.filter((n) => n.kind === "match").length, 0);
  });
});

describe("negation (no C between A and B)", () => {
  test("in-order C between A and B prevents the match", () => {
    const { engine, notifications } = makeEngine();
    engine.ingest(ev("A", 0));
    engine.ingest(ev("C", 2_000));
    engine.ingest(ev("B", 5_000));
    assert.equal(notifications.length, 0);
    assert.equal(engine.journal.filter((j) => j.kind === "invalidated").length, 1);
  });

  test("late B after an already-known C does not match (buffer scan)", () => {
    const { engine, notifications } = makeEngine();
    engine.ingest(ev("A", 0));
    engine.ingest(ev("C", 2_000));
    engine.ingest(ev("B", 5_000)); // arrives late but C is already known
    assert.equal(notifications.filter((n) => n.kind === "match").length, 0);
  });

  test("C outside the (A, B) span does not affect the match", () => {
    const { engine, notifications } = makeEngine();
    engine.ingest(ev("A", 0));
    engine.ingest(ev("B", 5_000));
    engine.ingest(ev("C", 8_000)); // after B: harmless
    assert.equal(notifications.length, 1);
    assert.equal(notifications[0].kind, "match");
  });
});

describe("out-of-order ingestion", () => {
  test("late A after already-seen B still produces the match", () => {
    const { engine, notifications } = makeEngine();
    engine.ingest(ev("B", 5_000));
    engine.ingest(ev("A", 0));
    assert.equal(notifications.filter((n) => n.kind === "match").length, 1);
  });
});

describe("multi-pattern support", () => {
  test("independent patterns match concurrently on one stream", () => {
    const { engine, notifications } = makeEngine();
    engine.registerPattern({
      name: "X-then-Y",
      sequence: ["X", "Y"],
      windowMs: 5_000,
    });
    engine.ingest(ev("A", 0));
    engine.ingest(ev("X", 1_000));
    engine.ingest(ev("B", 2_000));
    engine.ingest(ev("Y", 3_000));
    const matched = notifications
      .filter((n) => n.kind === "match")
      .map((n) => n.pattern)
      .sort();
    assert.deepEqual(matched, ["A-then-B-no-C", "X-then-Y"]);
  });

  test("nondeterminism: overlapping runs of one pattern both match", () => {
    const { engine, notifications } = makeEngine();
    engine.ingest(ev("A", 0));
    engine.ingest(ev("A", 1_000));
    engine.ingest(ev("B", 5_000));
    assert.equal(notifications.filter((n) => n.kind === "match").length, 2);
  });
});
