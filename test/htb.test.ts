import { test } from "node:test";
import assert from "node:assert/strict";
import { HtbAllocator, TokenBucket, distribute } from "../src/index.ts";

const EPS = 1e-6;

function closeTo(actual: number, expected: number, eps = EPS): void {
  assert.ok(
    Math.abs(actual - expected) < eps,
    `expected ${actual} to be close to ${expected}`,
  );
}

/** Acceptance tree: root 100MB, dept A 40MB, dept B 60MB, one business line each. */
function makeAcceptanceAllocator(): HtbAllocator {
  return new HtbAllocator({
    name: "root",
    rate: 100,
    children: [
      { name: "deptA", rate: 40, children: [{ name: "bizA", rate: 40 }] },
      { name: "deptB", rate: 60, children: [{ name: "bizB", rate: 60 }] },
    ],
  });
}

/** Tick until allocations converge to their targets. */
function settle(alloc: HtbAllocator, seconds = 20, dt = 0.5): void {
  for (let t = 0; t < seconds; t += dt) alloc.tick(dt);
}

test("acceptance: idle sibling bandwidth can be borrowed (A uses 10, B bursts to 90)", () => {
  const htb = makeAcceptanceAllocator();
  htb.setDemand("bizA", 10);
  htb.setDemand("bizB", 100);
  htb.tick(0.1);

  closeTo(htb.targetOf("bizA"), 10);
  closeTo(htb.targetOf("bizB"), 90); // 60 guaranteed + 30 borrowed from A
  closeTo(htb.targetOf("deptB"), 90);
  closeTo(htb.targetOf("root"), 100);

  settle(htb);
  closeTo(htb.allocOf("bizB"), 90, 1e-3);
  closeTo(htb.allocOf("bizA"), 10, 1e-3);
});

test("acceptance: when A recovers, fair share returns to 40/60", () => {
  const htb = makeAcceptanceAllocator();
  htb.setDemand("bizA", 10);
  htb.setDemand("bizB", 100);
  settle(htb);

  htb.setDemand("bizA", 40);
  htb.tick(0.1);
  closeTo(htb.targetOf("bizA"), 40);
  closeTo(htb.targetOf("bizB"), 60);

  settle(htb);
  closeTo(htb.allocOf("bizA"), 40, 1e-3);
  closeTo(htb.allocOf("bizB"), 60, 1e-3);
});

test("acceptance: reclaim is smooth, not an instant cut", () => {
  const htb = makeAcceptanceAllocator();
  htb.setDemand("bizA", 10);
  htb.setDemand("bizB", 100);
  settle(htb);
  closeTo(htb.allocOf("bizB"), 90, 1e-3);

  // A's traffic recovers; B must be suppressed back to 60 gradually.
  htb.setDemand("bizA", 40);
  const trace: number[] = [];
  for (let i = 0; i < 100; i++) {
    htb.tick(0.1);
    trace.push(htb.allocOf("bizB"));
  }

  // Not instant: right after recovery B is still well above 60.
  assert.ok(trace[0] > 85, `first tick after reclaim: ${trace[0]}`);
  // Monotonic decrease toward the fair share.
  for (let i = 1; i < trace.length; i++) {
    assert.ok(trace[i] <= trace[i - 1] + EPS, `not monotonic at ${i}`);
    assert.ok(trace[i] >= 60 - EPS, `overshot below 60 at ${i}`);
  }
  // Converges to 60.
  closeTo(trace[trace.length - 1], 60, 1e-2);
});

test("borrowing: sibling leaves share within their department", () => {
  const htb = new HtbAllocator({
    name: "root",
    rate: 100,
    children: [
      {
        name: "deptA",
        rate: 40,
        children: [
          { name: "a1", rate: 20 },
          { name: "a2", rate: 20 },
        ],
      },
      { name: "deptB", rate: 60, children: [{ name: "b1", rate: 60 }] },
    ],
  });

  // a2 idle: a1 borrows its 20 and reaches the department's 40.
  htb.setDemand("a1", 100);
  htb.setDemand("a2", 0);
  htb.setDemand("b1", 60);
  htb.tick(0.1);
  closeTo(htb.targetOf("a1"), 40);
  closeTo(htb.targetOf("b1"), 60);

  // B also idle: a1 borrows across departments up to the root's 100.
  htb.setDemand("b1", 0);
  htb.tick(0.1);
  closeTo(htb.targetOf("a1"), 100);
});

