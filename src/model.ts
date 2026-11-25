import type { DFA, DomainLanguageSpec } from "./dfa.ts";
import { compileDomainLanguage } from "./dfa.ts";
import type { Instant } from "./util.ts";
import { MAX_TIME, MIN_TIME, normalizeEmail, toMillis, uniqueSorted } from "./util.ts";

export interface KeyRecord {
  materialFingerprint?: string;
  material?: string;
  fingerprint?: string;
  id?: string;
  aliases?: string[];
  userIds?: string[];
  adminDomain?: string;
  revocationPredecessor?: string;
  revokedAt?: Instant;
  isTrustAnchor?: boolean;
  trustAnchor?: boolean;
}

export interface UserIdRecord {
  id: string;
  email?: string;
  key?: string;
  keyFingerprint?: string;
  fingerprint?: string;
  revokedAt?: Instant;
}

export type RevocationStatus = "effective" | "withdrawn" | "unresolved" | "conflict";

export interface RevocationCertificate {
  id: string;
  signatureId?: string;
  signature?: string;
  targetSignature?: string;
  targetSignatureId?: string;
  target?: string;
  issuedAt?: Instant;
  issued?: Instant;
  effectiveAt?: Instant;
  effective?: Instant;
  revokedAt?: Instant;
  status?: RevocationStatus;
  supersedes?: string[];
  reason?: string;
}

export interface TrustSignature {
  id: string;
  from?: string;
  to?: string;
  sender?: string;
  receiver?: string;
  issuedAt?: Instant;
  issued?: Instant;
  notBefore?: Instant;
  notAfter?: Instant;
  validFrom?: Instant;
  validUntil?: Instant;
  depth?: number;
  delegationDepth?: number;
  maxDepth?: number;
  weight?: number;
  trustWeight?: number;
  domains?: DomainLanguageSpec;
  domain?: DomainLanguageSpec;
  language?: DomainLanguageSpec;
  domainRegex?: string;
  allowedDomains?: DomainLanguageSpec;
  domainLanguage?: DomainLanguageSpec;
  adminDomain?: string;
  intermediateDomain?: string;
  managementDomain?: string;
  revocationPredecessors?: string[];
  revocationPredecessor?: string;
  requires?: string[];
  requiredSignatures?: string[];
  requiredEvidence?: string[];
}

export interface KeyringSnapshot {
  id?: string;
  asOf?: Instant;
  keys: KeyRecord[];
  userIds: UserIdRecord[];
  signatures?: TrustSignature[];
  trustSignatures?: TrustSignature[];
  revocations?: RevocationCertificate[];
  revocationCertificates?: RevocationCertificate[];
  roots?: string[];
  trustAnchors?: string[];
  evidence?: string[];
  presentSignatures?: string[];
  requiredEvidence?: string[];
  missingSignatures?: string[];
  missingEvidence?: string[];
  revocationConflicts?: string[];
}

export interface NormalizedKey {
  canonical: string;
  aliases: string[];
  adminDomain: string | null;
  revocationPredecessor: string | null;
  revokedAt: number;
  trustAnchor: boolean;
  known: boolean;
  userIds: string[];
}

export interface NormalizedUserId {
  id: string;
  email: string;
  emailKey: string;
  domain: string;
  key: string;
  revokedAt: number;
}

export interface NormalizedSignature {
  raw: TrustSignature;
  id: string;
  from: string;
  to: string;
  issuedAt: number;
  notBefore: number;
  notAfter: number;
  validFrom: number;
  depth: number;
  weight: number;
  language: DFA | null;
  languagePatterns: string[];
  adminDomain: string | null;
  revocationPredecessors: string[];
  requires: string[];
  invalidReasons: string[];
  unknownReasons: string[];
}

export interface NormalizedRevocation {
  raw: RevocationCertificate;
  id: string;
  signatureId: string;
  issuedAt: number;
  effectiveAt: number;
  status: RevocationStatus;
  supersedes: string[];
  invalidReasons: string[];
}

export interface NormalizedSnapshot {
  raw: KeyringSnapshot;
  keys: Map<string, NormalizedKey>;
  aliasToKey: Map<string, string>;
  userIds: NormalizedUserId[];
  signatures: NormalizedSignature[];
  signaturesById: Map<string, NormalizedSignature>;
  revocations: NormalizedRevocation[];
  revocationsBySignature: Map<string, NormalizedRevocation[]>;
  roots: string[];
  evidence: Set<string>;
  globalUnknownReasons: string[];
  aliasConflicts: string[];
  asOf: number | null;
}

