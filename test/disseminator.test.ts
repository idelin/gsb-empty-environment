import assert from "node:assert/strict";
import { test } from "node:test";
import { PiggybackDisseminator } from "../src/disseminator.ts";
import { EVENT_WIRE_BYTES, type ChangeEvent } from "../src/events.ts";

const join = (nodeId: string, incarnation = 0): ChangeEvent => ({
  type: "join",
  nodeId,
  incarnation,
});

test("max rounds scale with log2(clusterSize)", () => {
  assert.equal(PiggybackDisseminator.roundsFor(2, 1), 1);
  assert.equal(PiggybackDisseminator.roundsFor(8, 1), 3);
  assert.equal(PiggybackDisseminator.roundsFor(100, 1), 7);
  assert.equal(PiggybackDisseminator.roundsFor(100), 21);
});

test("piggyback selection respects the byte budget", () => {
  const d = new PiggybackDisseminator(100);
  for (let i = 0; i < 10; i += 1) d.queueEvent(join(`n${i}`));
  const capacity = Math.floor(140 / EVENT_WIRE_BYTES);
  const picked = d.getPiggyback(140);
  assert.equal(picked.length, capacity);
  assert.equal(d.pendingCount, 10);
});

test("least-transmitted events are picked first", () => {
  const d = new PiggybackDisseminator(100);
  d.queueEvent(join("old"));
  d.getPiggyback(EVENT_WIRE_BYTES * 2);
  d.queueEvent(join("fresh"));
  const picked = d.getPiggyback(EVENT_WIRE_BYTES);
  assert.deepEqual(
    picked.map((e) => e.nodeId),
    ["fresh"],
  );
});

test("events are evicted after maxRounds transmissions", () => {
  const d = new PiggybackDisseminator(4, 1);
  assert.equal(d.roundsLimit, 2);
  d.queueEvent(join("n1"));
  assert.equal(d.getPiggyback(140).length, 1);
  assert.equal(d.pendingCount, 1);
  assert.equal(d.getPiggyback(140).length, 1);
  assert.equal(d.pendingCount, 0);
  assert.equal(d.getPiggyback(140).length, 0);
});

test("a fresher event replaces the queued one and resets its count", () => {
  const d = new PiggybackDisseminator(100);
  d.queueEvent({ type: "suspect", nodeId: "n1", incarnation: 3 });
  d.getPiggyback(140);
  d.queueEvent({ type: "alive", nodeId: "n1", incarnation: 4 });
  assert.equal(d.pendingCount, 1);
  assert.equal(d.transmitCountOf("n1"), 0);
  const [picked] = d.getPiggyback(140);
  assert.equal(picked.type, "alive");
  assert.equal(picked.incarnation, 4);
});

test("stale or duplicate events are not re-queued", () => {
  const d = new PiggybackDisseminator(100);
  d.queueEvent({ type: "alive", nodeId: "n1", incarnation: 5 });
  d.getPiggyback(140);
  d.queueEvent({ type: "alive", nodeId: "n1", incarnation: 5 });
  d.queueEvent({ type: "suspect", nodeId: "n1", incarnation: 4 });
  assert.equal(d.transmitCountOf("n1"), 1);
});
