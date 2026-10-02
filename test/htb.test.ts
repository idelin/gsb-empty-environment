import { test } from "node:test";
import assert from "node:assert/strict";
import { HtbAllocator } from "../src/htb.ts";

const EPS = 1e-6;

function approx(actual: number, expected: number, msg?: string): void {
  assert.ok(
    Math.abs(actual - expected) < EPS,
    `${msg ?? "value"}: expected ${expected}, got ${actual}`,
  );
}

/** Tick until allocations stop changing (or maxTicks reached). */
function settle(
  htb: HtbAllocator,
  maxTicks = 100,
): { history: Map<string, number>[]; final: Map<string, number> } {
  const history: Map<string, number>[] = [];
  let prev = "";
  for (let i = 0; i < maxTicks; i++) {
    const snap = htb.tick();
    history.push(snap);
    const key = [...snap.values()].map((v) => v.toFixed(9)).join(",");
    if (key === prev) return { history, final: snap };
    prev = key;
  }
  throw new Error("did not converge");
}

function buildAcceptanceTree(): HtbAllocator {
  return new HtbAllocator({
    name: "root",
    rate: 100,
    children: [
      { name: "A", rate: 40 },
      { name: "B", rate: 60, ceil: 90 },
    ],
  });
}

test("acceptance: idle quota of A is lent to B up to its ceil (90MB)", () => {
  const htb = buildAcceptanceTree();
  htb.setDemand(["A"], 10);
  htb.setDemand(["B"], 100);

  const { final } = settle(htb);
  approx(final.get("A")!, 10, "A keeps what it uses");
  approx(final.get("B")!, 90, "B borrows A's idle 30MB up to ceil");
  approx(final.get("A")! + final.get("B")!, 100, "root budget fully used");
});

test("acceptance: B can never exceed its ceil even with more spare capacity", () => {
  const htb = buildAcceptanceTree();
  htb.setDemand(["A"], 0);
  htb.setDemand(["B"], 100);

  const { final } = settle(htb);
  approx(final.get("A")!, 0);
  approx(final.get("B")!, 90, "B capped at ceil=90 despite 100MB spare");
});

test("acceptance: when A resumes, B is smoothly pressed back to 60MB", () => {
  const htb = buildAcceptanceTree();
  htb.setDemand(["A"], 10);
  htb.setDemand(["B"], 100);
  settle(htb);

  // A's traffic recovers to its full guaranteed rate.
  htb.setDemand(["A"], 40);
  const { history, final } = settle(htb);

  assert.ok(history.length > 2, "reclaim is gradual, not a single-step cut");

  let prevB = Infinity;
  for (const snap of history) {
    const a = snap.get("A")!;
    const b = snap.get("B")!;
    assert.ok(a + b <= 100 + EPS, `root budget never exceeded (A=${a}, B=${b})`);
    assert.ok(b <= prevB + EPS, "B decreases monotonically");
    assert.ok(b >= 60 - EPS, "B never drops below its guaranteed 60MB");
    prevB = b;
  }

  approx(final.get("A")!, 40, "A fully recovers its guaranteed rate");
  approx(final.get("B")!, 60, "B is pressed back to its guaranteed rate");
});

test("guaranteed rates are honored before any borrowing", () => {
  const htb = buildAcceptanceTree();
  htb.setDemand(["A"], 40);
  htb.setDemand(["B"], 60);

  const { final } = settle(htb);
  approx(final.get("A")!, 40);
  approx(final.get("B")!, 60);
});

test("spare bandwidth is shared between siblings proportional to weight", () => {
  const htb = new HtbAllocator({
    name: "root",
    rate: 100,
    children: [
      { name: "x", rate: 30, weight: 1 },
      { name: "y", rate: 30, weight: 1 },
      { name: "z", rate: 30, weight: 2 },
    ],
  });
  htb.setDemand(["x"], 100);
  htb.setDemand(["y"], 100);
  htb.setDemand(["z"], 100);

  const { final } = settle(htb);
  // 90MB guaranteed; the remaining 10MB splits 1:1:2 -> 2.5 / 2.5 / 5.
  approx(final.get("x")!, 32.5);
  approx(final.get("y")!, 32.5);
  approx(final.get("z")!, 35);
});

test("oversubscribed guarantees are reclaimed proportional to weight", () => {
  const htb = new HtbAllocator({
    name: "root",
    rate: 100,
    children: [
      { name: "x", rate: 80, weight: 1 },
      { name: "y", rate: 80, weight: 3 },
    ],
  });
  htb.setDemand(["x"], 100);
  htb.setDemand(["y"], 100);

  const { final } = settle(htb);
  // 160MB demanded vs 100MB: the 60MB deficit is cut 1:3 -> 15 / 45.
  approx(final.get("x")!, 65);
  approx(final.get("y")!, 35);
});

test("hierarchy: borrowing is scoped by department and department ceil", () => {
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
      {
        name: "deptB",
        rate: 60,
        children: [{ name: "b1", rate: 60, ceil: 90 }],
      },
    ],
  });

  // a1 idle: a2 borrows within deptA, but deptA cannot take deptB's share.
  htb.setDemand(["deptA", "a1"], 0);
  htb.setDemand(["deptA", "a2"], 50);
  htb.setDemand(["deptB", "b1"], 60);
  let { final } = settle(htb);
  approx(final.get("deptA/a1")!, 0);
  approx(final.get("deptA/a2")!, 40, "a2 capped by deptA's 40MB share");
  approx(final.get("deptB/b1")!, 60);

  // deptA mostly idle: b1 borrows across departments up to its ceil.
  htb.setDemand(["deptA", "a2"], 10);
  htb.setDemand(["deptB", "b1"], 100);
  ({ final } = settle(htb));
  approx(final.get("deptA/a2")!, 10);
  approx(final.get("deptB/b1")!, 90, "b1 borrows deptA's idle 30MB");
});

test("input validation", () => {
  const htb = buildAcceptanceTree();
  assert.throws(() => htb.setDemand(["A"], -1), />= 0/);
  assert.throws(() => htb.setDemand(["nope"], 1), /unknown node/);
});
