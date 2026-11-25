import type { ChainEvidence } from "./chain.ts";
import {
  analyzePath,
  pathAcceptsEmail,
} from "./chain.ts";
import type { KeyringSnapshot, NormalizedSnapshot } from "./model.ts";
import { normalizeSnapshot, resolveKey } from "./model.ts";
import type { SerializedDfa } from "./dfa.ts";
import {
  dfaAccepts,
  dfaEquivalent,
  dfaSample,
  deserializeDfa,
  serializeDfa,
} from "./dfa.ts";
import { finiteOrNull, normalizeEmail, stableDigest, uniqueSorted } from "./util.ts";
import { pathConflictReasons } from "./selection.ts";

export interface CertificateTarget {
  query?: string;
  keyFingerprint: string;
  userIds: string[];
  emails: string[];
}

export interface CertificateEdge {
  id: string;
  from: string;
  to: string;
  issuedAt: number | null;
  notBefore: number | null;
  notAfter: number | null;
  depth: number;
  weight: number;
  domains: string[];
  adminDomain: string | null;
  revocationPredecessors: string[];
  requires: string[];
}

export interface DomainCertificate {
  kind: "gsb.domain-certificate/v1";
  snapshotId: string | null;
  at: number;
  target: {
    query: string;
    keyFingerprint: string;
    userIds: string[];
    emails: string[];
  };
  path: {
    edgeIds: string[];
    nodes: string[];
    signatures: CertificateEdge[];
  };
  weight: number;
  validFrom: number | null;
  expiresAt: number | null;
  depthRemaining: number;
  language: SerializedDfa;
  languageDigest: string;
  languagePatterns: string[];
  intermediateDomains: string[];
  revocationPredecessors: string[];
  revocations: Array<{
    id: string;
    signatureId: string;
    issuedAt: number;
    effectiveAt: number;
    status: string;
  }>;
  acceptance: {
    email: string;
    domain: string;
    accepted: boolean;
  };
  digest: string;
}

export interface IndependenceCertificate {
  kind: "gsb.independence-certificate/v1";
  at: number;
  targetKey: string;
  userIds: string[];
  emails: string[];
  certificates: DomainCertificate[];
  totalWeight: number;
  intermediateDomains: string[];
  revocationPredecessors: string[];
  pairwise: Array<{
    left: string;
    right: string;
    compatible: boolean;
    reasons: string[];
  }>;
  digest: string;
}

export interface CertificateVerification {
  ok: boolean;
  errors: string[];
  recomputed?: {
    chain: ChainEvidence;
    targetKey: string;
  };
}

export interface IndependenceVerification {
  ok: boolean;
  errors: string[];
}

function certificatePayload(certificate: DomainCertificate): Omit<DomainCertificate, "digest"> {
  const { digest: _digest, ...payload } = certificate;
  return payload;
}

function independencePayload(
  certificate: IndependenceCertificate,
): Omit<IndependenceCertificate, "digest"> {
  const { digest: _digest, ...payload } = certificate;
  return payload;
}

function chooseAcceptanceEmail(path: ChainEvidence, targetEmails: string[]): string {
  const accepted = path.acceptedEmails.find((email) => targetEmails.includes(email));
  if (accepted) return accepted;
  if (path.acceptedEmails.length > 0) return path.acceptedEmails[0];
  if (targetEmails.length > 0) return targetEmails[0];
  const sample = dfaSample(deserializeDfa(path.language)) || "example.com";
  return `alice@${sample}`;
}

function edgeFrom(ns: NormalizedSnapshot, id: string): CertificateEdge {
  const signature = ns.signaturesById.get(id);
  if (!signature) throw new Error(`certificate references unknown signature: ${id}`);
  return {
    id: signature.id,
    from: signature.from,
    to: signature.to,
    issuedAt: finiteOrNull(signature.issuedAt),
    notBefore: finiteOrNull(signature.notBefore),
    notAfter: finiteOrNull(signature.notAfter),
    depth: signature.depth,
    weight: signature.weight,
    domains: [...signature.languagePatterns],
    adminDomain: signature.adminDomain,
    revocationPredecessors: [...signature.revocationPredecessors],
    requires: [...signature.requires],
  };
}

