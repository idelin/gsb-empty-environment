import { createHash, createVerify, timingSafeEqual, X509Certificate } from "node:crypto";
import {
  canonicalizeXml,
  C14N_1_0,
  C14N_1_0_WITH_COMMENTS,
  EXCLUSIVE_C14N,
  EXCLUSIVE_C14N_WITH_COMMENTS,
  isExclusiveCanonicalizationAlgorithm,
  isSupportedCanonicalizationAlgorithm,
} from "./c14n.ts";
import {
  elementText,
  findDirectChildren,
  getUnqualifiedAttribute,
  XMLDSIG_NS,
  type XmlElement,
} from "./xml.ts";

export const DIGEST_SHA256 = "http://www.w3.org/2001/04/xmlenc#sha256";
export const DIGEST_SHA384 = "http://www.w3.org/2001/04/xmldsig-more#sha384";
export const DIGEST_SHA512 = "http://www.w3.org/2001/04/xmlenc#sha512";

export const SIGNATURE_RSA_SHA256 = "http://www.w3.org/2001/04/xmldsig-more#rsa-sha256";
export const SIGNATURE_RSA_SHA384 = "http://www.w3.org/2001/04/xmldsig-more#rsa-sha384";
export const SIGNATURE_RSA_SHA512 = "http://www.w3.org/2001/04/xmldsig-more#rsa-sha512";
export const SIGNATURE_ECDSA_SHA256 = "http://www.w3.org/2001/04/xmldsig-more#ecdsa-sha256";
export const SIGNATURE_ECDSA_SHA384 = "http://www.w3.org/2001/04/xmldsig-more#ecdsa-sha384";
export const SIGNATURE_ECDSA_SHA512 = "http://www.w3.org/2001/04/xmldsig-more#ecdsa-sha512";

export const ENVELOPED_SIGNATURE_TRANSFORM = "http://www.w3.org/2000/09/xmldsig#enveloped-signature";

const SAFE_DIGESTS = new Set([DIGEST_SHA256, DIGEST_SHA384, DIGEST_SHA512]);
const SAFE_SIGNATURES = new Set([
  SIGNATURE_RSA_SHA256,
  SIGNATURE_RSA_SHA384,
  SIGNATURE_RSA_SHA512,
  SIGNATURE_ECDSA_SHA256,
  SIGNATURE_ECDSA_SHA384,
  SIGNATURE_ECDSA_SHA512,
]);
const SAFE_CANONICALIZATIONS = new Set([
  C14N_1_0,
  C14N_1_0_WITH_COMMENTS,
  EXCLUSIVE_C14N,
  EXCLUSIVE_C14N_WITH_COMMENTS,
]);

const DIGEST_TO_HASH = new Map<string, string>([
  [DIGEST_SHA256, "sha256"],
  [DIGEST_SHA384, "sha384"],
  [DIGEST_SHA512, "sha512"],
]);

const SIGNATURE_TO_CRYPTO = new Map<string, { hash: string; keyType: "rsa" | "ec" }>([
  [SIGNATURE_RSA_SHA256, { hash: "sha256", keyType: "rsa" }],
  [SIGNATURE_RSA_SHA384, { hash: "sha384", keyType: "rsa" }],
  [SIGNATURE_RSA_SHA512, { hash: "sha512", keyType: "rsa" }],
  [SIGNATURE_ECDSA_SHA256, { hash: "sha256", keyType: "ec" }],
  [SIGNATURE_ECDSA_SHA384, { hash: "sha384", keyType: "ec" }],
  [SIGNATURE_ECDSA_SHA512, { hash: "sha512", keyType: "ec" }],
]);

export interface SamlAlgorithmPolicyInput {
  readonly digestAlgorithms?: readonly string[];
  readonly signatureAlgorithms?: readonly string[];
  readonly canonicalizationAlgorithms?: readonly string[];
}

