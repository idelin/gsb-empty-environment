import assert from "node:assert/strict";
import { test } from "node:test";
import {
  canonicalizeForm,
  defineAliases,
  signGrant,
  stableStringify,
  verifyGrant,
  type SigningKey,
} from "../src/index.ts";
import { buildGrant, testKey } from "../src/testing/index.ts";

const aliases = defineAliases([
  ["beneficiary", ["payee", "legalName"]],
  ["amt", ["amount"]],
]);

test("path aliases canonicalize to the same digest as canonical fields", () => {
  const viaAlias = canonicalizeForm({ beneficiary: "Acme GmbH", amt: 10, memo: "x" }, aliases);
  const viaCanonical = canonicalizeForm(
    { "payee/legalName": "Acme GmbH", amount: 10, memo: "x" },
    aliases,
  );
  assert.equal(viaAlias.digest, viaCanonical.digest);
  assert.deepEqual(
    viaAlias.bindings.map((binding) => binding.path),
    [["amount"], ["memo"], ["payee", "legalName"]],
  );
});

test("stable serialization ignores key insertion order", () => {
  assert.equal(stableStringify({ b: 1, a: { d: 2, c: 3 } }), stableStringify({ a: { c: 3, d: 2 }, b: 1 }));
});

test("different field values or paths produce different digests", () => {
  const first = canonicalizeForm({ amt: 1 }, aliases);
  const second = canonicalizeForm({ amt: 2 }, aliases);
  const third = canonicalizeForm({ other: 1 }, aliases);
  assert.notEqual(first.digest, second.digest);
  assert.notEqual(first.digest, third.digest);
});

test("signature covers subject, window session and object version bindings", () => {
  const grant = buildGrant();
  const keys: readonly SigningKey[] = [testKey];
  assert.deepEqual(verifyGrant(grant, keys), { ok: true });

  const tamperedSubject = { ...grant, subject: "attacker" };
  assert.equal(verifyGrant(tamperedSubject, keys).ok, false);
  const tamperedWindow = { ...grant, windowSessionId: "window-B" };
  assert.equal(verifyGrant(tamperedWindow, keys).ok, false);
  const tamperedVersion = { ...grant, objectVersion: "obj-v8" };
  assert.equal(verifyGrant(tamperedVersion, keys).ok, false);
  const tamperedCap = {
    ...grant,
    actions: grant.actions.map((action) => ({ ...action, maxActionConsumptions: 99 })),
  };
  assert.equal(verifyGrant(tamperedCap, keys).ok, false);
});

test("expired and foreign-key grants are rejected", () => {
  const expired = buildGrant({ expiresAt: Date.now() - 1 });
  assert.equal(verifyGrant(expired, [testKey]).ok, false);
  const otherKey: SigningKey = { kid: testKey.kid, secret: "different" };
  assert.equal(verifyGrant(buildGrant(), [otherKey]).ok, false);
});

test("minting rejects duplicate action ids and unknown dependencies", () => {
  const base = buildGrant();
  assert.throws(() =>
    signGrant(
      {
        ...base,
        actions: [
          base.actions[0],
          { ...base.actions[0], actionId: base.actions[0].actionId },
        ],
      },
      testKey,
    ),
  );
});
