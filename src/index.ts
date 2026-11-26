export * from "./interval.ts";
export * from "./canonical.ts";
export * from "./bridge.ts";
export * from "./normalize.ts";
export * from "./exhaustive.ts";
export type * from "./types.ts";

import {
  analyzeAcceptance,
  authorizeDevice,
  auditBridges,
  canOpenMessage,
  certificateJson,
  countDistinctSources,
  decideNormalizedCoalition,
  decideNormalizedDevice,
  evaluateCoalition,
  evaluateId,
  evaluateShares,
  manifestJson,
  normalizedDeviceForId,
  partitionRanges,
  recomputeAcceptance,
  resultJson,
  verifyAcceptance,
  verifyCertificate,
} from "./analyze.ts";
import { exhaustiveCheck } from "./exhaustive.ts";

export {
  analyzeAcceptance,
  authorizeDevice,
  auditBridges,
  canOpenMessage,
  certificateJson,
  countDistinctSources,
  decideNormalizedCoalition,
  decideNormalizedDevice,
  evaluateCoalition,
  evaluateId,
  evaluateShares,
  manifestJson,
  normalizedDeviceForId,
  partitionRanges,
  recomputeAcceptance,
  resultJson,
  verifyAcceptance,
  verifyCertificate,
};

/**
 * Public spellings.  The canonical entry point is `analyzeAcceptance`; these
 * aliases make the package convenient for hosts that call it a "header" or a
 * "certificate" build step.
 */
export const buildHeaderAcceptance = analyzeAcceptance;
export const createHeaderAcceptance = analyzeAcceptance;
export const analyzeHeaderAcceptance = analyzeAcceptance;
export const buildAcceptanceHeader = analyzeAcceptance;
export const createAcceptanceHeader = analyzeAcceptance;
export const createAcceptance = analyzeAcceptance;
export const acceptHeader = analyzeAcceptance;
export const evaluateAcceptance = analyzeAcceptance;
export const evaluateHeader = analyzeAcceptance;
export const buildHeader = analyzeAcceptance;
export const makeHeaderAcceptance = analyzeAcceptance;

export const verifyHeaderAcceptance = verifyAcceptance;
export const verifyAcceptanceHeader = verifyAcceptance;
export const verifyHeader = verifyAcceptance;
export const checkAcceptance = verifyAcceptance;
export const recomputeHeaderAcceptance = recomputeAcceptance;
export const analyzeTenantMigration = analyzeAcceptance;
export const analyzeBroadcastHeader = analyzeAcceptance;
export const buildAcceptancePackage = analyzeAcceptance;
export const createHeader = analyzeAcceptance;
export const authorizeBroadcast = analyzeAcceptance;
export const verifyAcceptancePackage = verifyAcceptance;
export const verifyHeaderPackage = verifyAcceptance;
export const verifyAcceptanceResult = verifyAcceptance;
export const verifyHeaderCertificate = verifyCertificate;
export const verifyAcceptanceCertificate = verifyCertificate;

export { evaluateDomain, exhaustiveCheck, evaluateIdDecision } from "./exhaustive.ts";
export const exhaustiveVerify = exhaustiveCheck;
export const verifySmallDomain = exhaustiveCheck;
export const buildPartitions = (policy: Parameters<typeof analyzeAcceptance>[0]) =>
  analyzeAcceptance(policy).partitions;
export const buildCertificate = (policy: Parameters<typeof analyzeAcceptance>[0]) =>
  analyzeAcceptance(policy).certificate;
export const classifyDevices = (policy: Parameters<typeof analyzeAcceptance>[0]) =>
  analyzeAcceptance(policy).classes;
export const assessRisk = (policy: Parameters<typeof analyzeAcceptance>[0]) =>
  analyzeAcceptance(policy).risk;
export const authorize = authorizeDevice;
export const deviceDecision = authorizeDevice;
export const canOpen = canOpenMessage;
