import { createHash, createPublicKey, verify as cryptoVerify } from "node:crypto";
import { Rational, rat } from "./rational.ts";
import type { RationalInput } from "./rational.ts";
import { leaseTerm } from "./time.ts";
import type {
  LeaseTerm,
  LeaseTermInput,
  TimeMarker,
} from "./time.ts";
import { normalizeResourceScope, resourceScopeToJSON } from "./resources.ts";
import type {
  ResourceInterval,
  ResourceScope,
  ResourceScopeInput,
} from "./resources.ts";

export interface DeviceIdentity {
  /** Cryptographic key identifier, not a human display name. */
  id: string;
  /** Key generation / epoch.  A different generation is a different device. */
  generation: string | number;
  /** Optional display name.  Never used for identity or equality. */
  name?: string;
  /** Optional PEM/SPKI public key for the built-in Ed25519 verifier. */
  publicKey?: string;
}

export interface Lease {
  id: string;
  device: DeviceIdentity;
  resources: ResourceScopeInput;
  /** Term is written on the holder's local device clock. */
  term: LeaseTermInput;
  parentId?: string;
  parentDigest?: string;
  issuedAt?: RationalInput;
  signature?: string;
}

export interface EarlyReturn {
  id: string;
  leaseId: string;
  leaseDigest?: string;
  device: DeviceIdentity;
  /** Release instant on the returning device's local clock. */
  returnedAt: RationalInput;
  /** Omitted means the whole effective lease scope. */
  resources?: ResourceScopeInput;
  signature?: string;
}

/**
 * A signed claim that, at local time `at`, the complete descendant set of
 * `leaseId` is exactly `descendants`.  One proof may cover any number of
 * generations of descendants.  Without such a proof an early-returned lease
 * remains an opaque possibility until its true original term end.
 */
export interface CompletenessProof {
  id: string;
  leaseId: string;
  leaseDigest?: string;
  device: DeviceIdentity;
  at: RationalInput;
  descendants: string[];
  /** Optional commitment; when present it must equal the computed root. */
  root?: string;
  signature?: string;
}

export interface ClockObservation {
  device: DeviceIdentity;
  /** Server reference time of the observation. */
  serverTime: RationalInput;
  /** Observed local reading interval at that server time. */
  deviceTime: { lo: RationalInput; hi: RationalInput };
}

export interface DeviceClock {
  device: DeviceIdentity;
  /** offset = deviceLocal - server. */
  offset: { lo: RationalInput; hi: RationalInput };
  /** d(offset)/d(server), bounded.  The clock must be monotone: 1 + drift > 0. */
  drift: { lo: RationalInput; hi: RationalInput };
  observations?: ClockObservation[];
}

export interface NormalizedLease {
  id: string;
  device: DeviceIdentity;
  deviceKey: string;
  resources: ResourceScope;
  declaredResources: ResourceScope;
  term: LeaseTerm;
  parentId: string | null;
  parentDigest: string | null;
  issuedAt: Rational | null;
  signature: string | null;
  digest: string;
}

export interface NormalizedReturn {
  id: string;
  leaseId: string;
  leaseDigest: string | null;
  device: DeviceIdentity;
  deviceKey: string;
  returnedAt: Rational;
  resources: ResourceScope | null;
  signature: string | null;
  digest: string;
}

export interface NormalizedCompleteness {
  id: string;
  leaseId: string;
  leaseDigest: string | null;
  device: DeviceIdentity;
  deviceKey: string;
  at: Rational;
  descendants: string[];
  root: string;
  signature: string | null;
  digest: string;
}

export type SignatureVerifier = (
  canonicalPayload: string,
  signature: string,
  device: DeviceIdentity,
) => boolean;

export class AuthorizationError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "AuthorizationError";
  }
}

export function deviceKey(device: DeviceIdentity): string {
  return `${device.id}#${String(device.generation)}`;
}

export function sameDevice(a: DeviceIdentity, b: DeviceIdentity): boolean {
  return deviceKey(a) === deviceKey(b);
}