export function issueDomainCertificateFromNormalized(
  ns: NormalizedSnapshot,
  path: ChainEvidence,
  target: CertificateTarget,
  at: number,
): DomainCertificate {
  const targetEmails = uniqueSorted(target.emails);
  const acceptanceEmail = chooseAcceptanceEmail(path, targetEmails);
  const certificate: DomainCertificate = {
    kind: "gsb.domain-certificate/v1",
    snapshotId: ns.raw.id ?? null,
    at,
    target: {
      query: target.query ?? target.keyFingerprint,
      keyFingerprint: target.keyFingerprint,
      userIds: uniqueSorted(target.userIds),
      emails: targetEmails,
    },
    path: {
      edgeIds: [...path.edgeIds],
      nodes: [...path.nodes],
      signatures: path.edgeIds.map((id) => edgeFrom(ns, id)),
    },
    weight: path.weight,
    validFrom: finiteOrNull(path.validFrom),
    expiresAt: finiteOrNull(path.expiresAt),
    depthRemaining: path.depthRemaining,
    language: serializeDfa(deserializeDfa(path.language)),
    languageDigest: path.languageDigest,
    languagePatterns: [...path.languagePatterns],
    intermediateDomains: [...path.intermediateDomains],
    revocationPredecessors: [...path.revocationPredecessors],
    revocations: path.revocations.map((item) => ({ ...item })),
    acceptance: {
      email: normalizeEmail(acceptanceEmail).email,
      domain: normalizeEmail(acceptanceEmail).domain,
      accepted: pathAcceptsEmail(path, acceptanceEmail),
    },
    digest: "",
  };
  certificate.digest = stableDigest(certificatePayload(certificate));
  return certificate;
}

export function issueDomainCertificate(
  snapshot: KeyringSnapshot,
  path: ChainEvidence,
  target?: CertificateTarget,
  at?: number,
): DomainCertificate {
  const ns = normalizeSnapshot(snapshot);
  const certificateAt = at ?? (Number.isFinite(path.expiresAt) ? path.expiresAt - 1 : path.validFrom);
  return issueDomainCertificateFromNormalized(
    ns,
    path,
    target ?? {
      keyFingerprint: path.targetKey,
      userIds: path.targetUserIds,
      emails: path.targetEmails,
    },
    certificateAt,
  );
}

export function issueIndependenceCertificateFromNormalized(
  ns: NormalizedSnapshot,
  paths: ChainEvidence[],
  target: CertificateTarget,
  at: number,
): IndependenceCertificate {
  const certificates = paths.map((path) => issueDomainCertificateFromNormalized(ns, path, target, at));
  const pairwise: IndependenceCertificate["pairwise"] = [];
  for (let left = 0; left < paths.length; left += 1) {
    for (let right = left + 1; right < paths.length; right += 1) {
      const reasons = pathConflictReasons(paths[left], paths[right]);
      pairwise.push({
        left: paths[left].pathId,
        right: paths[right].pathId,
        compatible: reasons.length === 0,
        reasons,
      });
    }
  }
  const certificate: IndependenceCertificate = {
    kind: "gsb.independence-certificate/v1",
    at,
    targetKey: target.keyFingerprint,
    userIds: uniqueSorted(target.userIds),
    emails: uniqueSorted(target.emails),
    certificates,
    totalWeight: paths.reduce((sum, path) => sum + path.weight, 0),
    intermediateDomains: uniqueSorted(paths.flatMap((path) => path.intermediateDomains)),
    revocationPredecessors: uniqueSorted(paths.flatMap((path) => path.revocationPredecessors)),
    pairwise,
    digest: "",
  };
  certificate.digest = stableDigest(independencePayload(certificate));
  return certificate;
}

export function issueIndependenceCertificate(
  snapshot: KeyringSnapshot,
  paths: ChainEvidence[],
  target?: CertificateTarget,
  at?: number,
): IndependenceCertificate {
  const ns = normalizeSnapshot(snapshot);
  const certificateAt =
    at ??
    (paths.length > 0 && Number.isFinite(paths[0].expiresAt)
      ? paths[0].expiresAt - 1
      : paths[0]?.validFrom ?? 0);
  return issueIndependenceCertificateFromNormalized(
    ns,
    paths,
    target ?? {
      keyFingerprint: paths[0]?.targetKey ?? "",
      userIds: uniqueSorted(paths.flatMap((path) => path.targetUserIds)),
      emails: uniqueSorted(paths.flatMap((path) => path.targetEmails)),
    },
    certificateAt,
  );
}