test("reclaim: oversubscription is split proportionally to weights", () => {
  const htb = new HtbAllocator({
    name: "root",
    rate: 100,
    children: [
      { name: "deptA", rate: 40, children: [{ name: "bizA", rate: 40 }] },
      { name: "deptB", rate: 60, children: [{ name: "bizB", rate: 60 }] },
    ],
  });
  htb.setDemand("bizA", 100);
  htb.setDemand("bizB", 100);
  htb.tick(0.1);
  closeTo(htb.targetOf("bizA"), 40); // 100 * 40 / (40 + 60)
  closeTo(htb.targetOf("bizB"), 60); // 100 * 60 / (40 + 60)
});

test("reclaim: weighted split inside a department (1:3)", () => {
  const htb = new HtbAllocator({
    name: "root",
    rate: 40,
    children: [
      {
        name: "dept",
        rate: 40,
        children: [
          { name: "small", rate: 10 },
          { name: "big", rate: 30 },
        ],
      },
    ],
  });
  htb.setDemand("small", 40);
  htb.setDemand("big", 40);
  htb.tick(0.1);
  closeTo(htb.targetOf("small"), 10);
  closeTo(htb.targetOf("big"), 30);
});

test("ceil caps borrowing", () => {
  const htb = new HtbAllocator({
    name: "root",
    rate: 100,
    children: [
      { name: "deptA", rate: 40, children: [{ name: "bizA", rate: 40 }] },
      {
        name: "deptB",
        rate: 60,
        children: [{ name: "bizB", rate: 60, ceil: 75 }],
      },
    ],
  });
  htb.setDemand("bizA", 0);
  htb.setDemand("bizB", 100);
  htb.tick(0.1);
  closeTo(htb.targetOf("bizB"), 75); // 40MB idle, but ceil stops the burst
});

test("token bucket enforces the smoothed allocation", () => {
  const htb = makeAcceptanceAllocator();
  htb.setDemand("bizA", 40);
  htb.setDemand("bizB", 100);
  settle(htb);

  // Drain the bucket (capacity = ceil * burstSeconds), then only refilled
  // bandwidth is available.
  htb.consume("bizB", 1000);
  const second = htb.consume("bizB", 1000);
  closeTo(second, 0);

  // After 1s of simulation, ~60MB is refilled.
  htb.tick(1);
  const third = htb.consume("bizB", 1000);
  closeTo(third, 60, 1e-3);
});

test("token bucket unit behaviour", () => {
  const bucket = new TokenBucket(10);
  closeTo(bucket.consume(4), 4);
  closeTo(bucket.consume(8), 6);
  bucket.refill(5);
  closeTo(bucket.consume(100), 5);
  bucket.refill(100);
  closeTo(bucket.tokensMb, 10);
});

test("distribute: weighted water-filling with caps", () => {
  closeTo(distribute([1, 1], [100, 100], 50)[0], 25);
  const [a, b] = distribute([40, 60], [10, 100], 100);
  closeTo(a, 10);
  closeTo(b, 90);
  const [c, d] = distribute([1, 3], [100, 100], 40);
  closeTo(c, 10);
  closeTo(d, 30);
});

test("validation: demand on inner nodes and unknown nodes throw", () => {
  const htb = makeAcceptanceAllocator();
  assert.throws(() => htb.setDemand("deptA", 10), /leaf/);
  assert.throws(() => htb.setDemand("nope", 10), /unknown/);
  assert.throws(() => htb.setDemand("bizA", -1), />= 0/);
  assert.throws(() => htb.tick(0), /dt/);
});
