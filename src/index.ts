import { createHash, randomUUID } from "node:crypto";

export type TimeValue = number | string | Date;

export interface Clock {
  now(): number | Date;
}

export type ClockLike = Clock | (() => number | Date) | number;

export interface MxRule {
  readonly host: string;
  readonly publicKeyDigests: readonly string[];
}

export interface RouteDeclaration {
  readonly domain: string;
  readonly sequence: number;
  readonly previousDigest: string | null;
  readonly mxRules: readonly MxRule[];
  readonly notBefore: TimeValue;
  readonly notAfter: TimeValue;
  readonly revoked?: boolean;
}

export interface RecoveryDeclaration
  extends Omit<RouteDeclaration, "previousDigest"> {
  readonly kind: "recovery";
  readonly previousDigest: readonly [string, string];
}

export type Declaration = RouteDeclaration | RecoveryDeclaration;

/**
 * Input accepted by submit(). The canonical property names are preferred, but
 * aliases are accepted so declarations can be fed directly from signature
 * payloads without an adapter.
 */
export interface DeclarationInput {
  readonly domain: string;
  readonly kind?: "route" | "recovery";
  readonly type?: "route" | "recovery";
  readonly sequence?: number;
  readonly seq?: number;
  readonly serial?: number;
  readonly revision?: number;
  readonly previousDigest?: string | readonly string[] | null;
  readonly previousDigests?: readonly string[];
  readonly previous?: string | readonly string[] | null;
  readonly prev?: string | readonly string[] | null;
  readonly prevDigest?: string | readonly string[] | null;
  readonly parentDigest?: string | readonly string[] | null;
  readonly previousHash?: string | readonly string[] | null;
  readonly recoveryHeads?: readonly string[];
  readonly forkHeads?: readonly string[];
  readonly branchHeads?: readonly string[];
  readonly heads?: readonly string[];
  readonly references?: readonly string[];
  readonly branches?: readonly string[];
  readonly forks?: readonly string[];
  readonly recoveryOf?: readonly string[];
  readonly mxRules?: readonly MxRuleInput[];
  readonly mx?: readonly MxRuleInput[];
  readonly rules?: readonly MxRuleInput[];
  readonly notBefore?: TimeValue;
  readonly startsAt?: TimeValue;
  readonly start?: TimeValue;
  readonly validFrom?: TimeValue;
  readonly from?: TimeValue;
  readonly startTime?: TimeValue;
  readonly notAfter?: TimeValue;
  readonly endsAt?: TimeValue;
  readonly end?: TimeValue;
  readonly validUntil?: TimeValue;
  readonly expiresAt?: TimeValue;
  readonly endTime?: TimeValue;
  readonly revoked?: boolean;
  readonly isRevoked?: boolean;
}

export interface MxRuleInput {
  readonly host?: string;
  readonly mxHost?: string;
  readonly mx?: string;
  readonly name?: string;
  readonly publicKeyDigests?: readonly string[];
  readonly publicKeys?: readonly string[];
  readonly keys?: readonly string[];
  readonly certificatePublicKeyDigests?: readonly string[];
  readonly spkiDigests?: readonly string[];
}

export interface NormalizedMxRule {
  readonly host: string;
  readonly publicKeyDigests: readonly string[];
}

export interface NormalizedRouteDeclaration {
  readonly kind: "route";
  readonly domain: string;
  readonly sequence: number;
  readonly previousDigests: readonly string[];
  readonly mxRules: readonly NormalizedMxRule[];
  readonly notBefore: number;
  readonly notAfter: number;
  readonly revoked: boolean;
}

export interface NormalizedRecoveryDeclaration {
  readonly kind: "recovery";
  readonly domain: string;
  readonly sequence: number;
  readonly previousDigests: readonly [string, string];
  readonly mxRules: readonly NormalizedMxRule[];
  readonly notBefore: number;
  readonly notAfter: number;
  readonly revoked: boolean;
}

export type NormalizedDeclaration =
  | NormalizedRouteDeclaration
  | NormalizedRecoveryDeclaration;

export interface VerificationContext {
  readonly kind: "route" | "recovery";
  readonly authority: "domain" | "trusted-root";
  readonly domain: string;
  readonly digest: string;
  readonly signature?: unknown;
}

export type VerificationResult =
  | boolean
  | void
  | {
      readonly valid?: boolean;
      readonly verified?: boolean;
      readonly trusted?: boolean;
      readonly trustedRoot?: boolean;
      readonly ok?: boolean;
      readonly authorized?: boolean;
      readonly signatureValid?: boolean;
    };

export type VerifyDeclaration = (
  declaration: NormalizedDeclaration,
  digest: string,
  context: VerificationContext,
  signature?: unknown,
) => VerificationResult | Promise<VerificationResult>;

export interface RoutingAuthorizationOptions {
  readonly clock?: ClockLike;
  readonly now?: ClockLike;
  readonly currentTime?: ClockLike;
  readonly verify?: VerifyDeclaration;
  readonly verifySignature?: VerifyDeclaration;
  readonly verifyDeclaration?: VerifyDeclaration;
  readonly verifyDomainDeclaration?: VerifyDeclaration;
  readonly verifyDomainSignature?: VerifyDeclaration;
  readonly verifyRoute?: VerifyDeclaration;
  readonly verifyDomain?: VerifyDeclaration;
  readonly verifyRecoveryDeclaration?: VerifyDeclaration;
  readonly verifyRecovery?: VerifyDeclaration;
  readonly verifyRoot?: VerifyDeclaration;
  readonly verifyRootSignature?: VerifyDeclaration;
}

export type SubmissionReason =
  | "accepted"
  | "queued"
  | "duplicate"
  | "invalid-declaration"
  | "invalid-signature"
  | "no-verifier"
  | "verification-error"
  | "invalid-chain"
  | "sequence-mismatch"
  | "not-a-fork"
  | "stale-recovery"
  | "invalid-recovery";

export interface SubmissionResult {
  readonly accepted: boolean;
  readonly reason: SubmissionReason;
  readonly digest?: string;
  readonly kind?: "route" | "recovery";
  readonly queued?: boolean;
  readonly duplicate?: boolean;
  readonly fork?: boolean;
  readonly recovered?: boolean;
  readonly error?: string;
}

