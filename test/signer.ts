import { createHash, sign } from "node:crypto";
import {
  canonicalizeXml,
  C14N_1_0,
  ENVELOPED_SIGNATURE_TRANSFORM,
  EXCLUSIVE_C14N,
  findDirectChild,
  findDirectSignature,
  parseSamlXml,
  SAML_ASSERTION_NS,
  SAML_PROTOCOL_NS,
  SIGNATURE_RSA_SHA256,
  XMLDSIG_NS,
} from "../index.ts";
import {
  ACS_URL,
  IDP_ENTITY_ID,
  REQUEST_ID,
  SP_ENTITY_ID,
  TEST_CERTIFICATE_BASE64,
  TEST_PRIVATE_KEY,
} from "./fixtures.ts";

export interface ResponseFixtureOptions {
  readonly assertionId?: string;
  readonly responseId?: string;
  readonly assertionInResponseTo?: string;
  readonly responseInResponseTo?: string;
  readonly assertionRecipient?: string;
  readonly responseDestination?: string;
  readonly audience?: string;
  readonly assertionNotBefore?: string;
  readonly assertionNotOnOrAfter?: string;
  readonly confirmationNotOnOrAfter?: string;
  readonly nameId?: string;
}

function assertionXml(options: Required<ResponseFixtureOptions>): string {
  return `  <saml:Assertion ID="${options.assertionId}" Version="2.0" IssueInstant="2027-01-01T00:00:00Z">
    <saml:Issuer>${IDP_ENTITY_ID}</saml:Issuer>
    <saml:Subject>
      <saml:NameID Format="urn:oasis:names:tc:SAML:1.1:nameid-format:emailAddress">${options.nameId}</saml:NameID>
      <saml:SubjectConfirmation Method="urn:oasis:names:tc:SAML:2.0:cm:bearer">
        <saml:SubjectConfirmationData NotOnOrAfter="${options.confirmationNotOnOrAfter}" Recipient="${options.assertionRecipient}" InResponseTo="${options.assertionInResponseTo}"></saml:SubjectConfirmationData>
      </saml:SubjectConfirmation>
    </saml:Subject>
    <saml:Conditions NotBefore="${options.assertionNotBefore}" NotOnOrAfter="${options.assertionNotOnOrAfter}">
      <saml:AudienceRestriction>
        <saml:Audience>${options.audience}</saml:Audience>
      </saml:AudienceRestriction>
    </saml:Conditions>
    <saml:AuthnStatement AuthnInstant="2027-01-01T00:00:00Z" SessionIndex="_session-1">
      <saml:AuthnContext>
        <saml:AuthnContextClassRef>urn:oasis:names:tc:SAML:2.0:ac:classes:PasswordProtectedTransport</saml:AuthnContextClassRef>
      </saml:AuthnContext>
    </saml:AuthnStatement>
  </saml:Assertion>`;
}

export function unsignedResponse(options: ResponseFixtureOptions = {}): string {
  const resolved: Required<ResponseFixtureOptions> = {
    assertionId: options.assertionId ?? "_assertion-1",
    responseId: options.responseId ?? "_response-1",
    assertionInResponseTo: options.assertionInResponseTo ?? REQUEST_ID,
    responseInResponseTo: options.responseInResponseTo ?? REQUEST_ID,
    assertionRecipient: options.assertionRecipient ?? ACS_URL,
    responseDestination: options.responseDestination ?? ACS_URL,
    audience: options.audience ?? SP_ENTITY_ID,
    assertionNotBefore: options.assertionNotBefore ?? "2026-12-31T23:59:00Z",
    assertionNotOnOrAfter: options.assertionNotOnOrAfter ?? "2027-01-01T00:05:00Z",
    confirmationNotOnOrAfter: options.confirmationNotOnOrAfter ?? "2027-01-01T00:05:00Z",
    nameId: options.nameId ?? "alice@example.test",
  };
  return `<samlp:Response xmlns:samlp="${SAML_PROTOCOL_NS}" xmlns:saml="${SAML_ASSERTION_NS}" ID="${resolved.responseId}" Version="2.0" IssueInstant="2027-01-01T00:00:00Z" Destination="${resolved.responseDestination}" InResponseTo="${resolved.responseInResponseTo}">
  <saml:Issuer>${IDP_ENTITY_ID}</saml:Issuer>
  <samlp:Status>
    <samlp:StatusCode Value="urn:oasis:names:tc:SAML:2.0:status:Success"></samlp:StatusCode>
  </samlp:Status>
${assertionXml(resolved)}
</samlp:Response>`;
}

interface SignatureTemplateOptions {
  readonly uri: string;
  readonly digest: string;
  readonly digestTransform?: string;
  readonly signedInfoCanonicalization?: string;
  readonly includeInclusiveNamespaces?: boolean;
  readonly omitDigestCanonicalization?: boolean;
  readonly includeKeyInfo?: boolean;
  readonly extraReference?: string;
}

