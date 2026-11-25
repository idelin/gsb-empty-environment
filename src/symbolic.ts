import type { DomainLanguageSpec, SerializedDfa } from "./dfa.ts";
import {
  dfaAccepts,
  compileDomainLanguage,
  dfaDigest,
  dfaIntersection,
  dfaIsEmpty,
  dfaSample,
  dfaSubset,
  dfaUnionAll,
  deserializeDfa,
  domainSuffixLanguage,
  serializeDfa,
} from "./dfa.ts";
import type { ChainEvidence } from "./chain.ts";
import { enumeratePaths, resolveTarget } from "./chain.ts";
import type { KeyringSnapshot, NormalizedSnapshot } from "./model.ts";
import { normalizeSnapshot, resolveKey } from "./model.ts";
import type { IndependentSelection } from "./selection.ts";
import { maxIndependentEndorsements } from "./selection.ts";
import type {
  DomainCertificate,
  IndependenceCertificate,
} from "./certificate.ts";
import {
  issueDomainCertificateFromNormalized,
  issueIndependenceCertificateFromNormalized,
} from "./certificate.ts";
import type { Instant } from "./util.ts";
import { normalizeEmail, toMillis, uniqueSorted } from "./util.ts";

export interface SymbolicQuery {
  suffix?: string;
  language?: DomainLanguageSpec;
  at: Instant;
  target?: string;
  threshold?: number;
  roots?: string[];
  maxPaths?: number;
  maxSamples?: number;
  qualifiedOnly?: boolean;
}

export interface SymbolicSublanguage {
  pathId: string;
  edgeIds: string[];
  weight: number;
  language: SerializedDfa;
  languageDigest: string;
  pathPatterns: string[];
  sampleDomains: string[];
  sampleEmails: string[];
  certificate: DomainCertificate;
}

export interface SymbolicMatch {
  target: {
    materialFingerprint: string;
    userIds: string[];
    emails: string[];
  };
  at: number;
  threshold: number;
  status: "trusted" | "untrusted" | "unknown";
  meetsThreshold: boolean;
  maxIndependentWeight: number | null;
  lowerBoundWeight: number;
  selectedPathIds: string[];
  sublanguages: SymbolicSublanguage[];
  acceptedLanguage: SerializedDfa;
  acceptedLanguageDigest: string;
  coversSuffix: boolean;
  sampleDomains: string[];
  sampleEmails: string[];
  independenceCertificate: IndependenceCertificate | null;
  unknownReasons: string[];
}

export interface SymbolicQueryResult {
  query: {
    suffix: string | null;
    at: number;
    threshold: number;
    roots: string[];
  };
  suffixLanguage: SerializedDfa;
  suffixLanguageDigest: string;
  matches: SymbolicMatch[];
  qualified: SymbolicMatch[];
  unknownReasons: string[];
}

function emptyLanguage(): SerializedDfa {
  return serializeDfa({
    version: 1,
    alphabet: "ascii-0-127+other",
    start: 0,
    accepting: [false],
    transitions: [new Array<number>(129).fill(-1)],
  });
}

function pathSuffixLanguage(path: ChainEvidence, suffix: ReturnType<typeof deserializeDfa>) {
  return dfaIntersection(deserializeDfa(path.language), suffix);
}

function sampleDomains(language: ReturnType<typeof deserializeDfa>, limit: number): string[] {
  if (dfaIsEmpty(language)) return [];
  const sample = dfaSample(language);
  if (sample === null) return [];
  return [sample].slice(0, Math.max(1, limit));
}

