export const FULL_TRUST_AMOUNT = 120;
export const MARGINAL_TRUST_AMOUNT = 60;
export const DEFAULT_REQUIRED_AMOUNT = FULL_TRUST_AMOUNT;
export const DEFAULT_ROOT_LEVEL = 255;

export interface TrustSignature {
  issuerKeyId: string;
  targetKeyId: string;
  userId: string;
  level: number;
  amount: number;
  regex?: string;
  createdAt: number;
  expiresAt?: number | null;
  revoked?: boolean;
}

export type SignatureVerifier = (signature: TrustSignature) => boolean;

export interface TrustRoot {
  keyId: string;
  maxDepth: number;
  level?: number;
  requiredAmount?: number;
  now?: number;
}

const TRUST_REGEX_SHAPE =
  /^<\[\^>\]\+\[@\.\][A-Za-z0-9](?:[A-Za-z0-9.-]*[A-Za-z0-9])?>\$$/;

export function compileTrustRegex(pattern: string): RegExp | null {
  if (!TRUST_REGEX_SHAPE.test(pattern)) return null;
  try {
    return new RegExp(pattern);
  } catch {
    return null;
  }
}

export function extractEmail(userId: string): string | null {
  const angle = /<([^<>\s]+@[^<>\s]+)>\s*$/.exec(userId);
  if (angle) return angle[1];
  if (/^[^\s<>]+@[^\s<>]+$/.test(userId)) return userId;
  return null;
}

function usableSignatures(
  signatures: readonly TrustSignature[],
  now: number,
  verify: SignatureVerifier,
): TrustSignature[] {
  const usable: TrustSignature[] = [];
  for (const sig of signatures) {
    if (sig.revoked) continue;
    if (sig.expiresAt != null && sig.expiresAt <= now) continue;
    if (!(sig.amount > 0)) continue;
    if (sig.regex !== undefined && compileTrustRegex(sig.regex) === null) {
      continue;
    }
    if (!verify(sig)) continue;
    usable.push(sig);
  }
  const latestByIssuerAndUserId = new Map<string, TrustSignature>();
  for (const sig of usable) {
    const key = `${sig.issuerKeyId} ${sig.userId}`;
    const prev = latestByIssuerAndUserId.get(key);
    if (!prev || sig.createdAt > prev.createdAt) {
      latestByIssuerAndUserId.set(key, sig);
    }
  }
  return [...latestByIssuerAndUserId.values()];
}

export function isUserIdValid(
  targetKeyId: string,
  userId: string,
  signatures: readonly TrustSignature[],
  root: TrustRoot,
  verify: SignatureVerifier,
): boolean {
  if (extractEmail(userId) === null) return false;
  const now = root.now ?? Date.now();
  const requiredAmount = root.requiredAmount ?? DEFAULT_REQUIRED_AMOUNT;
  const rootLevel = root.level ?? DEFAULT_ROOT_LEVEL;

  const edgesByIssuer = new Map<string, TrustSignature[]>();
  for (const edge of usableSignatures(signatures, now, verify)) {
    const list = edgesByIssuer.get(edge.issuerKeyId);
    if (list) list.push(edge);
    else edgesByIssuer.set(edge.issuerKeyId, [edge]);
  }

  const visited = new Set<string>([root.keyId]);

  const search = (
    keyId: string,
    previousLevel: number,
    depth: number,
    accumulated: number,
    constraints: readonly RegExp[],
  ): boolean => {
    if (depth >= root.maxDepth) return false;
    for (const edge of edgesByIssuer.get(keyId) ?? []) {
      if (edge.level >= previousLevel) continue;
      if (visited.has(edge.targetKeyId)) continue;
      if (!constraints.every((regex) => regex.test(edge.userId))) continue;
      const nextAccumulated =
        accumulated + Math.min(edge.amount, FULL_TRUST_AMOUNT);
      if (edge.targetKeyId === targetKeyId && edge.userId === userId) {
        if (nextAccumulated >= requiredAmount) return true;
        continue;
      }
      const nextConstraints =
        edge.regex !== undefined
          ? [...constraints, compileTrustRegex(edge.regex) as RegExp]
          : constraints;
      visited.add(edge.targetKeyId);
      const found = search(
        edge.targetKeyId,
        edge.level,
        depth + 1,
        nextAccumulated,
        nextConstraints,
      );
      visited.delete(edge.targetKeyId);
      if (found) return true;
    }
    return false;
  };

  return search(root.keyId, rootLevel, 0, 0, []);
}
