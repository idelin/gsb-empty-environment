import type { Instant } from "./util.ts";
import { MAX_TIME, normalizeEmail, toMillis, uniqueSorted } from "./util.ts";
import type { ChainEvidence } from "./chain.ts";
import {
  enumeratePaths,
  keyAdminDomain,
  resolveTarget,
} from "./chain.ts";
import type { KeyringSnapshot, NormalizedSnapshot } from "./model.ts";
import { normalizeSnapshot, resolveKey } from "./model.ts";
import type { CompatibleSet, IndependentSelection, PathConflict } from "./selection.ts";
import {
  conflictsFor,
  enumerateCompatibleEndorsementSets,
  maxIndependentEndorsements,
} from "./selection.ts";
import type {
  DomainCertificate,
  IndependenceCertificate,
} from "./certificate.ts";
import {
  issueDomainCertificateFromNormalized,
  issueIndependenceCertificateFromNormalized,
} from "./certificate.ts";

export interface TrustRequest {
  target: string;
  at: Instant;
  threshold?: number;
  roots?: string[];
}

export interface EvaluateOptions {
  threshold?: number;
  roots?: string[];
  maxPaths?: number;
  strictUnknown?: boolean;
  includeCompatibleSets?: boolean;
  compatibleSetLimit?: number;
}

export interface EvaluationTarget {
  query: string;
  materialFingerprint: string | null;
  userIds: string[];
  emails: string[];
  ambiguous: boolean;
}

export interface TrustEvaluation {
  query: {
    target: string;
    at: number;
    threshold: number;
    roots: string[];
  };
  target: EvaluationTarget;
  status: "trusted" | "untrusted" | "unknown";
  threshold: number;
  maxWeight: number | null;
  lowerBoundWeight: number;
  upperBoundWeight: number | null;
  selected: ChainEvidence[];
  certificates: DomainCertificate[];
  independenceCertificate: IndependenceCertificate | null;
  candidates: ChainEvidence[];
  conflicts: PathConflict[];
  compatibleSets?: CompatibleSet[];
  unknownReasons: string[];
  reason: string;
  duplicateVoteKey: string | null;
  voteCountKey: string | null;
  duplicateVote: boolean;
  truncated: boolean;
}

export interface BatchVoteSummary {
  totalWeight: number | null;
  lowerBoundWeight: number;
  perKey: Array<{
    materialFingerprint: string;
    userIds: string[];
    emails: string[];
    weight: number;
    pathIds: string[];
    coveredEmails: string[];
    coversAllEmails: boolean;
  }>;
  selected: ChainEvidence[];
  unknown: boolean;
  unknownReasons: string[];
  truncated: boolean;
  overflow: boolean;
}

interface PreparedEvaluation {
  ns: NormalizedSnapshot;
  at: number;
  threshold: number;
  roots: string[];
  resolved: ReturnType<typeof resolveTarget>;
  requestedEmails: string[];
  requestedUserIds: string[];
  allPaths: ChainEvidence[];
  relevantPaths: ChainEvidence[];
  validCandidates: ChainEvidence[];
  selection: IndependentSelection;
  unknownReasons: string[];
  truncated: boolean;
  overflow: boolean;
  targetRevoked: boolean;
}

