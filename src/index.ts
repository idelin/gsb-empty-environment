export type {
  DomainLanguageSpec,
  DFA,
  SerializedDfa,
  RegexLanguageSpec,
  CompileLanguageOptions,
} from "./dfa.ts";
export {
  DFA_ALPHABET_ID,
  DFA_ALPHABET_SIZE,
  canonicalDfa,
  charIndex,
  compileDomainLanguage,
  compileDomainLanguage as compileLanguage,
  compileRegex,
  deserializeDfa,
  dfaAccepts,
  dfaComplement,
  dfaDifference,
  dfaDigest,
  dfaEquivalent,
  dfaIntersection,
  dfaIntersection as intersectLanguages,
  dfaIntersectionAll,
  dfaIsEmpty,
  dfaSample,
  dfaSubset,
  dfaUnion,
  dfaUnionAll,
  domainMatchesLanguage,
  domainSuffixLanguage,
  indexChar,
  minimizeDfa,
  sanitizeDfa,
  serializeDfa,
} from "./dfa.ts";

export type {
  Instant,
  NormalizedEmail,
} from "./util.ts";
export {
  MAX_TIME,
  MIN_TIME,
  domainOfEmail,
  finiteOrNull,
  isLikelyEmail,
  isValidDomain,
  normalizeDomain,
  normalizeEmail,
  stableDigest,
  stableStringify,
  toMillis,
} from "./util.ts";

export type {
  KeyRecord,
  KeyringSnapshot,
  NormalizedKey,
  NormalizedRevocation,
  NormalizedSignature,
  NormalizedSnapshot,
  NormalizedUserId,
  RevocationCertificate,
  RevocationStatus,
  TrustSignature,
  UserIdRecord,
} from "./model.ts";
export {
  isNormalizedSnapshot,
  keyFor,
  normalizeSnapshot,
  resolveKey,
  userIdsForEmail,
  userIdsForKey,
} from "./model.ts";

export type {
  AllPathEnumerationOptions,
  ChainAnalysis,
  ChainEvidence,
  PathEnumerationOptions,
  PathEnumerationResult,
  RevocationEvidence,
  RevocationState,
  TargetResolution,
} from "./chain.ts";
export {
  analyzePath,
  enumeratePaths,
  enumerateAllSimplePaths,
  enumerateSimplePaths,
  keyAdminDomain,
  pathAcceptsEmail,
  resolveTarget,
  revocationState,
} from "./chain.ts";

export type {
  CompatibleSet,
  CompatibleSetEnumeration,
  CompatibleSetOptions,
  IndependentSelection,
  PathConflict,
} from "./selection.ts";
export {
  conflictsFor,
  enumerateCompatibleEndorsementSets,
  maxIndependentEndorsements,
  pathConflictReasons,
  pathsConflict,
  sumPathWeights,
} from "./selection.ts";

export type {
  BatchVoteSummary,
  EvaluateOptions,
  EvaluationTarget,
  TrustEvaluation,
  TrustRequest,
} from "./evaluate.ts";
export {
  evaluateBatch,
  evaluateEmail,
  evaluateTrust,
  evaluateTrust as evaluate,
  evaluateBatch as evaluateAll,
  maxIndependentBatchWeight,
  maxIndependentBatchWeight as maxIndependentBatch,
  maxIndependentTotalWeight,
} from "./evaluate.ts";

export type {
  CertificateEdge,
  CertificateTarget,
  CertificateVerification,
  DomainCertificate,
  IndependenceCertificate,
  IndependenceVerification,
} from "./certificate.ts";
export {
  createDomainCertificate,
  createIndependenceCertificate,
  issueDomainCertificate,
  issueDomainCertificateFromNormalized,
  issueIndependenceCertificate,
  issueIndependenceCertificateFromNormalized,
  verifyDomainCertificate,
  verifyDomainCertificate as verifyCertificate,
  verifyDomainCertificate as verifyPathCertificate,
  verifyIndependenceCertificate,
  verifyIndependenceCertificate as verifyIndependence,
} from "./certificate.ts";

export type {
  SymbolicMatch,
  SymbolicQuery,
  SymbolicQueryResult,
  SymbolicSublanguage,
} from "./symbolic.ts";
export {
  symbolicQuery,
  symbolicQuery as symbolic,
  symbolicQuery as querySublanguages,
} from "./symbolic.ts";

import { maxIndependentEndorsements } from "./selection.ts";
import type { ChainEvidence } from "./chain.ts";

export function maximumIndependentWeight(paths: ChainEvidence[]): number {
  return maxIndependentEndorsements(paths).weight;
}

export const maxIndependentWeight = maximumIndependentWeight;
export const computeMaxIndependentWeight = maximumIndependentWeight;
export const selectIndependentEndorsements = maxIndependentEndorsements;
