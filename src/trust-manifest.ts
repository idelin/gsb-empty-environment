/**
 * Signed trust-manifest root handling.
 *
 * Trust model:
 *  1. The manifest is canonical JSON signed with Ed25519. The SPKI public key
 *     embedded in the manifest is itself anchored by `rootKeyId`; host code
 *     supplies the expected anchor (or pins the key out-of-band).
 *  2. Verification checks the signature, the algorithm allow-list and that
 *     every digest entry is internally consistent.
 *  3. Context resolution then independently re-hashes each object and rejects
 *     anything absent from the signed allow-list. URL strings never grant
 *     trust.
 */

import {
  base64UrlDecode,
  base64UrlEncode,
  canonicalBytes,
  canonicalJson,
  constantTimeEqualHex,
  fail,
} from "./canonical.ts";
import { createHash } from "node:crypto";

import { digestFromCid, digestHexOf } from "./content-address.ts";
import { CONTENT_ID_ALGORITHM } from "./types.ts";
import type {
  AllowedContextEntry,
  ContentId,
  ContextObject,
  ContentStore,
  SignedManifestEnvelope,
  TrustManifest,
} from "./types.ts";

const subtle = (): SubtleCrypto => globalThis.crypto.subtle;

export interface KeyPairMaterial {
  readonly publicKey: CryptoKey;
  readonly privateKey: CryptoKey;
}

export async function generateRootKeyPair(): Promise<KeyPairMaterial> {
  const pair = (await subtle().generateKey(
    { name: "Ed25519" },
    false,
    ["sign", "verify"],
  )) as CryptoKeyPair;
  return { publicKey: pair.publicKey, privateKey: pair.privateKey };
}

export async function exportPublicKeySpki(key: CryptoKey): Promise<string> {
  const raw = await subtle().exportKey("spki", key);
  return base64UrlEncode(new Uint8Array(raw));
}

export async function importRootPublicKey(spkiBase64Url: string): Promise<CryptoKey> {
  return subtle().importKey(
    "spki",
    base64UrlDecode(spkiBase64Url),
    { name: "Ed25519" },
    false,
    ["verify"],
  );
}

/**
 * Build a manifest enumerating exactly the supplied contexts. Digests are
 * computed from canonical bytes, so later re-hashing reproduces them.
 */
export function buildManifest(
  rootKeyId: string,
  rootPublicKeySpki: string,
  contexts: ReadonlyArray<{ readonly cid: ContentId; readonly label?: string }>,
  issuedAt: string,
): TrustManifest {
  const entries: AllowedContextEntry[] = contexts.map((entry) => ({
    cid: entry.cid,
    digestHex: digestFromCid(entry.cid),
    label: entry.label ?? "",
  }));
  return {
    manifestVersion: 1,
    rootKeyId,
    rootPublicKeySpki,
    allowedContexts: entries,
    contentAlgorithm: CONTENT_ID_ALGORITHM,
    issuedAt,
  };
}

export async function signManifest(
  manifest: TrustManifest,
  privateKey: CryptoKey,
): Promise<SignedManifestEnvelope> {
  const manifestCanonical = canonicalJson(manifest);
  const signatureBuffer = await subtle().sign(
    { name: "Ed25519" },
    privateKey,
    canonicalBytes(manifest),
  );
  return {
    manifestCanonical,
    signature: base64UrlEncode(new Uint8Array(signatureBuffer)),
  };
}

export interface VerifiedManifest {
  readonly manifest: TrustManifest;
  readonly allowed: ReadonlyMap<ContentId, AllowedContextEntry>;
}

export interface VerifyOptions {
  /** Optional pinned key id; mismatch is a hard failure. */
  readonly expectedRootKeyId?: string;
  /** Optional pinned SPKI key; mismatch is a hard failure. */
  readonly expectedRootPublicKeySpki?: string;
}

