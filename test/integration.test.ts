import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { test } from "node:test";
import {
  handlePrivateTokenChallenge,
  encodeTokenChallengeBase64Url,
  TOKEN_TYPE_VOPRF,
} from "../src/private-token/index.ts";
import { OriginVerificationError } from "../src/private-token/errors.ts";
import { decodeBase64Url } from "../src/private-token/base64url.ts";
import type { RequestContext, TokenChallenge } from "../src/private-token/index.ts";

const challenge: TokenChallenge = {
  tokenType: TOKEN_TYPE_VOPRF,
  issuerName: "issuer.example",
  redemptionContext: new Uint8Array(32),
  originInfo: ["https://embedded.example"],
};

test("full pipeline parses header, enforces origin_info and returns digest", async () => {
  const encoded = encodeTokenChallengeBase64Url(challenge);
  const header = `PrivateToken challenge="${encoded}"`;
  const context: RequestContext = {
    requestOrigin: "https://embedded.example",
    topLevelOrigin: "https://top.example",
    isTopLevelNavigation: false,
  };

  const result = await handlePrivateTokenChallenge(header, context);
  const expectedDigest = createHash("sha256")
    .update(Uint8Array.of(0x00, 0x01))
    .update(decodeBase64Url(encoded))
    .digest("hex");

  assert.equal(result.challenge.tokenType, TOKEN_TYPE_VOPRF);
  assert.deepEqual(result.challenge.originInfo, ["https://embedded.example"]);
  assert.equal(result.digest.hex, expectedDigest);
});

test("pipeline blocks a forwarded challenge used from an unlisted origin", async () => {
  const encoded = encodeTokenChallengeBase64Url(challenge);
  const context: RequestContext = {
    requestOrigin: "https://attacker.example",
    topLevelOrigin: "https://top.example",
    isTopLevelNavigation: false,
  };

  await assert.rejects(
    () => handlePrivateTokenChallenge(`PrivateToken challenge="${encoded}"`, context),
    OriginVerificationError,
  );
});

test("pipeline blocks embedded use when origin_info is empty", async () => {
  const emptyOriginChallenge: TokenChallenge = { ...challenge, originInfo: [] };
  const encoded = encodeTokenChallengeBase64Url(emptyOriginChallenge);
  const context: RequestContext = {
    requestOrigin: "https://embedded.example",
    topLevelOrigin: "https://top.example",
    isTopLevelNavigation: false,
  };

  await assert.rejects(
    () => handlePrivateTokenChallenge(`PrivateToken challenge="${encoded}"`, context),
    OriginVerificationError,
  );
});
