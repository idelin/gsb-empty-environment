import test from "node:test";
import assert from "node:assert/strict";

import {
  IdentityPointError,
  InMemoryCompletedSidStore,
  ISKDeriver,
  SidReuseError,
  buildMessageTranscript,
  bytesToHexString,
  compareBytes,
  deriveISK,
  encodeLeb128,
  encodeUtf8,
  lvCat,
  orderedConcat,
  prependLength,
  toBytes,
  withISKSuffix,
} from "./index.ts";

const DOMAIN = withISKSuffix("CPaceRistretto255");
const SID = hexToBytes("7e4b4791d6a8ef019b936c79fb7f2c57");
const K = hexToBytes(
  "5a2c4c7d7a9e1f0b6d3c8b4a2e9f7c1d6b3a8e5f2c9d4a7b1e8f3c6a9d2b5e7f",
);

function hexToBytes(value: string): Uint8Array {
  const normalized = value.replace(/\s+/g, "");
  if (normalized.length % 2 !== 0) {
    throw new Error("hex input must have an even length");
  }
  const result = new Uint8Array(normalized.length / 2);
  for (let index = 0; index < result.length; index += 1) {
    result[index] = Number.parseInt(normalized.slice(index * 2, index * 2 + 2), 16);
  }
  return result;
}

function assertBytesEqual(actual: Uint8Array, expectedHex: string): void {
  assert.equal(bytesToHexString(actual), expectedHex);
}

test("lv_cat and LEB128 match the CPace reference vectors and boundaries", () => {
  assertBytesEqual(lvCat("1234", "5", "", "678"), "043132333401350003363738");
  assertBytesEqual(lvCat("1234", "5", "", "6789"), "04313233340135000436373839");

  const leb128Cases: Array<[number, string]> = [
    [0, "00"],
    [1, "01"],
    [127, "7f"],
    [128, "8001"],
    [255, "ff01"],
    [16383, "ff7f"],
    [16384, "808001"],
  ];
  for (const [value, expected] of leb128Cases) {
    assertBytesEqual(encodeLeb128(value), expected);
  }

  const payload127 = new Uint8Array(127).fill(0x61);
  const payload128 = new Uint8Array(128).fill(0x61);
  assertBytesEqual(prependLength(payload127).slice(0, 1), "7f");
  assertBytesEqual(prependLength(payload128).slice(0, 2), "8001");
});

test("length prefixes make differently split messages unambiguous", () => {
  assert.notDeepEqual(lvCat("ab", "c"), lvCat("a", "bc"));

  const common = {
    domainSeparator: DOMAIN,
    sid: "split-test",
    sharedPoint: K,
    mode: "initiator-responder" as const,
  };
  const firstSplit = deriveISK({ ...common, initiatorMessage: "ab", responderMessage: "c" });
  const secondSplit = deriveISK({ ...common, initiatorMessage: "a", responderMessage: "bc" });
  assert.notDeepEqual(firstSplit, secondSplit);
});

test("transcript_ir and transcript_oc match CPace's published vectors", () => {
  const ir = buildMessageTranscript({
    mode: "initiator-responder",
    initiatorMessage: { publicShare: "123", associatedData: "PartyA" },
    responderMessage: { publicShare: "234", associatedData: "PartyB" },
  });
  assertBytesEqual(
    ir,
    "03313233065061727479410332333406506172747942",
  );

  const first = lvCat("123", "PartyA");
  const second = lvCat("234", "PartyB");
  const oc = orderedConcat(first, second);
  assertBytesEqual(
    oc,
    "6f6303323334065061727479420331323306506172747941",
  );

  assert.equal(compareBytes(first, first), 0);
  assert.equal(compareBytes(new Uint8Array([1]), new Uint8Array([0, 0])), 1);
  assert.equal(compareBytes(new Uint8Array([0, 0]), new Uint8Array([0])), 1);
});

test("symmetric message swapping produces the same ISK", () => {
  const common = {
    domainSeparator: DOMAIN,
    sid: SID,
    sharedPoint: K,
    mode: "symmetric" as const,
    hash: "sha256" as const,
  };
  const forward = deriveISK({
    ...common,
    messages: ["message-from-a", "message-from-b"],
  });
  const swapped = deriveISK({
    ...common,
    messages: ["message-from-b", "message-from-a"],
  });

  assert.deepEqual(forward, swapped);
});