export interface ResolvedAlgorithmPolicy {
  readonly digestAlgorithms: ReadonlySet<string>;
  readonly signatureAlgorithms: ReadonlySet<string>;
  readonly canonicalizationAlgorithms: ReadonlySet<string>;
}

export class SamlSignatureError extends Error {
  readonly code: string;

  constructor(code: string, message: string) {
    super(message);
    this.name = "SamlSignatureError";
    this.code = code;
  }
}

function fail(code: string, message: string): never {
  throw new SamlSignatureError(code, message);
}

function selectAlgorithmSet(
  configured: readonly string[] | undefined,
  safe: ReadonlySet<string>,
  description: string,
): ReadonlySet<string> {
  if (configured !== undefined && !Array.isArray(configured)) {
    fail("SIGNATURE_POLICY_INVALID", `${description} allowlist must be an array`);
  }
  const values = configured ?? [...safe];
  if (values.length === 0) {
    fail("SIGNATURE_POLICY_INVALID", `${description} allowlist cannot be empty`);
  }
  const selected = new Set<string>();
  for (const value of values) {
    if (!safe.has(value)) {
      fail("SIGNATURE_ALGORITHM_FORBIDDEN", `${description} algorithm is not permitted: ${value}`);
    }
    selected.add(value);
  }
  return Object.freeze(selected);
}

export function resolveAlgorithmPolicy(input: SamlAlgorithmPolicyInput = {}): ResolvedAlgorithmPolicy {
  if (input === null || typeof input !== "object") {
    fail("SIGNATURE_POLICY_INVALID", "Algorithm policy must be an object");
  }
  return Object.freeze({
    digestAlgorithms: selectAlgorithmSet(input.digestAlgorithms, SAFE_DIGESTS, "Digest"),
    signatureAlgorithms: selectAlgorithmSet(input.signatureAlgorithms, SAFE_SIGNATURES, "Signature"),
    canonicalizationAlgorithms: selectAlgorithmSet(
      input.canonicalizationAlgorithms,
      SAFE_CANONICALIZATIONS,
      "Canonicalization",
    ),
  });
}

function elementChildren(element: XmlElement): readonly XmlElement[] {
  return element.children.filter((child): child is XmlElement => child.kind === "element");
}

function assertNoCommentsOrPis(element: XmlElement): void {
  for (const child of element.children) {
    if (child.kind === "comment" || child.kind === "processing-instruction") {
      fail("SIGNATURE_STRUCTURE_INVALID", `Comments or processing instructions are not allowed in ${element.localName}`);
    }
  }
}

function assertWhitespaceOnlyText(element: XmlElement): void {
  for (const child of element.children) {
    if (child.kind === "text" && child.value.trim().length > 0) {
      fail("SIGNATURE_STRUCTURE_INVALID", `Unexpected text inside ${element.localName}`);
    }
  }
}

function assertAttributes(element: XmlElement, allowed: readonly string[]): void {
  if (element.attributes.length !== allowed.length) {
    fail("SIGNATURE_STRUCTURE_INVALID", `${element.localName} has unexpected attributes`);
  }
  for (const attribute of element.attributes) {
    if (attribute.namespaceUri !== "" || !allowed.includes(attribute.localName)) {
      fail("SIGNATURE_STRUCTURE_INVALID", `${element.localName} has an unexpected attribute`);
    }
  }
}

function requireAttribute(element: XmlElement, localName: string): string {
  const value = getUnqualifiedAttribute(element, localName);
  if (value === null || value.length === 0) {
    fail("SIGNATURE_STRUCTURE_INVALID", `${element.localName} is missing ${localName}`);
  }
  return value;
}

function requireChildOrder(element: XmlElement, expected: readonly string[]): readonly XmlElement[] {
  assertNoCommentsOrPis(element);
  assertWhitespaceOnlyText(element);
  const children = elementChildren(element);
  if (children.length !== expected.length) {
    fail("SIGNATURE_STRUCTURE_INVALID", `${element.localName} has unexpected child elements`);
  }
  for (let index = 0; index < expected.length; index += 1) {
    const child = children[index];
    if (child.namespaceUri !== XMLDSIG_NS || child.localName !== expected[index]) {
      fail(
        "SIGNATURE_STRUCTURE_INVALID",
        `${element.localName} child ${index} must be ${expected[index]} in the XMLDSig namespace`,
      );
    }
  }
  return children;
}