export type AuthorizationReason =
  | "ok"
  | "unknown-domain"
  | "no-active-declaration"
  | "fork"
  | "revoked"
  | "not-yet-valid"
  | "expired"
  | "no-matching-rule"
  | "stale-declaration"
  | "unknown-handshake"
  | "invalid-handshake";

export interface DeliveryDecision {
  readonly authorized: boolean;
  readonly reason: AuthorizationReason;
  readonly domain?: string;
  readonly declarationDigest?: string;
  readonly mx?: string;
  readonly publicKeyDigest?: string;
  readonly ruleIndex?: number;
  readonly ruleHost?: string;
}

export interface HandshakeRequest {
  readonly domain: string;
  readonly mx?: string;
  readonly mxHost?: string;
  readonly host?: string;
  readonly publicKeyDigest?: string;
  readonly publicKey?: string;
  readonly key?: string;
}

/**
 * The four values below are exactly what the host must re-check immediately
 * before handing the message to the final SMTP delivery step.
 */
export interface DeliveryHandshake {
  readonly id: string;
  readonly domain: string;
  readonly declarationDigest: string;
  readonly mx: string;
  readonly publicKeyDigest: string;
  readonly mxHost: string;
  readonly publicKey: string;
  readonly ruleHost: string;
  readonly ruleIndex: number;
}

export interface RuleSelection {
  readonly matched: boolean;
  readonly reason: AuthorizationReason;
  readonly ruleIndex?: number;
  readonly rule?: NormalizedMxRule;
  readonly declarationDigest?: string;
}

export interface DomainState {
  readonly domain: string;
  readonly currentDigest: string | null;
  readonly sequence: number | null;
  readonly revoked: boolean;
  readonly expired: boolean;
  readonly fork: boolean;
  readonly forkSequence: number | null;
  readonly forkHeads: readonly string[];
  readonly recovered: boolean;
  readonly recoveryDigest: string | null;
  readonly pending: number;
}

interface NodeRecord {
  readonly digest: string;
  readonly declaration: NormalizedDeclaration;
  readonly sequence: number;
  readonly branchId: number;
  readonly parentDigest: string | null;
}

interface PendingDeclaration {
  readonly digest: string;
  readonly declaration: NormalizedDeclaration;
}

interface DomainRecord {
  readonly domain: string;
  readonly nodes: Map<string, NodeRecord>;
  readonly bySequence: Map<number, Set<string>>;
  readonly branchTips: Map<number, Set<string>>;
  readonly pendingByParent: Map<string, Map<string, PendingDeclaration>>;
  readonly pendingRecoveries: Map<string, PendingDeclaration>;
  nextBranchId: number;
  activeBranchId: number | null;
  unresolvedBranches: Set<number>;
  forkSequence: number | null;
  recoveryDigest: string | null;
}

interface ProcessOutcome {
  readonly accepted: boolean;
  readonly reason: SubmissionReason;
  readonly queued?: boolean;
  readonly duplicate?: boolean;
  readonly fork?: boolean;
  readonly recovered?: boolean;
}

const DECLARATION_DIGEST_VERSION = "mail-route-declaration-v1";
const HEX_DIGEST = /^[0-9a-fA-F]+$/;

function sha256(value: string): string {
  return createHash("sha256").update(value, "utf8").digest("hex");
}

function canonicalJson(value: unknown): string {
  if (value === null) return "null";

  switch (typeof value) {
    case "string":
    case "boolean":
      return JSON.stringify(value) as string;
    case "number":
      if (!Number.isFinite(value)) {
        throw new TypeError("Cannot canonicalize a non-finite number");
      }
      return JSON.stringify(value);
    case "object": {
      if (Array.isArray(value)) {
        return `[${value.map((item) => canonicalJson(item)).join(",")}]`;
      }

      const object = value as Record<string, unknown>;
      const keys = Object.keys(object)
        .filter((key) => object[key] !== undefined)
        .sort();

      return `{${keys
        .map((key) => `${JSON.stringify(key)}:${canonicalJson(object[key])}`)
        .join(",")}}`;
    }
    default:
      throw new TypeError(`Cannot canonicalize value of type ${typeof value}`);
  }
}

function firstDefined<T>(
  values: readonly (T | undefined)[],
): T | undefined {
  for (const value of values) {
    if (value !== undefined) return value;
  }
  return undefined;
}

function normalizeDomain(value: unknown): string {
  if (typeof value !== "string") {
    throw new TypeError("domain must be a string");
  }

  const domain = value.trim().toLowerCase().replace(/\.$/, "");
  if (
    domain.length === 0 ||
    domain.length > 253 ||
    !domain.split(".").every((label) => isValidLabel(label))
  ) {
    throw new TypeError(`Invalid domain: ${value}`);
  }

  return domain;
}

function isValidLabel(label: string): boolean {
  return (
    label.length > 0 &&
    label.length <= 63 &&
    /^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$/.test(label)
  );
}

function normalizeMxHost(value: unknown, allowWildcard: boolean): string {
  if (typeof value !== "string") {
    throw new TypeError("MX host must be a string");
  }

  let host = value.trim().toLowerCase().replace(/\.$/, "");
  let wildcard = false;

  if (host.startsWith("*.")) {
    if (!allowWildcard) {
      throw new TypeError(`Wildcard is not allowed here: ${value}`);
    }
    wildcard = true;
    host = host.slice(2);
  }

  if (host.length === 0 || host.length > 253 || host.includes("*")) {
    throw new TypeError(`Invalid MX host: ${value}`);
  }

  if (!host.split(".").every((label) => isValidLabel(label))) {
    throw new TypeError(`Invalid MX host: ${value}`);
  }

  return wildcard ? `*.${host}` : host;
}

function normalizeDigest(value: unknown, field: string): string {
  if (typeof value !== "string" || value.trim().length === 0) {
    throw new TypeError(`${field} must be a non-empty string`);
  }

  const digest = value.trim();
  return HEX_DIGEST.test(digest) ? digest.toLowerCase() : digest;
}

function normalizePublicKeyDigest(value: unknown): string {
  if (typeof value !== "string" || value.trim().length === 0) {
    throw new TypeError("public key digest must be a non-empty string");
  }
  return value.trim();
}