function prepareEvaluation(
  ns: NormalizedSnapshot,
  request: TrustRequest,
  options: EvaluateOptions,
): PreparedEvaluation {
  const at = toMillis(request.at, Date.now());
  const threshold = options.threshold ?? request.threshold ?? 1;
  const rootsResult = resolveRoots(ns, options.roots ?? request.roots);
  const resolved = resolveTarget(ns, request.target, at);
  const unknownReasons = [...ns.globalUnknownReasons, ...rootsResult.unknownReasons];
  const requestedEmails: string[] = [];
  const requestedUserIds: string[] = [];
  let targetRevoked = false;

  if (resolved.ok && resolved.materialFingerprint) {
    const key = ns.keys.get(resolved.materialFingerprint);
    if (key && !key.known) {
      unknownReasons.push(`unknown-target-key:${resolved.materialFingerprint}`);
    }
    if (key && key.revokedAt <= at) targetRevoked = true;

    const normalizedQuery = normalizeEmail(request.target);
    const matchingUserIds = resolved.userIds.filter(
      (item) => item.emailKey === normalizedQuery.emailKey,
    );
    const sourceUserIds = matchingUserIds.length > 0 ? matchingUserIds : resolved.userIds;
    for (const item of sourceUserIds) {
      if (item.revokedAt > at) {
        requestedUserIds.push(item.id);
        if (item.email) requestedEmails.push(item.email);
      }
    }
    if (sourceUserIds.length > 0 && requestedEmails.length === 0) targetRevoked = true;
  } else if (resolved.ambiguous || resolved.reason?.startsWith("unknown-target-key")) {
    unknownReasons.push(resolved.reason ?? "ambiguous-target");
  }

  if (!resolved.ok || !resolved.materialFingerprint || targetRevoked) {
    return {
      ns,
      at,
      threshold,
      roots: rootsResult.roots,
      resolved,
      requestedEmails: uniqueSorted(requestedEmails),
      requestedUserIds: uniqueSorted(requestedUserIds),
      allPaths: [],
      relevantPaths: [],
      validCandidates: [],
      selection: { paths: [], weight: 0, overflow: false },
      unknownReasons: uniqueSorted(unknownReasons),
      truncated: false,
      overflow: false,
      targetRevoked,
    };
  }

  const enumerated = enumeratePaths(ns, {
    roots: rootsResult.roots,
    targetKey: resolved.materialFingerprint,
    at,
    maxPaths: options.maxPaths,
    targetUserIds: resolved.userIds.map((item) => item.id),
    targetEmails: uniqueSorted(requestedEmails),
  });
  const allPaths = enumerated.paths;
  const emailSet = new Set(uniqueSorted(requestedEmails));
  const relevantPaths = allPaths.filter(
    (path) =>
      path.potentiallyValid &&
      (emailSet.size === 0 || path.acceptedEmails.some((email) => emailSet.has(email))),
  );
  const validCandidates = relevantPaths.filter((path) => path.valid);
  const relevantUnknown = relevantPaths.filter((path) => path.unknown);
  if (enumerated.truncated) {
    unknownReasons.push("path-enumeration-truncated");
  }
  for (const path of relevantUnknown) {
    for (const reason of path.unknownReasons) unknownReasons.push(`${path.pathId}:${reason}`);
  }
  const selection = maxIndependentEndorsements(validCandidates);
  if (selection.overflow) unknownReasons.push("independent-set-candidate-overflow");

  return {
    ns,
    at,
    threshold,
    roots: rootsResult.roots,
    resolved,
    requestedEmails: uniqueSorted(requestedEmails),
    requestedUserIds: uniqueSorted(requestedUserIds),
    allPaths,
    relevantPaths,
    validCandidates,
    selection,
    unknownReasons: uniqueSorted(unknownReasons),
    truncated: enumerated.truncated,
    overflow: selection.overflow,
    targetRevoked,
  };
}

function resolveRoots(
  ns: NormalizedSnapshot,
  explicit: string[] | undefined,
): { roots: string[]; unknownReasons: string[] } {
  const unknownReasons: string[] = [];
  if (!explicit || explicit.length === 0) return { roots: [...ns.roots], unknownReasons };
  const roots: string[] = [];
  for (const root of explicit) {
    const resolved = resolveKey(ns, root);
    if (!resolved) {
      unknownReasons.push(`unknown-root:${root}`);
      continue;
    }
    roots.push(resolved);
  }
  return { roots: uniqueSorted(roots), unknownReasons };
}