function decodeBase64(value: string, description: string): Buffer {
  const normalized = value.replace(/[\t\n\r ]+/g, "");
  if (
    normalized.length === 0 ||
    normalized.length % 4 !== 0 ||
    !/^[A-Za-z0-9+/]*={0,2}$/.test(normalized)
  ) {
    fail("SIGNATURE_BASE64_INVALID", `${description} is not valid base64`);
  }
  const decoded = Buffer.from(normalized, "base64");
  if (decoded.toString("base64") !== normalized) {
    fail("SIGNATURE_BASE64_INVALID", `${description} is not canonical base64`);
  }
  return decoded;
}

function readLeafBase64(element: XmlElement): Buffer {
  assertNoCommentsOrPis(element);
  assertAttributes(element, []);
  for (const child of element.children) {
    if (child.kind === "element") {
      fail("SIGNATURE_STRUCTURE_INVALID", `${element.localName} cannot contain child elements`);
    }
  }
  return decodeBase64(elementText(element), element.localName);
}

function parseInclusivePrefixes(element: XmlElement): readonly string[] | undefined {
  assertNoCommentsOrPis(element);
  assertWhitespaceOnlyText(element);
  const children = elementChildren(element);
  if (children.length === 0) {
    return undefined;
  }
  if (children.length !== 1) {
    fail("SIGNATURE_STRUCTURE_INVALID", `${element.localName} has unexpected children`);
  }
  const child = children[0];
  if (child.namespaceUri !== EXCLUSIVE_C14N || child.localName !== "InclusiveNamespaces") {
    fail("SIGNATURE_STRUCTURE_INVALID", "Only ec:InclusiveNamespaces is allowed here");
  }
  assertNoCommentsOrPis(child);
  assertWhitespaceOnlyText(child);
  if (elementChildren(child).length !== 0) {
    fail("SIGNATURE_STRUCTURE_INVALID", "InclusiveNamespaces cannot contain elements");
  }
  assertAttributes(child, ["PrefixList"]);
  const prefixList = requireAttribute(child, "PrefixList");
  const prefixes = prefixList.trim().split(/\s+/);
  if (prefixes.length === 0 || prefixes.some((prefix) => prefix.length === 0)) {
    fail("SIGNATURE_STRUCTURE_INVALID", "InclusiveNamespaces PrefixList is empty");
  }
  if (new Set(prefixes).size !== prefixes.length) {
    fail("SIGNATURE_STRUCTURE_INVALID", "InclusiveNamespaces contains duplicate prefixes");
  }
  for (const prefix of prefixes) {
    if (prefix !== "#default" && !/^[A-Za-z_][A-Za-z0-9_.:-]*$/.test(prefix)) {
      fail("SIGNATURE_STRUCTURE_INVALID", `Invalid InclusiveNamespaces prefix: ${prefix}`);
    }
  }
  return Object.freeze(prefixes);
}

interface ParsedReference {
  readonly uri: string;
  readonly digestAlgorithm: string;
  readonly digest: Buffer;
  readonly digestCanonicalization: string;
  readonly digestInclusivePrefixes: readonly string[];
}

interface ParsedSignatureStructure {
  readonly signedInfo: XmlElement;
  readonly canonicalizationAlgorithm: string;
  readonly canonicalizationInclusivePrefixes: readonly string[];
  readonly signatureAlgorithm: string;
  readonly reference: ParsedReference;
  readonly signatureValue: Buffer;
  readonly certificateDer: Buffer | null;
}

