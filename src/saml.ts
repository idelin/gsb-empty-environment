import { createHash } from "node:crypto";
import {
  findDirectChildren,
  isParsedSamlXmlDocument,
  SAML_ASSERTION_NS,
  SAML_PROTOCOL_NS,
  XMLDSIG_NS,
  type RawByteRange,
  type SamlXmlDocument,
  type XmlElement,
} from "./xml.ts";
import {
  findDirectSignature,
  loadTrustedCertificates,
  resolveAlgorithmPolicy,
  verifyEnvelopedSignature,
  type SamlAlgorithmPolicyInput,
  type TrustedCertificateInput,
  type VerifiedSignatureDetails,
} from "./signature.ts";

export const SAML_SUCCESS_STATUS = "urn:oasis:names:tc:SAML:2.0:status:Success";
export const SAML_BEARER_CONFIRMATION = "urn:oasis:names:tc:SAML:2.0:cm:bearer";

const MAX_CLOCK_SKEW_MS = 5 * 60 * 1000;

export type SamlSignaturePolicy = "response" | "assertion" | "both";
export type SignatureCoverage = "self" | "response" | "self+response";

export interface IdpMetadataGeneration {
  readonly tenantId: string;
  readonly entityId: string;
  readonly generation: number;
  readonly signingCertificates: readonly TrustedCertificateInput[];
}

export interface SamlVerificationPolicy {
  readonly requiredSignatures: SamlSignaturePolicy;
  readonly tenantId: string;
  readonly idpMetadata: IdpMetadataGeneration;
  readonly destination: string;
  readonly audience: string;
  readonly inResponseTo: string;
  /** Defaults to destination when omitted. */
  readonly recipient?: string;
  readonly now?: Date;
  readonly clockSkewMs?: number;
  readonly algorithms?: SamlAlgorithmPolicyInput;
}

export interface SamlVerificationInput {
  readonly document: SamlXmlDocument;
  readonly rawXml: Uint8Array;
  readonly policy: SamlVerificationPolicy;
}

export interface SubjectConfirmationInfo {
  readonly method: string;
  readonly recipient: string;
  readonly inResponseTo: string;
  readonly notBefore: string | null;
  readonly notOnOrAfter: string;
  readonly address: string | null;
}

export class SamlVerificationError extends Error {
  readonly code: string;

  constructor(code: string, message: string) {
    super(message);
    this.name = "SamlVerificationError";
    this.code = code;
  }
}

function fail(code: string, message: string): never {
  throw new SamlVerificationError(code, message);
}

function directChildren(element: XmlElement, namespaceUri: string, localName: string): readonly XmlElement[] {
  return findDirectChildren(element, namespaceUri, localName);
}

function oneDirectChild(
  element: XmlElement,
  namespaceUri: string,
  localName: string,
  description: string,
): XmlElement {
  const children = directChildren(element, namespaceUri, localName);
  if (children.length !== 1) {
    fail("SAML_STRUCTURE_INVALID", `${description} requires exactly one ${localName}`);
  }
  return children[0];
}

function requiredUnqualifiedAttribute(element: XmlElement, name: string): string {
  let found = 0;
  let value = "";
  for (const attribute of element.attributes) {
    if (attribute.localName === name) {
      found += 1;
      if (attribute.namespaceUri !== "") {
        fail("SAML_NAMESPACE_INVALID", `${name} on ${element.localName} must be unqualified`);
      }
      value = attribute.value;
    }
  }
  if (found !== 1 || value.length === 0) {
    fail("SAML_ATTRIBUTE_INVALID", `${element.localName} requires a non-empty ${name} attribute`);
  }
  return value;
}

function optionalUnqualifiedAttribute(element: XmlElement, name: string): string | null {
  let found = 0;
  let value: string | null = null;
  for (const attribute of element.attributes) {
    if (attribute.localName === name) {
      found += 1;
      if (attribute.namespaceUri !== "") {
        fail("SAML_NAMESPACE_INVALID", `${name} on ${element.localName} must be unqualified`);
      }
      value = attribute.value;
    }
  }
  if (found > 1) {
    fail("SAML_ATTRIBUTE_INVALID", `${element.localName} contains duplicate ${name} attributes`);
  }
  return value;
}

