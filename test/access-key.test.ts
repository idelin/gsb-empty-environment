import { test } from "node:test";
import assert from "node:assert/strict";
import {
  ACCESS_KEY_LENGTH,
  normalizeAccessKey,
  verifyUnidentifiedAccessKey,
  type AccessKeyRecord,
  type AccessKeyRegistry,
} from "../src/access-key.ts";
import { AccessKeyError } from "../src/errors.ts";
import { hex } from "./helpers.ts";

const rawKey = new Uint8Array(16).fill(7);

test("normalizeAccessKey: accepts raw 16-byte input", () => {
  assert.deepEqual(normalizeAccessKey(rawKey), rawKey);
});

test("normalizeAccessKey: accepts hex and base64", () => {
  assert.deepEqual(normalizeAccessKey(hex(rawKey)), rawKey);
  const base64 = btoa(String.fromCharCode(...rawKey));
  assert.deepEqual(normalizeAccessKey(base64), rawKey);
});

test("normalizeAccessKey: rejects malformed inputs with the same error", () => {
  const malformed: unknown[] = [
    new Uint8Array(15),
    new Uint8Array(17),
    "not-hex!!",
    "abcd",
    42,
    null,
    undefined,
    {},
  ];
  for (const value of malformed) {
    assert.throws(() => normalizeAccessKey(value), AccessKeyError);
  }
});

function registryWith(record?: AccessKeyRecord): AccessKeyRegistry {
  return async () => record;
}

test("verifyUnidentifiedAccessKey: matching key returns the record", async () => {
  const record: AccessKeyRecord = { recipientId: "alice", accessKey: rawKey };
  const result = await verifyUnidentifiedAccessKey({
    presentedKey: new Uint8Array(rawKey),
    recipientId: "alice",
    registry: registryWith(record),
  });
  assert.equal(result.recipientId, "alice");
});

test("verifyUnidentifiedAccessKey: every failure path throws the identical error type and code", async () => {
  const record: AccessKeyRecord = { recipientId: "alice", accessKey: rawKey };
  const wrongKey = new Uint8Array(16).fill(8);

  const cases: unknown[] = [
    { presented: wrongKey, record },
    { presented: rawKey, record: undefined },
    { presented: "malformed", record },
    { presented: new Uint8Array(8), record: undefined },
  ];

  for (const item of cases) {
    const { presented, record: maybeRecord } = item as {
      presented: unknown;
      record?: AccessKeyRecord;
    };
    await assert.rejects(
      verifyUnidentifiedAccessKey({
        presentedKey: presented,
        recipientId: "alice",
        registry: registryWith(maybeRecord),
      }),
      (error: unknown) =>
        error instanceof AccessKeyError &&
        error.code === "ACCESS_KEY_REJECTED" &&
        error.message === "unidentified access key rejected",
    );
  }
});

test("verifyUnidentifiedAccessKey: hex/base64 header values verify against stored bytes", async () => {
  const record: AccessKeyRecord = { recipientId: "alice", accessKey: rawKey };
  const base64 = btoa(String.fromCharCode(...rawKey));
  const byBase64 = await verifyUnidentifiedAccessKey({
    presentedKey: base64,
    recipientId: "alice",
    registry: registryWith(record),
  });
  assert.equal(byBase64.recipientId, "alice");
  const byHex = await verifyUnidentifiedAccessKey({
    presentedKey: hex(rawKey),
    recipientId: "alice",
    registry: registryWith(record),
  });
  assert.equal(byHex.recipientId, "alice");
});

test("ACCESS_KEY_LENGTH is the Signal UAK length", () => {
  assert.equal(ACCESS_KEY_LENGTH, 16);
});