function normalizeTime(value: unknown, field: string): number {
  let result: number;

  if (value instanceof Date) {
    result = value.getTime();
  } else if (typeof value === "number") {
    result = value;
  } else if (typeof value === "string" && value.trim().length > 0) {
    const trimmed = value.trim();
    result = /^-?\d+(?:\.\d+)?$/.test(trimmed)
      ? Number(trimmed)
      : Date.parse(trimmed);
  } else {
    throw new TypeError(`${field} must be an epoch millisecond value or date`);
  }

  if (!Number.isFinite(result)) {
    throw new TypeError(`Invalid ${field}`);
  }

  return result;
}

function normalizePreviousDigests(
  declaration: DeclarationInput | NormalizedDeclaration,
  kind: "route" | "recovery",
): readonly string[] {
  const source = declaration as DeclarationInput & NormalizedDeclaration;
  const raw = firstDefined<unknown>([
    source.previousDigests,
    source.recoveryHeads,
    source.forkHeads,
    source.branchHeads,
    source.heads,
    source.references,
    source.branches,
    source.forks,
    source.recoveryOf,
    source.previousDigest,
    source.previous,
    source.prev,
    source.prevDigest,
    source.parentDigest,
    source.previousHash,
  ]);

  const values: readonly unknown[] =
    raw === undefined || raw === null || raw === ""
      ? []
      : Array.isArray(raw)
        ? raw
        : [raw];

  const digests = values
    .filter((value) => value !== undefined && value !== null && value !== "")
    .map((value) => normalizeDigest(value, "previous digest"));

  if (kind === "route") {
    if (digests.length > 1) {
      throw new TypeError("A route declaration has at most one previous digest");
    }
    return digests;
  }

  if (digests.length !== 2 || digests[0] === digests[1]) {
    throw new TypeError(
      "A recovery declaration must reference two distinct branch heads",
    );
  }

  return digests;
}

function normalizeMxRules(
  declaration: DeclarationInput & NormalizedDeclaration,
): readonly NormalizedMxRule[] {
  const rawRules = firstDefined<readonly MxRuleInput[] | readonly NormalizedMxRule[]>([
    declaration.mxRules,
    declaration.mx,
    declaration.rules,
  ]);

  if (!Array.isArray(rawRules)) {
    throw new TypeError("mxRules must be an ordered array");
  }

  const rules = rawRules.map((rawRule, index) => {
    const rule = rawRule as MxRuleInput & NormalizedMxRule;
    const rawHost = firstDefined<unknown>([
      rule.host,
      rule.mxHost,
      rule.mx,
      rule.name,
    ]);
    const host = normalizeMxHost(rawHost, true);

    const rawKeys = firstDefined<readonly string[]>([
      rule.publicKeyDigests,
      rule.certificatePublicKeyDigests,
      rule.spkiDigests,
      rule.publicKeys,
      rule.keys,
    ]);

    if (!Array.isArray(rawKeys) || rawKeys.length === 0) {
      throw new TypeError(
        `mxRules[${index}].publicKeyDigests must be a non-empty array`,
      );
    }

    const publicKeyDigests: string[] = [];
    const seen = new Set<string>();
    for (const key of rawKeys) {
      const normalized = normalizePublicKeyDigest(key);
      if (!seen.has(normalized)) {
        seen.add(normalized);
        publicKeyDigests.push(normalized);
      }
    }

    return Object.freeze({
      host,
      publicKeyDigests: Object.freeze(publicKeyDigests),
    });
  });

  return Object.freeze(rules);
}

/**
 * Validates and canonicalizes a declaration. This is deterministic and does
 * not use the clock; an expired declaration remains valid chain history.
 */
export function normalizeDeclaration(
  input: DeclarationInput | Declaration | NormalizedDeclaration,
): NormalizedDeclaration {
  if (input === null || typeof input !== "object") {
    throw new TypeError("declaration must be an object");
  }

  const declaration = input as DeclarationInput & NormalizedDeclaration;
  const domain = normalizeDomain(declaration.domain);

  const rawSequence = firstDefined<unknown>([
    declaration.sequence,
    declaration.seq,
    declaration.serial,
    declaration.revision,
  ]);
  if (
    typeof rawSequence !== "number" ||
    !Number.isSafeInteger(rawSequence) ||
    rawSequence < 0
  ) {
    throw new TypeError("sequence must be a non-negative safe integer");
  }
  const sequence = rawSequence;

  const explicitRecovery =
    declaration.kind === "recovery" ||
    declaration.type === "recovery" ||
    declaration.recoveryHeads !== undefined ||
    declaration.forkHeads !== undefined ||
    declaration.branchHeads !== undefined ||
    declaration.heads !== undefined ||
    declaration.references !== undefined ||
    declaration.branches !== undefined ||
    declaration.forks !== undefined ||
    declaration.recoveryOf !== undefined;

  const previousValues = (() => {
    const raw = firstDefined<unknown>([
      declaration.previousDigests,
      declaration.recoveryHeads,
      declaration.forkHeads,
      declaration.branchHeads,
      declaration.heads,
      declaration.references,
      declaration.branches,
      declaration.forks,
      declaration.recoveryOf,
      declaration.previousDigest,
      declaration.previous,
      declaration.prev,
      declaration.prevDigest,
      declaration.parentDigest,
      declaration.previousHash,
    ]);
    return Array.isArray(raw) ? raw.length : raw === undefined || raw === null || raw === "" ? 0 : 1;
  })();

  const kind: "route" | "recovery" =
    explicitRecovery || previousValues === 2 ? "recovery" : "route";

  const declaredKind = declaration.kind ?? declaration.type;
  if (declaredKind !== undefined && declaredKind !== kind) {
    throw new TypeError("Declaration kind conflicts with its previous digests");
  }

  const previousDigests = normalizePreviousDigests(declaration, kind);

  if (kind === "route") {
    if (sequence === 0 && previousDigests.length !== 0) {
      throw new TypeError("Sequence 0 must not have a previous digest");
    }
    if (sequence > 0 && previousDigests.length !== 1) {
      throw new TypeError(
        "A non-genesis route declaration must have one previous digest",
      );
    }
  }

  const rawNotBefore = firstDefined<unknown>([
    declaration.notBefore,
    declaration.startsAt,
    declaration.start,
    declaration.validFrom,
    declaration.from,
    declaration.startTime,
  ]);
  const rawNotAfter = firstDefined<unknown>([
    declaration.notAfter,
    declaration.endsAt,
    declaration.end,
    declaration.validUntil,
    declaration.expiresAt,
    declaration.endTime,
  ]);

  const notBefore = normalizeTime(rawNotBefore, "notBefore");
  const notAfter = normalizeTime(rawNotAfter, "notAfter");
  if (notAfter < notBefore) {
    throw new TypeError("notAfter must not be earlier than notBefore");
  }

  const rawRevoked = firstDefined<unknown>([
    declaration.revoked,
    declaration.isRevoked,
  ]);
  if (rawRevoked !== undefined && typeof rawRevoked !== "boolean") {
    throw new TypeError("revoked must be a boolean");
  }

  const base = {
    domain,
    sequence,
    mxRules: normalizeMxRules(declaration),
    notBefore,
    notAfter,
    revoked: rawRevoked === true,
  };

  if (kind === "recovery") {
    return Object.freeze({
      kind: "recovery" as const,
      ...base,
      previousDigests: Object.freeze([
        previousDigests[0],
        previousDigests[1],
      ]) as unknown as readonly [string, string],
    });
  }

  return Object.freeze({
    kind: "route" as const,
    ...base,
    previousDigests: Object.freeze([...previousDigests]),
  });
}

