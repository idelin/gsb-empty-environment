import assert from "node:assert/strict";
import { test } from "node:test";
import {
  findPrivateTokenChallenge,
  parseWwwAuthenticate,
} from "../src/private-token/header.ts";
import { encodeTokenChallengeBase64Url } from "../src/private-token/challenge.ts";
import { TOKEN_TYPE_VOPRF } from "../src/private-token/challenge.ts";
import { ChallengeParseError } from "../src/private-token/errors.ts";
import type { TokenChallenge } from "../src/private-token/challenge.ts";

const challenge: TokenChallenge = {
  tokenType: TOKEN_TYPE_VOPRF,
  issuerName: "issuer.example",
  redemptionContext: new Uint8Array(0),
  originInfo: ["https://embedded.example"],
};
const challengeValue = encodeTokenChallengeBase64Url(challenge);

test("parses the PrivateToken scheme with quoted challenge", () => {
  const header = `PrivateToken challenge="${challengeValue}", key="k\\"x"`;
  const parsed = parseWwwAuthenticate(header);
  assert.equal(parsed.length, 1);
  assert.equal(parsed[0]?.scheme, "PrivateToken");
  assert.equal(parsed[0]?.params.get("challenge"), challengeValue);
  assert.equal(parsed[0]?.params.get("key"), 'k"x');
  assert.equal(findPrivateTokenChallenge(header), challengeValue);
});

test("scheme and parameter names are case-insensitive", () => {
  const header = `privatetoken CHALLENGE=${challengeValue}`;
  assert.equal(findPrivateTokenChallenge(header), challengeValue);
});

test("selects PrivateToken among several challenges", () => {
  const header = `Basic realm="x", PrivateToken challenge="${challengeValue}"`;
  const parsed = parseWwwAuthenticate(header);
  assert.equal(parsed.length, 2);
  assert.equal(parsed[0]?.scheme, "Basic");
  assert.equal(findPrivateTokenChallenge(header), challengeValue);
});

test("ignores OWS around commas and equals signs", () => {
  const header = `PrivateToken challenge = "${challengeValue}" , tokenkey = "abc"`;
  const parsed = parseWwwAuthenticate(header);
  assert.equal(parsed[0]?.params.get("challenge"), challengeValue);
  assert.equal(parsed[0]?.params.get("tokenkey"), "abc");
});

test("errors when no PrivateToken challenge or parameter is present", () => {
  assert.throws(
    () => findPrivateTokenChallenge(`Basic realm="x"`),
    ChallengeParseError,
  );
  assert.throws(
    () => findPrivateTokenChallenge(`PrivateToken tokenkey="abc"`),
    ChallengeParseError,
  );
});

test("rejects malformed header syntax", () => {
  assert.throws(
    () => parseWwwAuthenticate(`PrivateToken challenge="unterminated`),
    ChallengeParseError,
  );
});
