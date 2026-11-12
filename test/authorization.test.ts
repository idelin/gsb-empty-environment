import assert from "node:assert/strict";
import { test } from "node:test";
import {
  AddressDeniedError,
  BlobAddressRegistry,
  SandboxDocument,
} from "../index.ts";
import {
  bytes,
  decode,
  document,
  drain,
  FakeClock,
  generation,
  origin,
  partition,
} from "./helpers.ts";

function registry() {
  return new BlobAddressRegistry({ clock: new FakeClock() });
}

test("authorised creator document reads back the exact blob content", async () => {
  const reg = registry();
  const doc = document("partition-a");
  const binding = reg.createAddress({ document: doc, data: bytes("tenant-secret") });

  const stream = await reg.openRead(binding.url, { document: doc });
  assert.equal(decode(await drain(stream)), "tenant-secret");
});

test("copied address string is unreadable from another storage partition", async () => {
  const reg = registry();
  const owner = document("partition-a");
  const binding = reg.createAddress({ document: owner, data: bytes("tenant-secret") });

  const attacker = document("partition-b");
  // The string was copied, but possession alone grants nothing.
  await assert.rejects(
    reg.openRead(binding.url, { document: attacker }),
    AddressDeniedError,
  );
  assert.equal(reg.has(binding.url, attacker), false);
});

test("different origin inside the same partition is denied", async () => {
  const reg = registry();
  const owner = document("partition-a", "gen-1", "app.example");
  const binding = reg.createAddress({ document: owner, data: bytes("x") });

  const crossOrigin = new SandboxDocument(
    origin("evil.example"),
    owner.partition,
    owner.generation,
  );
  await assert.rejects(
    reg.openRead(binding.url, { document: crossOrigin }),
    AddressDeniedError,
  );
});

test("port difference changes the origin and denies the read", async () => {
  const reg = registry();
  const owner = new SandboxDocument(
    origin("app.example", "https", 443),
    { id: "partition-a" },
    { id: "gen-1" },
  );
  const binding = reg.createAddress({ document: owner, data: bytes("x") });

  const otherPort = new SandboxDocument(
    origin("app.example", "https", 8443),
    owner.partition,
    owner.generation,
  );
  await assert.rejects(
    reg.openRead(binding.url, { document: otherPort }),
    AddressDeniedError,
  );
});

test("a new instance generation cannot read prior-generation addresses", async () => {
  const reg = registry();
  const oldDoc = document("partition-a", "gen-1");
  const binding = reg.createAddress({ document: oldDoc, data: bytes("legacy") });

  const restarted = document("partition-a", "gen-2");
  await assert.rejects(
    reg.openRead(binding.url, { document: restarted }),
    AddressDeniedError,
  );

  // After the old generation is purged, the old document sees nothing either.
  reg.purgeGeneration("gen-1");
  await assert.rejects(
    reg.openRead(binding.url, { document: oldDoc }),
    AddressDeniedError,
  );
});

test("inactive documents cannot open new reads", async () => {
  const reg = registry();
  const doc = document("partition-a");
  const binding = reg.createAddress({ document: doc, data: bytes("x") });
  doc.deactivate();

  await assert.rejects(
    reg.openRead(binding.url, { document: doc }),
    AddressDeniedError,
  );
});

test("malformed or unknown URLs are denied without leaking existence", async () => {
  const reg = registry();
  const doc = document("partition-a");

  for (const bad of ["https://app.example/nope", "blob:", "blob:ghost-token"]) {
    await assert.rejects(
      reg.openRead(bad, { document: doc }),
      AddressDeniedError,
    );
  }
});

test("stored bytes are snapshot copies immune to caller mutation", async () => {
  const reg = registry();
  const doc = document("partition-a");
  const payload = bytes("original");
  const binding = reg.createAddress({ document: doc, data: payload });

  payload.fill(0);
  const stream = await reg.openRead(binding.url, { document: doc });
  assert.equal(decode(await drain(stream)), "original");
});

test("the full authorisation matrix grants exactly the one matching tuple", async () => {
  const reg = registry();
  const owner = document("partition-a", "gen-1", "app.example");
  const binding = reg.createAddress({ document: owner, data: bytes("matrix") });

  const sameTuple = new SandboxDocument(
    origin("app.example"),
    partition("partition-a"),
    generation("gen-1"),
  );
  const mismatches = [
    new SandboxDocument(origin("app.example"), partition("partition-b"), generation("gen-1")),
    new SandboxDocument(origin("other.example"), partition("partition-a"), generation("gen-1")),
    new SandboxDocument(origin("app.example", "http"), partition("partition-a"), generation("gen-1")),
    new SandboxDocument(origin("app.example"), partition("partition-a"), generation("gen-9")),
  ];

  const allowed = await reg.openRead(binding.url, { document: sameTuple });
  assert.equal(decode(await drain(allowed)), "matrix");

  for (const caller of mismatches) {
    await assert.rejects(
      reg.openRead(binding.url, { document: caller }),
      AddressDeniedError,
    );
  }
});
