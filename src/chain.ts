import type { DFA, SerializedDfa } from "./dfa.ts";
import {
  dfaAccepts,
  dfaDigest,
  dfaIntersection,
  dfaIsEmpty,
  serializeDfa,
} from "./dfa.ts";
import type {
  KeyringSnapshot,
  NormalizedKey,
  NormalizedRevocation,
  NormalizedSignature,
  NormalizedSnapshot,
  NormalizedUserId,
} from "./model.ts";
import { isNormalizedSnapshot, normalizeSnapshot, resolveKey } from "./model.ts";
import { normalizeEmail, uniqueSorted } from "./util.ts";
import { domainMatchesLanguage } from "./dfa.ts";

export interface RevocationEvidence {
  id: string;
  signatureId: string;
  issuedAt: number;
  effectiveAt: number;
  status: string;
}

export interface ChainEvidence {
  pathId: string;
  root: string;
  targetKey: string;
  edgeIds: string[];
  nodes: string[];
  weights: number[];
  weight: number;
  depthRemaining: number;
  validFrom: number;
  expiresAt: number;
  language: SerializedDfa;
  languageDigest: string;
  languagePatterns: string[];
  intermediateDomains: string[];
  revocationPredecessors: string[];
  revocations: RevocationEvidence[];
  requires: string[];
  targetUserIds: string[];
  targetEmails: string[];
  acceptedEmails: string[];
  valid: boolean;
  unknown: boolean;
  potentiallyValid: boolean;
  invalidReasons: string[];
  unknownReasons: string[];
}

export interface ChainAnalysis {
  evidence: ChainEvidence;
  languageDfa: DFA;
}

export interface RevocationState {
  status: "clear" | "revoked" | "unknown";
  revokedAt: number;
  nextDeadline: number;
  used: NormalizedRevocation[];
  reasons: string[];
}

export interface TargetResolution {
  ok: boolean;
  query: string;
  materialFingerprint: string | null;
  userIds: NormalizedUserId[];
  emails: string[];
  reason: string | null;
  ambiguous: boolean;
}

export interface PathEnumerationOptions {
  roots?: string[];
  targetKey: string;
  at: number;
  maxPaths?: number;
  includeInvalid?: boolean;
  includeUnknown?: boolean;
  targetUserIds?: string[];
  targetEmails?: string[];
}

export interface PathEnumerationResult {
  paths: ChainEvidence[];
  truncated: boolean;
}

export interface AllPathEnumerationOptions {
  roots?: string[];
  at: number;
  maxPaths?: number;
  includeInvalid?: boolean;
  includeUnknown?: boolean;
}

const DEFAULT_MAX_PATHS = 20000;

export function revocationState(
  snapshot: NormalizedSnapshot | KeyringSnapshot,
  signatureId: string,
  at: number,
): RevocationState {
  const ns = ensureNormalized(snapshot);
  const certificates = ns.revocationsBySignature.get(signatureId) ?? [];
  const known = certificates.filter((item) => item.issuedAt <= at);
  const reasons: string[] = [];
  const used: NormalizedRevocation[] = [];

  const superseded = new Set<string>();
  for (const item of known) {
    if (item.status === "withdrawn") {
      for (const target of item.supersedes) superseded.add(target);
    }
  }

  let unknown = false;
  for (const item of known) {
    if (item.invalidReasons.length > 0) {
      unknown = true;
      reasons.push(`invalid-revocation:${item.id}:${item.invalidReasons.join(",")}`);
    }
    if (item.status === "unresolved" || item.status === "conflict") {
      unknown = true;
      reasons.push(`unresolved-revocation:${item.id}`);
      used.push(item);
    }
  }

  const effective = known.filter(
    (item) => item.status === "effective" && !superseded.has(item.id) && item.invalidReasons.length === 0,
  );
  for (const item of effective) used.push(item);

  let revokedAt = Number.POSITIVE_INFINITY;
  for (const item of effective) revokedAt = Math.min(revokedAt, item.effectiveAt);

  if (unknown) {
    return {
      status: "unknown",
      revokedAt,
      nextDeadline: revokedAt,
      used,
      reasons,
    };
  }
  if (revokedAt <= at) {
    return {
      status: "revoked",
      revokedAt,
      nextDeadline: revokedAt,
      used,
      reasons: [`revoked-at:${new Date(revokedAt).toISOString()}`],
    };
  }
  return {
    status: "clear",
    revokedAt,
    nextDeadline: revokedAt,
    used,
    reasons: [],
  };
}

