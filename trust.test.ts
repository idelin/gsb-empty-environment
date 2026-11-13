import test from "node:test";
import assert from "node:assert/strict";
import {
  FULL_TRUST_AMOUNT,
  MARGINAL_TRUST_AMOUNT,
  compileTrustRegex,
  extractEmail,
  isUserIdValid,
  type TrustSignature,
} from "./trust.ts";

const NOW = 1_800_000_000_000;

const ROOT = "root-key";
const ALICE = "alice-key";
const BOB = "bob-key";
const TARGET = "target-key";
const TARGET_UID = "Carol <carol@example.com>";

let counter = 0;
function sig(partial: Partial<TrustSignature>): TrustSignature {
  counter += 1;
  return {
    issuerKeyId: ROOT,
    targetKeyId: TARGET,
    userId: TARGET_UID,
    level: 1,
    amount: FULL_TRUST_AMOUNT,
    createdAt: NOW - 1000 + counter,
    ...partial,
  };
}

const acceptAll = () => true;
const root = { keyId: ROOT, maxDepth: 4, now: NOW };

test("direct root certification with full amount is valid", () => {
  assert.equal(
    isUserIdValid(TARGET, TARGET_UID, [sig({})], root, acceptAll),
    true,
  );
});

test("marginal amount alone is insufficient", () => {
  assert.equal(
    isUserIdValid(
      TARGET,
      TARGET_UID,
      [sig({ amount: MARGINAL_TRUST_AMOUNT })],
      root,
      acceptAll,
    ),
    false,
  );
});

test("amounts accumulate along a level-decreasing path", () => {
  const signatures = [
    sig({ targetKeyId: ALICE, userId: "Alice <alice@example.com>", level: 2, amount: MARGINAL_TRUST_AMOUNT }),
    sig({ issuerKeyId: ALICE, level: 1, amount: MARGINAL_TRUST_AMOUNT }),
  ];
  assert.equal(
    isUserIdValid(TARGET, TARGET_UID, signatures, root, acceptAll),
    true,
  );
});

test("path with non-decreasing levels is rejected", () => {
  const signatures = [
    sig({ targetKeyId: ALICE, userId: "Alice <alice@example.com>", level: 1, amount: MARGINAL_TRUST_AMOUNT }),
    sig({ issuerKeyId: ALICE, level: 2, amount: FULL_TRUST_AMOUNT }),
  ];
  assert.equal(
    isUserIdValid(TARGET, TARGET_UID, signatures, root, acceptAll),
    false,
  );
});

test("introducer cannot certify outside its regex domain", () => {
  const signatures = [
    sig({
      targetKeyId: ALICE,
      userId: "Alice <alice@example.com>",
      level: 2,
      regex: "<[^>]+[@.]example.com>$",
    }),
    sig({ issuerKeyId: ALICE, userId: "Mallory <mallory@evil.net>" }),
  ];
  assert.equal(
    isUserIdValid(TARGET, "Mallory <mallory@evil.net>", signatures, root, acceptAll),
    false,
  );
});

test("introducer certification inside its regex domain is valid", () => {
  const signatures = [
    sig({
      targetKeyId: ALICE,
      userId: "Alice <alice@example.com>",
      level: 2,
      regex: "<[^>]+[@.]example.com>$",
    }),
    sig({ issuerKeyId: ALICE }),
  ];
  assert.equal(
    isUserIdValid(TARGET, TARGET_UID, signatures, root, acceptAll),
    true,
  );
});

test("regex scoping applies across multiple hops", () => {
  const signatures = [
    sig({
      targetKeyId: ALICE,
      userId: "Alice <alice@example.com>",
      level: 3,
      regex: "<[^>]+[@.]example.com>$",
    }),
    sig({
      issuerKeyId: ALICE,
      targetKeyId: BOB,
      userId: "Bob <bob@example.com>",
      level: 2,
    }),
    sig({ issuerKeyId: BOB, level: 1, userId: "Mallory <mallory@evil.net>" }),
  ];
  assert.equal(
    isUserIdValid(TARGET, "Mallory <mallory@evil.net>", signatures, root, acceptAll),
    false,
  );
});

test("malformed regex invalidates the whole signature", () => {
  for (const bad of [
    ".*",
    "<[^>]+@example.com>$",
    "<[^>]+[@.]example.com>",
    "^<[^>]+[@.]example.com>$",
    "<[^>]+[@.]example.com|evil.net>$",
  ]) {
    assert.equal(compileTrustRegex(bad), null, `expected ${bad} to be rejected`);
    const signatures = [
      sig({
        targetKeyId: ALICE,
        userId: "Alice <alice@example.com>",
        level: 2,
        regex: bad,
      }),
      sig({ issuerKeyId: ALICE }),
    ];
    assert.equal(
      isUserIdValid(TARGET, TARGET_UID, signatures, root, acceptAll),
      false,
    );
  }
});

