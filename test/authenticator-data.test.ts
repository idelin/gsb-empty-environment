import { describe, it } from "node:test";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import {
  FLAG_AT,
  FLAG_BE,
  FLAG_BS,
  FLAG_ED,
  FLAG_UP,
  FLAG_UV,
  parseAuthenticatorData,
  WebAuthnVerificationError,
} from "../src/index.ts";
import { RP_ID, buildAuthenticatorData } from "./helpers.ts";
import { concatBytes, utf8Encode } from "../src/bytes.ts";

function hashRpId(): Uint8Array {
  return new Uint8Array(createHash("sha256").update(utf8Encode(RP_ID)).digest());
}

describe("parseAuthenticatorData", () => {
  it("parses the fixed 37-byte prefix and exposes flags/counter", () => {
    const raw = buildAuthenticatorData({
      rpId: RP_ID,
      signCount: 0x11223344,
      flags: { up: true, uv: true, be: true, bs: true },
    });
    const parsed = parseAuthenticatorData(raw);
    assert.equal(parsed.signCount, 0x11223344);
    assert.equal(parsed.up, true);
    assert.equal(parsed.uv, true);
    assert.equal(parsed.be, true);
    assert.equal(parsed.bs, true);
    assert.equal(parsed.flags, FLAG_UP | FLAG_UV | FLAG_BE | FLAG_BS);
    assert.deepEqual(parsed.rpIdHash, hashRpId());
    assert.equal(parsed.attestedCredentialData, null);
    assert.equal(parsed.extensions, null);
  });

  it("rejects inputs shorter than 37 bytes", () => {
    assert.throws(
      () => parseAuthenticatorData(new Uint8Array(36)),
      (error: unknown) => (error as WebAuthnVerificationError).code === "AUTHENTICATOR_DATA_MALFORMED",
    );
  });

  it("rejects reserved flag bits 0x02 and 0x20", () => {
    const base = buildAuthenticatorData({ rpId: RP_ID, signCount: 1 });
    for (const reserved of [0x02, 0x20]) {
      const mutated = new Uint8Array(base);
      mutated[32] |= reserved;
      assert.throws(
        () => parseAuthenticatorData(mutated),
        (error: unknown) => (error as WebAuthnVerificationError).code === "AUTHENTICATOR_DATA_MALFORMED",
      );
    }
  });

  it("parses a definite-length CBOR extension when ED is set", () => {
    // CBOR map { "hmac-secret": true } -> A1 6B 68 6D 61 63 2D 73 65 63 72 65 74 F5
    const extension = new Uint8Array([
      0xa1, 0x6b, 0x68, 0x6d, 0x61, 0x63, 0x2d, 0x73, 0x65, 0x63, 0x72, 0x65, 0x74, 0xf5,
    ]);
    const raw = buildAuthenticatorData({
      rpId: RP_ID,
      signCount: 7,
      flags: { up: true, uv: true, ed: true },
      extensions: extension,
    });
    const parsed = parseAuthenticatorData(raw);
    assert.equal(parsed.ed, true);
    assert.deepEqual(parsed.extensions, extension);
  });

  it("rejects trailing bytes when no optional sections are flagged", () => {
    const raw = concatBytes(
      buildAuthenticatorData({ rpId: RP_ID, signCount: 1 }),
      new Uint8Array([0x00]),
    );
    assert.throws(
      () => parseAuthenticatorData(raw),
      (error: unknown) => (error as WebAuthnVerificationError).code === "AUTHENTICATOR_DATA_MALFORMED",
    );
  });

  it("rejects indefinite-length CBOR in the extension slot", () => {
    const extension = new Uint8Array([0xbf, 0xff]);
    const raw = buildAuthenticatorData({
      rpId: RP_ID,
      signCount: 1,
      flags: { up: true, uv: true, ed: true },
      extensions: extension,
    });
    assert.throws(
      () => parseAuthenticatorData(raw),
      (error: unknown) => (error as WebAuthnVerificationError).code === "AUTHENTICATOR_DATA_MALFORMED",
    );
  });

  it("exposes AT when the attested credential section is present and well formed", () => {
    // 16-byte AAGUID || 2-byte credential id length (2) || 2-byte id || minimal CBOR map {}
    const aaguid = new Uint8Array(16).fill(0xab);
    const credentialId = new Uint8Array([0x11, 0x22]);
    const credentialIdLength = new Uint8Array([0x00, 0x02]);
    const emptyCborMap = new Uint8Array([0xa0]);
    const suffix = concatBytes(aaguid, credentialIdLength, credentialId, emptyCborMap);
    const prefix = buildAuthenticatorData({
      rpId: RP_ID,
      signCount: 1,
      flags: { up: true, uv: true, at: true },
    });
    const parsed = parseAuthenticatorData(concatBytes(prefix, suffix));
    assert.equal(parsed.at, true);
    assert.ok(parsed.attestedCredentialData);
    assert.equal(parsed.attestedCredentialData.length, suffix.length);
    assert.equal(parsed.flags & FLAG_AT, FLAG_AT);
    assert.equal(parsed.flags & FLAG_ED, 0);
  });
});