function finalizeEvaluation(prepared: PreparedEvaluation, options: EvaluateOptions): TrustEvaluation {
  const strict = options.strictUnknown ?? true;
  const unknownReasons = [...prepared.unknownReasons];
  if (!prepared.resolved.ok && !prepared.resolved.ambiguous) {
    // A missing target is a definite negative, not an offline-snapshot unknown.
    const index = unknownReasons.indexOf("target-not-found");
    if (index >= 0) unknownReasons.splice(index, 1);
  }
  const unknown = strict && unknownReasons.length > 0;
  const weight = prepared.selection.weight;
  const targetUnknown =
    !prepared.resolved.ok && Boolean(prepared.resolved.reason?.startsWith("unknown-target-key"));
  const status = prepared.targetRevoked || (!prepared.resolved.ok && !prepared.resolved.ambiguous && !targetUnknown)
    ? "untrusted"
    : targetUnknown || prepared.resolved.ambiguous || unknown
      ? "unknown"
      : weight >= prepared.threshold
        ? "trusted"
        : "untrusted";
  const reason =
    status === "unknown"
      ? `unknown: ${unknownReasons.join("; ")}`
      : status === "trusted"
        ? `max independent weight ${weight} >= threshold ${prepared.threshold}`
        : prepared.targetRevoked
          ? "target key or user ID is revoked at the requested time"
          : !prepared.resolved.ok
            ? prepared.resolved.reason ?? "target not found"
            : `max independent weight ${weight} < threshold ${prepared.threshold}`;

  const target: EvaluationTarget = {
    query: prepared.resolved.query,
    materialFingerprint: prepared.resolved.materialFingerprint,
    userIds: prepared.resolved.userIds.map((item) => item.id),
    emails: prepared.resolved.emails,
    ambiguous: prepared.resolved.ambiguous,
  };
  const certificateTarget = {
    query: prepared.resolved.query,
    keyFingerprint: prepared.resolved.materialFingerprint ?? "",
    userIds: prepared.requestedUserIds,
    emails: prepared.requestedEmails,
  };
  const certificates =
    prepared.resolved.materialFingerprint && prepared.selection.paths.length > 0
      ? prepared.selection.paths.map((path) =>
          issueDomainCertificateFromNormalized(
            prepared.ns,
            path,
            certificateTarget,
            prepared.at,
          ),
        )
      : [];
  const result: TrustEvaluation = {
    query: {
      target: prepared.resolved.query,
      at: prepared.at,
      threshold: prepared.threshold,
      roots: [...prepared.roots],
    },
    target,
    status,
    threshold: prepared.threshold,
    maxWeight: status === "unknown" ? null : weight,
    lowerBoundWeight: weight,
    upperBoundWeight: status === "unknown" ? null : weight,
    selected: prepared.selection.paths,
    certificates,
    independenceCertificate:
      certificates.length > 0
        ? issueIndependenceCertificateFromNormalized(
            prepared.ns,
            prepared.selection.paths,
            certificateTarget,
            prepared.at,
          )
        : null,
    candidates: [...prepared.allPaths].sort((left, right) => {
      if (left.weight !== right.weight) return right.weight - left.weight;
      return left.pathId < right.pathId ? -1 : 1;
    }),
    conflicts: conflictsFor(prepared.validCandidates),
    unknownReasons,
    reason,
    duplicateVoteKey: prepared.resolved.materialFingerprint,
    voteCountKey: prepared.resolved.materialFingerprint,
    duplicateVote: false,
    truncated: prepared.truncated,
  };
  if (options.includeCompatibleSets) {
    const enumeration = enumerateCompatibleEndorsementSets(prepared.validCandidates, {
      maximalOnly: true,
      limit: options.compatibleSetLimit,
    });
    result.compatibleSets = enumeration.sets;
    if (enumeration.truncated) result.unknownReasons.push("compatible-set-enumeration-truncated");
  }
  return result;
}

export function evaluateTrust(
  snapshot: KeyringSnapshot,
  request: TrustRequest,
  options: EvaluateOptions = {},
): TrustEvaluation {
  const ns = normalizeSnapshot(snapshot);
  return finalizeEvaluation(prepareEvaluation(ns, request, options), options);
}

export function evaluateBatch(
  snapshot: KeyringSnapshot,
  requests: TrustRequest[],
  options: EvaluateOptions = {},
): TrustEvaluation[] {
  const ns = normalizeSnapshot(snapshot);
  const seen = new Set<string>();
  return requests.map((request) => {
    const evaluation = finalizeEvaluation(prepareEvaluation(ns, request, options), options);
    const key = evaluation.voteCountKey;
    if (key && seen.has(key)) evaluation.duplicateVote = true;
    if (key) seen.add(key);
    return evaluation;
  });
}