/**
 * SHA-256 over a canonical JSON representation. Recovery head references are
 * sorted because the set, not the root signer's branch ordering, identifies
 * the fork.
 */
export function computeDeclarationDigest(
  input: DeclarationInput | Declaration | NormalizedDeclaration,
): string {
  const declaration = normalizeDeclaration(input);
  const previous =
    declaration.kind === "recovery"
      ? [...declaration.previousDigests].sort()
      : [...declaration.previousDigests];

  const payload = {
    version: DECLARATION_DIGEST_VERSION,
    kind: declaration.kind,
    domain: declaration.domain,
    sequence: declaration.sequence,
    previous,
    mxRules: declaration.mxRules.map((rule) => ({
      host: rule.host,
      publicKeyDigests: [...rule.publicKeyDigests],
    })),
    notBefore: declaration.notBefore,
    notAfter: declaration.notAfter,
    revoked: declaration.revoked,
  };

  return sha256(canonicalJson(payload));
}

export const declarationDigest = computeDeclarationDigest;

function normalizeClock(clock: ClockLike | undefined): () => number {
  if (clock === undefined) return () => Date.now();
  if (typeof clock === "number") {
    if (!Number.isFinite(clock)) throw new TypeError("clock must be finite");
    return () => clock;
  }

  if (typeof clock === "function") {
    return () => {
      const value = clock();
      return value instanceof Date ? value.getTime() : value;
    };
  }

  if (typeof clock === "object" && clock !== null && typeof clock.now === "function") {
    return () => {
      const value = clock.now();
      return value instanceof Date ? value.getTime() : value;
    };
  }

  throw new TypeError("Invalid clock");
}

function verified(result: VerificationResult): boolean {
  if (result === true) return true;
  if (result === false || result === undefined || result === null) return false;
  if (typeof result !== "object") return false;

  if (result.valid !== undefined) return result.valid === true;
  if (result.verified !== undefined) return result.verified === true;
  if (result.signatureValid !== undefined) {
    return result.signatureValid === true;
  }
  if (result.ok !== undefined) return result.ok === true;
  if (result.authorized !== undefined) return result.authorized === true;
  if (result.trustedRoot !== undefined) return result.trustedRoot === true;
  if (result.trusted !== undefined) return result.trusted === true;
  return false;
}

function freezeArray<T>(values: readonly T[]): readonly T[] {
  return Object.freeze([...values]);
}

/**
 * Compares MX hosts as DNS labels. A rule host may be exact or a leading "*."
 * wildcard, which covers exactly one additional label (never the apex).
 */
export function mxRuleMatchesHost(ruleHost: string, mxHost: string): boolean {
  let rule: string;
  let host: string;

  try {
    rule = normalizeMxHost(ruleHost, true);
    host = normalizeMxHost(mxHost, false);
  } catch {
    return false;
  }

  if (!rule.startsWith("*.")) return rule === host;

  const ruleLabels = rule.slice(2).split(".");
  const hostLabels = host.split(".");
  if (hostLabels.length !== ruleLabels.length + 1) return false;

  return ruleLabels.every(
    (label, index) => hostLabels[index + 1] === label,
  );
}

export const hostMatchesRule = mxRuleMatchesHost;
export const wildcardMatchesHost = mxRuleMatchesHost;

/**
 * Selects a single rule. The host and key must be satisfied by that same rule;
 * combining a host from one rule with a key from another is never possible.
 */
export function selectMatchingRule(
  rules: readonly MxRule[] | readonly NormalizedMxRule[],
  mxHost: string,
  publicKeyDigest: string,
): { readonly matched: boolean; readonly ruleIndex?: number; readonly rule?: NormalizedMxRule } {
  const key = normalizePublicKeyDigest(publicKeyDigest);

  for (let index = 0; index < rules.length; index += 1) {
    const rawRule = rules[index] as MxRuleInput & NormalizedMxRule;
    const rawHost = firstDefined<unknown>([
      rawRule.host,
      rawRule.mxHost,
      rawRule.mx,
      rawRule.name,
    ]);
    let ruleHost: string;
    try {
      ruleHost = normalizeMxHost(rawHost, true);
    } catch {
      continue;
    }

    if (!mxRuleMatchesHost(ruleHost, mxHost)) continue;

    const rawKeys = firstDefined<readonly string[]>([
      rawRule.publicKeyDigests,
      rawRule.certificatePublicKeyDigests,
      rawRule.spkiDigests,
      rawRule.publicKeys,
      rawRule.keys,
    ]);
    if (!Array.isArray(rawKeys)) continue;

    const keys = rawKeys.map((value) => normalizePublicKeyDigest(value));
    if (!keys.includes(key)) continue;

    return {
      matched: true,
      ruleIndex: index,
      rule: Object.freeze({
        host: ruleHost,
        publicKeyDigests: freezeArray(keys),
      }),
    };
  }

  return { matched: false };
}

export const findMatchingRule = selectMatchingRule;
export const matchMxRule = selectMatchingRule;

function createDomainRecord(domain: string): DomainRecord {
  return {
    domain,
    nodes: new Map(),
    bySequence: new Map(),
    branchTips: new Map(),
    pendingByParent: new Map(),
    pendingRecoveries: new Map(),
    nextBranchId: 0,
    activeBranchId: null,
    unresolvedBranches: new Set(),
    forkSequence: null,
    recoveryDigest: null,
  };
}