function parseReference(element: XmlElement, policy: ResolvedAlgorithmPolicy): ParsedReference {
  assertNoCommentsOrPis(element);
  assertWhitespaceOnlyText(element);
  assertAttributes(element, ["URI"]);
  const uri = requireAttribute(element, "URI");
  const children = requireChildOrder(element, ["Transforms", "DigestMethod", "DigestValue"]);
  const transforms = children[0];
  assertAttributes(transforms, []);

  assertNoCommentsOrPis(transforms);
  assertWhitespaceOnlyText(transforms);
  const transformElements = elementChildren(transforms);
  if (transformElements.length < 1 || transformElements.length > 2) {
    fail(
      "SIGNATURE_TRANSFORM_FORBIDDEN",
      "Only enveloped-signature and one optional canonicalization transform are allowed",
    );
  }

  const enveloped = transformElements[0];
  if (
    enveloped.namespaceUri !== XMLDSIG_NS ||
    enveloped.localName !== "Transform" ||
    requireAttribute(enveloped, "Algorithm") !== ENVELOPED_SIGNATURE_TRANSFORM
  ) {
    fail("SIGNATURE_TRANSFORM_FORBIDDEN", "First Reference transform must be enveloped-signature");
  }
  assertAttributes(enveloped, ["Algorithm"]);
  assertNoCommentsOrPis(enveloped);
  assertWhitespaceOnlyText(enveloped);
  if (elementChildren(enveloped).length !== 0) {
    fail("SIGNATURE_TRANSFORM_FORBIDDEN", "The enveloped-signature transform cannot contain parameters");
  }

  let digestCanonicalization = C14N_1_0;
  let digestInclusivePrefixes: readonly string[] = Object.freeze([]);
  if (transformElements.length === 2) {
    const c14nTransform = transformElements[1];
    if (
      c14nTransform.namespaceUri !== XMLDSIG_NS ||
      c14nTransform.localName !== "Transform"
    ) {
      fail("SIGNATURE_TRANSFORM_FORBIDDEN", "Second Reference transform must be canonicalization");
    }
    assertAttributes(c14nTransform, ["Algorithm"]);
    digestCanonicalization = requireAttribute(c14nTransform, "Algorithm");
    digestInclusivePrefixes =
      parseInclusivePrefixes(c14nTransform) ?? Object.freeze([] as string[]);
  }
  if (
    !isSupportedCanonicalizationAlgorithm(digestCanonicalization) ||
    !policy.canonicalizationAlgorithms.has(digestCanonicalization)
  ) {
    fail("SIGNATURE_ALGORITHM_FORBIDDEN", `Digest canonicalization is not permitted: ${digestCanonicalization}`);
  }
  if (
    digestInclusivePrefixes.length > 0 &&
    !isExclusiveCanonicalizationAlgorithm(digestCanonicalization)
  ) {
    fail("SIGNATURE_TRANSFORM_FORBIDDEN", "InclusiveNamespaces requires exclusive canonicalization");
  }

  const digestMethod = children[1];
  assertNoCommentsOrPis(digestMethod);
  assertWhitespaceOnlyText(digestMethod);
  assertAttributes(digestMethod, ["Algorithm"]);
  if (elementChildren(digestMethod).length !== 0) {
    fail("SIGNATURE_STRUCTURE_INVALID", "DigestMethod cannot contain child elements");
  }
  const digestAlgorithm = requireAttribute(digestMethod, "Algorithm");
  if (!policy.digestAlgorithms.has(digestAlgorithm) || !DIGEST_TO_HASH.has(digestAlgorithm)) {
    fail("SIGNATURE_ALGORITHM_FORBIDDEN", `Digest algorithm is not permitted: ${digestAlgorithm}`);
  }

  const digestValue = children[2];
  const digest = readLeafBase64(digestValue);

  return {
    uri,
    digestAlgorithm,
    digest,
    digestCanonicalization,
    digestInclusivePrefixes,
  };
}

