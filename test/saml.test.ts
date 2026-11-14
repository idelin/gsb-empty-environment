import assert from "node:assert/strict";
import { test } from "node:test";
import {
  C14N_1_0,
  C14N_1_0_WITH_COMMENTS,
  canonicalizeXml,
  EXCLUSIVE_C14N,
  findDirectChild,
  parseSamlXml,
  readRawRange,
  resolveAlgorithmPolicy,
  SamlVerificationError,
  SAML_ASSERTION_NS,
  verifySamlResponse,
  type SamlVerificationPolicy,
} from "../index.ts";
import {
  ACS_URL,
  IDP_ENTITY_ID,
  NOW,
  OTHER_CERTIFICATE,
  REQUEST_ID,
  SP_ENTITY_ID,
  TEST_CERTIFICATE,
  TEST_CERTIFICATE_BASE64,
} from "./fixtures.ts";
import {
  addAssertionSignature,
  addResponseSignature,
  assertionOnlySignedResponse,
  doubleSignedResponse,
  responseOnlySignedResponse,
  unsignedResponse,
} from "./signer.ts";

const DIGEST_SHA1_FOR_TEST_ONLY = "http://www.w3.org/2000/09/xmldsig#sha1";

function parserErrorCode(xml: string): string | undefined {
  try {
    parseSamlXml(Buffer.from(xml, "utf8"));
    return undefined;
  } catch (error) {
    return (error as { code?: string }).code;
  }
}

function verificationErrorCode(
  xml: string,
  overrides: Partial<SamlVerificationPolicy> = {},
): string | undefined {
  try {
    const rawXml = Buffer.from(xml, "utf8");
    const document = parseSamlXml(rawXml);
    const policy: SamlVerificationPolicy = {
      requiredSignatures: "both",
      tenantId: "tenant-a",
      idpMetadata: {
        tenantId: "tenant-a",
        entityId: IDP_ENTITY_ID,
        generation: 7,
        signingCertificates: [TEST_CERTIFICATE],
      },
      destination: ACS_URL,
      audience: SP_ENTITY_ID,
      inResponseTo: REQUEST_ID,
      now: NOW,
      clockSkewMs: 0,
      ...overrides,
    };
    verifySamlResponse({ document, rawXml, policy });
    return undefined;
  } catch (error) {
    return (error as { code?: string }).code;
  }
}

function verifyDoubleSigned(xml: string, overrides: Partial<SamlVerificationPolicy> = {}) {
  const rawXml = Buffer.from(xml, "utf8");
  const document = parseSamlXml(rawXml);
  const policy: SamlVerificationPolicy = {
    requiredSignatures: "both",
    tenantId: "tenant-a",
    idpMetadata: {
      tenantId: "tenant-a",
      entityId: IDP_ENTITY_ID,
      generation: 7,
      signingCertificates: [TEST_CERTIFICATE],
    },
    destination: ACS_URL,
    audience: SP_ENTITY_ID,
    inResponseTo: REQUEST_ID,
    now: NOW,
    clockSkewMs: 0,
    ...overrides,
  };
  return { document, rawXml, result: verifySamlResponse({ document, rawXml, policy }) };
}

function extractFirstAssertion(xml: string): { start: number; end: number; block: string } {
  const start = xml.indexOf("  <saml:Assertion");
  const closing = "</saml:Assertion>";
  const closingStart = xml.indexOf(closing, start);
  if (start < 0 || closingStart < 0) {
    throw new Error("Assertion fixture not found");
  }
  const end = closingStart + closing.length;
  return { start, end, block: xml.slice(start, end) };
}

