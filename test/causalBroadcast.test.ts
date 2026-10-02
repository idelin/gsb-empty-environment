import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { CausalBroadcast } from "../src/causalBroadcast.ts";

function receiver(id: string) {
  const log: string[] = [];
  const node = new CausalBroadcast<string>(id, (m) => log.push(m.payload));
  return { node, log };
}

describe("causal broadcast middleware", () => {
  it("acceptance: M1 depends on M0, M1 arrives first, parked, then cascade-delivered after M0", () => {
    const alice = new CausalBroadcast<string>("alice");
    const { node: bob, log } = receiver("bob");

    const m0 = alice.broadcast("M0");
    const m1 = alice.broadcast("M1");
    assert.equal(m1.clock.alice, 2);

    bob.receive(m1);
    assert.deepEqual(log, [], "M1 must NOT be delivered before M0");
    assert.equal(bob.bufferedCount, 1, "M1 must be parked in the buffer");

    bob.receive(m0);
    assert.deepEqual(log, ["M0", "M1"], "causal order must be preserved");
    assert.equal(bob.bufferedCount, 0, "buffer must be drained");
  });

  it("delivers in-order messages immediately without buffering", () => {
    const alice = new CausalBroadcast<string>("alice");
    const { node: bob, log } = receiver("bob");

    bob.receive(alice.broadcast("M0"));
    assert.deepEqual(log, ["M0"]);
    assert.equal(bob.bufferedCount, 0);
    bob.receive(alice.broadcast("M1"));
    assert.deepEqual(log, ["M0", "M1"]);
    assert.equal(bob.bufferedCount, 0);
  });

  it("delivers concurrent (independent) messages in any arrival order", () => {
    const a = new CausalBroadcast<string>("A");
    const b = new CausalBroadcast<string>("B");
    const { node: c, log } = receiver("C");

    const ma = a.broadcast("from-A");
    const mb = b.broadcast("from-B");

    c.receive(mb);
    c.receive(ma);
    assert.deepEqual(log, ["from-B", "from-A"]);
    assert.equal(c.bufferedCount, 0);
  });

  it("drains a reversed chain M2,M1,M0 into causal order", () => {
    const alice = new CausalBroadcast<string>("alice");
    const { node: bob, log } = receiver("bob");

    const m0 = alice.broadcast("M0");
    const m1 = alice.broadcast("M1");
    const m2 = alice.broadcast("M2");

    bob.receive(m2);
    bob.receive(m1);
    assert.deepEqual(log, []);
    assert.equal(bob.bufferedCount, 2);

    bob.receive(m0);
    assert.deepEqual(log, ["M0", "M1", "M2"]);
    assert.equal(bob.bufferedCount, 0);
  });

  it("ignores duplicate deliveries", () => {
    const alice = new CausalBroadcast<string>("alice");
    const { node: bob, log } = receiver("bob");

    const m0 = alice.broadcast("M0");
    bob.receive(m0);
    bob.receive(m0);
    bob.receive(m0);
    assert.deepEqual(log, ["M0"]);
    assert.equal(bob.bufferedCount, 0);
  });

  it("propagates causal dependencies across three nodes", () => {
    const a = new CausalBroadcast<string>("A");
    const b = new CausalBroadcast<string>("B");
    const { node: c, log } = receiver("C");

    const a1 = a.broadcast("a1");
    b.receive(a1);
    const b1 = b.broadcast("b1");

    c.receive(b1);
    assert.deepEqual(log, [], "b1 must wait for its dependency a1");
    assert.equal(c.bufferedCount, 1);

    c.receive(a1);
    assert.deepEqual(log, ["a1", "b1"]);
    assert.equal(c.bufferedCount, 0);
  });

  it("broadcast loopback delivers locally and stamps vector timestamps", () => {
    const { node, log } = receiver("self");
    const m0 = node.broadcast("hello");
    const m1 = node.broadcast("world");
    assert.deepEqual(log, ["hello", "world"]);
    assert.deepEqual(m0.clock, { self: 1 });
    assert.deepEqual(m1.clock, { self: 2 });
    assert.deepEqual(node.vectorClock, { self: 2 });
  });

  it("merges vector clocks on delivery", () => {
    const a = new CausalBroadcast<string>("A");
    const b = new CausalBroadcast<string>("B");
    const { node: c } = receiver("C");

    const a1 = a.broadcast("x");
    c.receive(a1);
    b.receive(a1);
    c.receive(b.broadcast("y"));
    assert.deepEqual(c.vectorClock, { A: 1, B: 1 });
  });
});
