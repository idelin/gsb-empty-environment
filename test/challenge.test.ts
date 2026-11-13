import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { test } from "node:test";
import {
  computeChallengeDigest,
  encodeTokenChallenge,
  encodeTokenChallengeBase64Url,
  parseTokenChallenge,
  parseTokenChallengeBase64Url,
  TOKEN_TYPE_VOPRF,
  type TokenChallenge,
} from "../src/private-token/challenge.ts";
import { ChallengeParseError } from "../src/private-token/errors.ts";
import { fromHex } from "../src/private-token/bytes.ts";
import { encodeCborCanonical, type CborValue } from "../src/private-token/cbor.ts";

const THIRTY_TWO_BYTES = fromHex(
  "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
);

const BASE_CHALLENGE: TokenChallenge = {
  tokenType: TOKEN_TYPE_VOPRF,
  issuerName: "issuer.example",
  redemptionContext: THIRTY_TWO_BYTES,
  originInfo: ["https://embedded.example", "https://top.example"],
};

test("parses all four TokenChallenge fields", () => {
  const raw = encodeTokenChallenge(BASE_CHALLENGE);
  const parsed = parseTokenChallenge(raw);

  assert.equal(parsed.tokenType, 1);
  assert.equal(parsed.issuerName, "issuer.example");
  assert.deepEqual(parsed.redemptionContext, THIRTY_TWO_BYTES);
  assert.deepEqual(parsed.originInfo, [
    "https://embedded.example",
    "https://top.example",
  ]);
  assert.deepEqual(parsed.raw, raw);
});

test("accepts an empty redemption_context and rejects other lengths", () => {
  const empty: TokenChallenge = {
    ...BASE_CHALLENGE,
    redemptionContext: new Uint8Array(0),
  };
  assert.equal(
    parseTokenChallenge(encodeTokenChallenge(empty)).redemptionContext.length,
    0,
  );

  const wrongLength: TokenChallenge = {
    ...BASE_CHALLENGE,
    redemptionContext: new Uint8Array(31),
  };
  assert.throws(
    () => parseTokenChallenge(encodeTokenChallenge(wrongLength)),
    ChallengeParseError,
  );
});

test("re-encoding reproduces the exact original bytes", () => {
  const raw = encodeTokenChallenge(BASE_CHALLENGE);
  const parsed = parseTokenChallenge(raw);
  const reEncoded = encodeTokenChallenge(parsed);
  assert.deepEqual(reEncoded, raw);
});

test("base64url transport form round trips", () => {
  const encoded = encodeTokenChallengeBase64Url(BASE_CHALLENGE);
  assert.equal(encoded.includes("="), false);
  const parsed = parseTokenChallengeBase64Url(encoded);
  assert.deepEqual(
    encodeTokenChallenge(parsed),
    encodeTokenChallenge(BASE_CHALLENGE),
  );
});

test("challenge_digest is SHA-256 over 0x0001 prefix plus the raw challenge", async () => {
  const raw = encodeTokenChallenge(BASE_CHALLENGE);
  const expected = createHash("sha256")
    .update(Uint8Array.of(0x00, 0x01))
    .update(raw)
    .digest();

  const digest = await computeChallengeDigest(parseTokenChallenge(raw));
  assert.deepEqual(digest.bytes, new Uint8Array(expected));
  assert.equal(digest.hex, Buffer.from(expected).toString("hex"));
  assert.equal(digest.hex.length, 64);
});

test("rejects malformed CBOR structure and field types", () => {
  assert.throws(() => parseTokenChallenge(fromHex("00")), ChallengeParseError);
  assert.throws(
    () => parseTokenChallenge(encodeCbor([1, "issuer", new Uint8Array(0)])),
    ChallengeParseError,
  );
  assert.throws(
    () => parseTokenChallenge(encodeCbor(["1", "issuer", new Uint8Array(0), []])),
    ChallengeParseError,
  );
  assert.throws(
    () => parseTokenChallenge(encodeCbor([1, "", new Uint8Array(0), []])),
    ChallengeParseError,
  );
  assert.throws(
    () => parseTokenChallenge(encodeCbor([1, "issuer", "bytes", []])),
    ChallengeParseError,
  );
  assert.throws(
    () =>
      parseTokenChallenge(
        encodeCbor([1, "issuer", new Uint8Array(0), "origins"]),
      ),
    ChallengeParseError,
  );
});

test("rejects non-canonical wire bytes", () => {
  const raw = encodeTokenChallenge(BASE_CHALLENGE);
  const tampered = new Uint8Array(raw);
  tampered[1] = 0x18;
  tampered[2] = 0x01;
  assert.throws(() => parseTokenChallenge(tampered), ChallengeParseError);
});

test("rejects a structurally valid challenge whose CBOR is not canonical", () => {
  const nonCanonical = [
    0x84,
    0x18, 0x01,
    0x66, ...new TextEncoder().encode("issuer"),
    0x40,
    0x80,
  ];
  assert.throws(
    () => parseTokenChallenge(Uint8Array.from(nonCanonical)),
    ChallengeParseError,
  );
});

test("rejects non-https or malformed origin_info entries", () => {
  const badScheme: TokenChallenge = {
    ...BASE_CHALLENGE,
    originInfo: ["http://embedded.example"],
  };
  assert.throws(
    () => parseTokenChallenge(encodeTokenChallenge(badScheme)),
    ChallengeParseError,
  );

  const withPath: TokenChallenge = {
    ...BASE_CHALLENGE,
    originInfo: ["https://embedded.example/path"],
  };
  assert.throws(
    () => parseTokenChallenge(encodeTokenChallenge(withPath)),
    ChallengeParseError,
  );
});

function encodeCbor(value: CborValue): Uint8Array {
  return encodeCborCanonical(value);
}
