import { createHash } from "node:crypto";
import {
  feasibleComponentAreas,
  normalizeRegion,
} from "./geometry.ts";
import type { Region, RegionLike } from "./geometry.ts";
import {
  cmp,
  rational,
  toRationalJson,
} from "./rational.ts";
import type { Rational, RationalInput, RationalJson } from "./rational.ts";

export interface DisclosurePolicy {
  readonly minComponentArea?: RationalInput;
  readonly minArea?: RationalInput;
  readonly minimumComponentArea?: RationalInput;
  readonly minimumArea?: RationalInput;
  readonly areaThreshold?: RationalInput;
  readonly areaFloor?: RationalInput;
  readonly authorizedObservers?: readonly string[];
  readonly collusionGroups?: readonly (CollusionGroupInput | readonly string[])[];
}

export type DisclosurePolicyLike = DisclosurePolicy | RationalInput;

export interface CollusionGroupInput {
  readonly id?: string;
  readonly groupId?: string;
  readonly observerIds?: readonly string[];
  readonly observers?: readonly string[];
}

export interface SignedRegionDeclaration {
  readonly claimId?: string;
  readonly id?: string;
  readonly declarationId?: string;
  readonly observerId?: string;
  readonly observer?: string;
  readonly audienceId?: string;
  readonly signerId?: string;
  readonly watcherId?: string;
  readonly principalId?: string;
  readonly ownerId?: string;
  readonly associationId?: string;
  readonly trajectoryId?: string;
  readonly subjectId?: string;
  readonly trajectory?: string;
  readonly subject?: string;
  readonly linkId?: string;
  readonly linkGroupId?: string;
  readonly authenticatedLinkId?: string;
  readonly authLinkId?: string;
  readonly cohortId?: string;
  readonly observationGroupId?: string;
  readonly groupId?: string;
  readonly group?: string;
  readonly trajectoryGroupId?: string;
  readonly possibleTrajectoryId?: string;
  readonly correlationId?: string;
  readonly batchId?: string;
  readonly anonymousBatchId?: string;
  readonly exclusiveBatchId?: string;
  readonly anonymizedBatchId?: string;
  readonly batchGroupId?: string;
  readonly region?: RegionLike;
  readonly polygons?: RegionLike;
  readonly syntheticRegion?: RegionLike;
  readonly possibleRegion?: RegionLike;
  readonly revision?: number | bigint | string;
  readonly signature?: unknown;
  readonly sig?: unknown;
  readonly signed?: boolean;
  readonly payload?: Record<string, unknown>;
  readonly declaration?: Record<string, unknown>;
  readonly claim?: Record<string, unknown>;
  readonly [key: string]: unknown;
}

export interface ClaimReference {
  readonly claimId: string;
  readonly revision: number;
  readonly declarationDigest: string;
}

export interface GroupAssessment {
  readonly groupId: string;
  readonly kind: "authenticated-link" | "observer";
  readonly authenticated: boolean;
  readonly observerIds: readonly string[];
  readonly batchIds: readonly string[];
  readonly claimRefs: readonly ClaimReference[];
  readonly publishable: boolean;
  readonly allowed: boolean;
  readonly reason: "ok" | "no-positive-area" | "below-minimum" | "unauthenticated-association";
  readonly minimumComponentArea: RationalJson;
  readonly minimumArea: RationalJson;
  readonly componentCount: number;
  readonly componentAreas: readonly RationalJson[];
}

export interface DisclosureCertificate {
  readonly version: "geo-disclosure-admission/v1";
  readonly arithmetic: "exact-rational";
  readonly policy: {
    readonly minComponentArea: RationalJson;
  };
  readonly publishable: boolean;
  readonly groupCount: number;
  readonly minimumComponentArea: RationalJson;
  readonly groups: readonly {
    readonly groupId: string;
    readonly kind: "authenticated-link" | "observer";
    readonly authenticated: boolean;
    readonly observerIds: readonly string[];
    readonly batchIds: readonly string[];
    readonly claimRefs: readonly ClaimReference[];
    readonly publishable: boolean;
    readonly reason: GroupAssessment["reason"];
    readonly minimumComponentArea: RationalJson;
    readonly componentCount: number;
    readonly componentAreas: readonly RationalJson[];
  }[];
  readonly certificateDigest: string;
}

export interface DisclosureDecision {
  readonly publishable: boolean;
  readonly allowed: boolean;
  readonly decision: "allow" | "deny";
  readonly minimumComponentArea: RationalJson;
  readonly minimumArea: RationalJson;
  readonly groups: readonly GroupAssessment[];
  readonly certificate: DisclosureCertificate;
}

export interface EvaluationGroupInput {
  readonly id?: string;
  readonly groupId?: string;
  readonly claimIds?: readonly string[];
  readonly claims?: readonly string[];
  readonly observations?: readonly string[];
  readonly observerIds?: readonly string[];
  readonly authenticated?: boolean;
  readonly associationId?: string;
}

