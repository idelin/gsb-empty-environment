import assert from "node:assert/strict";
import { test } from "node:test";
import {
  AddressDeniedError,
  BlobAddressRegistry,
} from "../index.ts";
import {
  bytes,
  context,
  decode,
  document,
  drain,
  FakeClock,
  origin,
} from "./helpers.ts";

test("cross-partition navigation replaces the document and denies the copied address", async () => {
  const reg = new BlobAddressRegistry({ clock: new FakeClock() });
  const browsing = context("partition-a");
  const firstDoc = browsing.document;
  const binding = reg.createAddress({
    document: firstDoc,
    data: bytes("frame-secret"),
  });

  // The blob is minted and its address string leaks into the new frame.
  const leakedUrl = binding.url;

  const navigation = browsing.navigate({ partition: { id: "partition-b" } });
  assert.equal(navigation.sameOriginPartition, false);
  assert.equal(firstDoc.active, false);
  assert.equal(browsing.document.active, true);

  // Old document can no longer read; the new partition can't either.
  await assert.rejects(
    reg.openRead(leakedUrl, { document: firstDoc }),
    AddressDeniedError,
  );
  await assert.rejects(
    reg.openRead(leakedUrl, { document: browsing.document }),
    AddressDeniedError,
  );
});

test("same-origin same-partition navigation allows the new document but retires the old one", async () => {
  const reg = new BlobAddressRegistry({ clock: new FakeClock() });
  const browsing = context("partition-a");
  const oldDoc = browsing.document;
  const binding = reg.createAddress({ document: oldDoc, data: bytes("spa-state") });

  const navigation = browsing.navigate({});
  assert.equal(navigation.sameOriginPartition, true);
  const newDoc = browsing.document;

  assert.equal(oldDoc.active, false);
  await assert.rejects(
    reg.openRead(binding.url, { document: oldDoc }),
    AddressDeniedError,
  );

  const stream = await reg.openRead(binding.url, { document: newDoc });
  assert.equal(decode(await drain(stream)), "spa-state");
});

test("cross-origin navigation inside one partition still fails the origin binding", async () => {
  const reg = new BlobAddressRegistry({ clock: new FakeClock() });
  const browsing = context("partition-a", "gen-1", "app.example");
  const binding = reg.createAddress({
    document: browsing.document,
    data: bytes("origin-bound"),
  });

  browsing.navigate({ origin: origin("other.example") });
  await assert.rejects(
    reg.openRead(binding.url, { document: browsing.document }),
    AddressDeniedError,
  );
});

test("a read pending during navigation is aborted before any stream is issued", async () => {
  const reg = new BlobAddressRegistry({ clock: new FakeClock() });
  const browsing = context("partition-a");
  const doc = browsing.document;
  const binding = reg.createAddress({ document: doc, data: bytes("pending") });

  const pending = reg.openRead(binding.url, {
    document: doc,
    signal: doc.lifecycleSignal,
  });
  // Navigate before the microtask that issues the stream runs.
  browsing.navigate({});

  await assert.rejects(pending, /aborted/);
  assert.equal(reg.activeStreamCount(binding.url, browsing.document), 0);
});

test("navigation does not revoke: a same-tenant re-navigation recovers access", async () => {
  const reg = new BlobAddressRegistry({ clock: new FakeClock() });
  const browsing = context("partition-a");
  const binding = reg.createAddress({
    document: browsing.document,
    data: bytes("still-here"),
  });

  browsing.navigate({});
  browsing.navigate({});
  const current = browsing.document;

  const stream = await reg.openRead(binding.url, { document: current });
  assert.equal(decode(await drain(stream)), "still-here");
});

test("closing the browsing context deactivates the document and blocks reads", async () => {
  const reg = new BlobAddressRegistry({ clock: new FakeClock() });
  const browsing = context("partition-a");
  const binding = reg.createAddress({
    document: browsing.document,
    data: bytes("closing"),
  });

  browsing.close();
  await assert.rejects(
    reg.openRead(binding.url, { document: browsing.document }),
    AddressDeniedError,
  );
});