export function symbolicQuery(
  snapshot: KeyringSnapshot,
  query: SymbolicQuery,
): SymbolicQueryResult {
  const ns = normalizeSnapshot(snapshot);
  const at = toMillis(query.at, Date.now());
  const threshold = query.threshold ?? 1;
  const rootsResult = resolveRoots(ns, query.roots);
  const globalUnknown = [...ns.globalUnknownReasons, ...rootsResult.unknownReasons];
  const suffix = query.language !== undefined
    ? compileDomainLanguage(query.language)
    : domainSuffixLanguage(query.suffix ?? "");
  const suffixDfa = deserializeDfa(serializeDfa(suffix));
  const maxSamples = Math.max(1, query.maxSamples ?? 3);

  const targetKeys = new Set<string>();
  if (query.target) {
    const resolved = resolveTarget(ns, query.target, at);
    if (!resolved.ok || !resolved.materialFingerprint) {
      globalUnknown.push(resolved.reason ?? "target-not-found");
    } else {
      targetKeys.add(resolved.materialFingerprint);
    }
  } else {
    for (const userId of ns.userIds) {
      if (userId.revokedAt <= at) continue;
      if (dfaAcceptsLanguage(suffix, userId.domain)) targetKeys.add(userId.key);
    }
  }

  const matches: SymbolicMatch[] = [];
  for (const key of [...targetKeys].sort()) {
    const keyRecord = ns.keys.get(key);
    if (!keyRecord) continue;
    if (keyRecord.revokedAt <= at) continue;
    const activeUserIds = ns.userIds.filter(
      (item) => item.key === key && item.revokedAt > at && dfaAcceptsLanguage(suffix, item.domain),
    );
    if (activeUserIds.length === 0) continue;
    const requestedEmails = uniqueSorted(activeUserIds.map((item) => item.email).filter(Boolean));
    const enumerated = enumeratePaths(ns, {
      roots: rootsResult.roots,
      targetKey: key,
      at,
      maxPaths: query.maxPaths,
      targetUserIds: activeUserIds.map((item) => item.id),
      targetEmails: requestedEmails,
    });
    const relevant: ChainEvidence[] = [];
    const unknownReasons = [...globalUnknown];
    if (!keyRecord.known) unknownReasons.push(`unknown-target-key:${key}`);
    if (enumerated.truncated) unknownReasons.push("path-enumeration-truncated");
    const emailSet = new Set(requestedEmails);
    for (const path of enumerated.paths) {
      if (!path.potentiallyValid) continue;
      const pathSuffix = pathSuffixLanguage(path, suffixDfa);
      if (dfaIsEmpty(pathSuffix)) continue;
      if (emailSet.size > 0 && !path.acceptedEmails.some((email) => emailSet.has(email))) continue;
      relevant.push(path);
      if (path.unknown) {
        for (const reason of path.unknownReasons) unknownReasons.push(`${path.pathId}:${reason}`);
      }
    }
    const valid = relevant.filter((path) => path.valid);
    const selection: IndependentSelection = maxIndependentEndorsements(valid);
    if (selection.overflow) unknownReasons.push("independent-set-candidate-overflow");
    const selected = selection.paths;
    const sublanguages: SymbolicSublanguage[] = [];
    const certificateTarget = {
      query: query.target ?? key,
      keyFingerprint: key,
      userIds: activeUserIds.map((item) => item.id),
      emails: requestedEmails,
    };
    for (const path of selected) {
      const language = pathSuffixLanguage(path, suffixDfa);
      const domains = sampleDomains(language, maxSamples);
      const emails = path.acceptedEmails.filter((email) => emailSet.has(email));
      sublanguages.push({
        pathId: path.pathId,
        edgeIds: [...path.edgeIds],
        weight: path.weight,
        language: serializeDfa(language),
        languageDigest: dfaDigest(language),
        pathPatterns: [...path.languagePatterns],
        sampleDomains: domains,
        sampleEmails: emails.length > 0 ? emails : domains.map((domain) => `alice@${domain}`),
        certificate: issueDomainCertificateFromNormalized(
          ns,
          path,
          certificateTarget,
          at,
        ),
      });
    }
    const acceptedLanguage = sublanguages.length === 0
      ? deserializeDfa(emptyLanguage())
      : dfaUnionAll(sublanguages.map((item) => deserializeDfa(item.language)));
    const acceptedDomains = sampleDomains(acceptedLanguage, maxSamples);
    const certificates = sublanguages.map((item) => item.certificate);
    const unknown = unknownReasons.length > 0;
    const status: SymbolicMatch["status"] = unknown
      ? "unknown"
      : selection.weight >= threshold
        ? "trusted"
        : "untrusted";
    matches.push({
      target: {
        materialFingerprint: key,
        userIds: activeUserIds.map((item) => item.id),
        emails: requestedEmails,
      },
      at,
      threshold,
      status,
      meetsThreshold: status === "trusted",
      maxIndependentWeight: unknown ? null : selection.weight,
      lowerBoundWeight: selection.weight,
      selectedPathIds: selected.map((path) => path.pathId),
      sublanguages,
      acceptedLanguage: serializeDfa(acceptedLanguage),
      acceptedLanguageDigest: dfaDigest(acceptedLanguage),
      coversSuffix: !dfaIsEmpty(suffixDfa) && dfaSubset(suffixDfa, acceptedLanguage),
      sampleDomains: acceptedDomains,
      sampleEmails: acceptedDomains.map((domain) => `alice@${domain}`),
      independenceCertificate: selected.length > 0
        ? issueIndependenceCertificateFromNormalized(
            ns,
            selected,
            certificateTarget,
            at,
          )
        : null,
      unknownReasons: uniqueSorted(unknownReasons),
    });
  }

  matches.sort((left, right) =>
    left.target.materialFingerprint < right.target.materialFingerprint ? -1 : 1,
  );
  const filtered = query.qualifiedOnly ? matches.filter((item) => item.meetsThreshold) : matches;
  const matchUnknown = uniqueSorted(matches.flatMap((item) => item.unknownReasons));
  return {
    query: {
      suffix: query.language !== undefined ? null : (query.suffix ?? null),
      at,
      threshold,
      roots: rootsResult.roots,
    },
    suffixLanguage: serializeDfa(suffixDfa),
    suffixLanguageDigest: dfaDigest(suffixDfa),
    matches: filtered,
    qualified: filtered.filter((item) => item.meetsThreshold),
    unknownReasons: uniqueSorted([...globalUnknown, ...matchUnknown]),
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

function dfaAcceptsLanguage(language: ReturnType<typeof deserializeDfa>, domain: string): boolean {
  let normalized = normalizeEmail(domain).domain || domain.trim().toLowerCase();
  if (normalized.endsWith(".")) normalized = normalized.slice(0, -1);
  return dfaAccepts(language, normalized);
}