export interface EvaluationOptions {
  readonly groups?: readonly (EvaluationGroupInput | readonly string[])[];
  readonly requireSignatures?: boolean;
  readonly linkSameObserver?: boolean;
  readonly verifySignature?: (declaration: SignedRegionDeclaration) => boolean;
}

export interface DisclosureHostOptions extends EvaluationOptions {
  readonly initialClaims?: readonly SignedRegionDeclaration[];
}

export type HistoryEventType =
  | "claim-registered"
  | "claim-corrected"
  | "claim-retracted"
  | "batch-published";

export interface HistoryEvent {
  readonly sequence: number;
  readonly type: HistoryEventType;
  readonly claimId?: string;
  readonly revision?: number;
  readonly previousRevision?: number;
  readonly declarationDigest?: string;
  readonly previousDeclarationDigest?: string;
  readonly certificateDigest?: string;
  readonly groupCount?: number;
  readonly previousEventHash: string;
  readonly eventHash: string;
}

export class DisclosureAdmissionError extends Error {
  readonly code: string;

  constructor(code: string, message: string) {
    super(message);
    this.name = "DisclosureAdmissionError";
    this.code = code;
  }
}

export class UnauthenticatedAssociationError extends DisclosureAdmissionError {
  constructor(message: string) {
    super("unauthenticated-association", message);
    this.name = "UnauthenticatedAssociationError";
  }
}

export class HistoryMutationError extends DisclosureAdmissionError {
  constructor(message: string) {
    super("history-immutable", message);
    this.name = "HistoryMutationError";
  }
}

interface NormalizedDeclaration {
  readonly claimId: string;
  readonly observerId: string;
  readonly associationId: string | null;
  readonly batchId: string | null;
  readonly region: Region;
  readonly revision: number;
  readonly raw: SignedRegionDeclaration;
  readonly signature: unknown;
  readonly declarationDigest: string;
  readonly regionDigest: string;
  readonly signed: boolean;
}

interface DynamicRecord {
  declaration: NormalizedDeclaration;
  active: boolean;
}

interface NormalizedPolicy {
  readonly minComponentArea: Rational;
  readonly authorizedObservers: readonly string[];
  readonly collusionGroups: readonly string[][];
}

function stableValue(value: unknown): unknown {
  if (value === null || value === undefined) {
    return value === undefined ? undefined : null;
  }
  if (typeof value === "bigint") {
    return value.toString();
  }
  if (Array.isArray(value)) {
    return value.map((entry) => stableValue(entry));
  }
  if (typeof value === "object") {
    const output: Record<string, unknown> = {};
    for (const key of Object.keys(value as Record<string, unknown>).sort()) {
      const entry = (value as Record<string, unknown>)[key];
      if (entry !== undefined) {
        output[key] = stableValue(entry);
      }
    }
    return output;
  }
  return value;
}

function stableStringify(value: unknown): string {
  return JSON.stringify(stableValue(value)) ?? "null";
}

function sha256(value: string): string {
  return createHash("sha256").update(value).digest("hex");
}

function asPolicyObject(policy: DisclosurePolicyLike): DisclosurePolicy {
  if (typeof policy === "object" && policy !== null && !Array.isArray(policy)) {
    const object = policy as Record<string, unknown>;
    const policyKeys = [
      "minComponentArea",
      "minArea",
      "minimumComponentArea",
      "minimumArea",
      "areaThreshold",
      "areaFloor",
      "authorizedObservers",
      "collusionGroups",
    ];
    if (policyKeys.some((key) => Object.prototype.hasOwnProperty.call(object, key))) {
      return policy as DisclosurePolicy;
    }
    const rationalKeys = ["numerator", "denominator", "n", "d", "num", "den"];
    if (rationalKeys.some((key) => Object.prototype.hasOwnProperty.call(object, key))) {
      return { minComponentArea: policy as RationalInput };
    }
    return policy as DisclosurePolicy;
  }
  return { minComponentArea: policy as RationalInput };
}

function normalizePolicy(input: DisclosurePolicyLike): NormalizedPolicy {
  const policy = asPolicyObject(input);
  const minimum =
    policy.minComponentArea ??
    policy.minArea ??
    policy.minimumComponentArea ??
    policy.minimumArea ??
    policy.areaThreshold ??
    policy.areaFloor;
  if (minimum === undefined) {
    throw new TypeError("Policy must define minComponentArea");
  }
  const minComponentArea = rational(minimum);
  if (minComponentArea.n < 0n) {
    throw new RangeError("Minimum component area must not be negative");
  }
  const collusionGroups = (policy.collusionGroups ?? []).map((group) => {
    const observers = Array.isArray(group)
      ? (group as readonly string[])
      : (group as CollusionGroupInput).observerIds ??
        (group as CollusionGroupInput).observers ??
        [];
    return [...new Set(observers)].sort();
  });
  return Object.freeze({
    minComponentArea,
    authorizedObservers: Object.freeze([...(policy.authorizedObservers ?? [])].sort()),
    collusionGroups: Object.freeze(collusionGroups.map((group) => Object.freeze(group))),
  });
}