test("only the latest signature per issuer and user id counts", () => {
  const older = sig({
    issuerKeyId: ALICE,
    amount: MARGINAL_TRUST_AMOUNT,
    createdAt: NOW - 5000,
  });
  const newer = sig({
    issuerKeyId: ALICE,
    amount: MARGINAL_TRUST_AMOUNT,
    createdAt: NOW - 1000,
  });
  const viaRoot = sig({ level: 2, amount: MARGINAL_TRUST_AMOUNT, targetKeyId: ALICE, userId: "Alice <alice@example.com>" });
  const signatures = [viaRoot, older, newer];
  // 60 (root->alice) + 60 (latest alice->target) = 120: valid.
  assert.equal(
    isUserIdValid(TARGET, TARGET_UID, signatures, root, acceptAll),
    true,
  );
  // If duplicates were summed (60+60+60) this would pass; with dedup it is 60+50 < 120.
  const weak = [
    viaRoot,
    { ...older, amount: 50 },
    { ...newer, amount: 50 },
  ];
  assert.equal(
    isUserIdValid(TARGET, TARGET_UID, weak, root, acceptAll),
    false,
  );
});

test("revoked signatures do not count", () => {
  const signatures = [sig({ revoked: true })];
  assert.equal(
    isUserIdValid(TARGET, TARGET_UID, signatures, root, acceptAll),
    false,
  );
});

test("revocation of the duplicate leaves the surviving latest", () => {
  const older = sig({ amount: FULL_TRUST_AMOUNT, createdAt: NOW - 5000 });
  const revokedNewer = sig({
    amount: 1,
    createdAt: NOW - 1000,
    revoked: true,
  });
  assert.equal(
    isUserIdValid(TARGET, TARGET_UID, [older, revokedNewer], root, acceptAll),
    true,
  );
});

test("expired signatures do not count", () => {
  const expired = sig({ expiresAt: NOW - 1 });
  const stillValid = sig({ expiresAt: NOW + 1000 });
  assert.equal(
    isUserIdValid(TARGET, TARGET_UID, [expired], root, acceptAll),
    false,
  );
  assert.equal(
    isUserIdValid(TARGET, TARGET_UID, [stillValid], root, acceptAll),
    true,
  );
});

test("paths deeper than the root limit do not count", () => {
  const signatures = [
    sig({ targetKeyId: ALICE, userId: "Alice <alice@example.com>", level: 3, amount: MARGINAL_TRUST_AMOUNT }),
    sig({ issuerKeyId: ALICE, targetKeyId: BOB, userId: "Bob <bob@example.com>", level: 2, amount: MARGINAL_TRUST_AMOUNT }),
    sig({ issuerKeyId: BOB, level: 1, amount: MARGINAL_TRUST_AMOUNT }),
  ];
  assert.equal(
    isUserIdValid(TARGET, TARGET_UID, signatures, { ...root, maxDepth: 3 }, acceptAll),
    true,
  );
  assert.equal(
    isUserIdValid(TARGET, TARGET_UID, signatures, { ...root, maxDepth: 2 }, acceptAll),
    false,
  );
});

test("cryptographic verification is delegated to the caller", () => {
  const forged = sig({});
  const rejectAll = () => false;
  assert.equal(
    isUserIdValid(TARGET, TARGET_UID, [forged], root, rejectAll),
    false,
  );
  const rejectForged = (s: TrustSignature) => s !== forged;
  assert.equal(
    isUserIdValid(TARGET, TARGET_UID, [forged, sig({})], root, rejectForged),
    true,
  );
});

test("user id without an email can never be valid", () => {
  assert.equal(
    isUserIdValid(TARGET, "Carol", [sig({ userId: "Carol" })], root, acceptAll),
    false,
  );
});

test("compileTrustRegex accepts the allowed domain-scoped form", () => {
  const regex = compileTrustRegex("<[^>]+[@.]example.com>$");
  assert.ok(regex);
  assert.ok(regex.test("Carol <carol@example.com>"));
  assert.ok(regex.test("Carol <carol@sub.example.com>"));
  assert.ok(!regex.test("Mallory <mallory@evil.net>"));
});

test("extractEmail parses angle-bracket and bare forms", () => {
  assert.equal(extractEmail("Carol <carol@example.com>"), "carol@example.com");
  assert.equal(extractEmail("carol@example.com"), "carol@example.com");
  assert.equal(extractEmail("Carol"), null);
});
