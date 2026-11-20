import type { TenantPolicy } from "./policy.ts";
import type { Resolution } from "./resolver.ts";
import { canonicalIp } from "./address.ts";
import { normalizeHostname } from "./hostname.ts";

export interface Credential {
  tenantId: string;
  hostname: string;
  address: string;
  port: number;
  generation: number;
  expiresAtMs: number;
}

export type DenyReason =
  | "cname-loop"
  | "suffix-not-allowed"
  | "invalid-address"
  | "address-not-in-resolution"
  | "address-denied"
  | "already-expired";

export interface AuthorizeSuccess {
  ok: true;
  credential: Credential;
}

export interface AuthorizeFailure {
  ok: false;
  reason: DenyReason;
  detail: string;
}

export type AuthorizeResult = AuthorizeSuccess | AuthorizeFailure;

export interface AuthorizeInput {
  policy: TenantPolicy;
  hostname: string;
  port: number;
  resolution: Resolution;
  address: string;
  nowMs: number;
}

export function authorizeAddress(input: AuthorizeInput): AuthorizeResult {
  const { policy, port, resolution, nowMs } = input;
  const hostname = normalizeHostname(input.hostname);

  const seen = new Set<string>();
  let expiresAtMs = policy.deadlineMs;
  for (const hop of resolution.chain) {
    const hopName = normalizeHostname(hop.name);
    if (seen.has(hopName)) {
      return { ok: false, reason: "cname-loop", detail: hopName };
    }
    seen.add(hopName);
    if (!policy.allowsHostname(hopName)) {
      return { ok: false, reason: "suffix-not-allowed", detail: hopName };
    }
    expiresAtMs = Math.min(expiresAtMs, nowMs + hop.ttlSec * 1000);
  }

  const canonical = canonicalIp(input.address);
  if (canonical === null) {
    return { ok: false, reason: "invalid-address", detail: input.address };
  }
  const addressEntry = resolution.addresses.find(
    entry => canonicalIp(entry.address) === canonical,
  );
  if (!addressEntry) {
    return { ok: false, reason: "address-not-in-resolution", detail: input.address };
  }
  expiresAtMs = Math.min(expiresAtMs, nowMs + addressEntry.ttlSec * 1000);

  if (!policy.isAddressAllowed(canonical)) {
    return { ok: false, reason: "address-denied", detail: canonical };
  }
  if (!(expiresAtMs > nowMs)) {
    return { ok: false, reason: "already-expired", detail: canonical };
  }

  return {
    ok: true,
    credential: {
      tenantId: policy.tenantId,
      hostname,
      address: canonical,
      port,
      generation: policy.generation,
      expiresAtMs,
    },
  };
}
