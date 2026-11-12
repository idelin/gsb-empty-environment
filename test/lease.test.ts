import assert from "node:assert/strict";
import { test } from "node:test";
import {
  AddressDeniedError,
  BlobAddressRegistry,
  ReadClosedError,
} from "../index.ts";
import {
  bytes,
  decode,
  document,
  drain,
  FakeClock,
} from "./helpers.ts";

test("a lease expires on schedule and further reads throw", async () => {
  const clock = new FakeClock();
  const reg = new BlobAddressRegistry({ clock, defaultLeaseMs: 1_000 });
  const doc = document("partition-a");
  const binding = reg.createAddress({
    document: doc,
    data: bytes("0123456789"),
  });

  const stream = await reg.openRead(binding.url, {
    document: doc,
    chunkSize: 4,
  });
  assert.equal(stream.remainingMs, 1_000);
  assert.equal((await stream.read())?.byteLength, 4);

  clock.advance(999);
  assert.equal(stream.active, true);
  clock.advance(2);
  assert.equal(stream.active, false);

  await assert.rejects(stream.read(), ReadClosedError);
  assert.equal(stream.leaseState, "expired");
  assert.equal(reg.activeStreamCount(binding.url, doc), 0);
});

test("lease expiry of an old stream does not block new reads within a fresh lease", async () => {
  const clock = new FakeClock();
  const reg = new BlobAddressRegistry({ clock, defaultLeaseMs: 1_000 });
  const doc = document("partition-a");
  const binding = reg.createAddress({ document: doc, data: bytes("renew") });

  const first = await reg.openRead(binding.url, { document: doc });
  clock.advance(1_001);
  await assert.rejects(first.read(), ReadClosedError);

  const second = await reg.openRead(binding.url, { document: doc, leaseMs: 5_000 });
  assert.equal(second.remainingMs, 5_000);
  assert.equal(decode(await drain(second)), "renew");
});

test("interleaved revoke, reuse, navigation and cancel only lets authorised documents read", async () => {
  const clock = new FakeClock();
  const reg = new BlobAddressRegistry({ clock, defaultLeaseMs: 2_000 });
  const tenantADoc = document("partition-a", "gen-1");
  const tenantBDoc = document("partition-b", "gen-1");

  // 1. Tenant A mints a blob and begins reading in chunks.
  const bindingA = reg.createAddress({
    document: tenantADoc,
    data: bytes("a-secret-data"),
  });
  const streamA = await reg.openRead(bindingA.url, {
    document: tenantADoc,
    chunkSize: 3,
  });
  assert.equal(decode((await streamA.read()) as Uint8Array), "a-s");

  // 2. Tenant B copies the URL and is denied at every interleaving point.
  await assert.rejects(
    reg.openRead(bindingA.url, { document: tenantBDoc }),
    AddressDeniedError,
  );

  // 3. A revokes; B's denial stays opaque and A's stream survives.
  reg.revoke(bindingA.url, { document: tenantADoc });
  await assert.rejects(
    reg.openRead(bindingA.url, { document: tenantBDoc }),
    AddressDeniedError,
  );
  assert.equal(decode(await drain(streamA)), "ecret-data");

  // 4. A mints a replacement; B still cannot read either address.
  const bindingA2 = reg.createAddress({
    document: tenantADoc,
    data: bytes("a-replacement"),
  });
  await assert.rejects(
    reg.openRead(bindingA2.url, { document: tenantBDoc }),
    AddressDeniedError,
  );

  // 5. A's document is replaced by a new generation; old and new
  //    addresses alike stop resolving.
  const nextGeneration = document("partition-a", "gen-2");
  await assert.rejects(
    reg.openRead(bindingA2.url, { document: nextGeneration }),
    AddressDeniedError,
  );
});

test("empty blobs complete their lease immediately but stay governed by the binding", async () => {
  const reg = new BlobAddressRegistry({ clock: new FakeClock() });
  const owner = document("partition-a");
  const outsider = document("partition-b");
  const binding = reg.createAddress({ document: owner, data: new Uint8Array(0) });

  const stream = await reg.openRead(binding.url, { document: owner });
  assert.equal(await stream.read(), null);
  assert.equal(stream.leaseState, "complete");
  await assert.rejects(
    reg.openRead(binding.url, { document: outsider }),
    AddressDeniedError,
  );
});
