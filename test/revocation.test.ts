import assert from "node:assert/strict";
import { test } from "node:test";
import {
  AddressDeniedError,
  AddressRevokedError,
  BlobAddressRegistry,
  SandboxDocument,
} from "../index.ts";
import { bytes, decode, document, drain, FakeClock } from "./helpers.ts";

test("revocation fails new reads while an already issued stream finishes its lease", async () => {
  const clock = new FakeClock();
  const reg = new BlobAddressRegistry({ clock, defaultLeaseMs: 5_000 });
  const owner = document("partition-a");
  const binding = reg.createAddress({ document: owner, data: bytes("revoke-me") });

  const stream = await reg.openRead(binding.url, {
    document: owner,
    chunkSize: 4,
  });
  assert.equal(reg.activeStreamCount(binding.url, owner), 1);

  reg.revoke(binding.url, { document: owner });

  // New reads now fail with the precise owner-facing error.
  await assert.rejects(
    reg.openRead(binding.url, { document: owner }),
    AddressRevokedError,
  );
  // The outstanding stream still serves its full content until lease end.
  assert.equal(decode(await drain(stream)), "revoke-me");
  assert.equal(stream.leaseState, "complete");
  assert.equal(reg.activeStreamCount(binding.url, owner), 0);
});

test("revoked addresses always read as denied to other tenants", async () => {
  const reg = new BlobAddressRegistry({ clock: new FakeClock() });
  const owner = document("partition-a");
  const outsider = document("partition-b");
  const binding = reg.createAddress({ document: owner, data: bytes("x") });

  reg.revoke(binding.url, { document: owner });
  await assert.rejects(
    reg.openRead(binding.url, { document: outsider }),
    AddressDeniedError,
  );
});

test("outsiders cannot revoke another tenant's address", () => {
  const reg = new BlobAddressRegistry({ clock: new FakeClock() });
  const owner = document("partition-a");
  const outsider = document("partition-b");
  const binding = reg.createAddress({ document: owner, data: bytes("x") });

  assert.throws(
    () => reg.revoke(binding.url, { document: outsider }),
    AddressDeniedError,
  );
  // The owner's address is untouched by the failed cross-tenant revoke.
  assert.equal(reg.has(binding.url, owner), true);
});

test("a stream whose lease outlives revocation continues until the clock expires", async () => {
  const clock = new FakeClock();
  const reg = new BlobAddressRegistry({ clock, defaultLeaseMs: 10_000 });
  const owner = document("partition-a");
  const binding = reg.createAddress({ document: owner, data: bytes("0123456789") });

  const stream = await reg.openRead(binding.url, {
    document: owner,
    chunkSize: 4,
  });
  assert.equal((await stream.read())?.byteLength, 4);

  reg.revoke(binding.url, { document: owner });
  clock.advance(9_000);
  // Lease still alive: the pre-revocation stream keeps reading.
  assert.equal(stream.active, true);
  assert.equal((await stream.read())?.byteLength, 4);

  clock.advance(2_000);
  await assert.rejects(stream.read(), /lease has expired/);
  assert.equal(stream.leaseState, "expired");
});

test("double revoke surfaces the revoked error to the owner", () => {
  const reg = new BlobAddressRegistry({ clock: new FakeClock() });
  const owner = document("partition-a");
  const binding = reg.createAddress({ document: owner, data: bytes("x") });

  reg.revoke(binding.url, { document: owner });
  assert.throws(
    () => reg.revoke(binding.url, { document: owner }),
    AddressRevokedError,
  );
});

test("revoke emits an event carrying the token and timestamp", () => {
  const clock = new FakeClock(4_242);
  const reg = new BlobAddressRegistry({ clock });
  const owner = document("partition-a");
  const binding = reg.createAddress({ document: owner, data: bytes("x") });

  const events: unknown[] = [];
  reg.addEventListener("revoke", (event) => events.push(event));
  reg.revoke(binding.url, { document: owner });

  assert.deepEqual(events, [
    { token: binding.token, url: binding.url, revokedAt: 4_242 },
  ]);
});

test("a string copied before revocation stays unreadable to every other tenant", async () => {
  const clock = new FakeClock();
  const reg = new BlobAddressRegistry({ clock, defaultLeaseMs: 5_000 });
  const owner = document("partition-a");
  const otherPartition = document("partition-b");
  const otherOrigin = new SandboxDocument(
    { scheme: "https", host: "evil.example", port: null },
    owner.partition,
    owner.generation,
  );
  const nextGeneration = document("partition-a", "gen-2");
  const binding = reg.createAddress({ document: owner, data: bytes("leaked") });

  // Owner holds an outstanding stream; the address string is copied out.
  const outstanding = await reg.openRead(binding.url, { document: owner });
  reg.revoke(binding.url, { document: owner });

  // Every non-owner view of the copied string is a uniform denial.
  for (const caller of [otherPartition, otherOrigin, nextGeneration]) {
    await assert.rejects(
      reg.openRead(binding.url, { document: caller }),
      AddressDeniedError,
    );
  }
  // But the pre-revocation stream still ends its own lease normally.
  assert.equal(decode(await drain(outstanding)), "leaked");
});