function readString(value: unknown): string | null {
  return typeof value === "string" && value.length > 0 ? value : null;
}

function readRevision(value: unknown, fallback = 1): number {
  if (value === undefined) {
    return fallback;
  }
  if (typeof value === "number" && Number.isSafeInteger(value) && value > 0) {
    return value;
  }
  if (typeof value === "bigint" && value > 0n && value <= BigInt(Number.MAX_SAFE_INTEGER)) {
    return Number(value);
  }
  if (typeof value === "string" && /^\d+$/.test(value)) {
    const parsed = Number(value);
    if (Number.isSafeInteger(parsed) && parsed > 0) {
      return parsed;
    }
  }
  throw new TypeError("A declaration revision must be a positive safe integer");
}

function unwrapDeclaration(raw: SignedRegionDeclaration): SignedRegionDeclaration {
  const envelope =
    raw.payload ?? raw.declaration ?? raw.claim;
  if (envelope !== undefined && envelope !== null && typeof envelope === "object") {
    const payload = envelope as SignedRegionDeclaration;
    return Object.freeze({
      ...payload,
      claimId: payload.claimId ?? raw.claimId,
      declarationId: payload.declarationId ?? raw.declarationId,
      id: payload.id ?? raw.id,
      observerId: payload.observerId ?? raw.observerId,
      associationId: payload.associationId ?? raw.associationId,
      batchId: payload.batchId ?? raw.batchId,
      region: payload.region ?? raw.region,
      signature: raw.signature ?? raw.sig ?? payload.signature,
      sig: raw.sig,
      signed: raw.signed,
    });
  }
  return raw;
}

function declarationRegion(raw: SignedRegionDeclaration): Region {
  const region = raw.region ?? raw.polygons ?? raw.syntheticRegion ?? raw.possibleRegion;
  if (region === undefined) {
    throw new TypeError("A declaration must contain a synthetic region");
  }
  return normalizeRegion(region);
}

function regionCanonical(region: Region): unknown {
  return {
    polygons: region.polygons.map((polygon) => ({
      holes: polygon.holes.map((ring) =>
        ring.map((entry) => [entry.x.n.toString(), entry.x.d.toString(), entry.y.n.toString(), entry.y.d.toString()]),
      ),
      outer: polygon.outer.map((entry) => [
        entry.x.n.toString(),
        entry.x.d.toString(),
        entry.y.n.toString(),
        entry.y.d.toString(),
      ]),
    })),
  };
}

function normalizeDeclaration(
  input: SignedRegionDeclaration,
  fallbackId: string,
  options: EvaluationOptions,
): NormalizedDeclaration {
  const raw = unwrapDeclaration(input);
  const claimId =
    readString(raw.claimId) ??
    readString(raw.declarationId) ??
    readString(raw.id) ??
    fallbackId;
  const observerId =
    readString(raw.observerId) ??
    readString(raw.observer) ??
    readString(raw.audienceId) ??
    readString(raw.signerId) ??
    readString(raw.watcherId) ??
    readString(raw.principalId) ??
    readString(raw.ownerId) ??
    "anonymous";
  const associationId =
    readString(raw.associationId) ??
    readString(raw.trajectoryId) ??
    readString(raw.subjectId) ??
    readString(raw.trajectory) ??
    readString(raw.subject) ??
    readString(raw.linkId) ??
    readString(raw.linkGroupId) ??
    readString(raw.authenticatedLinkId) ??
    readString(raw.authLinkId) ??
    readString(raw.cohortId) ??
    readString(raw.observationGroupId) ??
    readString(raw.groupId) ??
    readString(raw.group) ??
    readString(raw.trajectoryGroupId) ??
    readString(raw.possibleTrajectoryId) ??
    readString(raw.correlationId);
  const batchId =
    readString(raw.batchId) ??
    readString(raw.anonymousBatchId) ??
    readString(raw.exclusiveBatchId) ??
    readString(raw.anonymizedBatchId) ??
    readString(raw.batchGroupId);
  const region = declarationRegion(raw);
  const revision = readRevision(raw.revision);
  const signature = raw.signature ?? raw.sig;
  const signed = signature !== undefined || raw.signed === true;
  if (options.requireSignatures === true && !signed) {
    throw new DisclosureAdmissionError("signature-required", `Claim ${claimId} is not signed`);
  }
  if (options.verifySignature !== undefined && !options.verifySignature(raw)) {
    throw new DisclosureAdmissionError("invalid-signature", `Claim ${claimId} failed signature verification`);
  }
  const regionDigest = sha256(stableStringify(regionCanonical(region)));
  const declarationDigest = sha256(
    stableStringify({
      associationId,
      batchId,
      claimId,
      observerId,
      region: regionCanonical(region),
      revision,
      signature: signature ?? null,
      signed,
    }),
  );
  return Object.freeze({
    claimId,
    observerId,
    associationId,
    batchId,
    region,
    revision,
    raw,
    signature,
    declarationDigest,
    regionDigest,
    signed,
  });
}