function computeIndependenceSets(
  ns: NormalizedSnapshot,
  path: NormalizedSignature[],
  nodes: string[],
): { intermediateDomains: string[]; revocationPredecessors: string[] } {
  const intermediateDomains: string[] = [];
  const revocationPredecessors: string[] = [];
  const root = nodes[0];
  const target = nodes[nodes.length - 1];

  for (let index = 0; index < path.length; index += 1) {
    const edge = path[index];
    if (index < path.length - 1 && edge.adminDomain) intermediateDomains.push(edge.adminDomain);
  }
  for (let index = 1; index < nodes.length - 1; index += 1) {
    const key = ns.keys.get(nodes[index]);
    if (key?.adminDomain) {
      intermediateDomains.push(key.adminDomain);
    } else if (key) {
      for (const userId of ns.userIds) {
        if (userId.key === key.canonical) intermediateDomains.push(userId.domain);
      }
    }
  }

  for (const edge of path) {
    revocationPredecessors.push(...edge.revocationPredecessors);
  }
  for (let index = 1; index < nodes.length - 1; index += 1) {
    const key = ns.keys.get(nodes[index]);
    if (key?.revocationPredecessor) revocationPredecessors.push(key.revocationPredecessor);
  }

  const normalizedPredecessors: string[] = [];
  for (const predecessor of revocationPredecessors) {
    if (predecessor === root || predecessor === target) continue;
    normalizedPredecessors.push(ns.aliasToKey.get(predecessor) ?? predecessor);
  }

  return {
    intermediateDomains: uniqueSorted(intermediateDomains.map((item) => item.trim()).filter(Boolean)),
    revocationPredecessors: uniqueSorted(
      normalizedPredecessors.filter((item) => item !== root && item !== target),
    ),
  };
}