test("initiator/responder message swapping produces a different ISK", () => {
  const common = {
    domainSeparator: DOMAIN,
    sid: SID,
    sharedPoint: K,
    mode: "initiator-responder" as const,
    hash: "sha256" as const,
  };
  const forward = deriveISK({
    ...common,
    initiatorMessage: "message-from-a",
    responderMessage: "message-from-b",
  });
  const swapped = deriveISK({
    ...common,
    initiatorMessage: "message-from-b",
    responderMessage: "message-from-a",
  });

  assert.notDeepEqual(forward, swapped);
});

test("ISK binds the domain separator, sid, shared point, and transcript", () => {
  const base = {
    domainSeparator: DOMAIN,
    sid: SID,
    sharedPoint: K,
    mode: "initiator-responder" as const,
    initiatorMessage: "initiator",
    responderMessage: "responder",
    hash: "sha256" as const,
  };
  const baseISK = deriveISK(base);

  assert.equal(baseISK.length, 32);
  assert.notDeepEqual(
    baseISK,
    deriveISK({ ...base, domainSeparator: withISKSuffix("OtherSuite") }),
  );
  assert.notDeepEqual(baseISK, deriveISK({ ...base, sid: "different-sid" }));

  const differentK = new Uint8Array(K);
  differentK[0] ^= 0x01;
  assert.notDeepEqual(baseISK, deriveISK({ ...base, sharedPoint: differentK }));
});

test("completed sid reuse is rejected, including equivalent byte/string encodings", () => {
  const deriver = new ISKDeriver({ hash: "sha256" });
  const input = {
    domainSeparator: DOMAIN,
    sid: "login-session-1",
    sharedPoint: K,
    mode: "symmetric" as const,
    messages: ["a", "b"] as const,
  };

  deriver.derive(input);
  assert.throws(
    () => deriver.derive({ ...input, messages: ["c", "d"] }),
    SidReuseError,
  );
  assert.throws(
    () => deriver.derive({ ...input, sid: encodeUtf8("login-session-1") }),
    SidReuseError,
  );

  const nextSession = deriver.derive({ ...input, sid: "login-session-2" });
  assert.equal(nextSession.length, 32);
});

test("a shared completed-sid store prevents replay across SDK instances", () => {
  const store = new InMemoryCompletedSidStore();
  const first = new ISKDeriver({ completedSidStore: store, hash: "sha256" });
  const second = new ISKDeriver({ completedSidStore: store, hash: "sha256" });
  const base = {
    domainSeparator: DOMAIN,
    sid: "shared-store-sid",
    sharedPoint: K,
    mode: "initiator-responder" as const,
    initiatorMessage: "a",
    responderMessage: "b",
  };

  first.derive(base);
  assert.throws(() => second.derive(base), SidReuseError);
});

test("the group identity is rejected and does not consume the sid", () => {
  const zeroK = new Uint8Array(32);
  const base = {
    domainSeparator: DOMAIN,
    sid: "identity-session",
    mode: "symmetric" as const,
    messages: ["a", "b"] as const,
    hash: "sha256" as const,
  };

  assert.throws(
    () => deriveISK({ ...base, sharedPoint: zeroK }),
    IdentityPointError,
  );

  const deriver = new ISKDeriver({ hash: "sha256" });
  assert.throws(
    () => deriver.derive({ ...base, sharedPoint: zeroK }),
    IdentityPointError,
  );
  assert.equal(deriver.derive({ ...base, sharedPoint: K }).length, 32);
});

test("a group-specific encoded identity can be supplied", () => {
  const identityElement = hexToBytes("0100");
  const input = {
    domainSeparator: DOMAIN,
    sid: "custom-identity",
    sharedPoint: identityElement,
    identityElement,
    mode: "symmetric" as const,
    messages: ["a", "b"] as const,
  };

  assert.throws(() => deriveISK(input), IdentityPointError);
  assert.deepEqual(toBytes("abc"), encodeUtf8("abc"));
});