function newNode(
  digest: string,
  declaration: NormalizedDeclaration,
  branchId: number,
  parentDigest: string | null,
): NodeRecord {
  return {
    digest,
    declaration,
    sequence: declaration.sequence,
    branchId,
    parentDigest,
  };
}

export class RoutingAuthorization {
  private readonly clock: () => number;
  private readonly verifyRouteSignature: VerifyDeclaration | undefined;
  private readonly verifyRecoverySignature: VerifyDeclaration | undefined;
  private readonly domains = new Map<string, DomainRecord>();
  private readonly handshakes = new Map<string, DeliveryHandshake>();

  constructor(options: RoutingAuthorizationOptions = {}) {
    this.clock = normalizeClock(
      firstDefined<ClockLike>([options.clock, options.now, options.currentTime]),
    );

    this.verifyRouteSignature = firstDefined<VerifyDeclaration>([
      options.verifyDomainDeclaration,
      options.verifyDomainSignature,
      options.verifyRoute,
      options.verifyDomain,
      options.verifyDeclaration,
      options.verifySignature,
      options.verify,
    ]);

    this.verifyRecoverySignature = firstDefined<VerifyDeclaration>([
      options.verifyRecoveryDeclaration,
      options.verifyRecovery,
      options.verifyRoot,
      options.verifyRootSignature,
      options.verifyDeclaration,
      options.verifySignature,
      options.verify,
    ]);
  }

  private now(): number {
    const value = this.clock();
    if (typeof value !== "number" || !Number.isFinite(value)) {
      throw new TypeError("clock returned an invalid time");
    }
    return value;
  }

  private domainRecord(domain: string): DomainRecord {
    let record = this.domains.get(domain);
    if (record === undefined) {
      record = createDomainRecord(domain);
      this.domains.set(domain, record);
    }
    return record;
  }

  private stateFor(domain: string): DomainRecord | undefined {
    return this.domains.get(domain);
  }

  private verifierFor(
    kind: "route" | "recovery",
  ): VerifyDeclaration | undefined {
    return kind === "route"
      ? this.verifyRouteSignature
      : this.verifyRecoverySignature;
  }

  /**
   * Verifies and ingests a declaration. A successor whose parent has not
   * arrived is retained as pending and becomes live as soon as the parent is
   * accepted.
   */
  async submit(
    input: DeclarationInput | Declaration | NormalizedDeclaration,
    signature?: unknown,
  ): Promise<SubmissionResult> {
    let declaration: NormalizedDeclaration;
    let digest: string;

    try {
      declaration = normalizeDeclaration(input);
      digest = computeDeclarationDigest(declaration);
    } catch (error) {
      return {
        accepted: false,
        reason: "invalid-declaration",
        error: error instanceof Error ? error.message : String(error),
      };
    }

    const existing = this.domains.get(declaration.domain);
    const pendingDuplicate =
      existing !== undefined &&
      [...existing.pendingByParent.values()].some((entries) =>
        entries.has(digest),
      );
    if (
      existing?.nodes.has(digest) ||
      existing?.pendingRecoveries.has(digest) ||
      pendingDuplicate
    ) {
      return {
        accepted: true,
        reason: "duplicate",
        digest,
        kind: declaration.kind,
        duplicate: true,
      };
    }

    const verifier = this.verifierFor(declaration.kind);
    if (verifier === undefined) {
      return {
        accepted: false,
        reason: "no-verifier",
        digest,
        kind: declaration.kind,
      };
    }

    const context: VerificationContext = Object.freeze({
      kind: declaration.kind,
      authority:
        declaration.kind === "recovery" ? "trusted-root" : "domain",
      domain: declaration.domain,
      digest,
      signature,
    });

    try {
      const result = await verifier(
        declaration,
        digest,
        context,
        signature,
      );
      if (!verified(result)) {
        return {
          accepted: false,
          reason: "invalid-signature",
          digest,
          kind: declaration.kind,
        };
      }
    } catch (error) {
      return {
        accepted: false,
        reason: "verification-error",
        digest,
        kind: declaration.kind,
        error: error instanceof Error ? error.message : String(error),
      };
    }

    const state = this.domainRecord(declaration.domain);
    const outcome = this.processVerified(state, declaration, digest);
    this.drain(state);

    return {
      accepted: outcome.accepted,
      reason: outcome.reason,
      digest,
      kind: declaration.kind,
      queued: outcome.queued,
      duplicate: outcome.duplicate,
      fork: state.unresolvedBranches.size > 0,
      recovered: state.recoveryDigest !== null,
    };
  }

  submitDeclaration(
    input: DeclarationInput | Declaration | NormalizedDeclaration,
    signature?: unknown,
  ): Promise<SubmissionResult> {
    return this.submit(input, signature);
  }

  ingest(
    input: DeclarationInput | Declaration | NormalizedDeclaration,
    signature?: unknown,
  ): Promise<SubmissionResult> {
    return this.submit(input, signature);
  }

  submitRoute(
    input: DeclarationInput | Declaration | NormalizedDeclaration,
    signature?: unknown,
  ): Promise<SubmissionResult> {
    return this.submit(input, signature);
  }

  submitRecovery(
    input: DeclarationInput | Declaration | NormalizedDeclaration,
    signature?: unknown,
  ): Promise<SubmissionResult> {
    return this.submit({ ...input, kind: "recovery" } as DeclarationInput, signature);
  }

  private processVerified(
    state: DomainRecord,
    declaration: NormalizedDeclaration,
    digest: string,
  ): ProcessOutcome {
    if (state.nodes.has(digest)) {
      return { accepted: true, reason: "duplicate", duplicate: true };
    }

    if (declaration.kind === "recovery") {
      return this.processRecovery(state, declaration, digest);
    }

    if (declaration.sequence === 0) {
      this.acceptNormal(state, declaration, digest, null);
      return {
        accepted: true,
        reason: "accepted",
        fork: state.unresolvedBranches.size > 0,
      };
    }

    const parentDigest = declaration.previousDigests[0];
    const parent = state.nodes.get(parentDigest);
    if (parent === undefined) {
      let pending = state.pendingByParent.get(parentDigest);
      if (pending === undefined) {
        pending = new Map();
        state.pendingByParent.set(parentDigest, pending);
      }
      if (!pending.has(digest)) {
        pending.set(digest, { digest, declaration });
      }
      return { accepted: true, reason: "queued", queued: true };
    }

    if (declaration.sequence !== parent.sequence + 1) {
      return {
        accepted: false,
        reason: "sequence-mismatch",
      };
    }

    this.acceptNormal(state, declaration, digest, parent.digest);
    return {
      accepted: true,
      reason: "accepted",
      fork: state.unresolvedBranches.size > 0,
    };
  }