export function analyzePath(
  snapshot: NormalizedSnapshot | KeyringSnapshot,
  path: NormalizedSignature[],
  at: number,
  target: { key: string; userIds: string[]; emails: string[] },
): ChainAnalysis {
  const ns = ensureNormalized(snapshot);
  const invalidReasons: string[] = [];
  const unknownReasons: string[] = [];
  const edgeIds: string[] = [];
  const nodes: string[] = [];
  const weights: number[] = [];
  const revocations: RevocationEvidence[] = [];
  const requires: string[] = [];
  const languagePatterns: string[] = [];

  if (path.length === 0) {
    invalidReasons.push("empty-path");
  }

  let validFrom = Number.NEGATIVE_INFINITY;
  let expiresAt = Number.POSITIVE_INFINITY;
  let weight = Number.POSITIVE_INFINITY;
  let depthRemaining = Number.POSITIVE_INFINITY;
  let language = compileUniversalLanguage();

  if (path.length > 0) nodes.push(path[0].from);
  const seenNodes = new Set<string>(nodes);

  for (let index = 0; index < path.length; index += 1) {
    const edge = path[index];
    edgeIds.push(edge.id);
    if (path.length > 0 && edge.from !== nodes[nodes.length - 1]) {
      invalidReasons.push(`broken-chain:${edge.id}`);
    }
    if (seenNodes.has(edge.to)) {
      invalidReasons.push(`cycle:${edge.to}`);
    }
    seenNodes.add(edge.to);
    nodes.push(edge.to);

    if (edge.invalidReasons.length > 0) invalidReasons.push(...edge.invalidReasons);
    unknownReasons.push(...edge.unknownReasons);
    if (!edge.language) {
      invalidReasons.push(`missing-language:${edge.id}`);
    } else {
      language = dfaIntersection(language, edge.language);
      languagePatterns.push(...edge.languagePatterns);
    }

    const revocation = revocationState(ns, edge.id, at);
    for (const item of revocation.used) {
      revocations.push({
        id: item.id,
        signatureId: item.signatureId,
        issuedAt: item.issuedAt,
        effectiveAt: item.effectiveAt,
        status: item.status,
      });
    }
    if (revocation.status === "unknown") {
      unknownReasons.push(...revocation.reasons);
    } else if (revocation.status === "revoked") {
      invalidReasons.push(...revocation.reasons);
    }

    if (at < edge.validFrom) invalidReasons.push(`not-yet-valid:${edge.id}`);
    if (at >= edge.notAfter) invalidReasons.push(`expired:${edge.id}`);
    validFrom = Math.max(validFrom, edge.validFrom);
    expiresAt = Math.min(expiresAt, edge.notAfter, revocation.nextDeadline);

    if (typeof edge.weight === "number" && Number.isFinite(edge.weight)) {
      weight = Math.min(weight, edge.weight);
      weights.push(edge.weight);
    } else {
      weight = 0;
      weights.push(0);
    }

    if (depthRemaining < 1) {
      invalidReasons.push(`depth-exhausted-before:${edge.id}`);
    }
    if (Number.isInteger(edge.depth) && edge.depth >= 0) {
      depthRemaining =
        depthRemaining === Number.POSITIVE_INFINITY
          ? edge.depth
          : Math.min(depthRemaining - 1, edge.depth);
    } else {
      depthRemaining = 0;
    }
    if (index < path.length - 1 && depthRemaining < 1) {
      invalidReasons.push(`depth-exhausted-after:${edge.id}`);
    }

    requires.push(...edge.requires);
    for (const required of edge.requires) {
      if (!ns.evidence.has(required) || ns.globalUnknownReasons.some((reason) => reason.endsWith(`:${required}`))) {
        unknownReasons.push(`missing-required-signature:${edge.id}:${required}`);
      }
    }
  }

  const uniqueRequires = uniqueSorted(requires);

  if (dfaIsEmpty(language)) invalidReasons.push("empty-language-intersection");
  if (path.length === 0) weight = 0;

  const independence = computeIndependenceSets(ns, path, nodes);
  const acceptedEmails: string[] = [];
  for (const email of target.emails) {
    const normalized = normalizeEmail(email);
    if (domainMatchesLanguage(language, normalized.domain)) acceptedEmails.push(normalized.email);
  }

  const invalid = [...new Set(invalidReasons)];
  const unknownOnly = [...new Set(unknownReasons)].filter(
    (reason) => !invalid.some((invalidReason) => invalidReason === reason),
  );
  const potentiallyValid = invalid.length === 0;
  const valid = potentiallyValid && unknownOnly.length === 0;
  const targetKey = target.key || nodes[nodes.length - 1] || "";
  const pathId = edgeIds.length > 0 ? edgeIds.join(">") : `${nodes[0] ?? ""}->${targetKey}`;

  return {
    languageDfa: language,
    evidence: {
      pathId,
      root: nodes[0] ?? "",
      targetKey,
      edgeIds,
      nodes,
      weights,
      weight: Number.isFinite(weight) ? weight : 0,
      depthRemaining: Number.isFinite(depthRemaining) ? Math.max(0, depthRemaining) : 0,
      validFrom,
      expiresAt,
      language: serializeDfa(language),
      languageDigest: dfaDigest(language),
      languagePatterns: uniqueSorted(languagePatterns),
      intermediateDomains: independence.intermediateDomains,
      revocationPredecessors: independence.revocationPredecessors,
      revocations: dedupeRevocations(revocations),
      requires: uniqueRequires,
      targetUserIds: [...target.userIds],
      targetEmails: [...target.emails],
      acceptedEmails,
      valid,
      unknown: potentiallyValid && unknownOnly.length > 0,
      potentiallyValid,
      invalidReasons: invalid,
      unknownReasons: unknownOnly,
    },
  };
}

function compileUniversalLanguage(): DFA {
  // One accepting state with every transition pointing back to itself.
  return {
    version: 1,
    alphabet: "ascii-0-127+other",
    start: 0,
    accepting: [true],
    transitions: [new Array<number>(129).fill(0)],
  };
}

function dedupeRevocations(items: RevocationEvidence[]): RevocationEvidence[] {
  const seen = new Set<string>();
  const result: RevocationEvidence[] = [];
  for (const item of items) {
    if (seen.has(item.id)) continue;
    seen.add(item.id);
    result.push(item);
  }
  return result.sort((a, b) => (a.id < b.id ? -1 : a.id > b.id ? 1 : 0));
}

function adjacency(ns: NormalizedSnapshot): Map<string, NormalizedSignature[]> {
  const result = new Map<string, NormalizedSignature[]>();
  for (const signature of ns.signatures) {
    if (!signature.from || !signature.to) continue;
    const list = result.get(signature.from) ?? [];
    list.push(signature);
    result.set(signature.from, list);
  }
  for (const list of result.values()) list.sort((a, b) => (a.id < b.id ? -1 : a.id > b.id ? 1 : 0));
  return result;
}