test("canonicalization matches the C14N 1.0 and exclusive C14N rules", () => {
  const xml =
    '<doc xmlns="urn:d" xmlns:b="urn:b" b:z="2" a="3" xml:lang="en"><e xmlns=""><x a="&lt;&amp;&quot;&#x9;&#xA;&#xD;">a &amp; b &lt; c &gt; d&#13;&#10;e</x></e></doc>';
  const document = parseSamlXml(Buffer.from(xml, "utf8"));
  const expected =
    '<doc xmlns="urn:d" xmlns:b="urn:b" a="3" xml:lang="en" b:z="2"><e xmlns=""><x a="&lt;&amp;&quot;&#x9;&#xA;&#xD;">a &amp; b &lt; c &gt; d&#xD;\ne</x></e></doc>';
  assert.equal(canonicalizeXml(document.root, C14N_1_0).toString("utf8"), expected);
  assert.equal(canonicalizeXml(document.root, EXCLUSIVE_C14N).toString("utf8"), expected);

  const comments = parseSamlXml(Buffer.from("<doc><!--signed--><a/></doc>", "utf8"));
  assert.equal(canonicalizeXml(comments.root, C14N_1_0).toString("utf8"), "<doc><a></a></doc>");
  assert.equal(
    canonicalizeXml(comments.root, C14N_1_0_WITH_COMMENTS).toString("utf8"),
    "<doc><!--signed--><a></a></doc>",
  );
});

test("parser retains UTF-8 byte locations and stable node identities", () => {
  const xml = '<?xml version="1.0" encoding="UTF-8"?><doc><x ID="alpha">你好 &amp; 世界</x></doc>';
  const raw = Buffer.from(xml, "utf8");
  const document = parseSamlXml(raw);
  const x = findDirectChild(document.root, "", "x");
  assert.ok(x);
  const bytes = Buffer.from(readRawRange(document, raw, x.rawRange));
  assert.equal(bytes.toString("utf8"), '<x ID="alpha">你好 &amp; 世界</x>');
  assert.equal(x.identity, 2);
  assert.equal(document.idIndex.alpha, x);
  assert.ok(Object.isFrozen(x));
  assert.ok(Object.isFrozen(x.rawRange));
});

test("parser rejects duplicate IDs, namespace aliases, DTD/entities, and XSLT", () => {
  assert.equal(
    parserErrorCode('<doc><a ID="same"></a><b ID="same"></b></doc>'),
    "XML_DUPLICATE_ID",
  );
  assert.equal(
    parserErrorCode('<evil:Assertion xmlns:evil="urn:evil"></evil:Assertion>'),
    "XML_NAMESPACE_CONFUSION",
  );
  assert.equal(
    parserErrorCode('<doc xmlns:e="urn:e" e:ID="x"></doc>'),
    "XML_ID_FORBIDDEN",
  );
  assert.equal(
    parserErrorCode('<doc><!DOCTYPE doc [<!ENTITY boom "boom">]></doc>'),
    "XML_DTD_FORBIDDEN",
  );
  assert.equal(parserErrorCode("<doc>&boom;</doc>"), "XML_ENTITY_FORBIDDEN");
  assert.equal(
    parserErrorCode('<xsl:stylesheet xmlns:xsl="http://www.w3.org/1999/XSL/Transform"></xsl:stylesheet>'),
    "XSLT_FORBIDDEN",
  );
  assert.equal(
    parserErrorCode('<?xml-stylesheet href="x"?><doc></doc>'),
    "XSLT_FORBIDDEN",
  );
});