export function stableStringify(value: unknown): string {
  return JSON.stringify(stableValue(value));
}

function stableValue(value: unknown): unknown {
  if (value instanceof Rational) {
    return { numerator: value.n.toString(), denominator: value.d.toString() };
  }
  if (Array.isArray(value)) return value.map(stableValue);
  if (value !== null && typeof value === "object") {
    const object = value as Record<string, unknown>;
    const output: Record<string, unknown> = {};
    for (const key of Object.keys(object).sort()) {
      if (object[key] !== undefined) output[key] = stableValue(object[key]);
    }
    return output;
  }
  if (typeof value === "bigint") return value.toString();
  return value;
}

export function sha256Hex(payload: string): string {
  return createHash("sha256").update(payload, "utf8").digest("hex");
}

export function normalizedDevice(device: DeviceIdentity): Record<string, unknown> {
  return {
    id: device.id,
    generation: String(device.generation),
    ...(device.name === undefined ? {} : { name: device.name }),
    ...(device.publicKey === undefined ? {} : { publicKey: device.publicKey }),
  };
}

export function normalizedTerm(term: LeaseTermInput): Record<string, unknown> {
  const normalized = leaseTerm(term);
  return {
    start: normalized.start.toJSON(),
    end: normalized.end.toJSON(),
    startInclusive: normalized.startInclusive,
    endInclusive: normalized.endInclusive,
  };
}

export function leasePayload(lease: Lease): string {
  return stableStringify({
    type: "lease/v1",
    id: lease.id,
    device: normalizedDevice(lease.device),
    resources: resourceScopeToJSON(normalizeResourceScope(lease.resources)),
    term: normalizedTerm(lease.term),
    parentId: lease.parentId ?? null,
    parentDigest: lease.parentDigest ?? null,
    issuedAt: lease.issuedAt == null ? null : rat(lease.issuedAt).toJSON(),
  });
}

export function leaseDigest(lease: Lease): string {
  return sha256Hex(leasePayload(lease));
}

export function earlyReturnPayload(earlyReturn: EarlyReturn, resolvedLeaseDigest?: string): string {
  return stableStringify({
    type: "early-return/v1",
    id: earlyReturn.id,
    leaseId: earlyReturn.leaseId,
    leaseDigest: earlyReturn.leaseDigest ?? resolvedLeaseDigest ?? null,
    device: normalizedDevice(earlyReturn.device),
    returnedAt: rat(earlyReturn.returnedAt).toJSON(),
    resources:
      earlyReturn.resources === undefined
        ? null
        : resourceScopeToJSON(normalizeResourceScope(earlyReturn.resources)),
  });
}

export function earlyReturnDigest(
  earlyReturn: EarlyReturn,
  resolvedLeaseDigest?: string,
): string {
  return sha256Hex(earlyReturnPayload(earlyReturn, resolvedLeaseDigest));
}

export function completenessPayload(
  proof: CompletenessProof,
  descendantDigests: string[],
): string {
  return stableStringify({
    type: "subtree-complete/v1",
    id: proof.id,
    leaseId: proof.leaseId,
    leaseDigest: proof.leaseDigest ?? null,
    device: normalizedDevice(proof.device),
    at: rat(proof.at).toJSON(),
    descendants: [...proof.descendants].sort(),
    descendantDigests: [...descendantDigests].sort(),
    root: proof.root ?? null,
  });
}

export function completenessRoot(leaseDigestValue: string, descendantDigests: string[]): string {
  return sha256Hex(
    stableStringify({
      type: "subtree-root/v1",
      leaseDigest: leaseDigestValue,
      descendants: [...descendantDigests].sort(),
    }),
  );
}

export function completenessDigest(
  proof: CompletenessProof,
  descendantDigests: string[],
): string {
  return sha256Hex(completenessPayload(proof, descendantDigests));
}