export function enumeratePaths(
  snapshot: NormalizedSnapshot | KeyringSnapshot,
  options: PathEnumerationOptions,
): PathEnumerationResult {
  const ns = ensureNormalized(snapshot);
  const roots = uniqueSorted(
    (options.roots && options.roots.length > 0 ? options.roots : ns.roots)
      .map((root) => resolveKey(ns, root) ?? root)
      .filter((root) => ns.keys.has(root)),
  );
  const targetKey = resolveKey(ns, options.targetKey) ?? options.targetKey;
  const at = options.at;
  const maxPaths = options.maxPaths ?? DEFAULT_MAX_PATHS;
  const graph = adjacency(ns);
  const paths: ChainEvidence[] = [];
  const seenPathIds = new Set<string>();
  let truncated = false;

  const targetInfo = {
    key: targetKey,
    userIds: options.targetUserIds ?? [],
    emails: options.targetEmails ?? [],
  };

  const addPath = (path: NormalizedSignature[]): void => {
    if (paths.length >= maxPaths) {
      truncated = true;
      return;
    }
    const analysis = analyzePath(ns, path, at, targetInfo);
    if (seenPathIds.has(analysis.evidence.pathId)) return;
    seenPathIds.add(analysis.evidence.pathId);
    paths.push(analysis.evidence);
  };

  const dfs = (current: string, path: NormalizedSignature[], visited: Set<string>): void => {
    if (truncated) return;
    for (const edge of graph.get(current) ?? []) {
      if (visited.has(edge.to)) continue;
      const nextPath = [...path, edge];
      if (edge.to === targetKey) {
        addPath(nextPath);
        if (truncated) return;
        continue;
      }
      visited.add(edge.to);
      dfs(edge.to, nextPath, visited);
      visited.delete(edge.to);
      if (truncated) return;
    }
  };

  for (const root of roots) {
    if (root === targetKey) continue;
    if (!ns.keys.has(root)) continue;
    const visited = new Set<string>([root]);
    dfs(root, [], visited);
    if (truncated) break;
  }

  paths.sort((a, b) => (a.pathId < b.pathId ? -1 : a.pathId > b.pathId ? 1 : 0));
  return { paths, truncated };
}

export function enumerateSimplePaths(
  snapshot: NormalizedSnapshot | KeyringSnapshot,
  options: PathEnumerationOptions,
): ChainEvidence[] {
  const result = enumeratePaths(snapshot, options);
  return result.paths.filter((path) => {
    if (options.includeInvalid === false && !path.valid) return false;
    if (options.includeUnknown === false && path.unknown) return false;
    return true;
  });
}

export function enumerateAllSimplePaths(
  snapshot: NormalizedSnapshot | KeyringSnapshot,
  options: AllPathEnumerationOptions,
): ChainEvidence[] {
  const ns = ensureNormalized(snapshot);
  const roots = uniqueSorted(
    (options.roots && options.roots.length > 0 ? options.roots : ns.roots)
      .map((root) => resolveKey(ns, root) ?? root)
      .filter((root) => ns.keys.has(root)),
  );
  const graph = adjacency(ns);
  const maxPaths = options.maxPaths ?? DEFAULT_MAX_PATHS;
  const paths: ChainEvidence[] = [];
  const seen = new Set<string>();
  let truncated = false;

  const addPath = (path: NormalizedSignature[], targetKey: string): void => {
    if (paths.length >= maxPaths) {
      truncated = true;
      return;
    }
    const targetUserIds = ns.userIds
      .filter((item) => item.key === targetKey && item.revokedAt > options.at)
      .map((item) => item.id);
    const targetEmails = uniqueSorted(
      ns.userIds
        .filter((item) => item.key === targetKey && item.revokedAt > options.at)
        .map((item) => item.email)
        .filter(Boolean),
    );
    const evidence = analyzePath(ns, path, options.at, {
      key: targetKey,
      userIds: targetUserIds,
      emails: targetEmails,
    }).evidence;
    if (seen.has(evidence.pathId)) return;
    seen.add(evidence.pathId);
    paths.push(evidence);
  };

  const dfs = (current: string, path: NormalizedSignature[], visited: Set<string>): void => {
    if (truncated) return;
    for (const edge of graph.get(current) ?? []) {
      if (visited.has(edge.to)) continue;
      const nextPath = [...path, edge];
      addPath(nextPath, edge.to);
      if (truncated) return;
      visited.add(edge.to);
      dfs(edge.to, nextPath, visited);
      visited.delete(edge.to);
      if (truncated) return;
    }
  };

  for (const root of roots) {
    if (!ns.keys.has(root)) continue;
    dfs(root, [], new Set<string>([root]));
    if (truncated) break;
  }

  paths.sort((left, right) => (left.pathId < right.pathId ? -1 : left.pathId > right.pathId ? 1 : 0));
  return paths.filter((path) => {
    if (options.includeInvalid === false && !path.valid) return false;
    if (options.includeUnknown === false && path.unknown) return false;
    return true;
  });
}

