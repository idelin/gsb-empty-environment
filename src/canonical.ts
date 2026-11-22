import { createHash } from "node:crypto";
import type { Invitation, InvitationDigest } from "./types.ts";

/**
 * Deterministic JSON serialization. Object keys are sorted recursively so
 * every node computes the same digest regardless of property insertion order.
 */
export function canonicalJson(value: unknown): string {
  if (value === null || typeof value !== "object") {
    return canonicalScalar(value);
  }
  if (Array.isArray(value)) {
    return `[${value.map((entry) => canonicalJson(entry)).join(",")}]`;
  }
  const record = value as Record<string, unknown>;
  const keys = Object.keys(record).sort();
  const parts: string[] = [];
  for (const key of keys) {
    if (record[key] === undefined) {
      continue;
    }
    parts.push(`${JSON.stringify(key)}:${canonicalJson(record[key])}`);
  }
  return `{${parts.join(",")}}`;
}

function canonicalScalar(value: unknown): string {
  const kind = typeof value;
  if (kind === "bigint") {
    throw new TypeError("bigint values cannot be canonicalized");
  }
  if (kind === "number" && !Number.isFinite(value)) {
    throw new TypeError("non-finite numbers cannot be canonicalized");
  }
  if (kind === "function" || kind === "symbol" || kind === "undefined") {
    throw new TypeError(`${kind} values cannot be canonicalized`);
  }
  return JSON.stringify(value);
}

export function sha256Hex(input: string): string {
  return createHash("sha256").update(input, "utf8").digest("hex");
}

const INVITATION_DOMAIN = "relay-session:invitation:v1";

/** Digest of the original invitation. Every forwarded answer binds to this. */
export function invitationDigest(invitation: Invitation): InvitationDigest {
  return sha256Hex(`${INVITATION_DOMAIN}\n${canonicalJson(invitation)}`);
}

export const FACT_SIGNING_DOMAIN = "relay-session:signed-fact:v1";

/** Canonical bytes that a signer covers for a signed-fact envelope. */
export function factSigningInput(
  envelope: {
    factId: string;
    kind: string;
    payload: unknown;
    signer: string;
    signedAt: number;
  },
): string {
  return `${FACT_SIGNING_DOMAIN}\n${canonicalJson({
    factId: envelope.factId,
    kind: envelope.kind,
    payload: envelope.payload,
    signer: envelope.signer,
    signedAt: envelope.signedAt,
  })}`;
}