  private acceptNormal(
    state: DomainRecord,
    declaration: NormalizedDeclaration,
    digest: string,
    parentDigest: string | null,
  ): void {
    const parent =
      parentDigest === null ? undefined : state.nodes.get(parentDigest);

    const collision = state.bySequence.get(declaration.sequence);
    const sequenceCollision =
      collision !== undefined &&
      [...collision].some((candidate) => {
        if (candidate === digest) return false;
        if (parent === undefined) return true;
        return state.nodes.get(candidate)?.branchId === parent.branchId;
      });

    let branchId: number;
    let split = false;

    if (parent === undefined) {
      if (state.nodes.size === 0) {
        branchId = this.createBranch(state);
        state.activeBranchId = branchId;
      } else {
        branchId = this.createBranch(state);
        split = true;
      }
    } else {
      const parentBranch = parent.branchId;
      const parentTips = state.branchTips.get(parentBranch);
      const parentIsTip = parentTips?.has(parent.digest) === true;

      if (parentIsTip && !sequenceCollision) {
        branchId = parentBranch;
      } else {
        branchId = this.createBranch(state);
        split = true;
      }
    }

    const node = newNode(digest, declaration, branchId, parentDigest);
    state.nodes.set(digest, node);

    let atSequence = state.bySequence.get(declaration.sequence);
    if (atSequence === undefined) {
      atSequence = new Set();
      state.bySequence.set(declaration.sequence, atSequence);
    }
    atSequence.add(digest);

    if (parent !== undefined) {
      const parentTips = state.branchTips.get(parent.branchId);
      parentTips?.delete(parent.digest);
    }

    let tips = state.branchTips.get(branchId);
    if (tips === undefined) {
      tips = new Set();
      state.branchTips.set(branchId, tips);
    }
    tips.add(digest);

    if (split) {
      const splitsActiveBranch =
        state.recoveryDigest === null ||
        (parent !== undefined && parent.branchId === state.activeBranchId);

      if (state.recoveryDigest === null) {
        for (const [candidateBranch, candidateTips] of state.branchTips) {
          if (candidateTips.size > 0) {
            state.unresolvedBranches.add(candidateBranch);
          }
        }
        if (parent !== undefined) {
          state.unresolvedBranches.add(parent.branchId);
        }
      } else if (
        parent !== undefined &&
        parent.branchId === state.activeBranchId
      ) {
        state.unresolvedBranches.add(parent.branchId);
        state.unresolvedBranches.add(branchId);
      }

      if (splitsActiveBranch) {
        if (state.forkSequence === null) {
          state.forkSequence = declaration.sequence;
        } else {
          state.forkSequence = Math.min(
            state.forkSequence,
            declaration.sequence,
          );
        }
      }
    }

    if (
      state.recoveryDigest === null &&
      state.unresolvedBranches.size === 0 &&
      state.activeBranchId === null
    ) {
      state.activeBranchId = branchId;
    }
  }

  private createBranch(state: DomainRecord): number {
    const branchId = state.nextBranchId;
    state.nextBranchId += 1;
    state.branchTips.set(branchId, new Set());
    return branchId;
  }

  private processRecovery(
    state: DomainRecord,
    declaration: NormalizedRecoveryDeclaration,
    digest: string,
  ): ProcessOutcome {
    if (state.nodes.has(digest)) {
      return { accepted: true, reason: "duplicate", duplicate: true };
    }

    const heads = declaration.previousDigests;
    const missingHead = heads.some((head) => !state.nodes.has(head));
    if (missingHead) {
      state.pendingRecoveries.set(digest, { digest, declaration });
      return { accepted: true, reason: "queued", queued: true };
    }

    if (state.unresolvedBranches.size === 0) {
      return { accepted: false, reason: "not-a-fork" };
    }

    if (state.unresolvedBranches.size !== 2) {
      return { accepted: false, reason: "invalid-recovery" };
    }

    const branchIds = [...state.unresolvedBranches];
    const currentHeads: string[] = [];
    for (const branchId of branchIds) {
      const tips = state.branchTips.get(branchId);
      if (tips === undefined || tips.size !== 1) {
        return { accepted: false, reason: "invalid-recovery" };
      }
      currentHeads.push([...tips][0]);
    }

    const expected = [...currentHeads].sort();
    const supplied = [...heads].sort();
    if (
      expected.length !== supplied.length ||
      expected.some((head, index) => head !== supplied[index])
    ) {
      return { accepted: false, reason: "stale-recovery" };
    }

    const maximumHeadSequence = Math.max(
      ...heads.map((head) => state.nodes.get(head)!.sequence),
    );
    if (declaration.sequence <= maximumHeadSequence) {
      return { accepted: false, reason: "invalid-recovery" };
    }

    const branchId = this.createBranch(state);
    const node = newNode(digest, declaration, branchId, null);
    state.nodes.set(digest, node);
    state.branchTips.get(branchId)!.add(digest);

    let atSequence = state.bySequence.get(declaration.sequence);
    if (atSequence === undefined) {
      atSequence = new Set();
      state.bySequence.set(declaration.sequence, atSequence);
    }
    atSequence.add(digest);

    state.activeBranchId = branchId;
    state.recoveryDigest = digest;
    state.unresolvedBranches = new Set();
    state.forkSequence = null;
    state.pendingRecoveries.delete(digest);

    return { accepted: true, reason: "accepted", recovered: true };
  }