function simpleText(element: XmlElement, description: string): string {
  for (const child of element.children) {
    if (child.kind === "element") {
      fail("SAML_STRUCTURE_INVALID", `${description} cannot contain child elements`);
    }
  }
  const value = element.children
    .filter((child) => child.kind === "text")
    .map((child) => child.value)
    .join("")
    .trim();
  if (value.length === 0) {
    fail("SAML_STRUCTURE_INVALID", `${description} must have text content`);
  }
  return value;
}

export function parseSamlDateTime(value: string, description: string): number {
  if (
    !/^(-?\d{4,})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(\.[0-9]+)?(Z|[+-]\d{2}:\d{2})$/.test(value)
  ) {
    fail("SAML_TIME_INVALID", `${description} is not an XML Schema dateTime with a timezone`);
  }
  const epochMs = Date.parse(value);
  if (!Number.isFinite(epochMs)) {
    fail("SAML_TIME_INVALID", `${description} is not a valid dateTime`);
  }
  return epochMs;
}

function readIssuer(element: XmlElement): string {
  const issuer = oneDirectChild(element, SAML_ASSERTION_NS, "Issuer", element.localName);
  return simpleText(issuer, "Issuer");
}

function readId(element: XmlElement): string {
  if (element.idAttribute === null) {
    fail("SAML_ID_REQUIRED", `${element.localName} must have an unqualified ID`);
  }
  return element.idAttribute.value;
}

function readVersion(element: XmlElement): string {
  const version = requiredUnqualifiedAttribute(element, "Version");
  if (version !== "2.0") {
    fail("SAML_VERSION_INVALID", `${element.localName} Version must be 2.0`);
  }
  return version;
}

function readIssueInstant(element: XmlElement): { value: string; epochMs: number } {
  const value = requiredUnqualifiedAttribute(element, "IssueInstant");
  return { value, epochMs: parseSamlDateTime(value, `${element.localName} IssueInstant`) };
}

function readAudiences(assertion: XmlElement): readonly string[] {
  const conditions = oneDirectChild(assertion, SAML_ASSERTION_NS, "Conditions", "Assertion");
  const restrictions = directChildren(conditions, SAML_ASSERTION_NS, "AudienceRestriction");
  if (restrictions.length === 0) {
    fail("SAML_AUDIENCE_MISSING", "Assertion has no AudienceRestriction");
  }
  const audiences: string[] = [];
  for (const restriction of restrictions) {
    const entries = directChildren(restriction, SAML_ASSERTION_NS, "Audience");
    if (entries.length === 0) {
      fail("SAML_AUDIENCE_MISSING", "AudienceRestriction has no Audience");
    }
    for (const entry of entries) {
      audiences.push(simpleText(entry, "Audience"));
    }
  }
  return Object.freeze(audiences);
}

function readConditions(assertion: XmlElement): {
  readonly notBefore: string | null;
  readonly notOnOrAfter: string;
  readonly audienceRestrictions: readonly XmlElement[];
} {
  const conditions = oneDirectChild(assertion, SAML_ASSERTION_NS, "Conditions", "Assertion");
  const notBefore = optionalUnqualifiedAttribute(conditions, "NotBefore");
  const notOnOrAfter = requiredUnqualifiedAttribute(conditions, "NotOnOrAfter");
  if (notBefore !== null) {
    parseSamlDateTime(notBefore, "Conditions NotBefore");
  }
  const audienceRestrictions = directChildren(conditions, SAML_ASSERTION_NS, "AudienceRestriction");
  if (audienceRestrictions.length === 0) {
    fail("SAML_AUDIENCE_MISSING", "Assertion Conditions has no AudienceRestriction");
  }
  return Object.freeze({ notBefore, notOnOrAfter, audienceRestrictions: Object.freeze(audienceRestrictions) });
}