test("valid double-signed response returns immutable handles tied to verified nodes", () => {
  const xml = doubleSignedResponse();
  const { document, rawXml, result } = verifyDoubleSigned(xml);
  const parsedAssertion = findDirectChild(document.root, SAML_ASSERTION_NS, "Assertion");
  assert.ok(parsedAssertion);

  assert.equal(result.signaturePolicy, "both");
  assert.equal(result.tenantId, "tenant-a");
  assert.equal(result.idpGeneration, 7);
  assert.equal(result.signatures.length, 2);
  assert.deepEqual(
    result.signatures.map((signature) => signature.signedNodeIdentity),
    [document.root.identity, parsedAssertion.identity],
  );
  assert.equal(result.response?.signatureCoverage, "self");
  assert.equal(result.assertion.signatureCoverage, "self+response");
  assert.equal(result.response?.assertion, result.assertion);
  assert.equal(result.assertions.length, 1);

  assert.equal(result.issuer, IDP_ENTITY_ID);
  assert.equal(result.destination, ACS_URL);
  assert.equal(result.audience, SP_ENTITY_ID);
  assert.equal(result.inResponseTo, REQUEST_ID);
  assert.equal(result.nameId, "alice@example.test");
  assert.equal(
    result.nameIdFormat,
    "urn:oasis:names:tc:SAML:1.1:nameid-format:emailAddress",
  );
  assert.equal(result.assertion.notBefore, "2026-12-31T23:59:00Z");
  assert.equal(result.assertion.notOnOrAfter, "2027-01-01T00:05:00Z");
  assert.equal(result.subjectConfirmation.method, "urn:oasis:names:tc:SAML:2.0:cm:bearer");
  assert.equal(result.subjectConfirmation.recipient, ACS_URL);
  assert.equal(result.subjectConfirmation.inResponseTo, REQUEST_ID);

  assert.equal(result.assertion.nodeIdentity, parsedAssertion.identity);
  assert.equal(result.assertion.rawRange.start, parsedAssertion.rawRange.start);
  assert.equal(result.assertion.rawRange.end, parsedAssertion.rawRange.end);
  const assertionBytes = Buffer.from(readRawRange(document, rawXml, result.assertion.rawRange));
  const responseBytes = Buffer.from(readRawRange(document, rawXml, result.response!.rawRange));
  assert.match(assertionBytes.toString("utf8"), /^<saml:Assertion ID="_assertion-1"/);
  assert.match(responseBytes.toString("utf8"), /^<samlp:Response/);
  assert.match(responseBytes.toString("utf8"), /ID="_assertion-1"/);

  assert.ok(Object.isFrozen(result));
  assert.ok(Object.isFrozen(result.assertion));
  assert.ok(Object.isFrozen(result.assertions));
  assert.throws(() => {
    (result.assertion as unknown as { id: string }).id = "attacker";
  }, TypeError);
});

test("policy can require only Response or only Assertion signatures", () => {
  const responseOnly = verifyDoubleSigned(responseOnlySignedResponse(), {
    requiredSignatures: "response",
  });
  assert.ok(responseOnly.result.response);
  assert.equal(responseOnly.result.assertion.signatureCoverage, "response");

  const assertionOnlyRaw = Buffer.from(assertionOnlySignedResponse(), "utf8");
  const assertionOnlyDocument = parseSamlXml(assertionOnlyRaw);
  const assertionOnlyPolicy: SamlVerificationPolicy = {
    requiredSignatures: "assertion",
    tenantId: "tenant-a",
    idpMetadata: {
      tenantId: "tenant-a",
      entityId: IDP_ENTITY_ID,
      generation: 7,
      signingCertificates: [TEST_CERTIFICATE],
    },
    destination: ACS_URL,
    audience: SP_ENTITY_ID,
    inResponseTo: REQUEST_ID,
    now: NOW,
    clockSkewMs: 0,
  };
  const assertionOnly = verifySamlResponse({
    document: assertionOnlyDocument,
    rawXml: assertionOnlyRaw,
    policy: assertionOnlyPolicy,
  });
  assert.equal(assertionOnly.response, null);
  assert.equal(assertionOnly.assertion.signatureCoverage, "self");
  assert.equal(assertionOnly.inResponseTo, REQUEST_ID);
});

test("inclusive C14N is accepted when it is in the configured allowlist", () => {
  const xml = addResponseSignature(
    addAssertionSignature(unsignedResponse(), {
      digestTransform: C14N_1_0,
      signedInfoCanonicalization: C14N_1_0,
    }),
    { signedInfoCanonicalization: C14N_1_0 },
  );
  const { result } = verifyDoubleSigned(xml);
  assert.equal(result.signatures.length, 2);
  assert.equal(result.assertion.signatureCoverage, "self+response");
});

test("IdP metadata accepts base64 DER certificate encoding", () => {
  const { result } = verifyDoubleSigned(doubleSignedResponse(), {
    idpMetadata: {
      tenantId: "tenant-a",
      entityId: IDP_ENTITY_ID,
      generation: 7,
      signingCertificates: [TEST_CERTIFICATE_BASE64],
    },
  });
  assert.equal(result.idpGeneration, 7);
});

test("a signature without KeyInfo is validated against tenant metadata certificates", () => {
  const xml = addResponseSignature(
    addAssertionSignature(unsignedResponse(), { includeKeyInfo: false }),
  );
  const { result } = verifyDoubleSigned(xml);
  assert.equal(result.signatures.length, 2);
  assert.equal(result.assertion.signatureCoverage, "self+response");
});

