import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Context } from '../src/context.ts';
import type { CancelReason } from '../src/context.ts';
import { VirtualClock } from '../src/clock.ts';

test('nested deadline: child cannot exceed parent deadline', () => {
  const clock = new VirtualClock();
  const parent = Context.background(clock).withDeadline(100);
  const child = parent.withDeadline(200);
  assert.equal(parent.deadline, 100);
  assert.equal(child.deadline, 100, 'child effective deadline clamped to parent');

  const tighter = parent.withDeadline(50);
  assert.equal(tighter.deadline, 50);
});

test('shortening a deadline propagates to children immediately', () => {
  const clock = new VirtualClock();
  const parent = Context.background(clock).withDeadline(100);
  const child = parent.withCancel().context;
  assert.equal(child.deadline, 100);

  parent.setDeadline(40);
  assert.equal(parent.deadline, 40);
  assert.equal(child.deadline, 40, 'child sees shortened parent deadline synchronously');

  clock.advance(40);
  assert.ok(parent.cancelled);
  assert.ok(child.cancelled);
  assert.equal(child.reason, parent.reason, 'shared unique reason object');
  assert.equal(child.reason?.code, 'deadline_exceeded');
});

test('extending a deadline is clamped by the parent', () => {
  const clock = new VirtualClock();
  const parent = Context.background(clock).withDeadline(100);
  const child = parent.withDeadline(50);
  assert.equal(child.deadline, 50);

  child.setDeadline(500);
  assert.equal(child.deadline, 100, 'extension clamped to parent deadline');

  parent.setDeadline(80);
  assert.equal(child.deadline, 80, 'parent shortening still propagates');

  parent.setDeadline(300);
  assert.equal(parent.deadline, 300);
  assert.equal(child.deadline, 300, 'child follows parent extension up to its own request');

  child.setDeadline(250);
  assert.equal(child.deadline, 250);
  clock.advance(250);
  assert.ok(child.cancelled);
  assert.ok(!parent.cancelled, 'parent survives child expiry');
});

test('shortening into the past cancels immediately and synchronously', () => {
  const clock = new VirtualClock();
  const parent = Context.background(clock).withDeadline(100);
  const child = parent.withCancel().context;
  clock.advance(60);

  parent.setDeadline(30);
  assert.ok(parent.cancelled, 'parent cancelled synchronously');
  assert.ok(child.cancelled, 'child cancelled synchronously via propagation');
  assert.equal(parent.reason?.code, 'deadline_exceeded');
});

test('a cancelled context cannot be revived', () => {
  const clock = new VirtualClock();
  const ctx = Context.background(clock).withDeadline(100);
  clock.advance(100);
  assert.ok(ctx.cancelled);
  const reason = ctx.reason;
  assert.equal(reason?.code, 'deadline_exceeded');

  ctx.setDeadline(500);
  assert.ok(ctx.cancelled, 'still cancelled after setDeadline');
  assert.equal(ctx.reason, reason, 'reason object unchanged');

  assert.equal(ctx.cancel('second attempt'), false, 'second cancel rejected');
  assert.equal(ctx.reason, reason, 'first reason wins');
});

test('manual cancel: unique reason shared across the tree', () => {
  const clock = new VirtualClock();
  const root = Context.background(clock);
  const { context: child, cancel } = root.withCancel();
  const grandchild = child.withCancel().context;

  assert.equal(cancel('shutdown'), true);
  assert.ok(child.cancelled);
  assert.ok(grandchild.cancelled);
  assert.equal(grandchild.reason, child.reason, 'identical reason object propagates');
  assert.equal(child.reason?.message, 'shutdown');

  assert.equal(cancel('other'), false);
  assert.equal(child.reason?.message, 'shutdown');
});

test('detached child is immune to parent cancellation and deadline changes', () => {
  const clock = new VirtualClock();
  const parent = Context.background(clock).withDeadline(100);
  const detached = parent.detach();
  assert.equal(detached.deadline, 100, 'detached keeps deadline snapshot');

  parent.setDeadline(10);
  assert.equal(detached.deadline, 100, 'parent shortening does not reach detached child');

  parent.cancel('parent gone');
  assert.ok(!detached.cancelled, 'detached survives parent cancellation');

  clock.advance(100);
  assert.ok(detached.cancelled, 'detached still expires on its own clock');
  assert.equal(detached.reason?.code, 'deadline_exceeded');
});

test('detaching a cancelled context yields a cancelled context with the same reason', () => {
  const clock = new VirtualClock();
  const parent = Context.background(clock);
  parent.cancel('boom');
  const detached = parent.detach();
  assert.ok(detached.cancelled);
  assert.equal(detached.reason, parent.reason);
});

test('onCancel fires immediately when registering after expiry', () => {
  const clock = new VirtualClock();
  const ctx = Context.background(clock).withDeadline(50);
  clock.advance(50);
  assert.ok(ctx.cancelled);

  const seen: CancelReason[] = [];
  ctx.onCancel((reason) => seen.push(reason));
  assert.equal(seen.length, 1, 'callback invoked synchronously at registration');
  assert.equal(seen[0], ctx.reason);
});

test('onCancel registered during cancellation runs immediately (re-entrant)', () => {
  const clock = new VirtualClock();
  const ctx = Context.background(clock);
  const order: string[] = [];

  ctx.onCancel((reason) => {
    order.push(`first:${reason.message}`);
    ctx.onCancel((inner) => order.push(`inner:${inner.message}`));
  });
  ctx.cancel('halt');
  assert.deepEqual(order, ['first:halt', 'inner:halt']);
});

test('expiry between checks and registration still delivers the callback', () => {
  const clock = new VirtualClock();
  const ctx = Context.background(clock).withTimeout(30);
  assert.ok(!ctx.cancelled);
  clock.advance(31);

  let fired = 0;
  ctx.onCancel(() => fired++);
  assert.equal(fired, 1);
  assert.equal(ctx.reason?.code, 'deadline_exceeded');
});

test('unsubscribe prevents callback delivery', () => {
  const clock = new VirtualClock();
  const ctx = Context.background(clock);
  let fired = 0;
  const off = ctx.onCancel(() => fired++);
  off();
  ctx.cancel();
  assert.equal(fired, 0);
});

test('child of an already-cancelled parent is born cancelled with the same reason', () => {
  const clock = new VirtualClock();
  const parent = Context.background(clock);
  parent.cancel('early');
  const { context: child } = parent.withCancel();
  assert.ok(child.cancelled);
  assert.equal(child.reason, parent.reason);

  const withDeadline = parent.withDeadline(clock.now() + 1000);
  assert.ok(withDeadline.cancelled);
  assert.equal(withDeadline.reason, parent.reason);
});

test('timers are released on cancel (no stray wakeups)', () => {
  const clock = new VirtualClock();
  const ctx = Context.background(clock).withDeadline(100);
  assert.equal(clock.pendingCount, 1);
  ctx.cancel();
  assert.equal(clock.pendingCount, 0);
});