  private drain(state: DomainRecord): void {
    let changed = true;

    while (changed) {
      changed = false;

      for (const [parentDigest, pendingMap] of state.pendingByParent) {
        const parent = state.nodes.get(parentDigest);
        if (parent === undefined) continue;

        for (const [digest, pending] of pendingMap) {
          pendingMap.delete(digest);
          if (state.nodes.has(digest)) continue;
          if (pending.declaration.sequence !== parent.sequence + 1) {
            continue;
          }
          this.processVerified(state, pending.declaration, digest);
          changed = true;
        }

        if (pendingMap.size === 0) {
          state.pendingByParent.delete(parentDigest);
        }
      }

      for (const [digest, pending] of state.pendingRecoveries) {
        if (!(pending.declaration.kind === "recovery")) continue;
        const allHeadsPresent = pending.declaration.previousDigests.every(
          (head) => state.nodes.has(head),
        );
        if (!allHeadsPresent) continue;

        state.pendingRecoveries.delete(digest);
        const outcome = this.processRecovery(
          state,
          pending.declaration,
          digest,
        );
        if (outcome.queued) {
          state.pendingRecoveries.set(digest, pending);
        }
        changed = true;
      }
    }
  }

  private currentNode(state: DomainRecord): NodeRecord | undefined {
    if (state.unresolvedBranches.size > 0) return undefined;
    if (state.activeBranchId === null) return undefined;

    const tips = state.branchTips.get(state.activeBranchId);
    if (tips === undefined || tips.size !== 1) return undefined;

    return state.nodes.get([...tips][0]);
  }

  private declarationStatus(
    state: DomainRecord,
    node: NodeRecord | undefined,
  ): {
    readonly ok: boolean;
    readonly reason: AuthorizationReason;
  } {
    if (state.unresolvedBranches.size > 0) {
      return { ok: false, reason: "fork" };
    }
    if (node === undefined) {
      return {
        ok: false,
        reason:
          state.nodes.size === 0 ? "no-active-declaration" : "fork",
      };
    }

    if (node.declaration.revoked) {
      return { ok: false, reason: "revoked" };
    }

    const now = this.now();
    if (now < node.declaration.notBefore) {
      return { ok: false, reason: "not-yet-valid" };
    }
    if (now > node.declaration.notAfter) {
      return { ok: false, reason: "expired" };
    }

    return { ok: true, reason: "ok" };
  }

  getDomainState(domainInput: string): DomainState {
    const domain = normalizeDomain(domainInput);
    const state = this.stateFor(domain);
    if (state === undefined) {
      return Object.freeze({
        domain,
        currentDigest: null,
        sequence: null,
        revoked: false,
        expired: false,
        fork: false,
        forkSequence: null,
        forkHeads: Object.freeze([]) as readonly string[],
        recovered: false,
        recoveryDigest: null,
        pending: 0,
      });
    }

    const node = this.currentNode(state);
    const now = this.now();
    const forkHeads = [...state.unresolvedBranches].flatMap((branchId) => [
      ...(state.branchTips.get(branchId) ?? []),
    ]);
    const pending =
      [...state.pendingByParent.values()].reduce(
        (total, entries) => total + entries.size,
        0,
      ) + state.pendingRecoveries.size;

    return Object.freeze({
      domain,
      currentDigest: node?.digest ?? null,
      sequence: node?.sequence ?? null,
      revoked: node?.declaration.revoked ?? false,
      expired:
        node === undefined ? false : now > node.declaration.notAfter,
      fork: state.unresolvedBranches.size > 0,
      forkSequence: state.forkSequence,
      forkHeads: freezeArray(forkHeads),
      recovered: state.recoveryDigest !== null,
      recoveryDigest: state.recoveryDigest,
      pending,
    });
  }

  status(domain: string): DomainState {
    return this.getDomainState(domain);
  }

  private selectRuleFor(
    state: DomainRecord,
    node: NodeRecord | undefined,
    mxHost: string,
    publicKeyDigest: string,
  ): RuleSelection {
    const status = this.declarationStatus(state, node);
    if (!status.ok) {
      return { matched: false, reason: status.reason };
    }

    const selection = selectMatchingRule(
      node!.declaration.mxRules,
      mxHost,
      publicKeyDigest,
    );
    if (!selection.matched) {
      return {
        matched: false,
        reason: "no-matching-rule",
        declarationDigest: node!.digest,
      };
    }

    return {
      matched: true,
      reason: "ok",
      ruleIndex: selection.ruleIndex,
      rule: selection.rule,
      declarationDigest: node!.digest,
    };
  }

  selectRule(
    domainInput: string,
    mxHost: string,
    publicKeyDigest: string,
  ): RuleSelection {
    let domain: string;
    let host: string;
    let key: string;
    try {
      domain = normalizeDomain(domainInput);
      host = normalizeMxHost(mxHost, false);
      key = normalizePublicKeyDigest(publicKeyDigest);
    } catch {
      return { matched: false, reason: "unknown-domain" };
    }

    const state = this.stateFor(domain);
    if (state === undefined) {
      return { matched: false, reason: "unknown-domain" };
    }

    return this.selectRuleFor(state, this.currentNode(state), host, key);
  }

  /**
   * Creates and caches a handshake ticket. The ticket is deliberately bound
   * to the current declaration digest, so accepting a successor invalidates
   * the in-flight handshake rather than silently upgrading it.
   */
  beginHandshake(
    domainOrRequest: string | HandshakeRequest,
    mxHost?: string,
    publicKeyDigest?: string,
  ): DeliveryHandshake | null {
    const request: HandshakeRequest =
      typeof domainOrRequest === "string"
        ? {
            domain: domainOrRequest,
            mx: mxHost,
            publicKeyDigest,
          }
        : domainOrRequest;

    let domain: string;
    let host: string;
    let key: string;
    try {
      domain = normalizeDomain(request.domain);
      const hostValue = firstDefined<unknown>([
        request.mx,
        request.mxHost,
        request.host,
      ]);
      const keyValue = firstDefined<unknown>([
        request.publicKeyDigest,
        request.publicKey,
        request.key,
      ]);

      host = normalizeMxHost(hostValue, false);
      key = normalizePublicKeyDigest(keyValue);
    } catch {
      return null;
    }
    const state = this.stateFor(domain);
    if (state === undefined) return null;

    const node = this.currentNode(state);
    const selection = this.selectRuleFor(state, node, host, key);
    if (!selection.matched || node === undefined) return null;

    const id = `handshake-${randomUUID()}`;

    const handshake: DeliveryHandshake = Object.freeze({
      id,
      domain,
      declarationDigest: node.digest,
      mx: host,
      publicKeyDigest: key,
      mxHost: host,
      publicKey: key,
      ruleHost: selection.rule!.host,
      ruleIndex: selection.ruleIndex!,
    });

    this.handshakes.set(id, handshake);
    return handshake;
  }