test("an omitted digest C14N transform falls back to configured C14N 1.0", () => {
  const xml = addResponseSignature(
    addAssertionSignature(unsignedResponse(), { omitDigestCanonicalization: true }),
  );
  const { result } = verifyDoubleSigned(xml);
  assert.equal(result.signatures.length, 2);
});

test("exclusive C14N honors InclusiveNamespaces PrefixList", () => {
  const xml = addResponseSignature(
    addAssertionSignature(unsignedResponse(), {
      includeInclusiveNamespaces: true,
    }),
  );
  const { result } = verifyDoubleSigned(xml);
  assert.equal(result.signatures.length, 2);
});

test("Conditions NotBefore is optional while NotOnOrAfter remains mandatory", () => {
  const unsigned = unsignedResponse().replace(' NotBefore="2026-12-31T23:59:00Z"', "");
  const xml = addResponseSignature(addAssertionSignature(unsigned));
  const { result } = verifyDoubleSigned(xml);
  assert.equal(result.notBefore, null);
  assert.equal(result.notOnOrAfter, "2027-01-01T00:05:00Z");
});

test("duplicate ID wrapping attack is rejected before signature selection", () => {
  const xml = doubleSignedResponse().replace('ID="_assertion-1"', 'ID="_response-1"');
  assert.equal(parserErrorCode(xml), "XML_DUPLICATE_ID");
  assert.equal(verificationErrorCode(xml), "XML_DUPLICATE_ID");
});

test("namespace rebinding on the signed Assertion is rejected", () => {
  const xml = doubleSignedResponse().replace(
    '<saml:Assertion ID=',
    '<saml:Assertion xmlns:saml="urn:evil:assertion" ID=',
  );
  assert.equal(parserErrorCode(xml), "XML_NAMESPACE_CONFUSION");
  assert.equal(verificationErrorCode(xml), "XML_NAMESPACE_CONFUSION");
});

test("moving a signed Assertion behind a wrapper is rejected, even with a direct malicious Assertion", () => {
  const legitimate = doubleSignedResponse();
  const oldAssertion = extractFirstAssertion(legitimate);
  const maliciousAssertion = extractFirstAssertion(
    unsignedResponse({ assertionId: "_assertion-malicious", nameId: "mallory@example.test" }),
  ).block;
  const wrapped =
    legitimate.slice(0, oldAssertion.start) +
    maliciousAssertion +
    '\n  <evil:Extension xmlns:evil="urn:evil">' +
    oldAssertion.block +
    "</evil:Extension>" +
    legitimate.slice(oldAssertion.end);
  assert.equal(verificationErrorCode(wrapped), "SAML_ASSERTION_PLACEMENT_INVALID");
});

test("an old signed Assertion inside an unsigned Response cannot borrow trust from unsigned fields", () => {
  const oldAssertion = assertionOnlySignedResponse({
    nameId: "old-user@example.test",
    assertionInResponseTo: "_old-request",
  });
  assert.equal(verificationErrorCode(oldAssertion, { requiredSignatures: "both" }), "SAML_SIGNATURE_REQUIRED");
  assert.equal(
    verificationErrorCode(oldAssertion, { requiredSignatures: "assertion" }),
    "SAML_INRESPONSETO_MISMATCH",
  );
});

test("multiple References are rejected even when the SignedInfo signature is valid", () => {
  const extraReference = `<ds:Reference URI="#_assertion-1"><ds:Transforms><ds:Transform Algorithm="http://www.w3.org/2000/09/xmldsig#enveloped-signature"></ds:Transform><ds:Transform Algorithm="${EXCLUSIVE_C14N}"></ds:Transform></ds:Transforms><ds:DigestMethod Algorithm="http://www.w3.org/2001/04/xmlenc#sha256"></ds:DigestMethod><ds:DigestValue>AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=</ds:DigestValue></ds:Reference>`;
  const xml = addResponseSignature(addAssertionSignature(unsignedResponse()), { extraReference });
  assert.equal(verificationErrorCode(xml), "SIGNATURE_STRUCTURE_INVALID");
});

