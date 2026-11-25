import type {
  KeyRecord,
  KeyringSnapshot,
  TrustSignature,
  UserIdRecord,
} from "../src/index.ts";

export const T0 = Date.parse("2026-01-01T00:00:00Z");
export const DAY = 86_400_000;

export function key(
  materialFingerprint: string,
  extra: Partial<KeyRecord> = {},
): KeyRecord {
  return { materialFingerprint, ...extra };
}

export function uid(
  id: string,
  email: string,
  keyFingerprint: string,
  extra: Partial<UserIdRecord> = {},
): UserIdRecord {
  return { id, email, key: keyFingerprint, ...extra };
}

export function sig(
  id: string,
  from: string,
  to: string,
  options: Partial<TrustSignature> & { depth: number; weight: number },
): TrustSignature {
  return {
    id,
    from,
    to,
    issuedAt: T0,
    notBefore: T0,
    notAfter: T0 + 30 * DAY,
    domains: "example\\.com",
    ...options,
  };
}

export function snapshot(
  options: Partial<KeyringSnapshot> & {
    keys: KeyRecord[];
    userIds: UserIdRecord[];
    signatures: TrustSignature[];
  },
): KeyringSnapshot {
  return {
    id: "snapshot-1",
    roots: ["R"],
    ...options,
  };
}

export function at(offsetMs: number): number {
  return T0 + offsetMs;
}