function readSubjectConfirmation(assertion: XmlElement): SubjectConfirmationInfo {
  const subject = oneDirectChild(assertion, SAML_ASSERTION_NS, "Subject", "Assertion");
  const confirmations = directChildren(subject, SAML_ASSERTION_NS, "SubjectConfirmation");
  if (confirmations.length !== 1) {
    fail("SAML_SUBJECT_CONFIRMATION_INVALID", "Assertion must contain exactly one SubjectConfirmation");
  }
  const confirmation = confirmations[0];
  const method = requiredUnqualifiedAttribute(confirmation, "Method");
  if (method !== SAML_BEARER_CONFIRMATION) {
    fail("SAML_SUBJECT_CONFIRMATION_INVALID", "Only the SAML bearer SubjectConfirmation method is accepted");
  }
  const data = oneDirectChild(
    confirmation,
    SAML_ASSERTION_NS,
    "SubjectConfirmationData",
    "SubjectConfirmation",
  );
  for (const child of data.children) {
    if (child.kind === "element") {
      fail("SAML_SUBJECT_CONFIRMATION_INVALID", "Bearer SubjectConfirmationData cannot contain elements");
    }
  }
  const recipient = requiredUnqualifiedAttribute(data, "Recipient");
  const inResponseTo = requiredUnqualifiedAttribute(data, "InResponseTo");
  const notOnOrAfter = requiredUnqualifiedAttribute(data, "NotOnOrAfter");
  const notBefore = optionalUnqualifiedAttribute(data, "NotBefore");
  const address = optionalUnqualifiedAttribute(data, "Address");
  parseSamlDateTime(notOnOrAfter, "SubjectConfirmationData NotOnOrAfter");
  if (notBefore !== null) {
    parseSamlDateTime(notBefore, "SubjectConfirmationData NotBefore");
  }
  return Object.freeze({
    method,
    recipient,
    inResponseTo,
    notBefore,
    notOnOrAfter,
    address,
  });
}

function readNameId(assertion: XmlElement): { readonly value: string; readonly format: string | null } {
  const subject = oneDirectChild(assertion, SAML_ASSERTION_NS, "Subject", "Assertion");
  const nameId = oneDirectChild(subject, SAML_ASSERTION_NS, "NameID", "Subject");
  return Object.freeze({
    value: simpleText(nameId, "NameID"),
    format: optionalUnqualifiedAttribute(nameId, "Format"),
  });
}

function readResponseStatus(response: XmlElement): string {
  const status = oneDirectChild(response, SAML_PROTOCOL_NS, "Status", "Response");
  const statusCode = oneDirectChild(status, SAML_PROTOCOL_NS, "StatusCode", "Status");
  const value = requiredUnqualifiedAttribute(statusCode, "Value");
  if (value !== SAML_SUCCESS_STATUS) {
    fail("SAML_STATUS_INVALID", `SAML Response status is not Success: ${value}`);
  }
  return value;
}

function readResponseDestination(response: XmlElement): string {
  return requiredUnqualifiedAttribute(response, "Destination");
}

function readResponseInResponseTo(response: XmlElement): string {
  return requiredUnqualifiedAttribute(response, "InResponseTo");
}

const HANDLE_NODE = new WeakMap<VerifiedNodeHandle, XmlElement>();

/**
 * Immutable, identity-bearing handle for the exact element that was verified.
 * All claim accessors use direct child links on this handle's frozen element.
 */
export class VerifiedNodeHandle {
  readonly kind: "response" | "assertion";
  readonly nodeIdentity: number;
  readonly id: string;
  readonly rawRange: RawByteRange;
  readonly signatureCoverage: SignatureCoverage;

  constructor(element: XmlElement, kind: "response" | "assertion", coverage: SignatureCoverage) {
    HANDLE_NODE.set(this, element);
    this.kind = kind;
    this.nodeIdentity = element.identity;
    this.id = readId(element);
    this.rawRange = Object.freeze({ start: element.rawRange.start, end: element.rawRange.end });
    this.signatureCoverage = coverage;
  }

  protected node(): XmlElement {
    const element = HANDLE_NODE.get(this);
    if (element === undefined) {
      throw new SamlVerificationError("SAML_HANDLE_INVALID", "Verified handle is not attached to a node");
    }
    return element;
  }

  get issuer(): string {
    return readIssuer(this.node());
  }

  get issueInstant(): string {
    return readIssueInstant(this.node()).value;
  }
}

export class VerifiedAssertionHandle extends VerifiedNodeHandle {
  readonly version: string;

  constructor(element: XmlElement, coverage: SignatureCoverage) {
    super(element, "assertion", coverage);
    this.version = readVersion(element);
    Object.freeze(this);
  }

  get audience(): string {
    return readAudiences(this.node())[0];
  }

  get nameId(): string {
    return readNameId(this.node()).value;
  }

  get nameIdFormat(): string | null {
    return readNameId(this.node()).format;
  }

  get audiences(): readonly string[] {
    return readAudiences(this.node());
  }