test("unknown transforms and SHA-1 are forbidden", () => {
  const unknownTransform = doubleSignedResponse().replaceAll(
    `<ds:Transform Algorithm="${EXCLUSIVE_C14N}">`,
    '<ds:Transform Algorithm="urn:evil:transform">',
  );
  assert.equal(verificationErrorCode(unknownTransform), "SIGNATURE_ALGORITHM_FORBIDDEN");

  assert.equal(
    verificationErrorCode(doubleSignedResponse(), {
      algorithms: {
        digestAlgorithms: ["http://www.w3.org/2001/04/xmlenc#sha256"],
        signatureAlgorithms: ["http://www.w3.org/2001/04/xmldsig-more#rsa-sha256"],
        canonicalizationAlgorithms: [C14N_1_0],
      },
    }),
    "SIGNATURE_ALGORITHM_FORBIDDEN",
  );

  assert.throws(
    () => resolveAlgorithmPolicy({ digestAlgorithms: [DIGEST_SHA1_FOR_TEST_ONLY] }),
    (error: unknown) =>
      error instanceof Error && (error as { code?: string }).code === "SIGNATURE_ALGORITHM_FORBIDDEN",
  );
});

test("a certificate from another IdP metadata generation is rejected", () => {
  assert.equal(
    verificationErrorCode(doubleSignedResponse(), {
      idpMetadata: {
        tenantId: "tenant-a",
        entityId: IDP_ENTITY_ID,
        generation: 8,
        signingCertificates: [OTHER_CERTIFICATE],
      },
    }),
    "SIGNATURE_CERTIFICATE_UNTRUSTED",
  );
  assert.equal(
    verificationErrorCode(doubleSignedResponse(), {
      idpMetadata: {
        tenantId: "tenant-b",
        entityId: IDP_ENTITY_ID,
        generation: 8,
        signingCertificates: [TEST_CERTIFICATE],
      },
    }),
    "IDP_METADATA_TENANT_MISMATCH",
  );
});

test("expired assertions and changed validation inputs fail on the signed claims", () => {
  assert.equal(
    verificationErrorCode(doubleSignedResponse(), { now: new Date("2027-01-01T00:10:00Z") }),
    "SAML_TIME_EXPIRED",
  );
  assert.equal(
    verificationErrorCode(doubleSignedResponse(), { audience: "https://other-sp.example.test" }),
    "SAML_AUDIENCE_MISMATCH",
  );
  assert.equal(
    verificationErrorCode(doubleSignedResponse(), {
      destination: "https://evil.example.test/acs",
      recipient: ACS_URL,
    }),
    "SAML_DESTINATION_MISMATCH",
  );
  assert.equal(
    verificationErrorCode(doubleSignedResponse(), { inResponseTo: "_other-request" }),
    "SAML_INRESPONSETO_MISMATCH",
  );
  assert.equal(
    verificationErrorCode(doubleSignedResponse(), {
      idpMetadata: {
        tenantId: "tenant-a",
        entityId: "https://other-idp.example.test/metadata",
        generation: 7,
        signingCertificates: [TEST_CERTIFICATE],
      },
    }),
    "SAML_ISSUER_MISMATCH",
  );
  assert.equal(
    verificationErrorCode(doubleSignedResponse(), {
      recipient: "https://other-sp.example.test/acs",
    }),
    "SAML_RECIPIENT_MISMATCH",
  );
});

test("raw bytes must be the exact source used to build the parsed tree", () => {
  const rawXml = Buffer.from(doubleSignedResponse(), "utf8");
  const document = parseSamlXml(rawXml);
  const mutated = Buffer.from(rawXml);
  mutated[mutated.length - 2] = 0x20;
  try {
    verifySamlResponse({
      document,
      rawXml: mutated,
      policy: {
        requiredSignatures: "both",
        tenantId: "tenant-a",
        idpMetadata: {
          tenantId: "tenant-a",
          entityId: IDP_ENTITY_ID,
          generation: 7,
          signingCertificates: [TEST_CERTIFICATE],
        },
        destination: ACS_URL,
        audience: SP_ENTITY_ID,
        inResponseTo: REQUEST_ID,
        now: NOW,
        clockSkewMs: 0,
      },
    });
    assert.fail("mutated source bytes were accepted");
  } catch (error) {
    assert.ok(error instanceof SamlVerificationError);
    assert.equal(error.code, "SAML_SOURCE_MISMATCH");
  }
});