export function resolveTarget(
  snapshot: NormalizedSnapshot | KeyringSnapshot,
  query: string,
  at: number,
): TargetResolution {
  const ns = ensureNormalized(snapshot);
  const trimmed = query.trim();
  const exactUserIds = ns.userIds.filter((item) => item.id.trim() === trimmed);
  if (exactUserIds.length > 0) {
    const keys = uniqueSorted(exactUserIds.map((item) => item.key));
    if (keys.length > 1) {
      return {
        ok: false,
        query,
        materialFingerprint: null,
        userIds: exactUserIds,
        emails: [],
        reason: `ambiguous-user-id:${keys.join(",")}`,
        ambiguous: true,
      };
    }
    return resolutionFor(keys[0], exactUserIds, query);
  }

  const normalized = normalizeEmail(trimmed);
  const emailMatches = ns.userIds.filter((item) => item.emailKey === normalized.emailKey);
  if (emailMatches.length > 0) {
    const keys = uniqueSorted(emailMatches.map((item) => item.key));
    if (keys.length > 1) {
      return {
        ok: false,
        query,
        materialFingerprint: null,
        userIds: emailMatches,
        emails: [],
        reason: `ambiguous-email:${keys.join(",")}`,
        ambiguous: true,
      };
    }
    return resolutionFor(keys[0], emailMatches, query);
  }

  const key = ns.aliasToKey.get(trimmed) ?? (ns.keys.has(trimmed) ? trimmed : null);
  if (key) {
    if (!ns.keys.get(key)?.known) {
      return {
        ok: false,
        query,
        materialFingerprint: key,
        userIds: ns.userIds.filter((item) => item.key === key),
        emails: [],
        reason: `unknown-target-key:${key}`,
        ambiguous: false,
      };
    }
    return resolutionFor(key, ns.userIds.filter((item) => item.key === key), query);
  }

  return {
    ok: false,
    query,
    materialFingerprint: null,
    userIds: [],
    emails: [],
    reason: "target-not-found",
    ambiguous: false,
  };
}

function resolutionFor(
  key: string,
  userIds: NormalizedUserId[],
  query: string,
): TargetResolution {
  return {
    ok: true,
    query,
    materialFingerprint: key,
    userIds: uniqueById(userIds),
    emails: uniqueSorted(userIds.map((item) => item.email).filter(Boolean)),
    reason: null,
    ambiguous: false,
  };
}

function uniqueById(items: NormalizedUserId[]): NormalizedUserId[] {
  const seen = new Map<string, NormalizedUserId>();
  for (const item of items) {
    const key = `${item.key}\u0000${item.id}`;
    if (!seen.has(key)) seen.set(key, item);
  }
  return [...seen.values()].sort((a, b) => (a.id < b.id ? -1 : a.id > b.id ? 1 : 0));
}

export function pathAcceptsEmail(path: ChainEvidence, email: string): boolean {
  return dfaAccepts(path.language, normalizeEmail(email).domain);
}

export function keyAdminDomain(
  snapshot: NormalizedSnapshot | KeyringSnapshot,
  key: string,
): string | null {
  const ns = ensureNormalized(snapshot);
  const record: NormalizedKey | undefined = ns.keys.get(key);
  return record?.adminDomain ?? null;
}

function ensureNormalized(snapshot: NormalizedSnapshot | KeyringSnapshot): NormalizedSnapshot {
  return isNormalizedSnapshot(snapshot) ? snapshot : normalizeSnapshot(snapshot as KeyringSnapshot);
}