  prepareDelivery(
    domainOrRequest: string | HandshakeRequest,
    mxHost?: string,
    publicKeyDigest?: string,
  ): DeliveryHandshake | null {
    return this.beginHandshake(domainOrRequest, mxHost, publicKeyDigest);
  }

  private resolveHandshake(
    handshakeOrId: DeliveryHandshake | string,
  ): DeliveryHandshake | undefined {
    const id =
      typeof handshakeOrId === "string" ? handshakeOrId : handshakeOrId?.id;
    if (typeof id !== "string") return undefined;
    return this.handshakes.get(id);
  }

  private handshakeWasTampered(
    supplied: DeliveryHandshake,
    cached: DeliveryHandshake,
  ): boolean {
    return (
      supplied.domain !== cached.domain ||
      supplied.declarationDigest !== cached.declarationDigest ||
      supplied.mx !== cached.mx ||
      supplied.mxHost !== cached.mxHost ||
      supplied.publicKeyDigest !== cached.publicKeyDigest ||
      supplied.publicKey !== cached.publicKey ||
      supplied.ruleHost !== cached.ruleHost ||
      supplied.ruleIndex !== cached.ruleIndex
    );
  }

  /**
   * Synchronous, atomic final check. It re-resolves the cached ticket against
   * the current domain head and requires one rule to authorize both the MX and
   * the key in that same declaration.
   */
  authorizeDelivery(
    handshakeOrId: DeliveryHandshake | string,
  ): DeliveryDecision {
    const handshake = this.resolveHandshake(handshakeOrId);
    if (handshake === undefined) {
      return { authorized: false, reason: "unknown-handshake" };
    }

    if (
      typeof handshakeOrId !== "string" &&
      this.handshakeWasTampered(handshakeOrId, handshake)
    ) {
      return {
        authorized: false,
        reason: "invalid-handshake",
        domain: handshake.domain,
        declarationDigest: handshake.declarationDigest,
        mx: handshake.mx,
        publicKeyDigest: handshake.publicKeyDigest,
      };
    }

    const state = this.stateFor(handshake.domain);
    if (state === undefined) {
      return {
        authorized: false,
        reason: "unknown-domain",
        domain: handshake.domain,
        declarationDigest: handshake.declarationDigest,
        mx: handshake.mx,
        publicKeyDigest: handshake.publicKeyDigest,
      };
    }

    if (state.unresolvedBranches.size > 0) {
      return {
        authorized: false,
        reason: "fork",
        domain: handshake.domain,
        declarationDigest: handshake.declarationDigest,
        mx: handshake.mx,
        publicKeyDigest: handshake.publicKeyDigest,
      };
    }

    const node = this.currentNode(state);
    if (node === undefined || node.digest !== handshake.declarationDigest) {
      return {
        authorized: false,
        reason:
          node === undefined ? "no-active-declaration" : "stale-declaration",
        domain: handshake.domain,
        declarationDigest: handshake.declarationDigest,
        mx: handshake.mx,
        publicKeyDigest: handshake.publicKeyDigest,
      };
    }

    const status = this.declarationStatus(state, node);
    if (!status.ok) {
      return {
        authorized: false,
        reason: status.reason,
        domain: handshake.domain,
        declarationDigest: handshake.declarationDigest,
        mx: handshake.mx,
        publicKeyDigest: handshake.publicKeyDigest,
      };
    }

    const selection = selectMatchingRule(
      node.declaration.mxRules,
      handshake.mx,
      handshake.publicKeyDigest,
    );
    if (!selection.matched) {
      return {
        authorized: false,
        reason: "no-matching-rule",
        domain: handshake.domain,
        declarationDigest: handshake.declarationDigest,
        mx: handshake.mx,
        publicKeyDigest: handshake.publicKeyDigest,
      };
    }

    return {
      authorized: true,
      reason: "ok",
      domain: handshake.domain,
      declarationDigest: handshake.declarationDigest,
      mx: handshake.mx,
      publicKeyDigest: handshake.publicKeyDigest,
      ruleIndex: selection.ruleIndex,
      ruleHost: selection.rule!.host,
    };
  }

  validateHandshake(
    handshakeOrId: DeliveryHandshake | string,
  ): DeliveryDecision {
    return this.authorizeDelivery(handshakeOrId);
  }

  checkDelivery(
    handshakeOrId: DeliveryHandshake | string,
  ): DeliveryDecision {
    return this.authorizeDelivery(handshakeOrId);
  }

  finalizeDelivery(handshakeOrId: DeliveryHandshake | string): boolean {
    return this.authorizeDelivery(handshakeOrId).authorized;
  }

  finalize(handshakeOrId: DeliveryHandshake | string): boolean {
    return this.finalizeDelivery(handshakeOrId);
  }

  isDeliveryAuthorized(handshakeOrId: DeliveryHandshake | string): boolean {
    return this.authorizeDelivery(handshakeOrId).authorized;
  }

  discardHandshake(handshakeOrId: DeliveryHandshake | string): boolean {
    const id =
      typeof handshakeOrId === "string" ? handshakeOrId : handshakeOrId?.id;
    return typeof id === "string" ? this.handshakes.delete(id) : false;
  }
}

export function createRoutingAuthorization(
  options: RoutingAuthorizationOptions = {},
): RoutingAuthorization {
  return new RoutingAuthorization(options);
}

export const createRoutingAuthority = createRoutingAuthorization;
export const createRouteAuthorizer = createRoutingAuthorization;
export const createAuthorization = createRoutingAuthorization;
export const createRouteAuthorization = createRoutingAuthorization;
export const createDeliveryAuthorizer = createRoutingAuthorization;
export const RouteAuthorizer = RoutingAuthorization;
export const RoutingAuthority = RoutingAuthorization;
export const RouteAuthorization = RoutingAuthorization;
export const DeliveryAuthorizer = RoutingAuthorization;

export type RouteAuthorizer = RoutingAuthorization;
export type RoutingAuthority = RoutingAuthorization;
export type RouteAuthorization = RoutingAuthorization;
export type DeliveryAuthorizer = RoutingAuthorization;

export default RoutingAuthorization;
