import test from "node:test";
import assert from "node:assert/strict";

import { BoundedChannel, ChannelClosedError } from "../src/channel.ts";

const tick = (): Promise<void> => new Promise((resolve) => setTimeout(resolve, 0));

test("delivers items FIFO", async () => {
  const ch = new BoundedChannel<number>(3);
  await ch.send(1);
  await ch.send(2);
  await ch.send(3);
  assert.equal(await ch.receive(), 1);
  assert.equal(await ch.receive(), 2);
  assert.equal(await ch.receive(), 3);
});

test("send blocks at capacity until a receive frees space", async () => {
  const ch = new BoundedChannel<number>(1, "t");
  await ch.send(1);
  let unblocked = false;
  const pending = ch.send(2).then(() => {
    unblocked = true;
  });
  await tick();
  assert.equal(unblocked, false, "send must wait while the channel is full");
  assert.equal(await ch.receive(), 1);
  await pending;
  assert.equal(unblocked, true);
  assert.equal(await ch.receive(), 2);
});

test("close resolves pending receivers with undefined once drained", async () => {
  const ch = new BoundedChannel<number>(1);
  const first = ch.receive();
  await ch.send(42);
  assert.equal(await first, 42);
  const second = ch.receive();
  ch.close();
  assert.equal(await second, undefined);
  assert.equal(await ch.receive(), undefined);
});

test("close(error) rejects pending receivers and future sends", async () => {
  const ch = new BoundedChannel<number>(1, "boom-ch");
  const pending = ch.receive();
  const boom = new Error("boom");
  ch.close(boom);
  await assert.rejects(pending, /boom/);
  await assert.rejects(ch.send(1), ChannelClosedError);
});

test("drain returns buffered items and unblocks receivers", async () => {
  const ch = new BoundedChannel<string>(4);
  await ch.send("a");
  await ch.send("b");
  ch.close();
  assert.deepEqual(ch.drain(), ["a", "b"]);
  assert.equal(await ch.receive(), undefined);
});