function signedInfoXml(options: SignatureTemplateOptions): string {
  const digestTransform = options.omitDigestCanonicalization
    ? C14N_1_0
    : options.digestTransform ?? EXCLUSIVE_C14N;
  const inclusive = options.includeInclusiveNamespaces
    ? `<ec:InclusiveNamespaces xmlns:ec="${EXCLUSIVE_C14N}" PrefixList="saml samlp"></ec:InclusiveNamespaces>`
    : "";
  const extraReference = options.extraReference ?? "";
  const signedInfoCanonicalization = options.signedInfoCanonicalization ?? EXCLUSIVE_C14N;
  const inheritedNamespaces =
    signedInfoCanonicalization === C14N_1_0
      ? ` xmlns:saml="${SAML_ASSERTION_NS}" xmlns:samlp="${SAML_PROTOCOL_NS}"`
      : "";
  const digestTransformElement = options.omitDigestCanonicalization
    ? ""
    : `<ds:Transform Algorithm="${digestTransform}">${inclusive}</ds:Transform>`;
  return `<ds:SignedInfo xmlns:ds="${XMLDSIG_NS}"${inheritedNamespaces}>
    <ds:CanonicalizationMethod Algorithm="${signedInfoCanonicalization}"></ds:CanonicalizationMethod>
    <ds:SignatureMethod Algorithm="${SIGNATURE_RSA_SHA256}"></ds:SignatureMethod>
    <ds:Reference URI="${options.uri}">
      <ds:Transforms>
        <ds:Transform Algorithm="${ENVELOPED_SIGNATURE_TRANSFORM}"></ds:Transform>${digestTransformElement}
      </ds:Transforms>
      <ds:DigestMethod Algorithm="http://www.w3.org/2001/04/xmlenc#sha256"></ds:DigestMethod>
      <ds:DigestValue>${options.digest}</ds:DigestValue>
    </ds:Reference>${extraReference}
  </ds:SignedInfo>`;
}

function signatureXml(options: SignatureTemplateOptions): string {
  const signedInfo = signedInfoXml(options);
  const signedInfoDocument = parseSamlXml(Buffer.from(signedInfo, "utf8"));
  const canonicalSignedInfo = canonicalizeXml(
    signedInfoDocument.root,
    options.signedInfoCanonicalization ?? EXCLUSIVE_C14N,
  );
  const signatureValue = sign("sha256", canonicalSignedInfo, TEST_PRIVATE_KEY).toString("base64");
  const keyInfo =
    options.includeKeyInfo === false
      ? ""
      : `<ds:KeyInfo><ds:X509Data><ds:X509Certificate>${TEST_CERTIFICATE_BASE64}</ds:X509Certificate></ds:X509Data></ds:KeyInfo>`;
  return `<ds:Signature xmlns:ds="${XMLDSIG_NS}">${signedInfo}<ds:SignatureValue>${signatureValue}</ds:SignatureValue>${keyInfo}</ds:Signature>`;
}

function insertBeforeClosingTag(xml: string, targetId: string, signature: string): string {
  const raw = Buffer.from(xml, "utf8");
  const document = parseSamlXml(raw);
  const assertion = findDirectChild(document.root, SAML_ASSERTION_NS, "Assertion");
  let target = document.root;
  if (targetId !== assertion?.idAttribute?.value) {
    if (target.idAttribute?.value !== targetId) {
      if (assertion?.idAttribute?.value !== targetId) {
        throw new Error(`Fixture target ${targetId} not found`);
      }
      target = assertion;
    }
  } else if (assertion !== null) {
    target = assertion;
  }
  if (findDirectSignature(target) !== null) {
    throw new Error(`Fixture target ${targetId} is already signed`);
  }
  const closing = `</${target.qName}>`;
  const closingBytes = Buffer.byteLength(closing);
  const insertionOffset = target.rawRange.end - closingBytes;
  return Buffer.concat([
    raw.subarray(0, insertionOffset),
    Buffer.from(signature, "utf8"),
    raw.subarray(insertionOffset),
  ]).toString("utf8");
}

export function addAssertionSignature(
  xml: string,
  options: Partial<SignatureTemplateOptions> = {},
): string {
  const document = parseSamlXml(Buffer.from(xml, "utf8"));
  const assertion = findDirectChild(document.root, SAML_ASSERTION_NS, "Assertion");
  if (assertion === null || assertion.idAttribute === null) {
    throw new Error("Fixture has no Assertion ID");
  }
  const digest = createHash("sha256")
    .update(
      canonicalizeXml(
        assertion,
        options.omitDigestCanonicalization
          ? C14N_1_0
          : options.digestTransform ?? EXCLUSIVE_C14N,
        {
          inclusivePrefixes: options.includeInclusiveNamespaces ? ["saml", "samlp"] : [],
        },
      ),
    )
    .digest("base64");
  return insertBeforeClosingTag(
    xml,
    assertion.idAttribute.value,
    signatureXml({ ...options, uri: `#${assertion.idAttribute.value}`, digest }),
  );
}

export function addResponseSignature(
  xml: string,
  options: Partial<SignatureTemplateOptions> = {},
): string {
  const document = parseSamlXml(Buffer.from(xml, "utf8"));
  const response = document.root;
  if (
    response.namespaceUri !== SAML_PROTOCOL_NS ||
    response.localName !== "Response" ||
    response.idAttribute === null
  ) {
    throw new Error("Fixture has no Response ID");
  }
  const digest = createHash("sha256")
    .update(
      canonicalizeXml(
        response,
        options.omitDigestCanonicalization
          ? C14N_1_0
          : options.digestTransform ?? EXCLUSIVE_C14N,
        {
          inclusivePrefixes: options.includeInclusiveNamespaces ? ["saml", "samlp"] : [],
        },
      ),
    )
    .digest("base64");
  return insertBeforeClosingTag(
    xml,
    response.idAttribute.value,
    signatureXml({ ...options, uri: `#${response.idAttribute.value}`, digest }),
  );
}

export function doubleSignedResponse(options: ResponseFixtureOptions = {}): string {
  return addResponseSignature(addAssertionSignature(unsignedResponse(options)));
}

export function assertionOnlySignedResponse(options: ResponseFixtureOptions = {}): string {
  return addAssertionSignature(unsignedResponse(options));
}

export function responseOnlySignedResponse(options: ResponseFixtureOptions = {}): string {
  return addResponseSignature(unsignedResponse(options));
}