function compareNumberOrNull(actual: number | null, expected: number | null): boolean {
  if (actual === null || expected === null) return actual === expected;
  return actual === expected;
}

function compareStringSet(actual: string[], expected: string[]): boolean {
  return actual.length === expected.length && actual.every((item, index) => item === expected[index]);
}

export function verifyDomainCertificate(
  snapshot: KeyringSnapshot,
  certificate: DomainCertificate,
  options: { roots?: string[]; verifyAnchor?: boolean } = {},
): CertificateVerification {
  const errors: string[] = [];
  let ns: NormalizedSnapshot;
  try {
    ns = normalizeSnapshot(snapshot);
  } catch (error) {
    return { ok: false, errors: [`snapshot-invalid:${(error as Error).message}`] };
  }
  if (!certificate || certificate.kind !== "gsb.domain-certificate/v1") {
    return { ok: false, errors: ["wrong-certificate-kind"] };
  }
  if (
    !certificate.target ||
    !certificate.path ||
    !Array.isArray(certificate.path.edgeIds) ||
    !Array.isArray(certificate.path.nodes) ||
    !Array.isArray(certificate.path.signatures) ||
    certificate.path.signatures.some((item) => !item || typeof item !== "object") ||
    !Array.isArray(certificate.revocations) ||
    certificate.revocations.some((item) => !item || typeof item !== "object") ||
    !Array.isArray(certificate.languagePatterns) ||
    !Array.isArray(certificate.intermediateDomains) ||
    !Array.isArray(certificate.revocationPredecessors) ||
    !Array.isArray(certificate.target.userIds) ||
    !Array.isArray(certificate.target.emails) ||
    certificate.target.userIds.some((item) => typeof item !== "string") ||
    certificate.target.emails.some((item) => typeof item !== "string") ||
    !certificate.language ||
    !certificate.acceptance ||
    typeof certificate.acceptance.email !== "string" ||
    typeof certificate.acceptance.domain !== "string" ||
    typeof certificate.target.keyFingerprint !== "string"
  ) {
    return { ok: false, errors: ["malformed-certificate"] };
  }
  for (const reason of ns.globalUnknownReasons) {
    if (
      reason.startsWith("missing-") ||
      reason.startsWith("duplicate-") ||
      reason.startsWith("unknown-root:") ||
      reason.startsWith("alias-conflict:") ||
      reason.startsWith("revocation-conflict:")
    ) {
      errors.push(`snapshot-unknown:${reason}`);
    }
  }
  const digest = stableDigest(certificatePayload(certificate));
  if (digest !== certificate.digest) errors.push("certificate-digest-mismatch");
  if (!Number.isFinite(certificate.at)) errors.push("invalid-at");
  if (certificate.snapshotId !== null && ns.raw.id !== undefined && certificate.snapshotId !== ns.raw.id) {
    errors.push("snapshot-id-mismatch");
  }

  const targetKey = resolveKey(ns, certificate.target.keyFingerprint);
  if (!targetKey) errors.push(`unknown-target-key:${certificate.target.keyFingerprint}`);
  if (targetKey && targetKey !== certificate.target.keyFingerprint) {
    errors.push("target-key-is-not-canonical");
  }
  if (targetKey) {
    const keyRecord = ns.keys.get(targetKey);
    if (keyRecord && keyRecord.revokedAt <= certificate.at) {
      errors.push("target-key-revoked");
    }
    for (const userId of certificate.target.userIds) {
      const record = ns.userIds.find((item) => item.id === userId && item.key === targetKey);
      if (!record) errors.push(`target-user-id-not-bound:${userId}`);
      else if (record.revokedAt <= certificate.at) errors.push(`target-user-id-revoked:${userId}`);
    }
    for (const email of certificate.target.emails) {
      const emailKey = normalizeEmail(email).emailKey;
      const record = ns.userIds.find((item) => item.key === targetKey && item.emailKey === emailKey);
      if (!record) errors.push(`target-email-not-bound:${email}`);
      else if (record.revokedAt <= certificate.at) errors.push(`target-email-revoked:${email}`);
    }
  }

  const pathSigs = certificate.path.edgeIds.map((id) => ns.signaturesById.get(id));
  if (pathSigs.some((item) => item === undefined)) {
    errors.push("certificate-references-missing-signature");
    return { ok: false, errors };
  }
  const signatures = pathSigs.map((item) => item!);
  if (certificate.path.nodes.length !== signatures.length + 1) {
    errors.push("node-count-mismatch");
  }
  if (certificate.path.signatures.map((item) => item.id).join("\u0000") !== certificate.path.edgeIds.join("\u0000")) {
    errors.push("signature-list-order-mismatch");
  }
  for (let index = 0; index < signatures.length; index += 1) {
    if (signatures[index].from !== certificate.path.nodes[index]) errors.push(`node-from-mismatch:${index}`);
    if (signatures[index].to !== certificate.path.nodes[index + 1]) errors.push(`node-to-mismatch:${index}`);
    if (index > 0 && signatures[index - 1].to !== signatures[index].from) {
      errors.push(`broken-path:${index}`);
    }
  }
  if (new Set(certificate.path.nodes).size !== certificate.path.nodes.length) {
    errors.push("certificate-path-has-cycle");
  }

  const roots = uniqueSorted((options.roots ?? ns.roots).map((root) => resolveKey(ns, root) ?? root));
  if (
    roots.length > 0 &&
    certificate.path.nodes.length > 0 &&
    !roots.includes(certificate.path.nodes[0]) &&
    options.verifyAnchor !== false
  ) {
    errors.push(`path-not-rooted:${certificate.path.nodes[0]}`);
  }

  const analysis = analyzePath(ns, signatures, certificate.at, {
    key: targetKey ?? certificate.target.keyFingerprint,
    userIds: [...certificate.target.userIds],
    emails: [...certificate.target.emails],
  });
  if (!analysis.evidence.valid) {
    errors.push(`chain-invalid:${analysis.evidence.invalidReasons.join(",") || analysis.evidence.unknownReasons.join(",")}`);
  }
  if (analysis.evidence.weight !== certificate.weight) errors.push("weight-mismatch");
  if (analysis.evidence.depthRemaining !== certificate.depthRemaining) errors.push("depth-mismatch");
  if (!compareNumberOrNull(finiteOrNull(analysis.evidence.validFrom), certificate.validFrom)) {
    errors.push("valid-from-mismatch");
  }
  if (!compareNumberOrNull(finiteOrNull(analysis.evidence.expiresAt), certificate.expiresAt)) {
    errors.push("expires-at-mismatch");
  }
  if (analysis.evidence.languageDigest !== certificate.languageDigest) {
    errors.push("language-digest-mismatch");
  }
  if (!compareStringSet(analysis.evidence.languagePatterns, certificate.languagePatterns)) {
    errors.push("language-patterns-mismatch");
  }
  try {
    if (!dfaEquivalent(deserializeDfa(certificate.language), analysis.languageDfa)) {
      errors.push("language-not-equivalent");
    }
  } catch (error) {
    errors.push(`language-invalid:${(error as Error).message}`);
  }
  if (!compareStringSet(analysis.evidence.intermediateDomains, certificate.intermediateDomains)) {
    errors.push("intermediate-domain-mismatch");
  }
  if (!compareStringSet(analysis.evidence.revocationPredecessors, certificate.revocationPredecessors)) {
    errors.push("revocation-predecessor-mismatch");
  }
  const revocationIds = analysis.evidence.revocations.map((item) => item.id).sort();
  const certificateRevocationIds = certificate.revocations.map((item) => item.id).sort();
  if (!compareStringSet(revocationIds, certificateRevocationIds)) {
    errors.push("revocation-set-mismatch");
  }
  const revocationById = new Map(analysis.evidence.revocations.map((item) => [item.id, item]));
  for (const item of certificate.revocations) {
    const actual = revocationById.get(item.id);
    if (!actual) continue;
    if (actual.signatureId !== item.signatureId) errors.push(`revocation-target-mismatch:${item.id}`);
    if (actual.issuedAt !== item.issuedAt) errors.push(`revocation-issued-at-mismatch:${item.id}`);
    if (actual.effectiveAt !== item.effectiveAt) errors.push(`revocation-effective-at-mismatch:${item.id}`);
    if (actual.status !== item.status) errors.push(`revocation-status-mismatch:${item.id}`);
  }

  const acceptanceDomain = normalizeEmail(certificate.acceptance.email).domain;
  if (certificate.target.emails.length > 0) {
    const acceptanceKey = normalizeEmail(certificate.acceptance.email).emailKey;
    if (!certificate.target.emails.some((email) => normalizeEmail(email).emailKey === acceptanceKey)) {
      errors.push("acceptance-email-not-in-target");
    }
  }
  if (acceptanceDomain !== certificate.acceptance.domain) errors.push("acceptance-domain-mismatch");
  if (dfaAccepts(analysis.languageDfa, acceptanceDomain) !== certificate.acceptance.accepted) {
    errors.push("acceptance-decision-mismatch");
  }
  if (!dfaAccepts(analysis.languageDfa, acceptanceDomain)) {
    errors.push("acceptance-email-not-in-language");
  }

  for (const signature of certificate.path.signatures) {
    const actual = ns.signaturesById.get(signature.id);
    if (!actual) {
      errors.push(`missing-edge:${signature.id}`);
      continue;
    }
    if (
      !Array.isArray(signature.domains) ||
      !Array.isArray(signature.revocationPredecessors) ||
      !Array.isArray(signature.requires)
    ) {
      errors.push(`malformed-edge:${signature.id}`);
      continue;
    }
    if (actual.from !== signature.from) errors.push(`edge-from-mismatch:${signature.id}`);
    if (actual.to !== signature.to) errors.push(`edge-to-mismatch:${signature.id}`);
    if (actual.depth !== signature.depth) errors.push(`edge-depth-mismatch:${signature.id}`);
    if (actual.weight !== signature.weight) errors.push(`edge-weight-mismatch:${signature.id}`);
    if (!compareNumberOrNull(finiteOrNull(actual.issuedAt), signature.issuedAt)) {
      errors.push(`edge-issued-at-mismatch:${signature.id}`);
    }
    if (!compareNumberOrNull(finiteOrNull(actual.notBefore), signature.notBefore)) {
      errors.push(`edge-not-before-mismatch:${signature.id}`);
    }
    if (!compareNumberOrNull(finiteOrNull(actual.notAfter), signature.notAfter)) {
      errors.push(`edge-not-after-mismatch:${signature.id}`);
    }
    if ((actual.adminDomain ?? null) !== signature.adminDomain) {
      errors.push(`edge-admin-domain-mismatch:${signature.id}`);
    }
    if (!compareStringSet(actual.revocationPredecessors, signature.revocationPredecessors)) {
      errors.push(`edge-revoker-mismatch:${signature.id}`);
    }
    if (!compareStringSet(actual.requires, signature.requires)) {
      errors.push(`edge-requires-mismatch:${signature.id}`);
    }
    if (!compareStringSet(actual.languagePatterns, signature.domains)) {
      errors.push(`edge-domain-patterns-mismatch:${signature.id}`);
    }
  }

  return {
    ok: errors.length === 0,
    errors,
    recomputed: {
      chain: analysis.evidence,
      targetKey: targetKey ?? certificate.target.keyFingerprint,
    },
  };
}

