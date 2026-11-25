import { createHmac, timingSafeEqual } from "node:crypto";
import { digestBytes, stableStringify } from "./canonical.ts";
import type { GrantedAction, SignedGrant, SubOperationSpec } from "./types.ts";

/** A named secret used to sign grants. */
export interface SigningKey {
  readonly kid: string;
  readonly secret: string;
}

/** Input required to mint a signed grant (signature is added server-side). */
export type GrantInput = Omit<SignedGrant, "signature">;

function hmac(secret: string, payload: string): string {
  return createHmac("sha256", secret).update(payload, "utf8").digest("base64url");
}

/**
 * Canonical bytes covered by the signature. Every bound attribute — subject,
 * window session, object version, form digest, action semantics and both
 * consumption limits — is included, so tampering with any of them invalidates
 * the signature.
 */
export function grantSigningPayload(grant: GrantInput): string {
  return stableStringify({
    v: 1,
    grantId: grant.grantId,
    subject: grant.subject,
    windowSessionId: grant.windowSessionId,
    objectVersion: grant.objectVersion,
    formDigest: grant.formDigest,
    expiresAt: grant.expiresAt,
    kid: grant.kid,
    actions: grant.actions.map((action: GrantedAction) => ({
      actionId: action.actionId,
      meaning: action.meaning,
      maxActionConsumptions: action.maxActionConsumptions,
      subOperations: action.subOperations.map((spec: SubOperationSpec) => ({
        subId: spec.subId,
        meaning: spec.meaning,
        dependsOn: [...spec.dependsOn].sort(),
        maxSubConsumptions: spec.maxSubConsumptions,
      })),
    })),
  });
}

/** Structural validation shared by minting and verification. */
export function validateGrantStructure(grant: SignedGrant): void {
  if (!grant.grantId || !grant.subject || !grant.windowSessionId) {
    throw new Error("grant is missing identity bindings");
  }
  if (!grant.objectVersion || !grant.formDigest || !grant.kid || !grant.signature) {
    throw new Error("grant is missing version, digest, key id or signature");
  }
  const actionIds = new Set<string>();
  for (const action of grant.actions) {
    if (actionIds.has(action.actionId)) {
      throw new Error(`duplicate action id: ${action.actionId}`);
    }
    actionIds.add(action.actionId);
    if (!Number.isInteger(action.maxActionConsumptions) || action.maxActionConsumptions < 1) {
      throw new Error(`action ${action.actionId} must declare maxActionConsumptions >= 1`);
    }
    const subIds = new Set<string>();
    for (const spec of action.subOperations) {
      if (subIds.has(spec.subId)) {
        throw new Error(`duplicate sub id ${spec.subId} under action ${action.actionId}`);
      }
      subIds.add(spec.subId);
      if (!Number.isInteger(spec.maxSubConsumptions) || spec.maxSubConsumptions < 1) {
        throw new Error(`sub ${spec.subId} must declare maxSubConsumptions >= 1`);
      }
    }
    for (const spec of action.subOperations) {
      for (const dependency of spec.dependsOn) {
        if (!subIds.has(dependency)) {
          throw new Error(
            `sub ${spec.subId} depends on unknown sibling ${dependency} in action ${action.actionId}`,
          );
        }
      }
    }
  }
}

/** Mint a signed grant using the supplied signing key. */
export function signGrant(input: GrantInput, key: SigningKey): SignedGrant {
  if (input.kid !== key.kid) {
    throw new Error(`key id mismatch: grant wants ${input.kid}, key is ${key.kid}`);
  }
  validateGrantStructure({ ...input, signature: "pending" });
  return { ...input, signature: hmac(key.secret, grantSigningPayload(input)) };
}

/**
 * Verify a grant's structure, expiry and HMAC. Policy-time concerns
 * (revocation, shrunk limits) are intentionally NOT checked here; they are
 * re-evaluated on every submission against the current policy.
 */
export function verifyGrant(
  grant: SignedGrant,
  keys: readonly SigningKey[],
  now: number = Date.now(),
): { readonly ok: true } | { readonly ok: false; readonly reason: string } {
  try {
    validateGrantStructure(grant);
  } catch (error) {
    return { ok: false, reason: (error as Error).message };
  }
  if (!Number.isFinite(grant.expiresAt) || grant.expiresAt <= now) {
    return { ok: false, reason: "grant expired" };
  }
  const key = keys.find((candidate) => candidate.kid === grant.kid);
  if (!key) {
    return { ok: false, reason: `unknown signing key: ${grant.kid}` };
  }
  const expected = hmac(key.secret, grantSigningPayload(grant));
  const actualBuffer = Buffer.from(grant.signature, "base64url");
  const expectedBuffer = Buffer.from(expected, "base64url");
  if (actualBuffer.length !== expectedBuffer.length) {
    return { ok: false, reason: "signature length mismatch" };
  }
  if (!timingSafeEqual(actualBuffer, expectedBuffer)) {
    return { ok: false, reason: "signature mismatch" };
  }
  return { ok: true };
}

/** Helper for callers that derive form digests outside this package. */
export { digestBytes };