function parseSignatureStructure(
  signature: XmlElement,
  policy: ResolvedAlgorithmPolicy,
): ParsedSignatureStructure {
  if (signature.namespaceUri !== XMLDSIG_NS || signature.localName !== "Signature") {
    fail("SIGNATURE_STRUCTURE_INVALID", "Expected a ds:Signature element");
  }
  assertNoCommentsOrPis(signature);
  assertWhitespaceOnlyText(signature);
  assertAttributes(signature, []);
  const signatureChildren = elementChildren(signature);
  if (
    signatureChildren.length < 2 ||
    signatureChildren.length > 3 ||
    signatureChildren[0].namespaceUri !== XMLDSIG_NS ||
    signatureChildren[0].localName !== "SignedInfo" ||
    signatureChildren[1].namespaceUri !== XMLDSIG_NS ||
    signatureChildren[1].localName !== "SignatureValue"
  ) {
    fail("SIGNATURE_STRUCTURE_INVALID", "Signature must contain SignedInfo, SignatureValue, and optional KeyInfo");
  }

  const signedInfo = signatureChildren[0];
  assertAttributes(signedInfo, []);
  const canonicalizationMethod = requireChildOrder(signedInfo, [
    "CanonicalizationMethod",
    "SignatureMethod",
    "Reference",
  ])[0];
  assertNoCommentsOrPis(canonicalizationMethod);
  assertAttributes(canonicalizationMethod, ["Algorithm"]);
  const canonicalizationAlgorithm = requireAttribute(canonicalizationMethod, "Algorithm");
  if (
    !isSupportedCanonicalizationAlgorithm(canonicalizationAlgorithm) ||
    !policy.canonicalizationAlgorithms.has(canonicalizationAlgorithm)
  ) {
    fail("SIGNATURE_ALGORITHM_FORBIDDEN", `SignedInfo canonicalization is not permitted: ${canonicalizationAlgorithm}`);
  }
  const canonicalizationInclusivePrefixes =
    parseInclusivePrefixes(canonicalizationMethod) ?? Object.freeze([] as string[]);
  if (
    canonicalizationInclusivePrefixes.length > 0 &&
    !isExclusiveCanonicalizationAlgorithm(canonicalizationAlgorithm)
  ) {
    fail("SIGNATURE_TRANSFORM_FORBIDDEN", "InclusiveNamespaces requires exclusive canonicalization");
  }

  const signatureMethod = findDirectChildren(signedInfo, XMLDSIG_NS, "SignatureMethod")[0];
  assertNoCommentsOrPis(signatureMethod);
  assertWhitespaceOnlyText(signatureMethod);
  assertAttributes(signatureMethod, ["Algorithm"]);
  if (elementChildren(signatureMethod).length !== 0) {
    fail("SIGNATURE_STRUCTURE_INVALID", "SignatureMethod cannot contain child elements");
  }
  const signatureAlgorithm = requireAttribute(signatureMethod, "Algorithm");
  if (!policy.signatureAlgorithms.has(signatureAlgorithm) || !SIGNATURE_TO_CRYPTO.has(signatureAlgorithm)) {
    fail("SIGNATURE_ALGORITHM_FORBIDDEN", `Signature algorithm is not permitted: ${signatureAlgorithm}`);
  }

  const referenceElement = findDirectChildren(signedInfo, XMLDSIG_NS, "Reference")[0];
  const reference = parseReference(referenceElement, policy);
  const signatureValue = readLeafBase64(signatureChildren[1]);

  let certificateDer: Buffer | null = null;
  if (signatureChildren.length === 3) {
    const keyInfo = signatureChildren[2];
    if (keyInfo.namespaceUri !== XMLDSIG_NS || keyInfo.localName !== "KeyInfo") {
      fail("SIGNATURE_STRUCTURE_INVALID", "Third Signature child must be ds:KeyInfo");
    }
    assertNoCommentsOrPis(keyInfo);
    assertWhitespaceOnlyText(keyInfo);
    assertAttributes(keyInfo, []);
    const keyInfoChildren = requireChildOrder(keyInfo, ["X509Data"]);
    const x509Data = keyInfoChildren[0];
    assertNoCommentsOrPis(x509Data);
    assertWhitespaceOnlyText(x509Data);
    assertAttributes(x509Data, []);
    const x509Children = requireChildOrder(x509Data, ["X509Certificate"]);
    certificateDer = readLeafBase64(x509Children[0]);
  }

  return {
    signedInfo,
    canonicalizationAlgorithm,
    canonicalizationInclusivePrefixes,
    signatureAlgorithm,
    reference,
    signatureValue,
    certificateDer,
  };
}

