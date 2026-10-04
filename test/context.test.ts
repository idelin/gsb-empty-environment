import { test } from "node:test";
import assert from "node:assert/strict";
import { VirtualClock } from "../src/clock.ts";
import {
  background,
  withCancel,
  withDeadline,
  withTimeout,
  CancellationReason,
  type Context,
} from "../src/context.ts";

function setup() {
  const clock = new VirtualClock(1000);
  const root = background(clock);
  return { clock, root };
}

test("child deadline shorter than parent fires at its own deadline", () => {
  const { clock, root } = setup();
  const parent = withTimeout(root, 100);
  const child = withTimeout(parent, 40);

  const fired: string[] = [];
  child.onCancel((r) => fired.push(`child:${r.message}`));
  parent.onCancel((r) => fired.push(`parent:${r.message}`));

  clock.advanceBy(39);
  assert.deepEqual(fired, []);
  clock.advanceBy(1);
  assert.deepEqual(fired, ["child:deadline exceeded"]);
  assert.equal(parent.isCancelled(), false);
});

test("child deadline beyond parent is bounded by parent deadline", () => {
  const { clock, root } = setup();
  const parent = withTimeout(root, 50);
  const child = withTimeout(parent, 500);

  assert.equal(child.deadline(), 1050);

  const reasons: CancellationReason[] = [];
  child.onCancel((r) => reasons.push(r));
  clock.advanceBy(50);

  assert.equal(parent.reason()?.message, "deadline exceeded");
  assert.equal(reasons.length, 1);
  assert.equal(reasons[0].message, "parent cancelled");
  assert.equal(reasons[0].cause, parent.reason());
});

test("shortening parent deadline propagates to children immediately", () => {
  const { clock, root } = setup();
  const parent = withTimeout(root, 1000);
  const child = withTimeout(parent, 900);
  const grandchild = withTimeout(child, 800);

  clock.advanceBy(100); // now = 1100
  parent.setDeadline(1100 + 50);

  assert.equal(child.deadline(), 1150);
  assert.equal(grandchild.deadline(), 1150);
  assert.equal(child.isCancelled(), false);

  const fired: string[] = [];
  grandchild.onCancel(() => fired.push("grandchild"));
  child.onCancel(() => fired.push("child"));

  clock.advanceBy(50);
  assert.deepEqual(fired.sort(), ["child", "grandchild"]);
});

test("shortening a deadline to the past cancels immediately", () => {
  const { clock, root } = setup();
  const parent = withTimeout(root, 1000);
  const child = withTimeout(parent, 900);

  clock.advanceBy(500); // now = 1500
  const reasons: CancellationReason[] = [];
  child.onCancel((r) => reasons.push(r));

  parent.setDeadline(1400); // already in the past
  assert.equal(parent.isCancelled(), true);
  assert.equal(child.isCancelled(), true);
  assert.equal(reasons.length, 1);
  assert.equal(reasons[0].cause, parent.reason());
});

test("extending a deadline cannot exceed the parent deadline", () => {
  const { clock, root } = setup();
  const parent = withTimeout(root, 100);
  const child = withTimeout(parent, 50);

  const clamped = child.setDeadline(1000 + 5000);
  assert.equal(clamped, 1100);
  assert.equal(child.deadline(), 1100);

  clock.advanceBy(100);
  assert.equal(child.isCancelled(), true);
  assert.equal(child.reason()?.message, "parent cancelled");
});

test("extending a parent deadline is clamped by the grandparent", () => {
  const { clock, root } = setup();
  const grandparent = withTimeout(root, 200);
  const parent = withTimeout(grandparent, 100);

  const clamped = parent.setDeadline(1000 + 9999);
  assert.equal(clamped, 1200);
  assert.equal(parent.deadline(), 1200);
});

test("extending a deadline within the parent bound works", () => {
  const { clock, root } = setup();
  const parent = withTimeout(root, 1000);
  const child = withTimeout(parent, 100);

  child.setDeadline(1000 + 500);
  assert.equal(child.deadline(), 1500);
  clock.advanceBy(499);
  assert.equal(child.isCancelled(), false);
  clock.advanceBy(1);
  assert.equal(child.isCancelled(), true);
  assert.equal(child.reason()?.message, "deadline exceeded");
});

test("detached child is unaffected by parent cancellation and keeps its own deadline", () => {
  const { clock, root } = setup();
  const parent = withTimeout(root, 100);
  const child = withTimeout(parent, 300);

  child.detach();
  assert.equal(child.deadline(), 1300);

  clock.advanceBy(100);
  assert.equal(parent.isCancelled(), true);
  assert.equal(child.isCancelled(), false);

  clock.advanceBy(200);
  assert.equal(child.isCancelled(), true);
  assert.equal(child.reason()?.message, "deadline exceeded");
});

test("detached child is unaffected by explicit parent cancel", () => {
  const { root } = setup();
  const parent = withCancel(root);
  const child = withCancel(parent);
  child.detach();

  parent.cancel("shutdown");
  assert.equal(parent.reason()?.message, "shutdown");
  assert.equal(child.isCancelled(), false);
});

