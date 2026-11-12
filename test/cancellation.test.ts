import assert from "node:assert/strict";
import { test } from "node:test";
import {
  BlobAddressRegistry,
  ReadAbortedError,
} from "../index.ts";
import {
  bytes,
  context,
  decode,
  document,
  drain,
  FakeClock,
} from "./helpers.ts";

test("a pre-aborted signal never opens a stream", async () => {
  const reg = new BlobAddressRegistry({ clock: new FakeClock() });
  const doc = document("partition-a");
  const binding = reg.createAddress({ document: doc, data: bytes("x") });

  const controller = new AbortController();
  controller.abort();

  await assert.rejects(
    reg.openRead(binding.url, { document: doc, signal: controller.signal }),
    ReadAbortedError,
  );
  assert.equal(reg.activeStreamCount(binding.url, doc), 0);
});

test("aborting while the open is pending cancels before issuance", async () => {
  const reg = new BlobAddressRegistry({ clock: new FakeClock() });
  const doc = document("partition-a");
  const binding = reg.createAddress({ document: doc, data: bytes("x") });

  const controller = new AbortController();
  const pending = reg.openRead(binding.url, {
    document: doc,
    signal: controller.signal,
  });
  controller.abort();

  await assert.rejects(pending, ReadAbortedError);
  assert.equal(reg.activeStreamCount(binding.url, doc), 0);
});

test("document lifecycle signal cancels a pending open on navigation", async () => {
  const reg = new BlobAddressRegistry({ clock: new FakeClock() });
  const browsing = context("partition-a");
  const doc = browsing.document;
  const binding = reg.createAddress({ document: doc, data: bytes("x") });

  const pending = reg.openRead(binding.url, {
    document: doc,
    signal: doc.lifecycleSignal,
  });
  browsing.navigate({});

  await assert.rejects(pending, ReadAbortedError);
});

test("cancelling an issued stream ends the lease but leaves other streams and the address intact", async () => {
  const clock = new FakeClock();
  const reg = new BlobAddressRegistry({ clock });
  const doc = document("partition-a");
  const binding = reg.createAddress({
    document: doc,
    data: bytes("0123456789"),
  });

  const streamA = await reg.openRead(binding.url, {
    document: doc,
    chunkSize: 4,
  });
  const streamB = await reg.openRead(binding.url, {
    document: doc,
    chunkSize: 4,
  });
  await streamA.read();
  streamA.cancel();

  assert.equal(streamA.leaseState, "cancelled");
  await assert.rejects(streamA.read(), ReadAbortedError);
  assert.equal(reg.activeStreamCount(binding.url, doc), 1);

  // Stream B and future opens are completely unaffected.
  assert.equal(decode(await drain(streamB)), "0123456789");
  const streamC = await reg.openRead(binding.url, { document: doc });
  assert.equal(decode(await drain(streamC)), "0123456789");
});

test("break from async iteration releases the lease with reason cancelled", async () => {
  const reg = new BlobAddressRegistry({ clock: new FakeClock() });
  const doc = document("partition-a");
  const binding = reg.createAddress({
    document: doc,
    data: bytes("0123456789"),
  });

  const stream = await reg.openRead(binding.url, {
    document: doc,
    chunkSize: 2,
  });
  for await (const chunk of stream) {
    assert.equal(chunk.byteLength, 2);
    break;
  }
  assert.equal(stream.leaseState, "cancelled");
});

test("waitForLeaseEnd resolves with the completion detail", async () => {
  const reg = new BlobAddressRegistry({ clock: new FakeClock() });
  const doc = document("partition-a");
  const binding = reg.createAddress({ document: doc, data: bytes("abc") });

  const stream = await reg.openRead(binding.url, { document: doc });
  const ended = stream.waitForLeaseEnd();
  await drain(stream);

  const detail = await ended;
  assert.equal(detail.reason, "complete");
  assert.equal(detail.bytesRead, 3);
  assert.equal(detail.token, binding.token);
});

test("leaseend events are emitted exactly once per stream", async () => {
  const reg = new BlobAddressRegistry({ clock: new FakeClock() });
  const doc = document("partition-a");
  const binding = reg.createAddress({ document: doc, data: bytes("abc") });

  const reasons: string[] = [];
  reg.addEventListener("leaseend", (event) => reasons.push(event.reason));

  const stream = await reg.openRead(binding.url, { document: doc });
  await drain(stream);
  stream.cancel(); // no-op after natural completion

  assert.deepEqual(reasons, ["complete"]);
});
