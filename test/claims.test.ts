import assert from "node:assert/strict";
import { Buffer } from "node:buffer";
import { describe, it } from "node:test";
import {
  SdJwtVerificationError,
  verifyDisclosures,
} from "../src/index.ts";
import {
  arrayItemDisclosure,
  arrayMarker,
  buildFixture,
  digestOf,
  propertyDisclosure,
} from "./helpers.ts";

function expectCode(
  code: string,
  fn: () => unknown,
): void {
  assert.throws(
    fn,
    (error: unknown) =>
      error instanceof SdJwtVerificationError && error.code === code,
    `expected SdJwtVerificationError with code ${code}`,
  );
}

describe("verifyDisclosures - happy path", () => {
  it("applies object, nested and array disclosures and strips _sd markers", () => {
    const { signedClaims, disclosures } = buildFixture();
    const presented = [
      disclosures.ssn,
      disclosures.street,
      disclosures.nestedSecret,
      disclosures.itemA,
      disclosures.itemB,
    ];

    const { claims } = verifyDisclosures(signedClaims, presented);

    assert.deepEqual(claims, {
      iss: "https://issuer.example",
      sub: "user-42",
      given_name: "Ada",
      ssn: "123-45-6789",
      address: { locality: "SF", street_address: "123 Market St" },
      tags: ["public-tag", "redacted-item-1", "redacted-item-2"],
      nested: { loyalty_number: "L-99" },
    });
    assert.equal(JSON.stringify(claims).includes("_sd"), false);
  });

  it("leaves undisclosed digests and array markers out of the result", () => {
    const { signedClaims, disclosures } = buildFixture();
    const { claims } = verifyDisclosures(signedClaims, [disclosures.ssn]);

    assert.equal(claims.ssn, "123-45-6789");
    assert.deepEqual(claims.address, { locality: "SF" });
    assert.deepEqual(claims.tags, ["public-tag"]);
    assert.equal("nested" in claims, false);
  });

  it("does not mutate the input signed claims", () => {
    const { signedClaims, disclosures } = buildFixture();
    const snapshot = structuredClone(signedClaims);
    verifyDisclosures(signedClaims, [disclosures.ssn]);
    assert.deepEqual(signedClaims, snapshot);
  });

  it("accepts disclosures in any order", () => {
    const { signedClaims, disclosures } = buildFixture();
    const presented = [
      disclosures.itemB,
      disclosures.nestedSecret,
      disclosures.ssn,
      disclosures.itemA,
      disclosures.street,
    ];
    const { claims } = verifyDisclosures(signedClaims, presented);
    assert.equal(claims.ssn, "123-45-6789");
    assert.deepEqual(claims.tags, ["public-tag", "redacted-item-1", "redacted-item-2"]);
  });

  it("resolves _sd nested inside a disclosed value (fixpoint expansion)", () => {
    const inner = propertyDisclosure("salt-inner", "inner_claim", "secret");
    const outer = propertyDisclosure("salt-outer", "outer", {
      _sd: [digestOf(inner)],
    });
    const signedClaims = { _sd: [digestOf(outer)], plain: true };

    const { claims } = verifyDisclosures(signedClaims, [outer, inner]);
    assert.deepEqual(claims, { plain: true, outer: { inner_claim: "secret" } });
  });
});