function groupKey(declaration: NormalizedDeclaration, linkSameObserver: boolean): string {
  if (declaration.associationId !== null) {
    return `link:${declaration.associationId}`;
  }
  if (
    linkSameObserver &&
    declaration.observerId !== "anonymous" &&
    declaration.batchId === null
  ) {
    return `observer:${declaration.observerId}`;
  }
  return `claim:${declaration.claimId}`;
}

function automaticGroups(
  declarations: readonly NormalizedDeclaration[],
  linkSameObserver: boolean,
): Map<string, NormalizedDeclaration[]> {
  const groups = new Map<string, NormalizedDeclaration[]>();
  for (const declaration of declarations) {
    const key = groupKey(declaration, linkSameObserver);
    const group = groups.get(key);
    if (group === undefined) {
      groups.set(key, [declaration]);
    } else {
      group.push(declaration);
    }
  }
  return groups;
}

function requestedGroups(
  declarations: readonly NormalizedDeclaration[],
  requested: readonly (EvaluationGroupInput | readonly string[])[],
): Map<string, NormalizedDeclaration[]> {
  const byId = new Map(declarations.map((entry) => [entry.claimId, entry]));
  const groups = new Map<string, NormalizedDeclaration[]>();
  const assigned = new Set<string>();
  for (let index = 0; index < requested.length; index += 1) {
    const rawSpec = requested[index]!;
    const spec: EvaluationGroupInput = Array.isArray(rawSpec)
      ? { claimIds: rawSpec as readonly string[] }
      : (rawSpec as EvaluationGroupInput);
    const id = readString(spec.id) ?? readString(spec.groupId) ?? `requested-group-${index + 1}`;
    if (groups.has(id)) {
      throw new TypeError(`Duplicate evaluation group id: ${id}`);
    }
    const members: NormalizedDeclaration[] = [];
    const requestedClaims = spec.claimIds ?? spec.claims ?? spec.observations ?? [];
    for (const claimId of requestedClaims) {
      const declaration = byId.get(claimId);
      if (declaration === undefined) {
        throw new TypeError(`Unknown claim in evaluation group: ${claimId}`);
      }
      if (assigned.has(claimId)) {
        throw new TypeError(`Claim belongs to more than one evaluation group: ${claimId}`);
      }
      assigned.add(claimId);
      members.push(declaration);
    }
    if (members.length === 0) {
      throw new TypeError(`Evaluation group ${id} has no declarations`);
    }
    if (spec.authenticated === false && members.length > 1) {
      throw new UnauthenticatedAssociationError(
        `Evaluation group ${id} cannot intersect hypotheses without authenticated association`,
      );
    }
    const associations = new Set(
      members.map((member) => member.associationId).filter((value): value is string => value !== null),
    );
    if (members.length > 1 && associations.size > 1) {
      throw new UnauthenticatedAssociationError(
        `Evaluation group ${id} mixes distinct hypothesis associations`,
      );
    }
    if (members.length > 1 && associations.size === 0 && spec.authenticated !== true) {
      throw new UnauthenticatedAssociationError(
        `Evaluation group ${id} has no authenticated association`,
      );
    }
    groups.set(id, members);
  }
  for (const declaration of declarations) {
    if (!assigned.has(declaration.claimId)) {
      const id = groupKey(declaration, true);
      let key = id;
      let suffix = 2;
      while (groups.has(key)) {
        key = `${id}#${suffix}`;
        suffix += 1;
      }
      groups.set(key, [declaration]);
    }
  }
  return groups;
}

function groupKind(members: readonly NormalizedDeclaration[]): "authenticated-link" | "observer" {
  return members.some((member) => member.associationId !== null)
    ? "authenticated-link"
    : "observer";
}

function referenceOf(declaration: NormalizedDeclaration): ClaimReference {
  return Object.freeze({
    claimId: declaration.claimId,
    revision: declaration.revision,
    declarationDigest: declaration.declarationDigest,
  });
}

function assessGroup(
  groupId: string,
  members: readonly NormalizedDeclaration[],
  policy: NormalizedPolicy,
): GroupAssessment {
  const areas = feasibleComponentAreas(members.map((member) => member.region));
  const minimum = areas.length === 0 ? rational(0n) : areas[0]!;
  const below = areas.some((area) => cmp(area, policy.minComponentArea) < 0);
  const kind = groupKind(members);
  const reason: GroupAssessment["reason"] =
    areas.length === 0 ? "no-positive-area" : below ? "below-minimum" : "ok";
  return Object.freeze({
    groupId,
    kind,
    authenticated: kind === "authenticated-link",
    observerIds: Object.freeze([...new Set(members.map((member) => member.observerId))].sort()),
    batchIds: Object.freeze(
      [...new Set(members.map((member) => member.batchId).filter((value): value is string => value !== null))].sort(),
    ),
    claimRefs: Object.freeze(members.map(referenceOf)),
    publishable: reason === "ok",
    allowed: reason === "ok",
    reason,
    minimumComponentArea: toRationalJson(minimum),
    minimumArea: toRationalJson(minimum),
    componentCount: areas.length,
    componentAreas: Object.freeze(areas.map(toRationalJson)),
  });
}