  get inResponseTo(): string {
    return readSubjectConfirmation(this.node()).inResponseTo;
  }

  get recipient(): string {
    return readSubjectConfirmation(this.node()).recipient;
  }

  get notBefore(): string | null {
    return readConditions(this.node()).notBefore;
  }

  get notOnOrAfter(): string {
    return readConditions(this.node()).notOnOrAfter;
  }

  get subjectConfirmation(): SubjectConfirmationInfo {
    return readSubjectConfirmation(this.node());
  }
}

export class VerifiedResponseHandle extends VerifiedNodeHandle {
  readonly assertion: VerifiedAssertionHandle;
  readonly version: string;

  constructor(element: XmlElement, assertion: VerifiedAssertionHandle) {
    super(element, "response", "self");
    this.assertion = assertion;
    this.version = readVersion(element);
    Object.freeze(this);
  }

  get destination(): string {
    return readResponseDestination(this.node());
  }

  get inResponseTo(): string {
    return readResponseInResponseTo(this.node());
  }

  get statusCode(): string {
    return readResponseStatus(this.node());
  }
}

export class VerifiedSamlResult {
  readonly response: VerifiedResponseHandle | null;
  readonly assertion: VerifiedAssertionHandle;
  readonly assertions: readonly VerifiedAssertionHandle[];
  readonly tenantId: string;
  readonly idpEntityId: string;
  readonly idpGeneration: number;
  readonly signaturePolicy: SamlSignaturePolicy;
  readonly signatures: readonly VerifiedSignatureDetails[];
  readonly receivedAt: string;
  readonly clockSkewMs: number;

  constructor(
    response: VerifiedResponseHandle | null,
    assertion: VerifiedAssertionHandle,
    policy: SamlVerificationPolicy,
    signatures: readonly VerifiedSignatureDetails[],
    receivedAt: string,
    clockSkewMs: number,
  ) {
    this.response = response;
    this.assertion = assertion;
    this.assertions = Object.freeze([assertion]);
    this.tenantId = policy.tenantId;
    this.idpEntityId = policy.idpMetadata.entityId;
    this.idpGeneration = policy.idpMetadata.generation;
    this.signaturePolicy = policy.requiredSignatures;
    this.signatures = Object.freeze([...signatures]);
    this.receivedAt = receivedAt;
    this.clockSkewMs = clockSkewMs;
    Object.freeze(this);
  }

  get issuer(): string {
    return this.response?.issuer ?? this.assertion.issuer;
  }

  get destination(): string | null {
    return this.response?.destination ?? null;
  }

  get audience(): string {
    return this.assertion.audience;
  }

  get audiences(): readonly string[] {
    return this.assertion.audiences;
  }

  get inResponseTo(): string {
    return this.assertion.inResponseTo;
  }

  get notBefore(): string | null {
    return this.assertion.notBefore;
  }

  get notOnOrAfter(): string {
    return this.assertion.notOnOrAfter;
  }

  get subjectConfirmation(): SubjectConfirmationInfo {
    return this.assertion.subjectConfirmation;
  }

  get nameId(): string {
    return this.assertion.nameId;
  }

  get nameIdFormat(): string | null {
    return this.assertion.nameIdFormat;
  }

  get statusCode(): string | null {
    return this.response?.statusCode ?? null;
  }
}

function assertSourceMatches(document: SamlXmlDocument, rawXml: Uint8Array): void {
  if (!(rawXml instanceof Uint8Array)) {
    fail("SAML_SOURCE_INVALID", "rawXml must be a Uint8Array");
  }
  if (rawXml.byteLength !== document.sourceByteLength) {
    fail("SAML_SOURCE_MISMATCH", "rawXml length does not match the parsed document");
  }
  const digest = createHash("sha256").update(rawXml).digest("hex");
  if (digest !== document.sourceSha256) {
    fail("SAML_SOURCE_MISMATCH", "rawXml bytes do not match the parsed document");
  }
}

/**
 * Returns a copy of the signed node's original UTF-8 bytes. It refuses to read
 * from bytes other than those used to build the supplied document.
 */
