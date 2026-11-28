import { describe, it } from "node:test";
import assert from "node:assert/strict";
import {
  WEB_AUTHN_GET_TYPE,
  WebAuthnVerificationError,
  encodeClientDataJSON,
  parseClientDataJSON,
} from "../src/index.ts";
import { ORIGIN } from "./helpers.ts";
import { utf8Encode } from "../src/bytes.ts";

describe("parseClientDataJSON", () => {
  it("parses a standard webauthn.get document", () => {
    const challenge = new Uint8Array([9, 8, 7, 6]);
    const raw = encodeClientDataJSON({ type: WEB_AUTHN_GET_TYPE, challenge, origin: ORIGIN });
    const parsed = parseClientDataJSON(raw);
    assert.equal(parsed.type, WEB_AUTHN_GET_TYPE);
    assert.equal(parsed.origin, ORIGIN);
    assert.equal(parsed.crossOrigin, false);
    assert.equal(typeof parsed.challenge, "string");
  });

  it("surfaces crossOrigin=true and preserves tokenBinding", () => {
    const raw = utf8Encode(
      JSON.stringify({
        type: WEB_AUTHN_GET_TYPE,
        challenge: "AAAA",
        origin: ORIGIN,
        crossOrigin: true,
        tokenBinding: { status: "supported" },
      }),
    );
    const parsed = parseClientDataJSON(raw);
    assert.equal(parsed.crossOrigin, true);
    assert.deepEqual(parsed.tokenBinding, { status: "supported" });
  });

  it("rejects malformed JSON and non-object documents", () => {
    assert.throws(
      () => parseClientDataJSON(utf8Encode("{not-json")),
      (error: unknown) => (error as WebAuthnVerificationError).code === "CLIENT_DATA_MALFORMED",
    );
    assert.throws(
      () => parseClientDataJSON(utf8Encode("[1,2,3]")),
      (error: unknown) => (error as WebAuthnVerificationError).code === "CLIENT_DATA_MALFORMED",
    );
  });

  it("rejects missing or mistyped required fields", () => {
    const valid = { type: WEB_AUTHN_GET_TYPE, challenge: "AAAA", origin: ORIGIN };
    for (const broken of [
      { ...valid, type: 42 },
      { ...valid, challenge: "" },
      { ...valid, origin: null },
      { ...valid, crossOrigin: "yes" },
    ]) {
      assert.throws(
        () => parseClientDataJSON(utf8Encode(JSON.stringify(broken))),
        (error: unknown) => (error as WebAuthnVerificationError).code === "CLIENT_DATA_MALFORMED",
      );
    }
  });
});