export function isNormalizedSnapshot(value: unknown): value is NormalizedSnapshot {
  if (!value || typeof value !== "object") return false;
  const record = value as Record<string, unknown>;
  return (
    record.keys instanceof Map &&
    record.aliasToKey instanceof Map &&
    Array.isArray(record.userIds) &&
    Array.isArray(record.signatures) &&
    record.signaturesById instanceof Map
  );
}

function getOrCreateKey(map: Map<string, NormalizedKey>, fingerprint: string): NormalizedKey {
  const existing = map.get(fingerprint);
  if (existing) return existing;
  const created: NormalizedKey = {
    canonical: fingerprint,
    aliases: [],
    adminDomain: null,
    revocationPredecessor: null,
    revokedAt: MAX_TIME,
    trustAnchor: false,
    known: false,
    userIds: [],
  };
  map.set(fingerprint, created);
  return created;
}

export function normalizeSnapshot(snapshot: KeyringSnapshot): NormalizedSnapshot {
  const keys = new Map<string, NormalizedKey>();
  const aliasToKey = new Map<string, string>();
  const globalUnknownReasons: string[] = [];
  const aliasConflicts: string[] = [];
  const evidence = new Set<string>();

  const bindAlias = (alias: string, canonical: string): void => {
    const trimmed = alias.trim();
    if (!trimmed) return;
    const existing = aliasToKey.get(trimmed);
    if (existing !== undefined && existing !== canonical) {
      const reason = `alias-conflict:${trimmed}:${existing}:${canonical}`;
      if (!aliasConflicts.includes(reason)) aliasConflicts.push(reason);
      return;
    }
    aliasToKey.set(trimmed, canonical);
  };

  for (const record of snapshot.keys ?? []) {
    const canonical = (
      record.materialFingerprint ??
      record.material ??
      record.fingerprint ??
      record.id ??
      ""
    ).trim();
    if (!canonical) {
      globalUnknownReasons.push("invalid-key-record:missing-fingerprint");
      continue;
    }
    const key = getOrCreateKey(keys, canonical);
    key.known = true;
    key.aliases = uniqueSorted(
      [...key.aliases, canonical, ...(record.aliases ?? [])]
        .map((alias) => alias.trim())
        .filter(Boolean),
    );
    key.adminDomain = record.adminDomain ?? key.adminDomain;
    key.revocationPredecessor = record.revocationPredecessor ?? key.revocationPredecessor;
    key.trustAnchor = key.trustAnchor || Boolean(record.isTrustAnchor || record.trustAnchor);
    if (record.revokedAt !== undefined) {
      const revokedAt = toMillis(record.revokedAt, MAX_TIME);
      key.revokedAt = Math.min(key.revokedAt, revokedAt);
    }
    for (const alias of [canonical, ...(record.aliases ?? [])]) bindAlias(alias, canonical);
  }

  const resolveRef = (ref: string | undefined, context: string): { key: string; known: boolean } => {
    const trimmed = (ref ?? "").trim();
    if (!trimmed) {
      globalUnknownReasons.push(`missing-key-reference:${context}`);
      return { key: "", known: false };
    }
    const existingAlias = aliasToKey.get(trimmed);
    if (existingAlias !== undefined) {
      return { key: existingAlias, known: keys.get(existingAlias)?.known ?? false };
    }
    const direct = keys.get(trimmed);
    if (direct) return { key: trimmed, known: direct.known };
    const placeholder = getOrCreateKey(keys, trimmed);
    bindAlias(trimmed, trimmed);
    return { key: placeholder.canonical, known: false };
  };

  const userIds: NormalizedUserId[] = [];
  for (const record of snapshot.userIds ?? []) {
    const ref = record.key ?? record.keyFingerprint ?? record.fingerprint;
    const resolved = resolveRef(ref, `user-id:${record.id}`);
    const email = normalizeEmail(record.email ?? record.id);
    if (!resolved.key) continue;
    const key = keys.get(resolved.key) as NormalizedKey;
    key.userIds = uniqueSorted([...key.userIds, record.id]);
    userIds.push({
      id: record.id,
      email: email.email,
      emailKey: email.emailKey,
      domain: email.domain,
      key: resolved.key,
      revokedAt: record.revokedAt === undefined ? MAX_TIME : toMillis(record.revokedAt, MAX_TIME),
    });
  }

  const signatures: NormalizedSignature[] = [];
  const signaturesById = new Map<string, NormalizedSignature>();
  const trustSignatures = [
    ...(snapshot.signatures ?? []),
    ...(snapshot.trustSignatures ?? []),
  ];
  for (const raw of trustSignatures) {
    const id = (raw.id ?? "").trim();
    const invalidReasons: string[] = [];
    const unknownReasons: string[] = [];
    if (!id) invalidReasons.push("missing-signature-id");
    if (id && signaturesById.has(id)) {
      invalidReasons.push(`duplicate-signature-id:${id}`);
      globalUnknownReasons.push(`duplicate-signature-id:${id}`);
    }
    const fromRef = raw.from ?? raw.sender;
    const toRef = raw.to ?? raw.receiver;
    const fromResolved = resolveRef(fromRef, `signature:${id}:from`);
    const toResolved = resolveRef(toRef, `signature:${id}:to`);
    if (!fromResolved.known) unknownReasons.push(`unknown-key:${fromResolved.key || fromRef || ""}`);
    if (!toResolved.known) unknownReasons.push(`unknown-key:${toResolved.key || toRef || ""}`);

    let issuedAt = MIN_TIME;
    let notBefore = MIN_TIME;
    let notAfter = MAX_TIME;
    try {
      issuedAt = toMillis(raw.issuedAt ?? raw.issued, MIN_TIME);
      notBefore = toMillis(raw.notBefore ?? raw.validFrom, issuedAt);
      notAfter = toMillis(raw.notAfter ?? raw.validUntil, MAX_TIME);
    } catch (error) {
      invalidReasons.push(`invalid-time:${(error as Error).message}`);
    }
    if (notAfter <= notBefore) invalidReasons.push("empty-validity-window");

    const depth = raw.depth ?? raw.delegationDepth ?? raw.maxDepth ?? Number.NaN;
    if (!Number.isInteger(depth) || depth < 0) invalidReasons.push(`invalid-depth:${String(depth)}`);
    const weight = raw.weight ?? raw.trustWeight ?? Number.NaN;
    if (typeof weight !== "number" || !Number.isFinite(weight) || weight < 0) {
      invalidReasons.push(`invalid-weight:${String(weight)}`);
    }

    const languageSpec =
      raw.domains ??
      raw.domain ??
      raw.language ??
      raw.domainRegex ??
      raw.allowedDomains ??
      raw.domainLanguage;
    let language: DFA | null = null;
    const languagePatterns: string[] = [];
    if (languageSpec === undefined) {
      invalidReasons.push("missing-domain-language");
    } else {
      try {
        language = compileDomainLanguage(languageSpec);
        if (typeof languageSpec === "string") languagePatterns.push(languageSpec);
        else if (languageSpec instanceof RegExp) languagePatterns.push(languageSpec.source);
        else if ("regex" in languageSpec && typeof languageSpec.regex === "string") {
          languagePatterns.push(languageSpec.regex);
        } else if ("pattern" in languageSpec && typeof languageSpec.pattern === "string") {
          languagePatterns.push(languageSpec.pattern);
        } else {
          languagePatterns.push("<dfa>");
        }
      } catch (error) {
        invalidReasons.push(`invalid-domain-language:${(error as Error).message}`);
      }
    }

    const adminDomainValue = raw.adminDomain ?? raw.intermediateDomain ?? raw.managementDomain ?? null;
    const adminDomain =
      typeof adminDomainValue === "string" && adminDomainValue.trim()
        ? adminDomainValue.trim()
        : null;
    const predecessors = [...(raw.revocationPredecessors ?? [])];
    if (raw.revocationPredecessor) predecessors.push(raw.revocationPredecessor);
    const requires = raw.requires ?? raw.requiredSignatures ?? raw.requiredEvidence ?? [];
    const normalized: NormalizedSignature = {
      raw,
      id,
      from: fromResolved.key,
      to: toResolved.key,
      issuedAt,
      notBefore,
      notAfter,
      validFrom: Math.max(issuedAt, notBefore),
      depth,
      weight,
      language,
      languagePatterns,
      adminDomain,
      revocationPredecessors: uniqueSorted(
        predecessors.map((item) => String(item).trim()).filter(Boolean),
      ),
      requires: uniqueSorted(requires.map((item) => String(item).trim()).filter(Boolean)),
      invalidReasons,
      unknownReasons,
    };
    signatures.push(normalized);
    if (id && !signaturesById.has(id)) {
      signaturesById.set(id, normalized);
      evidence.add(id);
    }
  }

  const revocations: NormalizedRevocation[] = [];
  const revocationsBySignature = new Map<string, NormalizedRevocation[]>();
  const revocationIds = new Set<string>();
  for (const raw of [
    ...(snapshot.revocations ?? []),
    ...(snapshot.revocationCertificates ?? []),
  ]) {
    const invalidReasons: string[] = [];
    const id = (raw.id ?? "").trim();
    const signatureId = (
      raw.signatureId ??
      raw.signature ??
      raw.targetSignature ??
      raw.targetSignatureId ??
      raw.target ??
      ""
    ).trim();
    if (!id) invalidReasons.push("missing-revocation-id");
    if (id && revocationIds.has(id)) {
      invalidReasons.push(`duplicate-revocation-id:${id}`);
      globalUnknownReasons.push(`duplicate-revocation-id:${id}`);
    }
    if (id) revocationIds.add(id);
    if (!signatureId) invalidReasons.push("missing-revocation-target");
    if ((raw.issuedAt ?? raw.issued) === undefined) {
      invalidReasons.push("missing-revocation-issued-at");
    }
    if ((raw.effectiveAt ?? raw.effective ?? raw.revokedAt) === undefined) {
      invalidReasons.push("missing-revocation-effective-at");
    }
    let issuedAt = MIN_TIME;
    let effectiveAt = MIN_TIME;
    try {
      issuedAt = toMillis(raw.issuedAt ?? raw.issued, MIN_TIME);
      effectiveAt = toMillis(raw.effectiveAt ?? raw.effective ?? raw.revokedAt, MIN_TIME);
    } catch (error) {
      invalidReasons.push(`invalid-revocation-time:${(error as Error).message}`);
    }
    const status: RevocationStatus = raw.status ?? "effective";
    if (!["effective", "withdrawn", "unresolved", "conflict"].includes(status)) {
      invalidReasons.push(`invalid-revocation-status:${String(status)}`);
    }
    const normalized: NormalizedRevocation = {
      raw,
      id,
      signatureId,
      issuedAt,
      effectiveAt,
      status,
      supersedes: uniqueSorted((raw.supersedes ?? []).map((item) => item.trim()).filter(Boolean)),
      invalidReasons,
    };
    revocations.push(normalized);
    if (signatureId) {
      const list = revocationsBySignature.get(signatureId) ?? [];
      list.push(normalized);
      revocationsBySignature.set(signatureId, list);
    }
  }

  for (const item of snapshot.missingSignatures ?? []) globalUnknownReasons.push(`missing-signature:${item}`);
  for (const item of snapshot.missingEvidence ?? []) globalUnknownReasons.push(`missing-evidence:${item}`);
  for (const item of snapshot.revocationConflicts ?? []) {
    globalUnknownReasons.push(`revocation-conflict:${item}`);
  }
  for (const reason of aliasConflicts) globalUnknownReasons.push(reason);
  for (const item of [...(snapshot.evidence ?? []), ...(snapshot.presentSignatures ?? [])]) {
    evidence.add(item);
  }
  for (const item of snapshot.requiredEvidence ?? []) {
    if (!evidence.has(item)) globalUnknownReasons.push(`missing-required-evidence:${item}`);
  }

  let roots: string[] = [];
  const explicitRoots = [...(snapshot.roots ?? []), ...(snapshot.trustAnchors ?? [])];
  if (explicitRoots.length > 0) {
    for (const root of explicitRoots) {
      const resolved = resolveRef(root, `root:${root}`);
      if (!resolved.known) globalUnknownReasons.push(`unknown-root:${root}`);
      if (resolved.key) roots.push(resolved.key);
    }
  } else {
    roots = [...keys.values()].filter((key) => key.trustAnchor && key.known).map((key) => key.canonical);
  }
  if (roots.length === 0) {
    const incoming = new Set<string>();
    for (const signature of signatures) {
      if (signature.from && signature.to && signature.from !== signature.to) incoming.add(signature.to);
    }
    roots = [...keys.values()]
      .filter((key) => key.known && !incoming.has(key.canonical))
      .map((key) => key.canonical);
  }
  roots = uniqueSorted(roots);

  return {
    raw: snapshot,
    keys,
    aliasToKey,
    userIds,
    signatures,
    signaturesById,
    revocations,
    revocationsBySignature,
    roots,
    evidence,
    globalUnknownReasons: uniqueSorted(globalUnknownReasons),
    aliasConflicts,
    asOf: snapshot.asOf === undefined ? null : toMillis(snapshot.asOf, MAX_TIME),
  };
}

export function resolveKey(ns: NormalizedSnapshot, reference: string): string | null {
  const trimmed = reference.trim();
  if (!trimmed) return null;
  const alias = ns.aliasToKey.get(trimmed);
  if (alias !== undefined) return alias;
  if (ns.keys.has(trimmed)) return trimmed;
  return null;
}

export function keyFor(ns: NormalizedSnapshot, reference: string): NormalizedKey | null {
  const canonical = resolveKey(ns, reference);
  return canonical ? (ns.keys.get(canonical) ?? null) : null;
}

export function userIdsForEmail(ns: NormalizedSnapshot, email: string): NormalizedUserId[] {
  const normalized = normalizeEmail(email);
  return ns.userIds.filter((record) => record.emailKey === normalized.emailKey);
}

export function userIdsForKey(ns: NormalizedSnapshot, key: string): NormalizedUserId[] {
  return ns.userIds.filter((record) => record.key === key);
}