export function readRawRange(
  document: SamlXmlDocument,
  rawXml: Uint8Array,
  range: RawByteRange,
): Uint8Array {
  assertSourceMatches(document, rawXml);
  if (
    !Number.isSafeInteger(range.start) ||
    !Number.isSafeInteger(range.end) ||
    range.start < 0 ||
    range.end < range.start ||
    range.end > rawXml.byteLength
  ) {
    fail("SAML_SOURCE_RANGE_INVALID", "Raw byte range is outside the document");
  }
  return Uint8Array.from(rawXml.subarray(range.start, range.end));
}

function validatePolicy(policy: SamlVerificationPolicy): {
  readonly now: Date;
  readonly nowMs: number;
  readonly clockSkewMs: number;
  readonly recipient: string;
  readonly trustedCertificates: ReturnType<typeof loadTrustedCertificates>;
  readonly algorithmPolicy: ReturnType<typeof resolveAlgorithmPolicy>;
} {
  if (policy === null || typeof policy !== "object") {
    fail("SAML_POLICY_INVALID", "policy is required");
  }
  if (
    policy.requiredSignatures !== "response" &&
    policy.requiredSignatures !== "assertion" &&
    policy.requiredSignatures !== "both"
  ) {
    fail("SAML_POLICY_INVALID", "requiredSignatures must be response, assertion, or both");
  }
  for (const [name, value] of [
    ["tenantId", policy.tenantId],
    ["destination", policy.destination],
    ["audience", policy.audience],
    ["inResponseTo", policy.inResponseTo],
  ] as const) {
    if (typeof value !== "string" || value.length === 0) {
      fail("SAML_POLICY_INVALID", `${name} must be a non-empty string`);
    }
  }
  if (policy.recipient !== undefined && (typeof policy.recipient !== "string" || policy.recipient.length === 0)) {
    fail("SAML_POLICY_INVALID", "recipient must be a non-empty string when supplied");
  }
  if (policy.idpMetadata === null || typeof policy.idpMetadata !== "object") {
    fail("IDP_METADATA_INVALID", "idpMetadata is required");
  }
  if (policy.idpMetadata.tenantId !== policy.tenantId) {
    fail("IDP_METADATA_TENANT_MISMATCH", "IdP metadata belongs to a different tenant");
  }
  if (typeof policy.idpMetadata.entityId !== "string" || policy.idpMetadata.entityId.length === 0) {
    fail("IDP_METADATA_INVALID", "IdP metadata entityId must be non-empty");
  }
  if (
    !Number.isSafeInteger(policy.idpMetadata.generation) ||
    policy.idpMetadata.generation <= 0
  ) {
    fail("IDP_METADATA_INVALID", "IdP metadata generation must be a positive integer");
  }

  if (policy.now !== undefined && !(policy.now instanceof Date)) {
    fail("SAML_POLICY_INVALID", "now must be a Date");
  }
  const now = policy.now === undefined ? new Date() : new Date(policy.now.getTime());
  const nowMs = now.getTime();
  if (!Number.isFinite(nowMs)) {
    fail("SAML_POLICY_INVALID", "now must be a valid Date");
  }
  const clockSkewMs = policy.clockSkewMs ?? 60_000;
  if (
    !Number.isSafeInteger(clockSkewMs) ||
    clockSkewMs < 0 ||
    clockSkewMs > MAX_CLOCK_SKEW_MS
  ) {
    fail("SAML_POLICY_INVALID", `clockSkewMs must be an integer between 0 and ${MAX_CLOCK_SKEW_MS}`);
  }

  return {
    now,
    nowMs,
    clockSkewMs,
    recipient: policy.recipient ?? policy.destination,
    trustedCertificates: loadTrustedCertificates(policy.idpMetadata.signingCertificates),
    algorithmPolicy: resolveAlgorithmPolicy(policy.algorithms),
  };
}

function assertStructure(root: XmlElement, assertion: XmlElement): void {
  const visit = (element: XmlElement, insideSignature: boolean): void => {
    if (element.namespaceUri === XMLDSIG_NS && !insideSignature) {
      fail("XMLDSIG_PLACEMENT_INVALID", `${element.localName} appears outside a SAML signature`);
    }
    if (
      element.namespaceUri === SAML_ASSERTION_NS &&
      element.localName === "Assertion" &&
      element !== assertion
    ) {
      fail("SAML_ASSERTION_PLACEMENT_INVALID", "Assertion must be the single direct child of Response");
    }
    if (
      element.namespaceUri === SAML_PROTOCOL_NS &&
      element.localName === "Response" &&
      element !== root
    ) {
      fail("SAML_RESPONSE_PLACEMENT_INVALID", "Nested Response elements are forbidden");
    }

    for (const child of element.children) {
      if (child.kind !== "element") {
        continue;
      }
      if (child.namespaceUri === XMLDSIG_NS && child.localName === "Signature") {
        if (child.parent !== root && child.parent !== assertion) {
          fail("SIGNATURE_PLACEMENT_INVALID", "Signature must directly sign Response or Assertion");
        }
        visit(child, true);
      } else {
        visit(child, insideSignature);
      }
    }
  };
  visit(root, false);
}