describe("verifyDisclosures - attack rejections", () => {
  it("rejects a substituted disclosure whose digest was never signed", () => {
    const { signedClaims } = buildFixture();
    const forged = propertyDisclosure("salt-ssn", "ssn", "attacker-ssn");
    expectCode("UNREFERENCED_DISCLOSURE", () =>
      verifyDisclosures(signedClaims, [forged]));
  });

  it("rejects a disclosure whose value was tampered after encoding", () => {
    const { signedClaims, disclosures } = buildFixture();
    // Re-encode with the same salt/name but a changed value: new digest.
    const tampered = propertyDisclosure("salt-ssn", "ssn", "999-99-9999");
    assert.notEqual(tampered, disclosures.ssn);
    expectCode("UNREFERENCED_DISCLOSURE", () =>
      verifyDisclosures(signedClaims, [tampered]));
  });

  it("rejects the same digest referenced twice inside the signed claims", () => {
    const disclosure = propertyDisclosure("salt-dup", "claim", 1);
    const digest = digestOf(disclosure);
    const signedClaims = {
      _sd: [digest],
      child: { _sd: [digest] },
    };
    expectCode("DUPLICATE_SD_DIGEST", () =>
      verifyDisclosures(signedClaims, [disclosure]));
  });

  it("rejects the same disclosure presented twice", () => {
    const { signedClaims, disclosures } = buildFixture();
    expectCode("DUPLICATE_DISCLOSURE_DIGEST", () =>
      verifyDisclosures(signedClaims, [disclosures.ssn, disclosures.ssn]));
  });

  it("rejects a disclosed name that collides with a plaintext claim", () => {
    const conflicting = propertyDisclosure("salt-name", "given_name", "Mallory");
    const signedClaims = {
      given_name: "Ada",
      _sd: [digestOf(conflicting)],
    };
    expectCode("CLAIM_NAME_CONFLICT", () =>
      verifyDisclosures(signedClaims, [conflicting]));
  });

  it("allows the same property name in sibling objects without conflict", () => {
    const left = propertyDisclosure("salt-l", "city", "A");
    const right = propertyDisclosure("salt-r", "city", "B");
    const signedClaims = {
      left: { _sd: [digestOf(left)] },
      right: { _sd: [digestOf(right)] },
    };
    const { claims } = verifyDisclosures(signedClaims, [left, right]);
    assert.deepEqual(claims, { left: { city: "A" }, right: { city: "B" } });
  });

  it('rejects reserved disclosure names "_sd" and "..."', () => {
    const reservedSd = propertyDisclosure("salt-1", "_sd", []);
    const reservedEllipsis = propertyDisclosure("salt-2", "...", 1);
    const signedClaims = {
      _sd: [digestOf(reservedSd), digestOf(reservedEllipsis)],
    };
    expectCode("RESERVED_DISCLOSURE_NAME", () =>
      verifyDisclosures(signedClaims, [reservedSd]));
    expectCode("RESERVED_DISCLOSURE_NAME", () =>
      verifyDisclosures(signedClaims, [reservedEllipsis]));
  });
});

describe("verifyDisclosures - malformed input", () => {
  it("rejects non-base64url / non-JSON disclosures", () => {
    expectCode("MALFORMED_DISCLOSURE", () =>
      verifyDisclosures({}, ["not base64!!!"]));
    expectCode("MALFORMED_DISCLOSURE", () =>
      verifyDisclosures({}, [""]));
  });

  it("rejects disclosures with wrong array shape or types", () => {
    const encode = (value: unknown) =>
      Buffer.from(JSON.stringify(value)).toString("base64url");

    expectCode("MALFORMED_DISCLOSURE", () =>
      verifyDisclosures({}, [encode(["only-salt"])]));
    expectCode("MALFORMED_DISCLOSURE", () =>
      verifyDisclosures({}, [encode(["salt", 1, 2, 3])]));
    expectCode("MALFORMED_DISCLOSURE", () =>
      verifyDisclosures({}, [encode(["", "name", "value"])]));
  });

  it("rejects malformed _sd arrays and array markers in the payload", () => {
    const disclosure = propertyDisclosure("salt", "name", "value");
    const digest = digestOf(disclosure);

    expectCode("MALFORMED_SD_ARRAY", () =>
      verifyDisclosures({ _sd: digest }, [disclosure]));
    expectCode("MALFORMED_SD_ARRAY", () =>
      verifyDisclosures({ _sd: [42] }, [disclosure]));
    expectCode("MALFORMED_ARRAY_MARKER", () =>
      verifyDisclosures({ list: [{ "...": digest, extra: 1 }] }, [disclosure]));
    expectCode("MALFORMED_ARRAY_MARKER", () =>
      verifyDisclosures({ list: [{ "...": 42 }] }, [disclosure]));
  });

  it("rejects an array-item disclosure that is not referenced", () => {
    const item = arrayItemDisclosure("salt-item", "x");
    expectCode("UNREFERENCED_DISCLOSURE", () =>
      verifyDisclosures({ list: ["plain"] }, [item]));
  });
});