function overallMinimum(groups: readonly GroupAssessment[]): RationalJson {
  if (groups.length === 0) {
    return toRationalJson(0n);
  }
  let minimum = rational(groups[0]!.minimumComponentArea);
  for (const group of groups.slice(1)) {
    const value = rational(group.minimumComponentArea);
    if (cmp(value, minimum) < 0) {
      minimum = value;
    }
  }
  return toRationalJson(minimum);
}

function certificatePayload(
  policy: NormalizedPolicy,
  groups: readonly GroupAssessment[],
  publishable: boolean,
): Omit<DisclosureCertificate, "certificateDigest"> {
  let minimum = groups.length === 0 ? rational(0n) : rational(groups[0]!.minimumComponentArea);
  for (const group of groups.slice(1)) {
    const candidate = rational(group.minimumComponentArea);
    if (cmp(candidate, minimum) < 0) {
      minimum = candidate;
    }
  }
  return Object.freeze({
    version: "geo-disclosure-admission/v1",
    arithmetic: "exact-rational",
    policy: Object.freeze({ minComponentArea: toRationalJson(policy.minComponentArea) }),
    publishable,
    groupCount: groups.length,
    minimumComponentArea: toRationalJson(minimum),
    groups: Object.freeze(
      groups.map((group) =>
        Object.freeze({
          groupId: group.groupId,
          kind: group.kind,
          authenticated: group.authenticated,
          observerIds: group.observerIds,
          batchIds: group.batchIds,
          claimRefs: group.claimRefs,
          publishable: group.publishable,
          reason: group.reason,
          minimumComponentArea: group.minimumComponentArea,
          componentCount: group.componentCount,
          componentAreas: group.componentAreas,
        }),
      ),
    ),
  });
}

function withDigest(
  policy: NormalizedPolicy,
  groups: readonly GroupAssessment[],
  publishable: boolean,
): DisclosureCertificate {
  const payload = certificatePayload(policy, groups, publishable);
  return Object.freeze({
    ...payload,
    certificateDigest: sha256(stableStringify(payload)),
  });
}

function evaluateNormalized(
  declarations: readonly NormalizedDeclaration[],
  policy: NormalizedPolicy,
  options: EvaluationOptions,
): DisclosureDecision {
  const grouped =
    options.groups !== undefined
      ? requestedGroups(declarations, options.groups)
      : automaticGroups(declarations, options.linkSameObserver !== false);
  const groups = [...grouped.entries()]
    .sort(([left], [right]) => left.localeCompare(right))
    .map(([id, members]) => assessGroup(id, members, policy));
  const publishable = groups.length > 0 && groups.every((group) => group.publishable);
  const minimum = overallMinimum(groups);
  const certificate = withDigest(policy, groups, publishable);
  return Object.freeze({
    publishable,
    allowed: publishable,
    decision: publishable ? "allow" : "deny",
    minimumComponentArea: minimum,
    minimumArea: minimum,
    groups: Object.freeze(groups),
    certificate,
  });
}

export function evaluateDisclosure(
  declarations: readonly SignedRegionDeclaration[],
  policy: DisclosurePolicyLike,
  options: EvaluationOptions = {},
): DisclosureDecision {
  const normalizedPolicy = normalizePolicy(policy);
  const normalized = declarations.map((declaration, index) =>
    normalizeDeclaration(declaration, `claim-${index + 1}`, options),
  );
  const seen = new Set<string>();
  for (const declaration of normalized) {
    if (seen.has(declaration.claimId)) {
      throw new TypeError(`Duplicate claim id: ${declaration.claimId}`);
    }
    seen.add(declaration.claimId);
  }
  return evaluateNormalized(normalized, normalizedPolicy, options);
}

export const assessDisclosure = evaluateDisclosure;
export const checkDisclosure = evaluateDisclosure;
export const canPublishDisclosure = evaluateDisclosure;
export const assessPublication = evaluateDisclosure;
export const admitDisclosure = evaluateDisclosure;
export const evaluateBatch = evaluateDisclosure;

export function certificateForDisclosure(
  declarations: readonly SignedRegionDeclaration[],
  policy: DisclosurePolicyLike,
  options: EvaluationOptions = {},
): DisclosureCertificate {
  return evaluateDisclosure(declarations, policy, options).certificate;
}

export const buildDisclosureCertificate = certificateForDisclosure;

export function isPublishable(
  declarations: readonly SignedRegionDeclaration[],
  policy: DisclosurePolicyLike,
  options: EvaluationOptions = {},
): boolean {
  return evaluateDisclosure(declarations, policy, options).publishable;
}

export const canPublish = isPublishable;

export interface CertificateVerification {
  readonly valid: boolean;
  readonly errors: readonly string[];
}