function assertAudienceConditions(assertion: XmlElement, expectedAudience: string): readonly string[] {
  const conditions = oneDirectChild(assertion, SAML_ASSERTION_NS, "Conditions", "Assertion");
  const restrictions = directChildren(conditions, SAML_ASSERTION_NS, "AudienceRestriction");
  if (restrictions.length === 0) {
    fail("SAML_AUDIENCE_MISSING", "Assertion has no AudienceRestriction");
  }
  const audiences: string[] = [];
  for (const restriction of restrictions) {
    const entries = directChildren(restriction, SAML_ASSERTION_NS, "Audience");
    if (entries.length === 0) {
      fail("SAML_AUDIENCE_MISSING", "AudienceRestriction has no Audience");
    }
    const values = entries.map((entry) => simpleText(entry, "Audience"));
    if (!values.includes(expectedAudience)) {
      fail("SAML_AUDIENCE_MISMATCH", "Every AudienceRestriction must include the requested audience");
    }
    audiences.push(...values);
  }
  return Object.freeze(audiences);
}

function assertIssueTime(epochMs: number, nowMs: number, clockSkewMs: number, description: string): void {
  if (epochMs > nowMs + clockSkewMs) {
    fail("SAML_TIME_INVALID", `${description} is in the future`);
  }
}

function assertValidityWindow(
  notBefore: string,
  notOnOrAfter: string,
  nowMs: number,
  clockSkewMs: number,
  description: string,
): void {
  const start = parseSamlDateTime(notBefore, `${description} NotBefore`);
  const end = parseSamlDateTime(notOnOrAfter, `${description} NotOnOrAfter`);
  if (start >= end) {
    fail("SAML_TIME_INVALID", `${description} has an empty validity window`);
  }
  if (nowMs + clockSkewMs < start) {
    fail("SAML_TIME_NOT_YET_VALID", `${description} is not yet valid`);
  }
  if (nowMs >= end + clockSkewMs) {
    fail("SAML_TIME_EXPIRED", `${description} has expired`);
  }
}

function verifyClaims(
  response: XmlElement,
  assertion: XmlElement,
  responseIsSigned: boolean,
  policy: SamlVerificationPolicy,
  nowMs: number,
  clockSkewMs: number,
  recipient: string,
  idpEntityId: string,
): void {
  readVersion(assertion);
  readId(assertion);
  const assertionIssueInstant = readIssueInstant(assertion);
  assertIssueTime(assertionIssueInstant.epochMs, nowMs, clockSkewMs, "Assertion IssueInstant");

  const assertionIssuer = readIssuer(assertion);
  if (assertionIssuer !== idpEntityId) {
    fail("SAML_ISSUER_MISMATCH", "Assertion issuer does not match the tenant IdP metadata");
  }
  readNameId(assertion);

  const conditions = readConditions(assertion);
  assertValidityWindow(
    conditions.notBefore ?? assertionIssueInstant.value,
    conditions.notOnOrAfter,
    nowMs,
    clockSkewMs,
    "Assertion Conditions",
  );
  assertAudienceConditions(assertion, policy.audience);

  const confirmation = readSubjectConfirmation(assertion);
  if (confirmation.recipient !== recipient) {
    fail("SAML_RECIPIENT_MISMATCH", "SubjectConfirmationData Recipient does not match the request");
  }
  if (confirmation.inResponseTo !== policy.inResponseTo) {
    fail("SAML_INRESPONSETO_MISMATCH", "SubjectConfirmationData InResponseTo does not match the request");
  }
  const confirmationStart = confirmation.notBefore ?? assertionIssueInstant.value;
  assertValidityWindow(
    confirmationStart,
    confirmation.notOnOrAfter,
    nowMs,
    clockSkewMs,
    "SubjectConfirmationData",
  );

  if (responseIsSigned) {
    readVersion(response);
    readId(response);
    const responseIssueInstant = readIssueInstant(response);
    assertIssueTime(responseIssueInstant.epochMs, nowMs, clockSkewMs, "Response IssueInstant");
    if (readIssuer(response) !== idpEntityId) {
      fail("SAML_ISSUER_MISMATCH", "Response issuer does not match the tenant IdP metadata");
    }
    if (readResponseDestination(response) !== policy.destination) {
      fail("SAML_DESTINATION_MISMATCH", "Response Destination does not match the request");
    }
    if (readResponseInResponseTo(response) !== policy.inResponseTo) {
      fail("SAML_INRESPONSETO_MISMATCH", "Response InResponseTo does not match the request");
    }
    readResponseStatus(response);
  }
}