/** Parse, structurally validate and signature-verify a manifest envelope. */
export async function verifyManifest(
  envelope: SignedManifestEnvelope,
  options: VerifyOptions = {},
): Promise<VerifiedManifest> {
  let manifest: TrustManifest;
  try {
    manifest = JSON.parse(envelope.manifestCanonical) as TrustManifest;
  } catch {
    fail("TRUST_MANIFEST_MALFORMED", "Manifest canonical bytes are not valid JSON");
  }
  validateManifestShape(manifest, options);

  const publicKey = await importRootPublicKey(manifest.rootPublicKeySpki);
  let valid = false;
  try {
    valid = await subtle().verify(
      { name: "Ed25519" },
      publicKey,
      base64UrlDecode(envelope.signature),
      canonicalBytes(manifest),
    );
  } catch {
    fail("TRUST_MANIFEST_SIGNATURE_INVALID", "Signature bytes could not be processed");
  }
  if (!valid) {
    fail("TRUST_MANIFEST_SIGNATURE_INVALID", "Ed25519 manifest signature does not verify");
  }

  const allowed = new Map<ContentId, AllowedContextEntry>();
  for (const entry of manifest.allowedContexts) {
    if (allowed.has(entry.cid)) {
      fail("TRUST_MANIFEST_MALFORMED", "Manifest lists a duplicate content id", entry.cid);
    }
    if (!constantTimeEqualHex(digestFromCid(entry.cid), entry.digestHex)) {
      fail("TRUST_MANIFEST_ROOT_MISMATCH", "Manifest digest does not match its content id", entry.cid);
    }
    allowed.set(entry.cid, entry);
  }
  return { manifest, allowed };
}

function validateManifestShape(
  manifest: TrustManifest,
  options: VerifyOptions,
): void {
  if (manifest === null || typeof manifest !== "object") {
    fail("TRUST_MANIFEST_MALFORMED", "Manifest is not an object");
  }
  if (manifest.manifestVersion !== 1) {
    fail("TRUST_MANIFEST_MALFORMED", "Unsupported manifest version", String(manifest.manifestVersion));
  }
  if (typeof manifest.rootKeyId !== "string" || manifest.rootKeyId.length === 0) {
    fail("TRUST_MANIFEST_MALFORMED", "Manifest rootKeyId is missing");
  }
  if (
    typeof manifest.rootPublicKeySpki !== "string" ||
    manifest.rootPublicKeySpki.length === 0
  ) {
    fail("TRUST_MANIFEST_MALFORMED", "Manifest root public key is missing");
  }
  if (manifest.contentAlgorithm !== CONTENT_ID_ALGORITHM) {
    fail("TRUST_MANIFEST_MALFORMED", "Manifest uses an unsupported content algorithm");
  }
  if (!Array.isArray(manifest.allowedContexts)) {
    fail("TRUST_MANIFEST_MALFORMED", "Manifest allow-list is missing");
  }
  for (const entry of manifest.allowedContexts) {
    if (
      entry === null ||
      typeof entry !== "object" ||
      typeof entry.cid !== "string" ||
      typeof entry.digestHex !== "string" ||
      typeof entry.label !== "string"
    ) {
      fail("TRUST_MANIFEST_MALFORMED", "Allow-list entry is malformed");
    }
  }
  if (
    options.expectedRootKeyId !== undefined &&
    options.expectedRootKeyId !== manifest.rootKeyId
  ) {
    fail("TRUST_MANIFEST_ROOT_MISMATCH", "Manifest rootKeyId does not match pinned anchor");
  }
  if (
    options.expectedRootPublicKeySpki !== undefined &&
    options.expectedRootPublicKeySpki !== manifest.rootPublicKeySpki
  ) {
    fail("TRUST_MANIFEST_ROOT_MISMATCH", "Manifest public key does not match pinned anchor");
  }
}

/**
 * Gate a context object against the signed allow-list and re-hash it. This is
 * the single chokepoint through which every resolver context read passes.
 */
export function assertContextTrusted(
  cid: ContentId,
  object: ContextObject | undefined,
  allowed: ReadonlyMap<ContentId, AllowedContextEntry>,
  store?: ContentStore,
): void {
  const entry = allowed.get(cid);
  if (entry === undefined) {
    fail(
      "CONTENT_NOT_IN_TRUST_LIST",
      "Context id is absent from the signed trust manifest",
      cid,
    );
  }
  if (object === undefined || object === null) {
    fail("CONTENT_MALFORMED", "Trusted id has no retrievable content", cid);
  }
  const storedBytes = store?.canonicalBytes(cid);
  const actual =
    storedBytes === undefined
      ? digestHexOf(object)
      : createHash("sha256").update(storedBytes).digest("hex");
  if (!constantTimeEqualHex(actual, entry.digestHex)) {
    fail("CONTENT_DIGEST_MISMATCH", "Context content digest disagrees with manifest", cid);
  }
  if (!constantTimeEqualHex(actual, digestFromCid(cid))) {
    fail("CONTENT_DIGEST_MISMATCH", "Context content digest disagrees with its id", cid);
  }
}