export type TrustedCertificateInput = string | Uint8Array;

export interface TrustedCertificateSet {
  readonly certificates: readonly X509Certificate[];
  readonly derCertificates: readonly Buffer[];
}

export function loadTrustedCertificates(inputs: readonly TrustedCertificateInput[]): TrustedCertificateSet {
  if (!Array.isArray(inputs) || inputs.length === 0) {
    fail("IDP_METADATA_INVALID", "IdP metadata generation has no signing certificates");
  }
  const certificates: X509Certificate[] = [];
  const derCertificates: Buffer[] = [];
  for (const input of inputs) {
    let certificate: X509Certificate;
    try {
      if (typeof input === "string" && !input.includes("-----BEGIN CERTIFICATE-----")) {
        const normalized = input.replace(/[\t\n\r ]+/g, "");
        if (normalized.length === 0 || normalized.length % 4 !== 0) {
          fail("IDP_METADATA_INVALID", "IdP metadata certificate is not valid PEM or base64 DER");
        }
        certificate = new X509Certificate(Buffer.from(normalized, "base64"));
      } else {
        certificate =
          typeof input === "string" ? new X509Certificate(input) : new X509Certificate(Buffer.from(input));
      }
    } catch {
      fail("IDP_METADATA_INVALID", "IdP metadata contains an invalid X.509 certificate");
    }
    if (!certificates.some((existing) => existing.raw.equals(certificate.raw))) {
      certificates.push(certificate);
      derCertificates.push(Buffer.from(certificate.raw));
    }
  }
  return Object.freeze({
    certificates: Object.freeze(certificates),
    derCertificates: Object.freeze(derCertificates),
  });
}

function checkCertificatePublicKey(certificate: X509Certificate, expectedType: "rsa" | "ec"): void {
  const key = certificate.publicKey;
  if (key.asymmetricKeyType !== expectedType) {
    fail("SIGNATURE_CERTIFICATE_MISMATCH", `Certificate key type ${key.asymmetricKeyType} does not match signature algorithm`);
  }
  const details = key.asymmetricKeyDetails as
    | { modulusLength?: number; namedCurve?: string }
    | undefined;
  if (expectedType === "rsa" && (details?.modulusLength ?? 0) < 2048) {
    fail("SIGNATURE_KEY_WEAK", "RSA signing keys must be at least 2048 bits");
  }
  if (expectedType === "ec") {
    const allowedCurves = new Set(["prime256v1", "secp384r1", "secp521r1"]);
    if (details?.namedCurve === undefined || !allowedCurves.has(details.namedCurve)) {
      fail("SIGNATURE_KEY_WEAK", "ECDSA signing keys must use P-256, P-384, or P-521");
    }
  }
}

function timingSafeBufferEqual(left: Buffer, right: Buffer): boolean {
  return left.length === right.length && timingSafeEqual(left, right);
}

function canonicalizeSignedPart(
  element: XmlElement,
  algorithm: string,
  inclusivePrefixes: readonly string[],
  excludeElement?: XmlElement,
): Buffer {
  try {
    return canonicalizeXml(element, algorithm, {
      excludeElement,
      inclusivePrefixes,
    });
  } catch (error) {
    fail(
      "SIGNATURE_CANONICALIZATION_INVALID",
      error instanceof Error ? error.message : "Canonicalization failed",
    );
  }
}