/**
 * Verifies a SAML 2.0 Web Browser SSO Response. The parsed tree and raw bytes
 * must be the exact pair produced by parseSamlXml. Only the returned handles
 * should be used to read login claims.
 */
export function verifySamlResponse(input: SamlVerificationInput): VerifiedSamlResult {
  if (input === null || typeof input !== "object") {
    fail("SAML_INPUT_INVALID", "verification input is required");
  }
  const { document, rawXml, policy } = input;
  if (!isParsedSamlXmlDocument(document)) {
    fail("SAML_INPUT_INVALID", "document must be produced by parseSamlXml");
  }
  assertSourceMatches(document, rawXml);

  const settings = validatePolicy(policy);
  const response = document.root;
  if (response.parent !== null) {
    fail("SAML_ROOT_INVALID", "Response must be the document root");
  }
  if (response.namespaceUri !== SAML_PROTOCOL_NS || response.localName !== "Response") {
    fail("SAML_ROOT_INVALID", "Document root must be a SAML 2.0 protocol Response");
  }
  readVersion(response);
  readId(response);
  readIssueInstant(response);

  const assertions = directChildren(response, SAML_ASSERTION_NS, "Assertion");
  if (assertions.length !== 1) {
    fail("SAML_ASSERTION_COUNT", "Login Response must contain exactly one direct Assertion");
  }
  const assertion = assertions[0];
  assertStructure(response, assertion);

  const responseSignature = findDirectSignature(response);
  const assertionSignature = findDirectSignature(assertion);
  if (policy.requiredSignatures === "response" && responseSignature === null) {
    fail("SAML_SIGNATURE_REQUIRED", "Policy requires a signed Response");
  }
  if (policy.requiredSignatures === "assertion" && assertionSignature === null) {
    fail("SAML_SIGNATURE_REQUIRED", "Policy requires a signed Assertion");
  }
  if (
    policy.requiredSignatures === "both" &&
    (responseSignature === null || assertionSignature === null)
  ) {
    fail("SAML_SIGNATURE_REQUIRED", "Policy requires both Response and Assertion signatures");
  }

  const signatureContext = {
    policy: settings.algorithmPolicy,
    trustedCertificates: settings.trustedCertificates,
    now: settings.now,
  };
  const verifiedSignatures: VerifiedSignatureDetails[] = [];
  if (responseSignature !== null) {
    verifiedSignatures.push(
      verifyEnvelopedSignature(responseSignature, response, signatureContext),
    );
  }
  if (assertionSignature !== null) {
    verifiedSignatures.push(
      verifyEnvelopedSignature(assertionSignature, assertion, signatureContext),
    );
  }

  verifyClaims(
    response,
    assertion,
    responseSignature !== null,
    policy,
    settings.nowMs,
    settings.clockSkewMs,
    settings.recipient,
    policy.idpMetadata.entityId,
  );

  let coverage: SignatureCoverage;
  if (responseSignature !== null && assertionSignature !== null) {
    coverage = "self+response";
  } else if (assertionSignature !== null) {
    coverage = "self";
  } else {
    coverage = "response";
  }
  const assertionHandle = new VerifiedAssertionHandle(assertion, coverage);
  const responseHandle =
    responseSignature === null ? null : new VerifiedResponseHandle(response, assertionHandle);

  return new VerifiedSamlResult(
    responseHandle,
    assertionHandle,
    policy,
    verifiedSignatures,
    settings.now.toISOString(),
    settings.clockSkewMs,
  );
}