export function maxIndependentBatchWeight(
  snapshot: KeyringSnapshot,
  requests: TrustRequest[],
  options: EvaluateOptions = {},
): BatchVoteSummary {
  const ns = normalizeSnapshot(snapshot);
  const unknownReasons = [...ns.globalUnknownReasons];
  const candidates = new Map<string, ChainEvidence>();
  const targetKeys = new Map<string, { userIds: Set<string>; emails: Set<string> }>();
  let truncated = false;

  for (const request of requests) {
    const prepared = prepareEvaluation(ns, request, options);
    if (prepared.truncated) truncated = true;
    if (prepared.targetRevoked) continue;
    if (!prepared.resolved.ok || !prepared.resolved.materialFingerprint) {
      if (
        prepared.resolved.ambiguous ||
        prepared.resolved.reason?.startsWith("unknown-target-key")
      ) {
        unknownReasons.push(prepared.resolved.reason ?? "ambiguous-target");
      }
      continue;
    }
    unknownReasons.push(...prepared.unknownReasons);
    const key = prepared.resolved.materialFingerprint;
    const target = targetKeys.get(key) ?? { userIds: new Set<string>(), emails: new Set<string>() };
    for (const userId of prepared.resolved.userIds) target.userIds.add(userId.id);
    for (const email of prepared.requestedEmails) target.emails.add(email);
    targetKeys.set(key, target);
    for (const path of prepared.validCandidates) {
      const existing = candidates.get(path.pathId);
      if (!existing) {
        candidates.set(path.pathId, {
          ...path,
          targetUserIds: [...path.targetUserIds],
          targetEmails: [...path.targetEmails],
          acceptedEmails: [...path.acceptedEmails],
        });
      } else {
        existing.targetUserIds = uniqueSorted([...existing.targetUserIds, ...path.targetUserIds]);
        existing.targetEmails = uniqueSorted([...existing.targetEmails, ...path.targetEmails]);
        existing.acceptedEmails = uniqueSorted([...existing.acceptedEmails, ...path.acceptedEmails]);
      }
    }
    for (const path of prepared.relevantPaths) {
      if (path.unknown) {
        for (const reason of path.unknownReasons) unknownReasons.push(`${path.pathId}:${reason}`);
      }
    }
  }

  const selection = maxIndependentEndorsements([...candidates.values()]);
  if (selection.overflow) unknownReasons.push("independent-set-candidate-overflow");
  const known = new Set(unknownReasons);
  const perKey = [...targetKeys.entries()].map(([materialFingerprint, target]) => {
    const selected = selection.paths.filter((path) => path.targetKey === materialFingerprint);
    const coveredEmails = uniqueSorted(selected.flatMap((path) => path.acceptedEmails));
    return {
      materialFingerprint,
      userIds: uniqueSorted(target.userIds),
      emails: uniqueSorted(target.emails),
      weight: selected.reduce((sum, path) => sum + path.weight, 0),
      pathIds: selected.map((path) => path.pathId).sort(),
      coveredEmails,
      coversAllEmails: [...target.emails].every((email) => coveredEmails.includes(email)),
    };
  });
  const unknown = known.size > 0;
  return {
    totalWeight: unknown || selection.overflow ? null : selection.weight,
    lowerBoundWeight: selection.weight,
    perKey,
    selected: selection.paths,
    unknown,
    unknownReasons: uniqueSorted(unknownReasons),
    truncated,
    overflow: selection.overflow,
  };
}

export function evaluateEmail(
  snapshot: KeyringSnapshot,
  email: string,
  at: Instant,
  threshold?: number,
  options: EvaluateOptions = {},
): TrustEvaluation {
  return evaluateTrust(snapshot, { target: email, at, threshold }, options);
}

export function maxIndependentTotalWeight(
  snapshot: KeyringSnapshot,
  requests: TrustRequest[],
  options: EvaluateOptions = {},
): number | null {
  return maxIndependentBatchWeight(snapshot, requests, options).totalWeight;
}

export { keyAdminDomain, MAX_TIME };
