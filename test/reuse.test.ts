import assert from "node:assert/strict";
import { test } from "node:test";
import {
  AddressDeniedError,
  AddressRevokedError,
  BlobAddressRegistry,
} from "../index.ts";
import { bytes, decode, document, drain, FakeClock, scriptedTokens } from "./helpers.ts";

test("reusing a known token string in a new address never revives old content", async () => {
  const clock = new FakeClock();
  const reg = new BlobAddressRegistry({ clock, tokenSource: scriptedTokens(["tok-1", "tok-1", "tok-2"]) });
  const owner = document("partition-a");

  const first = reg.createAddress({ document: owner, data: bytes("first-content") });
  assert.equal(first.url, "blob:tok-1");
  reg.revoke(first.url, { document: owner });

  // The token source asks for the same string again; the registry must
  // skip the still-present revoked entry and mint a fresh address instead.
  const second = reg.createAddress({ document: owner, data: bytes("second-content") });
  assert.equal(second.url, "blob:tok-2");
  assert.notEqual(second.url, first.url);

  await assert.rejects(
    reg.openRead(first.url, { document: owner }),
    AddressRevokedError,
  );
  const stream = await reg.openRead(second.url, { document: owner });
  assert.equal(decode(await drain(stream)), "second-content");
});

test("a token replayed in a different tenant cannot reach the original blob", async () => {
  const clock = new FakeClock();
  const reg = new BlobAddressRegistry({ clock, tokenSource: scriptedTokens(["shared-token"]) });
  const tenantA = document("partition-a");
  const tenantB = document("partition-b");

  const bindingA = reg.createAddress({ document: tenantA, data: bytes("a-secret") });
  assert.equal(bindingA.url, "blob:shared-token");

  // Tenant B holds the copied string but never owned the binding.
  await assert.rejects(
    reg.openRead(bindingA.url, { document: tenantB }),
    AddressDeniedError,
  );

  // Even if tenant B later mints its own address, B's registry entries
  // cannot collide with A's live token: a new unique string is produced.
  const bindingB = reg.createAddress({ document: tenantB, data: bytes("b-data") });
  assert.notEqual(bindingB.url, bindingA.url);
  const streamB = await reg.openRead(bindingB.url, { document: tenantB });
  assert.equal(decode(await drain(streamB)), "b-data");

  // A's content remains readable only by A; B still cannot probe A's URL.
  const streamA = await reg.openRead(bindingA.url, { document: tenantA });
  assert.equal(decode(await drain(streamA)), "a-secret");
  await assert.rejects(
    reg.openRead(bindingA.url, { document: tenantB }),
    AddressDeniedError,
  );
});

test("many concurrent creates never hand out the same address twice", () => {
  const reg = new BlobAddressRegistry({ clock: new FakeClock() });
  const doc = document("partition-a");
  const urls = new Set<string>();

  for (let index = 0; index < 200; index += 1) {
    const binding = reg.createAddress({ document: doc, data: bytes(`${index}`) });
    assert.equal(urls.has(binding.url), false);
    urls.add(binding.url);
  }
  assert.equal(urls.size, 200);
});