test("cancelled context cannot be restored", () => {
  const { clock, root } = setup();
  const ctx = withTimeout(root, 100);
  clock.advanceBy(100);
  assert.equal(ctx.isCancelled(), true);
  const original = ctx.reason()!;

  ctx.setDeadline(1000 + 9999);
  assert.equal(ctx.isCancelled(), true);
  assert.equal(ctx.reason(), original);

  const again = ctx.cancel("too late");
  assert.equal(again, original);

  ctx.clearDeadline();
  ctx.detach();
  assert.equal(ctx.isCancelled(), true);
  assert.equal(ctx.reason(), original);
});

test("onCancel registered after expiry fires immediately with the reason", () => {
  const { clock, root } = setup();
  const ctx = withTimeout(root, 50);
  clock.advanceBy(50);
  assert.equal(ctx.isCancelled(), true);

  const seen: CancellationReason[] = [];
  ctx.onCancel((r) => seen.push(r));
  assert.equal(seen.length, 1);
  assert.equal(seen[0], ctx.reason());
});

test("callback registering another callback during cancellation fires it immediately", () => {
  const { root } = setup();
  const ctx = withCancel(root);

  const order: string[] = [];
  ctx.onCancel((r) => {
    order.push("first");
    ctx.onCancel((r2) => {
      order.push("second");
      assert.equal(r2, r);
    });
  });
  ctx.cancel("boom");
  assert.deepEqual(order, ["first", "second"]);
});

test("deadline expiring while callbacks are being registered still notifies all", () => {
  const { clock, root } = setup();
  const parent = withTimeout(root, 100);
  const late = withTimeout(parent, 100);

  const order: string[] = [];
  parent.onCancel(() => {
    order.push("parent-cb");
    // The child has already been cancelled by propagation at this point only
    // if propagation happened first; registering now must fire immediately.
    late.onCancel((r) => order.push(`late-cb:${r.message}`));
  });

  clock.advanceBy(100);
  assert.deepEqual(order, ["parent-cb", "late-cb:parent cancelled"]);
});

test("every cancellation produces a unique reason", () => {
  const { clock, root } = setup();
  const a = withTimeout(root, 10);
  const b = withTimeout(root, 20);
  const parent = withTimeout(root, 30);
  const child = withCancel(parent);

  clock.advanceBy(30);

  const reasons = [a.reason()!, b.reason()!, parent.reason()!, child.reason()!];
  const ids = new Set(reasons.map((r) => r.id));
  assert.equal(ids.size, 4);
  assert.equal(child.reason()!.cause, parent.reason());
  assert.notEqual(child.reason(), parent.reason());
});

test("reason toString includes id, message and cause chain", () => {
  const { root } = setup();
  const parent = withCancel(root);
  const child = withCancel(parent);
  parent.cancel("shutdown");

  const text = child.reason()!.toString();
  assert.match(text, /^cancellation#\d+: parent cancelled \(caused by cancellation#\d+: shutdown\)$/);
});

test("child created from an already-cancelled parent is cancelled immediately", () => {
  const { root } = setup();
  const parent = withCancel(root);
  parent.cancel("gone");

  const child = withDeadline(parent, 1000 + 100);
  assert.equal(child.isCancelled(), true);
  assert.equal(child.reason()?.message, "parent cancelled");
  assert.equal(child.reason()?.cause, parent.reason());
});

test("done() promise resolves with the cancellation reason", async () => {
  const { clock, root } = setup();
  const ctx = withTimeout(root, 25);
  const promise = ctx.done();
  clock.advanceBy(25);
  const reason = await promise;
  assert.equal(reason, ctx.reason());
  assert.equal(reason.message, "deadline exceeded");
});

test("unregistered callbacks are not invoked", () => {
  const { root } = setup();
  const ctx = withCancel(root);
  let calls = 0;
  const off = ctx.onCancel(() => calls++);
  off();
  ctx.cancel();
  assert.equal(calls, 0);
});

test("clearDeadline removes only the own deadline, parent bound still applies", () => {
  const { clock, root } = setup();
  const parent = withTimeout(root, 100);
  const child = withTimeout(parent, 50);

  child.clearDeadline();
  assert.equal(child.deadline(), 1100);

  clock.advanceBy(100);
  assert.equal(child.isCancelled(), true);
  assert.equal(child.reason()?.message, "parent cancelled");
});

test("virtual clock fires timers in chronological order", () => {
  const { clock, root } = setup();
  const order: string[] = [];
  const a: Context = withTimeout(root, 30);
  const b: Context = withTimeout(root, 10);
  const c: Context = withTimeout(root, 20);
  a.onCancel(() => order.push("a"));
  b.onCancel(() => order.push("b"));
  c.onCancel(() => order.push("c"));
  clock.advanceBy(100);
  assert.deepEqual(order, ["b", "c", "a"]);
});