export function verifyCertificate(
  certificate: DisclosureCertificate,
  declarations: readonly SignedRegionDeclaration[],
  policy: DisclosurePolicyLike,
  options: EvaluationOptions = {},
): CertificateVerification {
  const errors: string[] = [];
  if (certificate.version !== "geo-disclosure-admission/v1") {
    errors.push("Unsupported certificate version");
  }
  if (certificate.arithmetic !== "exact-rational") {
    errors.push("Certificate does not declare exact rational arithmetic");
  }
  let normalizedPolicy: NormalizedPolicy;
  try {
    normalizedPolicy = normalizePolicy(policy);
  } catch (error) {
    return Object.freeze({
      valid: false,
      errors: Object.freeze([error instanceof Error ? error.message : String(error)]),
    });
  }
  if (
    stableStringify(certificate.policy.minComponentArea) !==
    stableStringify(toRationalJson(normalizedPolicy.minComponentArea))
  ) {
    errors.push("Certificate policy minimum does not match the supplied policy");
  }

  const normalized = declarations.map((declaration, index) =>
    normalizeDeclaration(declaration, `claim-${index + 1}`, options),
  );
  const byId = new Map(normalized.map((entry) => [entry.claimId, entry]));
  const used = new Set<string>();
  for (const group of certificate.groups) {
    for (const reference of group.claimRefs) {
      const declaration = byId.get(reference.claimId);
      if (declaration === undefined) {
        errors.push(`Missing declaration ${reference.claimId}`);
        continue;
      }
      if (used.has(reference.claimId)) {
        errors.push(`Declaration ${reference.claimId} appears in multiple certificate groups`);
      }
      used.add(reference.claimId);
      if (declaration.revision !== reference.revision) {
        errors.push(`Revision mismatch for ${reference.claimId}`);
      }
      if (declaration.declarationDigest !== reference.declarationDigest) {
        errors.push(`Declaration digest mismatch for ${reference.claimId}`);
      }
    }
  }
  if (used.size !== normalized.length) {
    errors.push("Certificate omits one or more declarations");
  }

  try {
    const recomputed = evaluateNormalized(normalized, normalizedPolicy, options);
    if (recomputed.certificate.certificateDigest !== certificate.certificateDigest) {
      errors.push("Certificate digest does not match the exact recomputation");
    }
    if (recomputed.publishable !== certificate.publishable) {
      errors.push("Publishability does not match the exact recomputation");
    }
    if (recomputed.groups.length !== certificate.groups.length) {
      errors.push("Group count does not match the exact recomputation");
    }
    for (let index = 0; index < Math.min(recomputed.groups.length, certificate.groups.length); index += 1) {
      const expected = recomputed.certificate.groups[index]!;
      const actual = certificate.groups[index]!;
      if (stableStringify(expected) !== stableStringify(actual)) {
        errors.push(`Group ${actual.groupId} does not match the exact recomputation`);
      }
    }
  } catch (error) {
    errors.push(error instanceof Error ? error.message : String(error));
  }

  return Object.freeze({
    valid: errors.length === 0,
    errors: Object.freeze(errors),
  });
}

export const verifyDisclosureCertificate = verifyCertificate;

export function isCertificateValid(
  certificate: DisclosureCertificate,
  declarations: readonly SignedRegionDeclaration[],
  policy: DisclosurePolicyLike,
  options: EvaluationOptions = {},
): boolean {
  return verifyCertificate(certificate, declarations, policy, options).valid;
}

export function createDisclosureHost(
  policy: DisclosurePolicyLike,
  options: DisclosureHostOptions = {},
): DisclosureAdmissionHost {
  return new DisclosureAdmissionHost(policy, options);
}

export function createAdmission(
  policy: DisclosurePolicyLike,
  options: DisclosureHostOptions = {},
): DisclosureAdmissionHost {
  return createDisclosureHost(policy, options);
}

export function createGeoDisclosureAdmission(
  policy: DisclosurePolicyLike,
  options: DisclosureHostOptions = {},
): DisclosureAdmissionHost {
  return createDisclosureHost(policy, options);
}

export const createAdmissionController = createGeoDisclosureAdmission;
export const createPublishHost = createGeoDisclosureAdmission;

export class DisclosureAdmissionHost {
  readonly #policy: NormalizedPolicy;
  readonly #options: DisclosureHostOptions;
  readonly #records = new Map<string, DynamicRecord>();
  readonly #history: HistoryEvent[] = [];
  #eventHash = "0".repeat(64);
  #lastPublication: DisclosureDecision | null = null;

  constructor(policy: DisclosurePolicyLike, options: DisclosureHostOptions = {}) {
    this.#policy = normalizePolicy(policy);
    this.#options = Object.freeze({ ...options });
    for (const declaration of options.initialClaims ?? []) {
      this.registerClaim(declaration);
    }
  }

