import { test } from "node:test";
import assert from "node:assert/strict";
import {
  parseKbJwt,
  replaceAudience,
  verifyPresentation,
} from "./index.ts";

function b64url(text: string): string {
  return Buffer.from(text, "utf8")
    .toString("base64")
    .replace(/\+/g, "-")
    .replace(/\//g, "_")
    .replace(/=+$/, "");
}

function makeKbJwt(payload: Record<string, unknown>): string {
  const header = b64url(JSON.stringify({ alg: "ES256", typ: "kb+jwt" }));
  const body = b64url(JSON.stringify(payload));
  const signature = b64url("original-holder-signature");
  return `${header}.${body}.${signature}`;
}

const verifierA = "https://verifier-a.example";
const verifierB = "https://verifier-b.example";
const challengeB = "nonce-challenge-b-123";

test("钱包保持签名，仅替换 aud", () => {
  const original = makeKbJwt({
    aud: verifierA,
    nonce: challengeB,
    sd_hash: "abc",
  });
  const forwarded = replaceAudience(original, verifierB);

  const origParts = original.split(".");
  const newParts = forwarded.split(".");
  assert.equal(newParts[0], origParts[0], "header 不变");
  assert.equal(newParts[2], origParts[2], "签名保持不变");
  assert.notEqual(newParts[1], origParts[1], "payload 已更新");

  const parsed = parseKbJwt(forwarded);
  assert.equal(parsed.payload.aud, verifierB);
  assert.equal(parsed.payload.nonce, challengeB);
  assert.equal(parsed.payload.sd_hash, "abc", "其他声明不变");
});

test("aud 与 nonce 都正确的出示被接受", () => {
  const kbJwt = replaceAudience(
    makeKbJwt({ aud: verifierA, nonce: challengeB }),
    verifierB,
  );
  const result = verifyPresentation(kbJwt, {
    audience: verifierB,
    nonce: challengeB,
  });
  assert.deepEqual(result, { valid: true });
});

test("aud 不符则整份出示无效", () => {
  const kbJwt = makeKbJwt({ aud: verifierA, nonce: challengeB });
  const result = verifyPresentation(kbJwt, {
    audience: verifierB,
    nonce: challengeB,
  });
  assert.deepEqual(result, { valid: false, reason: "aud_mismatch" });
});

test("nonce 不符则整份出示无效", () => {
  const kbJwt = makeKbJwt({ aud: verifierB, nonce: "stale-nonce" });
  const result = verifyPresentation(kbJwt, {
    audience: verifierB,
    nonce: challengeB,
  });
  assert.deepEqual(result, { valid: false, reason: "nonce_mismatch" });
});

test("畸形 kb-jwt 整份无效", () => {
  const result = verifyPresentation("not-a-jwt", {
    audience: verifierB,
    nonce: challengeB,
  });
  assert.deepEqual(result, { valid: false, reason: "malformed" });
});