function derLength(length: number): Buffer {
  if (length < 0x80) {
    return Buffer.from([length]);
  }
  const bytes: number[] = [];
  let remaining = length;
  while (remaining > 0) {
    bytes.unshift(remaining & 0xff);
    remaining >>>= 8;
  }
  return Buffer.from([0x80 | bytes.length, ...bytes]);
}

function derInteger(bytes: Buffer): Buffer {
  let offset = 0;
  while (offset < bytes.length - 1 && bytes[offset] === 0) {
    offset += 1;
  }
  let value = bytes.subarray(offset);
  if (value.length === 0) {
    value = Buffer.from([0]);
  }
  if ((value[0] & 0x80) !== 0) {
    value = Buffer.concat([Buffer.from([0]), value]);
  }
  return Buffer.concat([Buffer.from([0x02]), derLength(value.length), value]);
}

function xmlDsigEcdsaToDer(signature: Buffer, namedCurve: string): Buffer {
  const coordinateLengths: Readonly<Record<string, number>> = Object.freeze({
    prime256v1: 32,
    secp384r1: 48,
    secp521r1: 66,
  });
  const coordinateLength = coordinateLengths[namedCurve];
  if (coordinateLength === undefined || signature.length !== coordinateLength * 2) {
    fail(
      "SIGNATURE_CRYPTOGRAPHIC_INVALID",
      "ECDSA SignatureValue is not in XMLDSig r||s form for the certificate curve",
    );
  }
  const r = derInteger(signature.subarray(0, coordinateLength));
  const s = derInteger(signature.subarray(coordinateLength));
  const body = Buffer.concat([r, s]);
  return Buffer.concat([Buffer.from([0x30]), derLength(body.length), body]);
}

function isCertificateTimeValid(certificate: X509Certificate, now: Date): boolean {
  const from = Date.parse(certificate.validFrom);
  const to = Date.parse(certificate.validTo);
  if (!Number.isFinite(from) || !Number.isFinite(to)) {
    fail("IDP_METADATA_INVALID", "IdP certificate contains invalid validity dates");
  }
  const nowMs = now.getTime();
  return Number.isFinite(nowMs) && nowMs >= from && nowMs <= to;
}

export interface VerifiedSignatureDetails {
  readonly algorithm: string;
  readonly digestAlgorithm: string;
  readonly canonicalizationAlgorithm: string;
  readonly certificateSha256: string;
  readonly referenceUri: string;
  readonly signedNodeIdentity: number;
}

export interface SignatureVerificationContext {
  readonly policy: ResolvedAlgorithmPolicy;
  readonly trustedCertificates: TrustedCertificateSet;
  readonly now: Date;
}