  get policy(): DisclosurePolicy {
    return Object.freeze({
      minComponentArea: toRationalJson(this.#policy.minComponentArea),
      authorizedObservers: this.#policy.authorizedObservers,
      collusionGroups: this.#policy.collusionGroups.map((observerIds) => ({ observerIds })),
    });
  }

  get history(): readonly HistoryEvent[] {
    return this.getHistory();
  }

  get lastPublication(): DisclosureDecision | null {
    return this.#lastPublication;
  }

  registerClaim(declaration: SignedRegionDeclaration): ClaimReference {
    const normalized = normalizeDeclaration(
      declaration,
      `claim-${this.#records.size + 1}`,
      this.#options,
    );
    if (this.#records.has(normalized.claimId)) {
      throw new DisclosureAdmissionError(
        "duplicate-claim",
        `Claim ${normalized.claimId} has already been registered`,
      );
    }
    this.#records.set(normalized.claimId, { declaration: normalized, active: true });
    const event = this.#appendEvent({
      type: "claim-registered",
      claimId: normalized.claimId,
      revision: normalized.revision,
      declarationDigest: normalized.declarationDigest,
    });
    void event;
    return referenceOf(normalized);
  }

  addClaim(declaration: SignedRegionDeclaration): ClaimReference {
    return this.registerClaim(declaration);
  }

  ingestClaim(declaration: SignedRegionDeclaration): ClaimReference {
    return this.registerClaim(declaration);
  }

  declare(declaration: SignedRegionDeclaration): ClaimReference {
    return this.registerClaim(declaration);
  }

  correctClaim(
    claimId: string,
    correction: SignedRegionDeclaration | RegionLike,
  ): ClaimReference {
    const record = this.#records.get(claimId);
    if (record === undefined) {
      throw new DisclosureAdmissionError("unknown-claim", `Unknown claim ${claimId}`);
    }
    if (!record.active) {
      throw new DisclosureAdmissionError("retracted-claim", `Claim ${claimId} has been retracted`);
    }
    const previous = record.declaration;
    const looksLikePatch =
      correction !== null &&
      typeof correction === "object" &&
      !Array.isArray(correction) &&
      [
        "region",
        "polygons",
        "syntheticRegion",
        "possibleRegion",
        "signature",
        "sig",
        "signed",
        "revision",
        "observerId",
        "observer",
        "audienceId",
        "associationId",
        "trajectoryId",
        "subjectId",
        "linkId",
        "authenticatedLinkId",
        "batchId",
        "anonymousBatchId",
        "payload",
        "declaration",
        "claim",
      ].some((key) => key in (correction as Record<string, unknown>));
    const patch: SignedRegionDeclaration = looksLikePatch
      ? (correction as SignedRegionDeclaration)
      : ({ region: correction as RegionLike } as SignedRegionDeclaration);
    const observerId =
      readString(patch.observerId) ??
      readString(patch.observer) ??
      readString(patch.audienceId) ??
      previous.observerId;
    if (observerId !== previous.observerId) {
      throw new DisclosureAdmissionError("identity-change", "Correction cannot change the observer identity");
    }
    const associationId =
      readString(patch.associationId) ??
      readString(patch.trajectoryId) ??
      readString(patch.subjectId) ??
      readString(patch.linkId) ??
      readString(patch.authenticatedLinkId) ??
      previous.associationId;
    if (associationId !== previous.associationId) {
      throw new UnauthenticatedAssociationError(
        "Correction cannot silently change an authenticated association",
      );
    }
    const revision =
      patch.revision === undefined
        ? previous.revision + 1
        : Math.max(previous.revision + 1, readRevision(patch.revision));
    const regionChanged =
      patch.region !== undefined ||
      patch.polygons !== undefined ||
      patch.syntheticRegion !== undefined ||
      patch.possibleRegion !== undefined;
    const hasNewSignature = patch.signature !== undefined || patch.sig !== undefined;
    const merged: SignedRegionDeclaration = {
      ...previous.raw,
      ...patch,
      claimId,
      observerId,
      associationId: associationId ?? undefined,
      region: patch.region ?? patch.polygons ?? patch.syntheticRegion ?? patch.possibleRegion ?? previous.region,
      revision,
    };
    if (regionChanged && !hasNewSignature) {
      delete (merged as Record<string, unknown>).signature;
      delete (merged as Record<string, unknown>).sig;
      (merged as Record<string, unknown>).signed = false;
    } else if (hasNewSignature) {
      (merged as Record<string, unknown>).signature = patch.signature ?? patch.sig;
      (merged as Record<string, unknown>).signed = true;
    }
    const normalized = normalizeDeclaration(
      merged,
      claimId,
      this.#options,
    );
    record.declaration = normalized;
    this.#appendEvent({
      type: "claim-corrected",
      claimId,
      revision,
      previousRevision: previous.revision,
      declarationDigest: normalized.declarationDigest,
      previousDeclarationDigest: previous.declarationDigest,
    });
    return referenceOf(normalized);
  }

  updateClaim(
    claimId: string,
    correction: SignedRegionDeclaration | RegionLike | null,
  ): ClaimReference | HistoryEvent {
    if (correction === null) {
      return this.retractClaim(claimId);
    }
    if (typeof correction === "object" && !Array.isArray(correction)) {
      const update = correction as Record<string, unknown>;
      const status = typeof update.status === "string" ? update.status.toLowerCase() : null;
      if (
        update.retracted === true ||
        update.withdrawn === true ||
        update.revoked === true ||
        update.retract === true ||
        update.withdraw === true ||
        update.action === "retract" ||
        update.action === "withdraw" ||
        update.action === "revoke" ||
        status === "retracted" ||
        status === "withdrawn" ||
        status === "revoked" ||
        update.region === null
      ) {
        return this.retractClaim(claimId);
      }
    }
    return this.correctClaim(claimId, correction);
  }

  amendClaim(
    claimId: string,
    correction: SignedRegionDeclaration | RegionLike,
  ): ClaimReference {
    return this.correctClaim(claimId, correction);
  }

  correctRegion(
    claimId: string,
    region: RegionLike,
  ): ClaimReference {
    return this.correctClaim(claimId, region);
  }

  correct(
    claimId: string,
    correction: SignedRegionDeclaration | RegionLike,
  ): ClaimReference {
    return this.correctClaim(claimId, correction);
  }

  retractClaim(claimId: string): HistoryEvent {
    const record = this.#records.get(claimId);
    if (record === undefined) {
      throw new DisclosureAdmissionError("unknown-claim", `Unknown claim ${claimId}`);
    }
    if (!record.active) {
      throw new DisclosureAdmissionError("retracted-claim", `Claim ${claimId} has already been retracted`);
    }
    const previous = record.declaration;
    record.active = false;
    return this.#appendEvent({
      type: "claim-retracted",
      claimId,
      previousRevision: previous.revision,
      previousDeclarationDigest: previous.declarationDigest,
    });
  }

  revokeClaim(claimId: string): HistoryEvent {
    return this.retractClaim(claimId);
  }

  withdrawClaim(claimId: string): HistoryEvent {
    return this.retractClaim(claimId);
  }

  retract(claimId: string): HistoryEvent {
    return this.retractClaim(claimId);
  }

  revoke(claimId: string): HistoryEvent {
    return this.retractClaim(claimId);
  }

  withdraw(claimId: string): HistoryEvent {
    return this.retractClaim(claimId);
  }

  getClaimMetadata(claimId: string): ClaimReference & {
    readonly observerId: string;
    readonly associationId: string | null;
    readonly batchId: string | null;
    readonly active: boolean;
    readonly signed: boolean;
  } | null {
    const record = this.#records.get(claimId);
    if (record === undefined) {
      return null;
    }
    const declaration = record.declaration;
    return Object.freeze({
      ...referenceOf(declaration),
      observerId: declaration.observerId,
      associationId: declaration.associationId,
      batchId: declaration.batchId,
      active: record.active,
      signed: declaration.signed,
    });
  }

  preview(options: EvaluationOptions = {}): DisclosureDecision {
    const active = this.#activeDeclarations();
    return evaluateNormalized(active, this.#policy, { ...this.#options, ...options });
  }

  evaluate(options: EvaluationOptions = {}): DisclosureDecision {
    return this.preview(options);
  }

  check(options: EvaluationOptions = {}): DisclosureDecision {
    return this.preview(options);
  }

  dryRun(options: EvaluationOptions = {}): DisclosureDecision {
    return this.preview(options);
  }

  publish(options: EvaluationOptions = {}): DisclosureDecision {
    const decision = this.preview(options);
    if (!decision.publishable) {
      return decision;
    }
    const event = this.#appendEvent({
      type: "batch-published",
      certificateDigest: decision.certificate.certificateDigest,
      groupCount: decision.groups.length,
    });
    void event;
    this.#lastPublication = decision;
    return decision;
  }

  publishBatch(options: EvaluationOptions = {}): DisclosureDecision {
    return this.publish(options);
  }

  commit(options: EvaluationOptions = {}): DisclosureDecision {
    return this.publish(options);
  }

  getHistory(): readonly HistoryEvent[] {
    return Object.freeze(this.#history.map((event) => Object.freeze({ ...event })));
  }

  getStateDigest(): string {
    return this.#eventHash;
  }

  #activeDeclarations(): NormalizedDeclaration[] {
    return [...this.#records.values()]
      .filter((record) => record.active)
      .map((record) => record.declaration);
  }

  #appendEvent(
    event: Omit<HistoryEvent, "sequence" | "previousEventHash" | "eventHash">,
  ): HistoryEvent {
    const sequence = this.#history.length + 1;
    const previousEventHash = this.#eventHash;
    const payload = Object.freeze({ ...event, sequence, previousEventHash });
    const eventHash = sha256(stableStringify(payload));
    const stored = Object.freeze({ ...payload, eventHash });
    this.#history.push(stored);
    this.#eventHash = eventHash;
    return stored;
  }
}

export { DisclosureAdmissionHost as GeoDisclosureHost };
export { DisclosureAdmissionHost as GeoDisclosureAdmission };
export { DisclosureAdmissionHost as GeoDisclosureAdmissionComponent };
export { DisclosureAdmissionHost as DisclosureHost };
export { DisclosureAdmissionHost as PublishHost };