export function verifyIndependenceCertificate(
  snapshot: KeyringSnapshot,
  certificate: IndependenceCertificate,
  options: { roots?: string[]; verifyAnchor?: boolean } = {},
): IndependenceVerification {
  const errors: string[] = [];
  if (!certificate || certificate.kind !== "gsb.independence-certificate/v1") {
    return { ok: false, errors: ["wrong-certificate-kind"] };
  }
  if (
    !Array.isArray(certificate.certificates) ||
    !Array.isArray(certificate.pairwise) ||
    !Array.isArray(certificate.intermediateDomains) ||
    !Array.isArray(certificate.revocationPredecessors) ||
    !Array.isArray(certificate.userIds) ||
    !Array.isArray(certificate.emails) ||
    certificate.userIds.some((item) => typeof item !== "string") ||
    certificate.emails.some((item) => typeof item !== "string") ||
    typeof certificate.totalWeight !== "number"
  ) {
    return { ok: false, errors: ["malformed-independence-certificate"] };
  }
  if (stableDigest(independencePayload(certificate)) !== certificate.digest) {
    errors.push("independence-certificate-digest-mismatch");
  }
  if (!Number.isFinite(certificate.at)) errors.push("invalid-independence-at");
  const independenceSnapshot = normalizeSnapshot(snapshot);
  const resolvedTarget =
    typeof certificate.targetKey === "string"
      ? resolveKey(independenceSnapshot, certificate.targetKey)
      : null;
  if (!resolvedTarget || resolvedTarget !== certificate.targetKey) {
    errors.push("independence-target-key-not-canonical");
  }
  if (resolvedTarget) {
    for (const userId of certificate.userIds) {
      const record = independenceSnapshot.userIds.find(
        (item) => item.id === userId && item.key === resolvedTarget,
      );
      if (!record) errors.push(`independence-user-id-not-bound:${userId}`);
      else if (record.revokedAt <= certificate.at) {
        errors.push(`independence-user-id-revoked:${userId}`);
      }
    }
    for (const email of certificate.emails) {
      const emailKey = normalizeEmail(email).emailKey;
      const record = independenceSnapshot.userIds.find(
        (item) => item.key === resolvedTarget && item.emailKey === emailKey,
      );
      if (!record) errors.push(`independence-email-not-bound:${email}`);
      else if (record.revokedAt <= certificate.at) {
        errors.push(`independence-email-revoked:${email}`);
      }
    }
  }
  const chains: ChainEvidence[] = [];
  for (const item of certificate.certificates) {
    if (!item || typeof item !== "object") {
      errors.push("malformed-domain-certificate");
      continue;
    }
    if (item.at !== certificate.at) errors.push(`certificate-at-mismatch:${item.digest}`);
    const verification = verifyDomainCertificate(snapshot, item, options);
    if (!verification.ok) {
      const label =
        (item as Partial<DomainCertificate>).target?.keyFingerprint ??
        (item as Partial<DomainCertificate>).digest ??
        "certificate";
      errors.push(...verification.errors.map((error) => `${label}:${error}`));
      continue;
    }
    if (verification.recomputed) chains.push(verification.recomputed.chain);
  }
  if (chains.length !== certificate.certificates.length) {
    errors.push("missing-recomputed-chain");
  }
  for (let left = 0; left < chains.length; left += 1) {
    for (let right = left + 1; right < chains.length; right += 1) {
      const reasons = pathConflictReasons(chains[left], chains[right]);
      if (reasons.length > 0) {
        errors.push(`not-independent:${chains[left].pathId}:${chains[right].pathId}:${reasons.join("|")}`);
      }
    }
  }
  const total = chains.reduce((sum, chain) => sum + chain.weight, 0);
  if (total !== certificate.totalWeight) errors.push("total-weight-mismatch");
  const domains = uniqueSorted(chains.flatMap((chain) => chain.intermediateDomains));
  const predecessors = uniqueSorted(chains.flatMap((chain) => chain.revocationPredecessors));
  if (!compareStringSet(domains, certificate.intermediateDomains)) errors.push("independence-domain-union-mismatch");
  if (!compareStringSet(predecessors, certificate.revocationPredecessors)) {
    errors.push("independence-revoker-union-mismatch");
  }
  for (const chain of chains) {
    if (chain.targetKey !== certificate.targetKey) errors.push(`target-key-mismatch:${chain.pathId}`);
  }
  const recomputedPairwise: IndependenceCertificate["pairwise"] = [];
  for (let left = 0; left < chains.length; left += 1) {
    for (let right = left + 1; right < chains.length; right += 1) {
      const reasons = pathConflictReasons(chains[left], chains[right]);
      recomputedPairwise.push({
        left: chains[left].pathId,
        right: chains[right].pathId,
        compatible: reasons.length === 0,
        reasons,
      });
    }
  }
  if (stableDigest(recomputedPairwise) !== stableDigest(certificate.pairwise)) {
    errors.push("pairwise-independence-summary-mismatch");
  }
  return { ok: errors.length === 0, errors };
}

export const createDomainCertificate = issueDomainCertificate;
export const createIndependenceCertificate = issueIndependenceCertificate;