export function normalizeLease(lease: Lease): NormalizedLease {
  if (typeof lease.id !== "string" || lease.id.length === 0) {
    throw new AuthorizationError("lease id must be a non-empty string");
  }
  if (!lease.device || typeof lease.device.id !== "string") {
    throw new AuthorizationError(`lease ${lease.id}: missing device identity`);
  }
  const resources = normalizeResourceScope(lease.resources);
  return {
    id: lease.id,
    device: lease.device,
    deviceKey: deviceKey(lease.device),
    resources,
    declaredResources: resources,
    term: leaseTerm(lease.term),
    parentId: lease.parentId ?? null,
    parentDigest: lease.parentDigest ?? null,
    issuedAt: lease.issuedAt === undefined ? null : rat(lease.issuedAt),
    signature: lease.signature ?? null,
    digest: leaseDigest(lease),
  };
}

export function normalizeEarlyReturn(earlyReturn: EarlyReturn): NormalizedReturn {
  if (!earlyReturn.leaseId) {
    throw new AuthorizationError(`early return ${earlyReturn.id}: missing leaseId`);
  }
  return {
    id: earlyReturn.id,
    leaseId: earlyReturn.leaseId,
    leaseDigest: earlyReturn.leaseDigest ?? null,
    device: earlyReturn.device,
    deviceKey: deviceKey(earlyReturn.device),
    returnedAt: rat(earlyReturn.returnedAt),
    resources:
      earlyReturn.resources === undefined ? null : normalizeResourceScope(earlyReturn.resources),
    signature: earlyReturn.signature ?? null,
    digest: earlyReturnDigest(earlyReturn),
  };
}

export function ed25519Verifier(
  publicKeys: Map<string, string> | Record<string, string>,
): SignatureVerifier {
  const keyMap =
    publicKeys instanceof Map ? publicKeys : new Map(Object.entries(publicKeys));
  return (payload, signature, device) => {
    const pem = keyMap.get(deviceKey(device)) ?? device.publicKey;
    if (!pem) return false;
    try {
      const key = createPublicKey(pem);
      const signatureBytes = decodeSignature(signature);
      return cryptoVerify(null, Buffer.from(payload, "utf8"), key, signatureBytes);
    } catch {
      return false;
    }
  };
}

function decodeSignature(signature: string): Buffer {
  if (/^[0-9a-fA-F]+$/.test(signature) && signature.length % 2 === 0) {
    return Buffer.from(signature, "hex");
  }
  return Buffer.from(signature, "base64");
}

export interface SignaturePolicy {
  verifier?: SignatureVerifier;
  /** If true, every lease, return and proof must carry a valid signature. */
  requireSignatures?: boolean;
}

export function assertSignature(
  payload: string,
  signature: string | null,
  device: DeviceIdentity,
  policy: SignaturePolicy,
  what: string,
): boolean {
  if (policy.verifier) {
    if (signature === null || signature.length === 0) {
      if (policy.requireSignatures) throw new AuthorizationError(`${what}: missing signature`);
      return false;
    }
    const valid = policy.verifier(payload, signature, device);
    if (!valid && policy.requireSignatures) {
      throw new AuthorizationError(`${what}: invalid signature`);
    }
    return valid;
  }
  if (policy.requireSignatures) {
    if (signature === null || signature.length === 0) {
      throw new AuthorizationError(`${what}: missing signature`);
    }
    return true;
  }
  // Structural mode accepts unsigned illustrations.  When a verifier is
  // supplied the caller still receives false for an unverified signature.
  return true;
}

export interface MarkerJson {
  time: string | null;
  inclusive: boolean;
  infinite?: "negative" | "positive" | null;
}

export function markerToJSON(marker: TimeMarker): MarkerJson {
  return {
    time: marker.time === null ? null : marker.time.toString(),
    inclusive: marker.inclusive,
    ...(marker.time === null ? { infinite: marker.infinite ?? null } : {}),
  };
}

export function resourceScopeCopy(scope: ResourceScope): ResourceInterval[] {
  return resourceScopeToJSON(scope);
}
