import { test } from "node:test";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";

import {
  PrfValidationError,
  bytesToBase64Url,
  derivePrfSalt,
  isBase64Url,
  preparePrfInput,
} from "./index.ts";

function expectedSalt(salt: Uint8Array): Uint8Array {
  const hash = createHash("sha256");
  hash.update(Buffer.from("WebAuthn PRF", "utf8"));
  hash.update(Buffer.from([0x00]));
  hash.update(Buffer.from(salt));
  return new Uint8Array(hash.digest());
}

function rejectsCode(code: string) {
  return (error: unknown): boolean => {
    assert.ok(error instanceof PrfValidationError);
    assert.equal(error.code, code);
    return true;
  };
}

const salt = new Uint8Array([0xaa, 0xbb, 0xcc]);
const credentialId = new Uint8Array([0x01, 0x02, 0x03]);
const credentialKey = bytesToBase64Url(credentialId);

test("derives the domain-separated PRF salt", async () => {
  const input = new Uint8Array([0xff, ...salt, 0x00]);
  const view = input.subarray(1, 4);
  const result = await derivePrfSalt(view);

  assert.deepEqual(result, expectedSalt(salt));
  assert.notStrictEqual(result.buffer, input.buffer);
  assert.deepEqual(view, salt);
});

test("hashes eval and evalByCredential salts without mutating input", async () => {
  const second = new Uint8Array([0x11, 0x22]);
  const input = {
    eval: { first: salt, second },
    evalByCredential: {
      [credentialKey]: { first: salt },
    },
  };
  const result = await preparePrfInput(input, [{ id: credentialId }]);

  assert.deepEqual(result.eval?.first, expectedSalt(salt));
  assert.deepEqual(result.eval?.second, expectedSalt(second));
  assert.deepEqual(
    result.evalByCredential?.[credentialKey].first,
    expectedSalt(salt),
  );
  assert.deepEqual(input.eval.first, salt);
});

test("accepts evalByCredential only for a listed credential", async () => {
  await assert.rejects(
    preparePrfInput(
      {
        evalByCredential: {
          [bytesToBase64Url(new Uint8Array([4, 5, 6]))]: { first: salt },
        },
      },
      [{ id: credentialId }],
    ),
    rejectsCode("UNKNOWN_CREDENTIAL"),
  );
});

test("rejects non-base64url evalByCredential keys", async () => {
  await assert.rejects(
    preparePrfInput(
      {
        evalByCredential: {
          "AQID=": { first: salt },
        },
      },
      [{ id: credentialId }],
    ),
    rejectsCode("INVALID_BASE64URL"),
  );
});

test("rejects evalByCredential when allowCredentials is empty", async () => {
  await assert.rejects(
    preparePrfInput(
      {
        evalByCredential: {
          [credentialKey]: { first: salt },
        },
      },
      [],
    ),
    rejectsCode("EVAL_BY_CREDENTIAL_WITHOUT_ALLOW_CREDENTIALS"),
  );
});

test("rejects PRF values without first", async () => {
  await assert.rejects(
    preparePrfInput(
      {
        evalByCredential: {
          [credentialKey]: { second: salt },
        },
      },
      [{ id: credentialId }],
    ),
    rejectsCode("MISSING_FIRST"),
  );
});

test("base64url helpers reject padding and non-canonical input", () => {
  assert.equal(isBase64Url("AQID"), true);
  assert.equal(isBase64Url("AQID="), false);
  assert.equal(isBase64Url("AQI"), true);
  assert.equal(isBase64Url("AQJ"), false);
  assert.equal(isBase64Url(""), false);
});