export function verifyEnvelopedSignature(
  signature: XmlElement,
  signedNode: XmlElement,
  context: SignatureVerificationContext,
): VerifiedSignatureDetails {
  const structure = parseSignatureStructure(signature, context.policy);
  if (signature.parent !== signedNode) {
    fail("SIGNATURE_PLACEMENT_INVALID", "Signature must be a direct child of the node it signs");
  }
  const idAttribute = signedNode.idAttribute;
  if (idAttribute === null) {
    fail("SIGNATURE_TARGET_INVALID", `${signedNode.localName} has no ID`);
  }
  const expectedUri = `#${idAttribute.value}`;
  if (structure.reference.uri !== expectedUri) {
    fail(
      "SIGNATURE_REFERENCE_INVALID",
      `Reference ${structure.reference.uri} does not identify the enclosing ${signedNode.localName}`,
    );
  }

  const digestCanonical = canonicalizeSignedPart(
    signedNode,
    structure.reference.digestCanonicalization,
    structure.reference.digestInclusivePrefixes,
    signature,
  );
  const hashName = DIGEST_TO_HASH.get(structure.reference.digestAlgorithm);
  if (hashName === undefined) {
    fail("SIGNATURE_ALGORITHM_FORBIDDEN", "Unsupported digest algorithm");
  }
  const computedDigest = createHash(hashName).update(digestCanonical).digest();
  if (!timingSafeBufferEqual(computedDigest, structure.reference.digest)) {
    fail("SIGNATURE_DIGEST_MISMATCH", `Digest mismatch for ${signedNode.localName}`);
  }

  const canonicalSignedInfo = canonicalizeSignedPart(
    structure.signedInfo,
    structure.canonicalizationAlgorithm,
    structure.canonicalizationInclusivePrefixes,
  );

  const crypto = SIGNATURE_TO_CRYPTO.get(structure.signatureAlgorithm);
  if (crypto === undefined) {
    fail("SIGNATURE_ALGORITHM_FORBIDDEN", "Unsupported signature algorithm");
  }

  const candidates: Array<{ certificate: X509Certificate; der: Buffer }> = [];
  if (structure.certificateDer !== null) {
    for (let index = 0; index < context.trustedCertificates.certificates.length; index += 1) {
      if (timingSafeBufferEqual(context.trustedCertificates.derCertificates[index], structure.certificateDer)) {
        candidates.push({
          certificate: context.trustedCertificates.certificates[index],
          der: context.trustedCertificates.derCertificates[index],
        });
      }
    }
    if (candidates.length === 0) {
      fail(
        "SIGNATURE_CERTIFICATE_UNTRUSTED",
        "Signature certificate is not in the selected IdP metadata generation",
      );
    }
  } else {
    for (let index = 0; index < context.trustedCertificates.certificates.length; index += 1) {
      candidates.push({
        certificate: context.trustedCertificates.certificates[index],
        der: context.trustedCertificates.derCertificates[index],
      });
    }
  }

  let selected: { certificate: X509Certificate; der: Buffer } | null = null;
  let lastFailure: SamlSignatureError | null = null;
  for (const candidate of candidates) {
    try {
      if (!isCertificateTimeValid(candidate.certificate, context.now)) {
        fail("SIGNATURE_CERTIFICATE_EXPIRED", "IdP signing certificate is outside its validity period");
      }
      checkCertificatePublicKey(candidate.certificate, crypto.keyType);
      const keyDetails = candidate.certificate.publicKey.asymmetricKeyDetails as
        | { namedCurve?: string }
        | undefined;
      const cryptographicSignature =
        crypto.keyType === "ec"
          ? xmlDsigEcdsaToDer(structure.signatureValue, keyDetails?.namedCurve ?? "")
          : structure.signatureValue;
      const verifier = createVerify(crypto.hash);
      verifier.update(canonicalSignedInfo);
      verifier.end();
      if (verifier.verify({ key: candidate.certificate.publicKey }, cryptographicSignature)) {
        selected = candidate;
        break;
      }
      fail("SIGNATURE_CRYPTOGRAPHIC_INVALID", `SignatureValue is invalid for ${signedNode.localName}`);
    } catch (error) {
      if (error instanceof SamlSignatureError) {
        lastFailure = error;
      } else {
        lastFailure = new SamlSignatureError(
          "SIGNATURE_CRYPTOGRAPHIC_INVALID",
          `SignatureValue is invalid for ${signedNode.localName}`,
        );
      }
    }
  }
  if (selected === null) {
    throw (
      lastFailure ??
      new SamlSignatureError("SIGNATURE_CERTIFICATE_UNTRUSTED", "No trusted certificate validated the signature")
    );
  }

  return Object.freeze({
    algorithm: structure.signatureAlgorithm,
    digestAlgorithm: structure.reference.digestAlgorithm,
    canonicalizationAlgorithm: structure.canonicalizationAlgorithm,
    certificateSha256: createHash("sha256").update(selected.der).digest("hex"),
    referenceUri: structure.reference.uri,
    signedNodeIdentity: signedNode.identity,
  });
}

export function findDirectSignature(element: XmlElement): XmlElement | null {
  const signatures = findDirectChildren(element, XMLDSIG_NS, "Signature");
  if (signatures.length > 1) {
    fail("SIGNATURE_PLACEMENT_INVALID", `${element.localName} contains multiple direct Signature elements`);
  }
  return signatures[0] ?? null;
}
