export type EventId = number | string;
export type TimeNumber = number;
export type DomainInput = string | readonly string[] | DnsName;
export type PartitionInput = string | NetworkPartition;
export type SourceLayer = 'enterprise' | 'https' | 'network' | 'user' | string;
export type PolicyEffect = 'upgrade' | 'allow';
export type TemporalVerdict =
  | 'always-upgrade'
  | 'never-upgrade'
  | 'evidence-insufficient';

export interface DnsName {
  raw: string;
  labels: string[];
  fqdn: string;
  rootToLeaf: string[];
}

export interface NetworkPartition {
  id: string;
  generation: number | string;
  [coordinate: string]: unknown;
}

export interface TimeInterval {
  start: number;
  end: number;
}

export type TimeSetInput =
  | number
  | TimeInterval
  | readonly [number, number]
  | ReadonlyArray<number | TimeInterval | readonly [number, number]>;

export interface PolicyMetadata {
  [key: string]: unknown;
}

export interface PolicyInput {
  eventId: EventId;
  id?: EventId;
  host: DomainInput;
  name?: DomainInput;
  domain?: DomainInput;
  issuer?: string;
  principal?: string;
  issuedBy?: string;
  subject?: string;
  partition?: PartitionInput;
  networkPartition?: PartitionInput;
  partitionGeneration?: number | string;
  source?: SourceLayer;
  layer?: SourceLayer;
  includeSubDomains?: boolean;
  includeSubdomains?: boolean;
  subdomains?: boolean;
  effect?: PolicyEffect | 'exception' | 'no-upgrade' | 'hsts' | 'enforce';
  decision?: PolicyEffect | 'exception' | 'no-upgrade';
  expiresAt?: number | string | Date;
  expiry?: number | string | Date;
  absoluteExpiry?: number | string | Date;
  expires?: number | string | Date;
  notBefore?: number | string | Date;
  exemptionsAllowed?: boolean;
  exceptionsAllowed?: boolean;
  allowExceptions?: boolean;
  userExceptionsAllowed?: boolean;
  userOverrideAllowed?: boolean;
  canExempt?: boolean;
  noExemptions?: boolean;
  noExemption?: boolean;
  enterpriseNoExemption?: boolean;
  noUserOverride?: boolean;
  doNotOverride?: boolean;
  blockUserOverride?: boolean;
  cannotBeExcepted?: boolean;
  locked?: boolean;
  noOverride?: boolean;
  nonOverridable?: boolean;
  immutable?: boolean;
  deletableBy?: readonly string[];
  metadata?: PolicyMetadata;
  confirmed?: boolean;
}

export interface PolicyRecord {
  kind: 'add';
  eventId: EventId;
  host: DnsName;
  issuer: string;
  partition: NetworkPartition;
  partitionKey: string;
  source: string;
  includeSubDomains: boolean;
  effect: PolicyEffect;
  expiresAt: number;
  notBefore?: number;
  exemptionsAllowed: boolean;
  deletableBy?: string[];
  metadata: PolicyMetadata;
  confirmed: boolean;
}

export interface DeleteInput extends Omit<PolicyInput, 'effect' | 'expiresAt' | 'includeSubDomains'> {
  observedEventId?: EventId;
  observedVersion?: EventId;
  observedId?: EventId;
  version?: EventId;
  targetEventId?: EventId;
  basedOnEventId?: EventId;
  includeSubDomains?: boolean;
  expiresAt?: number | string | Date;
  reason?: string;
  authoritative?: boolean;
}

export interface DeleteRecord {
  kind: 'delete';
  eventId: EventId;
  targetVersion?: EventId;
  host?: DnsName;
  issuer?: string;
  partition?: NetworkPartition;
  partitionKey?: string;
  source?: string;
  includeSubDomains?: boolean;
  reason?: string;
  authoritative: boolean;
  confirmed: boolean;
  metadata: PolicyMetadata;
}

export type PolicyEvent = PolicyRecord | DeleteRecord;
export type EventInput = PolicyInput | DeleteInput | (PolicyInput & { type: string; op?: string });

export interface ApplyResult {
  applied: boolean;
  duplicated?: boolean;
  blocked?: boolean;
  pending?: boolean;
  hiddenByTombstone?: boolean;
  code?: string;
  reason?: string;
  eventId?: EventId;
  targetVersion?: EventId;
}

export interface Candidate {
  record: PolicyRecord;
  match: 'exact' | 'subdomain';
  specificity: number;
  active: boolean;
  deletion: DeletionStatus;
  blockedExceptions?: PolicyRecord[];
}

export interface DeletionStatus {
  status: 'live' | 'deleted' | 'blocked' | 'pending';
  deletions: DeleteRecord[];
  reason?: string;
}

export interface PointDecision {
  at?: number;
  outcome: 'upgrade' | 'allow' | 'none' | 'unknown';
  decision?: 'upgrade' | 'allow' | 'none' | 'unknown';
  shouldUpgrade: boolean;
  upgrade?: boolean;
  requiresUpgrade?: boolean;
  action?: 'upgrade' | 'allow' | 'skip' | 'unknown';
  evidenceSufficient: boolean;
  winner?: PolicyRecord;
  upgrade?: boolean;
  requiresUpgrade?: boolean;
  action?: 'upgrade' | 'allow' | 'skip' | 'unknown';
  candidates: Candidate[];
  blocked?: PolicyRecord[];
  scopeComplete: boolean;
  reasonCodes: string[];
}

export interface TemporalProbe {
  at: number;
  outcome: PointDecision['outcome'];
  shouldUpgrade: boolean;
  evidenceSufficient: boolean;
  winnerEventId?: EventId;
  reasonCodes: string[];
}

export interface TemporalDecision {
  verdict: TemporalVerdict;
  classification?: TemporalVerdict | 'must-upgrade' | 'no-upgrade' | 'unknown';
  result?: TemporalVerdict;
  mustUpgradeAtAllPossibleTimes: boolean;
  noUpgradeAtAnyPossibleTime: boolean;
  evidenceInsufficient: boolean;
  intervals: TimeInterval[];
  probes: TemporalProbe[];
}

export interface DecisionExplanation extends PointDecision {
  host: DnsName;
  queryHost: DnsName;
  partition: NetworkPartition;
  sourcePriority: string[];
  temporal?: TemporalDecision;
  suffixRules: string[];
  basis: Array<{
    eventId: EventId;
    host: string;
    source: string;
    issuer: string;
    effect: PolicyEffect;
    includeSubDomains: boolean;
    expiresAt: number;
    match: 'exact' | 'subdomain';
    active: boolean;
    deletionStatus: DeletionStatus['status'];
  }>;
}

export interface HSTSLibraryOptions {
  sourcePriority?: readonly string[] | Record<string, number> | Array<{ source: string; priority: number }>;
  sourceOrder?: readonly string[] | Record<string, number>;
  sourcePrecedence?: readonly string[] | Record<string, number>;
  priority?: readonly string[] | Record<string, number>;
  sources?: readonly string[] | Record<string, number> | Array<{ source: string; priority: number }>;
  precedence?: 'source-first' | 'specificity-first';
  expiryBoundary?: 'expired-at-expiry' | 'inclusive';
  validateDns?: boolean | 'strict';
  allowCrossLayerDelete?: boolean;
  denyCrossLayerDelete?: boolean;
  enterpriseLockedByDefault?: boolean;
  requireCompleteScopeForNegative?: boolean;
  comparePolicies?: (a: Candidate, b: Candidate) => number;
  now?: () => number;
}

const DEFAULT_PARTITION: NetworkPartition = { id: 'default', generation: 0 };
const UNSPECIFIED_ISSUER = 'unspecified';

function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value) && !(value instanceof Date);
}

function pick<T = unknown>(obj: Record<string, unknown>, keys: string[]): T | undefined {
  for (const key of keys) {
    if (obj[key] !== undefined) return obj[key] as T;
  }
  return undefined;
}

function asBoolean(value: unknown): boolean | undefined {
  if (value === undefined || value === null) return undefined;
  if (typeof value === 'boolean') return value;
  if (value === 'true' || value === 1 || value === '1') return true;
  if (value === 'false' || value === 0 || value === '0') return false;
  return undefined;
}

export function eventKey(id: EventId): string {
  return `${typeof id}:${String(id)}`;
}

export function compareEventId(a: EventId, b: EventId): number {
  if (typeof a === 'number' && typeof b === 'number') return a - b;
  const an = typeof a === 'number' ? String(a) : a;
  const bn = typeof b === 'number' ? String(b) : b;
  if (/^-?\d+$/.test(an) && /^-?\d+$/.test(bn)) {
    const ab = BigInt(an);
    const bb = BigInt(bn);
    return ab < bb ? -1 : ab > bb ? 1 : 0;
  }
  return an < bn ? -1 : an > bn ? 1 : 0;
}

export function maxEventId(ids: readonly EventId[]): EventId | undefined {
  let result: EventId | undefined;
  for (const id of ids) {
    if (result === undefined || compareEventId(id, result) > 0) result = id;
  }
  return result;
}

export function toAbsoluteTime(value: number | string | Date | undefined): number | undefined {
  if (value === undefined) return undefined;
  if (value instanceof Date) return value.getTime();
  if (typeof value === 'number') {
    if (!Number.isFinite(value)) throw new TypeError('Time must be finite');
    return value;
  }
  const parsed = Date.parse(value);
  if (Number.isNaN(parsed)) throw new TypeError(`Invalid absolute time: ${value}`);
  return parsed;
}

export class DomainNormalizationError extends TypeError {}
export class PolicyValidationError extends TypeError {}
export class CheckpointValidationError extends Error {}

export function normalizeDnsName(input: DomainInput, options: { validate?: boolean | 'strict' } = {}): DnsName {
  let raw: string;
  let presetLabels: string[] | undefined;

  if (Array.isArray(input)) {
    raw = input.join('.');
    presetLabels = input.map(String);
  } else if (typeof input === 'string') {
    raw = input;
  } else if (input && typeof input === 'object') {
    const name = input as DnsName | Record<string, unknown>;
    if (Array.isArray((name as DnsName).labels)) {
      presetLabels = [...(name as DnsName).labels];
      raw = (name as DnsName).raw ?? (name as DnsName).fqdn ?? presetLabels.join('.');
    } else {
      raw = String(pick(name as Record<string, unknown>, ['fqdn', 'name', 'host', 'domain']) ?? '');
    }
  } else {
    throw new DomainNormalizationError('A DNS name is required');
  }

  raw = raw.trim();
  if (!raw) throw new DomainNormalizationError('DNS name cannot be empty');

  let labels: string[];
  if (presetLabels) {
    labels = presetLabels.map((label) => String(label).trim().toLowerCase().replace(/\.$/, ''));
  } else {
    const withoutRootDot = raw.replace(/\.+$/u, '');
    let ascii = withoutRootDot;
    if (/[^\x00-\x7F]/u.test(ascii)) {
      try {
        ascii = new URL(`http://${withoutRootDot}`).hostname.replace(/\.+$/u, '');
      } catch (error) {
        throw new DomainNormalizationError(`Unable to IDN-normalize ${raw}: ${(error as Error).message}`);
      }
    }
    labels = ascii.split('.').map((label) => label.trim().toLowerCase());
  }

  if (labels.length === 0 || labels.some((label) => label.length === 0)) {
    throw new DomainNormalizationError(`Invalid DNS label sequence: ${raw}`);
  }

  const strict = options.validate === true || options.validate === 'strict';
  if (strict) {
    for (const label of labels) {
      if (label.length > 63) throw new DomainNormalizationError(`DNS label is longer than 63 octets: ${label}`);
      if (!/^[a-z0-9_*](?:[a-z0-9_-]*[a-z0-9_*])?$/u.test(label) && !label.startsWith('xn--')) {
        throw new DomainNormalizationError(`Invalid DNS label: ${label}`);
      }
    }
    if (labels.join('.').length > 253) throw new DomainNormalizationError('DNS name is longer than 253 octets');
  }

  const fqdn = labels.join('.');
  return { raw, labels, fqdn, rootToLeaf: [...labels].reverse() };
}

export function isSameDnsName(a: DomainInput, b: DomainInput): boolean {
  return normalizeDnsName(a).fqdn === normalizeDnsName(b).fqdn;
}

export function dnsRelation(child: DomainInput, parent: DomainInput): 'equal' | 'subdomain' | 'unrelated' {
  const childName = normalizeDnsName(child);
  const parentName = normalizeDnsName(parent);
  if (childName.labels.length < parentName.labels.length) return 'unrelated';
  const offset = childName.labels.length - parentName.labels.length;
  for (let i = 0; i < parentName.labels.length; i += 1) {
    if (childName.labels[offset + i] !== parentName.labels[i]) return 'unrelated';
  }
  return offset === 0 ? 'equal' : 'subdomain';
}

export function isSubdomainOf(child: DomainInput, parent: DomainInput): boolean {
  return dnsRelation(child, parent) !== 'unrelated';
}

export function normalizePartition(input: PartitionInput | undefined, generation?: number | string): NetworkPartition {
  if (input === undefined || input === null) return { ...DEFAULT_PARTITION };
  if (typeof input === 'string') return { id: input, generation: generation ?? 0 };
  const raw = input as Record<string, unknown>;
  const idValue = raw.id ?? raw.name ?? (typeof raw.partition === 'string' ? raw.partition : undefined) ?? DEFAULT_PARTITION.id;
  const id = String(idValue);
  const gen = (generation
    ?? input.generation
    ?? input.epoch
    ?? input.partitionGeneration
    ?? 0) as number | string;
  return { ...raw, id, generation: gen };
}

export function partitionKey(partition: PartitionInput | undefined, generation?: number | string): string {
  const normalized = normalizePartition(partition, generation);
  return stableStringify(normalized);
}

export class PartitionRegistry {
  readonly #generations = new Map<string, number | string>();

  current(id: string): NetworkPartition {
    const generation = this.#generations.get(id) ?? 0;
    return { id, generation };
  }

  register(id: string, generation: number | string): NetworkPartition {
    this.#generations.set(id, generation);
    return { id, generation };
  }

  reinstall(id: string, nextGeneration?: number | string): NetworkPartition {
    let next: number | string;
    if (nextGeneration !== undefined) {
      next = nextGeneration;
    } else {
      const current = this.#generations.get(id);
      next = typeof current === 'number' ? current + 1 : 1;
    }
    this.#generations.set(id, next);
    return { id, generation: next };
  }
}

interface TrieNode<T> {
  children: Map<string, TrieNode<T>>;
  values: T[];
}

export class DomainTrie<T> {
  readonly root: TrieNode<T> = { children: new Map(), values: [] };

  add(name: DomainInput, value: T): void {
    const dns = normalizeDnsName(name);
    let node = this.root;
    for (const label of dns.rootToLeaf) {
      let next = node.children.get(label);
      if (!next) {
        next = { children: new Map(), values: [] };
        node.children.set(label, next);
      }
      node = next;
    }
    node.values.push(value);
  }

  pathValues(name: DomainInput): T[] {
    const dns = normalizeDnsName(name);
    const values: T[] = [...this.root.values];
    let node: TrieNode<T> | undefined = this.root;
    for (const label of dns.rootToLeaf) {
      node = node?.children.get(label);
      if (!node) break;
      values.push(...node.values);
    }
    return values;
  }
}

export function stableStringify(value: unknown): string {
  return JSON.stringify(sortValue(value));
}

function sortValue(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(sortValue);
  if (value && typeof value === 'object' && !(value instanceof Date)) {
    const source = value as Record<string, unknown>;
    return Object.keys(source)
      .sort()
      .reduce<Record<string, unknown>>((result, key) => {
        const sorted = sortValue(source[key]);
        if (sorted !== undefined) result[key] = sorted;
        return result;
      }, {});
  }
  if (value instanceof Date) return value.toISOString();
  return value;
}

export function fnv1aDigest(value: unknown): string {
  const text = typeof value === 'string' ? value : stableStringify(value);
  let hash = 0x811c9dc5;
  for (let i = 0; i < text.length; i += 1) {
    hash ^= text.charCodeAt(i);
    hash = Math.imul(hash, 0x01000193);
  }
  return `fnv1a32:${(hash >>> 0).toString(16).padStart(8, '0')}`;
}

export const stableDigest = fnv1aDigest;

export function normalizeTimeSet(input: TimeSetInput | undefined): TimeInterval[] {
  if (input === undefined) return [];
  const rawIntervals = Array.isArray(input) && input.length === 2 && typeof input[0] === 'number' && typeof input[1] === 'number'
    ? [input as readonly [number, number]]
    : Array.isArray(input)
      ? input
      : [input];

  const intervals = rawIntervals.map((item) => {
    if (typeof item === 'number') return { start: item, end: item };
    if (Array.isArray(item)) return { start: item[0], end: item[1] };
    const interval = item as TimeInterval;
    return { start: interval.start, end: interval.end };
  });

  for (const interval of intervals) {
    if (!Number.isFinite(interval.start) || !Number.isFinite(interval.end) || interval.start > interval.end) {
      throw new RangeError(`Invalid closed time interval: ${stableStringify(interval)}`);
    }
  }

  intervals.sort((a, b) => a.start - b.start || a.end - b.end);
  const merged: TimeInterval[] = [];
  for (const interval of intervals) {
    const previous = merged[merged.length - 1];
    if (previous && interval.start <= previous.end) {
      previous.end = Math.max(previous.end, interval.end);
    } else {
      merged.push({ ...interval });
    }
  }
  return merged;
}

export function timeSetContains(timeSet: readonly TimeInterval[], time: number): boolean {
  return timeSet.some((interval) => time >= interval.start && time <= interval.end);
}

export interface QueryOptions {
  at?: number | string | Date;
  time?: TimeSetInput;
  possibleTime?: TimeSetInput;
  trustedTime?: TimeSetInput;
  timeSet?: TimeSetInput;
  partition?: PartitionInput;
  networkPartition?: PartitionInput;
  partitionId?: string;
  partitionGeneration?: number | string;
  issuer?: string;
  source?: string;
  requireCompleteScopeForNegative?: boolean;
  includeInactive?: boolean;
}

function queryPartition(options: QueryOptions): NetworkPartition {
  const partitionInput = options.partition
    ?? options.networkPartition
    ?? (options.partitionId === undefined ? undefined : { id: options.partitionId });
  return normalizePartition(partitionInput ?? DEFAULT_PARTITION, options.partitionGeneration);
}

export interface SerializedLibrary {
  format: 'hsts-policy-library';
  version: 1;
  options?: HSTSLibraryOptions;
  events: unknown[];
  confirmedVersions: EventId[];
  scopeSummaries?: SignedScopeSummary[];
}

export interface ScopeSummary {
  kind: 'hsts-scope-summary';
  version: 1;
  signer: string;
  prefix: DnsName;
  partition: NetworkPartition;
  partitionKey: string;
  complete: true;
  throughEventId?: EventId;
  digest: string;
  confirmedVersions?: EventId[];
  issuedAt?: number;
  expiresAt?: number;
  metadata?: PolicyMetadata;
}

export interface SignedScopeSummary extends ScopeSummary {
  algorithm: 'HMAC-SHA256';
  signature: string;
}

export interface PolicyCheckpoint {
  kind: 'hsts-policy-checkpoint';
  formatVersion: 1;
  createdAt: number;
  host?: string;
  partition: NetworkPartition;
  partitionGeneration: string;
  appliedThroughEventId?: EventId;
  eventCount: number;
  stateDigest: string;
  suffixRules: Array<Record<string, unknown>>;
  suffixRulesDigest: string;
  confirmedVersions: EventId[];
  scopeSummaryDigests: string[];
  snapshot?: SerializedLibrary;
  signature?: string;
  algorithm?: 'HMAC-SHA256';
}

function normalizeEffect(value: unknown): PolicyEffect {
  if (value === undefined || value === null) return 'upgrade';
  const effect = String(value).toLowerCase();
  if (['allow', 'exception', 'exempt', 'no-upgrade', 'skip', 'user-exception'].includes(effect)) return 'allow';
  if (['upgrade', 'hsts', 'enforce', 'must-upgrade'].includes(effect)) return 'upgrade';
  throw new PolicyValidationError(`Unsupported policy effect: ${String(value)}`);
}

function normalizeSourcePriority(
  input: HSTSLibraryOptions['sourcePriority'] | HSTSLibraryOptions['sourceOrder'],
): { ranks: Map<string, number>; ordered: string[] } {
  const ranks = new Map<string, number>();
  if (!input) {
    const fallback = ['enterprise', 'https', 'network', 'user'];
    fallback.forEach((source, index) => ranks.set(source, fallback.length - index));
    return { ranks, ordered: fallback };
  }

  if (Array.isArray(input)) {
    input.forEach((source, index) => ranks.set(String(source), input.length - index));
    return { ranks, ordered: input.map(String) };
  }

  if (isObject(input)) {
    const entries = Object.entries(input as Record<string, number>);
    for (const [source, priority] of entries) ranks.set(source, Number(priority));
    const ordered = entries.sort((a, b) => Number(b[1]) - Number(a[1])).map(([source]) => source);
    return { ranks, ordered };
  }

  const entries = [...input];
  for (const entry of entries) ranks.set(entry.source, entry.priority);
  return {
    ranks,
    ordered: entries.sort((a, b) => b.priority - a.priority).map((entry) => entry.source),
  };
}

function normalizeIssuer(raw: Record<string, unknown>): string {
  const issuer = pick<unknown>(raw, ['issuer', 'issuingPrincipal', 'principal', 'issuedBy', 'subject']);
  if (issuer === undefined || issuer === null) return UNSPECIFIED_ISSUER;
  if (typeof issuer === 'string') return issuer;
  if (isObject(issuer)) {
    const id = pick<string>(issuer, ['id', 'principal', 'name', 'commonName', 'dn']);
    return id ?? stableStringify(issuer);
  }
  return String(issuer);
}

function normalizePolicy(input: EventInput, options: HSTSLibraryOptions): PolicyRecord {
  const raw = input as PolicyInput & Record<string, unknown>;
  const eventId = pick<EventId>(raw, ['eventId', 'id', 'version']);
  if (eventId === undefined) throw new PolicyValidationError('add event requires a monotonic eventId');

  const hostInput = pick<DomainInput>(raw, ['host', 'name', 'domain', 'dnsName', 'dns', 'labels', 'dnsLabels']);
  if (hostInput === undefined) throw new PolicyValidationError(`add event ${String(eventId)} requires a host`);
  const host = normalizeDnsName(hostInput, { validate: options.validateDns });

  const issuer = normalizeIssuer(raw);
  const partitionInput = pick<PartitionInput>(raw, ['partition', 'networkPartition']);
  const partition = normalizePartition(partitionInput, raw.partitionGeneration as number | string | undefined);
  const source = String(pick<string>(raw, ['source', 'layer']) ?? 'unknown');

  const includeSubDomains = asBoolean(
    pick(raw, ['includeSubDomains', 'includeSubdomains', 'includeSubDomain', 'subdomains']),
  ) ?? false;

  const positiveExemption = asBoolean(
    pick(raw, [
      'exemptionsAllowed',
      'exceptionsAllowed',
      'allowExemptions',
      'allowExceptions',
      'userExceptionsAllowed',
      'userOverrideAllowed',
      'canExempt',
    ]),
  );
  const forbidExemption = asBoolean(
    pick(raw, [
      'noExemptions',
      'noExemption',
      'enterpriseNoExemption',
      'noUserOverride',
      'doNotAllowExceptions',
      'doNotOverride',
      'blockUserOverride',
      'cannotBeExcepted',
      'locked',
      'noOverride',
      'nonOverridable',
      'immutable',
      'pinned',
    ]),
  );
  let exemptionsAllowed: boolean;
  if (positiveExemption !== undefined) exemptionsAllowed = positiveExemption;
  else if (forbidExemption !== undefined) exemptionsAllowed = !forbidExemption;
  else if (options.enterpriseLockedByDefault && source === 'enterprise') exemptionsAllowed = false;
  else exemptionsAllowed = true;

  const expiresAt = toAbsoluteTime(
    pick<number | string | Date>(raw, ['expiresAt', 'expiry', 'absoluteExpiry', 'expires']),
  );
  if (expiresAt === undefined) {
    throw new PolicyValidationError(`policy event ${String(eventId)} requires an absolute expiresAt time`);
  }
  const notBefore = toAbsoluteTime(pick(raw, ['notBefore']));
  const effect = normalizeEffect(pick(raw, ['effect', 'decision', 'policy']));
  const deletableBy = pick<readonly string[]>(raw, ['deletableBy']);
  const metadata = (pick<PolicyMetadata>(raw, ['metadata']) ?? {}) as PolicyMetadata;
  const confirmed = asBoolean(pick(raw, ['confirmed'])) ?? false;

  return {
    kind: 'add',
    eventId,
    host,
    issuer: String(issuer),
    partition,
    partitionKey: partitionKey(partition),
    source,
    includeSubDomains,
    effect,
    expiresAt,
    notBefore,
    exemptionsAllowed,
    deletableBy: deletableBy ? [...deletableBy].map(String) : undefined,
    metadata,
    confirmed,
  };
}

function normalizeDelete(input: EventInput, options: HSTSLibraryOptions): DeleteRecord {
  const raw = input as DeleteInput & Record<string, unknown>;
  const eventId = pick<EventId>(raw, ['eventId', 'id']) ?? 0 as EventId;
  const targetVersion = pick<EventId>(raw, [
    'targetVersion',
    'observedEventId',
    'observedVersion',
    'observedId',
    'version',
    'targetEventId',
    'basedOnEventId',
  ]);

  const hostInput = pick<DomainInput>(raw, ['host', 'name', 'domain', 'dnsName', 'dns', 'labels', 'dnsLabels']);
  const partitionInput = pick<PartitionInput>(raw, ['partition', 'networkPartition']);
  const partition = partitionInput === undefined
    ? undefined
    : normalizePartition(partitionInput, raw.partitionGeneration as number | string | undefined);
  const issuerValue = normalizeIssuer(raw);
  const issuer = raw.issuer === undefined && raw.issuingPrincipal === undefined && raw.principal === undefined && raw.issuedBy === undefined && raw.subject === undefined
    ? undefined
    : issuerValue;
  const source = pick<string>(raw, ['source', 'layer']);
  const includeSubDomains = asBoolean(pick(raw, ['includeSubDomains', 'includeSubdomains', 'subdomains']));

  return {
    kind: 'delete',
    eventId,
    targetVersion,
    host: hostInput === undefined ? undefined : normalizeDnsName(hostInput, { validate: options.validateDns }),
    issuer: issuer === undefined ? undefined : String(issuer),
    partition,
    partitionKey: partition ? partitionKey(partition) : undefined,
    source: source === undefined ? undefined : String(source),
    includeSubDomains,
    reason: pick<string>(raw, ['reason']),
    authoritative: asBoolean(pick(raw, ['authoritative'])) ?? false,
    confirmed: asBoolean(pick(raw, ['confirmed'])) ?? false,
    metadata: (pick<PolicyMetadata>(raw, ['metadata']) ?? {}) as PolicyMetadata,
  };
}

function eventKind(input: EventInput): 'add' | 'delete' {
  const raw = input as Record<string, unknown>;
  const explicit = String(pick(raw, ['kind', 'type', 'op', 'eventType']) ?? '').toLowerCase();
  const lifecycle = String(pick(raw, ['effect', 'operation', 'status']) ?? '').toLowerCase();
  if (
    explicit === 'delete'
    || explicit === 'remove'
    || explicit === 'tombstone'
    || explicit === 'deleted'
    || lifecycle === 'delete'
    || lifecycle === 'deleted'
    || lifecycle === 'remove'
  ) return 'delete';
  if (explicit === 'add' || explicit === 'put' || explicit === 'upsert' || explicit === 'policy' || explicit === 'added') return 'add';
  if (
    'observedEventId' in raw
    || 'observedVersion' in raw
    || 'targetEventId' in raw
    || raw.deleted === true
    || raw.tombstone === true
    || raw.removed === true
  ) return 'delete';
  return 'add';
}

export class HSTSPolicyLibrary {
  readonly options: HSTSLibraryOptions;
  readonly trie = new DomainTrie<EventId>();
  readonly records = new Map<string, PolicyRecord>();
  readonly events: PolicyEvent[] = [];
  readonly deletionsByTarget = new Map<string, DeleteRecord[]>();
  readonly confirmedVersions = new Set<string>();
  readonly scopeSummaries: SignedScopeSummary[] = [];
  readonly #sourcePriority: { ranks: Map<string, number>; ordered: string[] };

  constructor(options: HSTSLibraryOptions = {}) {
    this.options = { expiryBoundary: 'expired-at-expiry', precedence: 'source-first', ...options };
    const priorityInput = options.sourcePriority
      ?? options.sourcePrecedence
      ?? options.sourceOrder
      ?? options.priority
      ?? options.sources;
    this.#sourcePriority = normalizeSourcePriority(priorityInput);
  }

  get sourcePriority(): string[] {
    return [...this.#sourcePriority.ordered];
  }

  sourceRank(source: string): number {
    return this.#sourcePriority.ranks.get(source) ?? 0;
  }

  add(input: PolicyInput): ApplyResult {
    return this.applyEvent({ ...input, kind: 'add' } as EventInput);
  }

  addPolicy(input: PolicyInput): ApplyResult {
    return this.add(input);
  }

  recordAdd(input: PolicyInput): ApplyResult {
    return this.add(input);
  }

  put(input: PolicyInput): ApplyResult {
    return this.add(input);
  }

  delete(input: DeleteInput): ApplyResult;
  delete(host: DomainInput, observedVersion?: EventId | Partial<DeleteInput>, extra?: Partial<DeleteInput>): ApplyResult;
  delete(
    input: DeleteInput | DomainInput,
    observedVersion?: EventId | Partial<DeleteInput>,
    maybeExtra?: Partial<DeleteInput>,
  ): ApplyResult {
    let normalizedInput: DeleteInput;
    if (typeof input === 'string' || Array.isArray(input) || isDnsName(input)) {
      const versionIsOptions = isObject(observedVersion);
      normalizedInput = {
        eventId: maybeExtra?.eventId ?? (versionIsOptions ? (observedVersion as Partial<DeleteInput>).eventId : undefined),
        host: input,
        ...(versionIsOptions ? observedVersion as Partial<DeleteInput> : { observedEventId: observedVersion as EventId | undefined }),
        ...(maybeExtra ?? {}),
      } as DeleteInput;
    } else {
      normalizedInput = input;
    }
    return this.applyEvent({ ...normalizedInput, kind: 'delete' } as EventInput);
  }

  deleteRule(input: DeleteInput | DomainInput, observedVersion?: EventId | Partial<DeleteInput>, extra?: Partial<DeleteInput>): ApplyResult {
    return this.delete(input as DeleteInput & DomainInput, observedVersion as EventId, extra);
  }

  removeRule(input: DeleteInput | DomainInput, observedVersion?: EventId | Partial<DeleteInput>, extra?: Partial<DeleteInput>): ApplyResult {
    return this.deleteRule(input, observedVersion, extra);
  }

  revoke(input: DeleteInput | DomainInput, observedVersion?: EventId | Partial<DeleteInput>, extra?: Partial<DeleteInput>): ApplyResult {
    return this.deleteRule(input, observedVersion, extra);
  }

  recordDelete(input: DeleteInput): ApplyResult {
    return this.delete(input);
  }

  remove(input: DeleteInput): ApplyResult {
    return this.delete(input);
  }

  tombstone(input: DeleteInput): ApplyResult {
    return this.delete(input);
  }

  applyEvent(input: EventInput): ApplyResult {
    const kind = eventKind(input);
    if (kind === 'delete') {
      const raw = input as Record<string, unknown>;
      if (pick<EventId>(raw, ['eventId', 'id']) === undefined) {
        raw.eventId = this.#nextEventId();
      }
      return this.#applyDelete(normalizeDelete(input, this.options));
    }
    return this.#applyAdd(normalizePolicy(input, this.options));
  }

  #nextEventId(): EventId {
    let next = 0;
    for (const event of this.events) {
      if (typeof event.eventId === 'number' && Number.isFinite(event.eventId)) next = Math.max(next, event.eventId);
      else if (typeof event.eventId === 'string' && /^\d+$/.test(event.eventId)) {
        next = Math.max(next, Number(BigInt(event.eventId)));
      }
    }
    return next + 1;
  }

  applyEvents(events: readonly EventInput[]): ApplyResult[] {
    return events.map((event) => this.applyEvent(event));
  }

  #applyAdd(record: PolicyRecord): ApplyResult {
    const key = eventKey(record.eventId);
    const existing = this.records.get(key);
    if (existing) {
      if (stableStringify(existing) === stableStringify(record)) {
        return { applied: false, duplicated: true, eventId: record.eventId, code: 'duplicate-event' };
      }
      throw new PolicyValidationError(`Monotonic event id ${String(record.eventId)} was already used by a different event`);
    }

    this.records.set(key, record);
    this.trie.add(record.host, record.eventId);
    this.events.push(record);
    if (record.confirmed) this.confirmedVersions.add(key);

    const deletion = this.deletionStatus(record);
    return {
      applied: true,
      eventId: record.eventId,
      hiddenByTombstone: deletion.status === 'deleted',
      blocked: deletion.status === 'blocked',
      code: deletion.status === 'deleted'
        ? 'added-but-version-is-deleted'
        : deletion.status === 'blocked'
          ? 'unauthorized-delete-blocked'
          : undefined,
    };
  }

  #applyDelete(del: DeleteRecord): ApplyResult {
    if (del.targetVersion === undefined) {
      if (!del.host) {
        return { applied: false, code: 'observed-version-required', reason: 'A delete needs an observedEventId or an exact host to observe' };
      }
      const matches = [...this.records.values()]
        .filter((record) => {
          if (del.host && record.host.fqdn !== del.host.fqdn) return false;
          if (del.partitionKey && record.partitionKey !== del.partitionKey) return false;
          if (del.issuer && record.issuer !== del.issuer) return false;
          if (del.source && record.source !== del.source) return false;
          if (del.includeSubDomains !== undefined && record.includeSubDomains !== del.includeSubDomains) return false;
          return true;
        })
        .sort((a, b) => compareEventId(b.eventId, a.eventId));
      if (matches.length === 0) {
        return { applied: false, code: 'no-observed-version', eventId: del.eventId, reason: 'No matching rule version is currently observed' };
      }
      del.targetVersion = matches[0].eventId;
    }

    const targetVersion = del.targetVersion;
    const targetKey = eventKey(del.targetVersion);
    const bucket = this.deletionsByTarget.get(targetKey) ?? [];
    const duplicate = bucket.some((existing) => eventKey(existing.eventId) === eventKey(del.eventId));
    if (duplicate) return { applied: false, duplicated: true, eventId: del.eventId, targetVersion };

    bucket.push(del);
    this.deletionsByTarget.set(targetKey, bucket);
    this.events.push(del);
    if (del.confirmed) this.confirmedVersions.add(targetKey);

    const target = this.records.get(targetKey);
    if (!target) {
      return { applied: true, pending: true, eventId: del.eventId, targetVersion, code: 'tombstone-recorded-for-unknown-version' };
    }

    const mismatch = this.#targetMismatch(target, del);
    if (mismatch) {
      return { applied: false, blocked: true, eventId: del.eventId, targetVersion, code: mismatch };
    }
    const authority = this.#deletionAuthority(target, del);
    if (!authority.allowed) {
      return { applied: false, blocked: true, eventId: del.eventId, targetVersion, code: authority.reason };
    }
    return { applied: true, eventId: del.eventId, targetVersion, code: 'version-deleted' };
  }

  #targetMismatch(record: PolicyRecord, del: DeleteRecord): string | undefined {
    if (del.host && del.host.fqdn !== record.host.fqdn) return 'delete-target-host-mismatch';
    if (del.issuer && del.issuer !== record.issuer) return 'delete-target-issuer-mismatch';
    if (del.partitionKey && del.partitionKey !== record.partitionKey) return 'delete-target-partition-mismatch';
    if (del.includeSubDomains !== undefined && del.includeSubDomains !== record.includeSubDomains) {
      return 'delete-target-scope-mismatch';
    }
    return undefined;
  }

  #deletionAuthority(record: PolicyRecord, del: DeleteRecord): { allowed: boolean; reason?: string } {
    if (this.options.allowCrossLayerDelete) return { allowed: true };
    if (del.authoritative) return { allowed: true };
    const deleteSource = del.source ?? record.source;
    if (deleteSource === record.source) return { allowed: true };
    if (record.deletableBy?.includes(deleteSource)) return { allowed: true };
    if (record.exemptionsAllowed === false) {
      return { allowed: false, reason: 'enterprise-no-exemption-delete-blocked' };
    }
    if (this.options.denyCrossLayerDelete) return { allowed: false, reason: 'cross-layer-delete-denied' };
    return { allowed: true };
  }

  confirmVersion(version: EventId): void {
    this.confirmedVersions.add(eventKey(version));
  }

  isConfirmedDeleted(version: EventId): boolean {
    return this.confirmedVersions.has(eventKey(version));
  }

  deletionStatus(record: PolicyRecord): DeletionStatus {
    const key = eventKey(record.eventId);
    if (this.confirmedVersions.has(key)) {
      return { status: 'deleted', deletions: this.deletionsByTarget.get(key) ?? [], reason: 'confirmed-tombstone' };
    }
    const deletions = this.deletionsByTarget.get(key) ?? [];
    const blocked: DeleteRecord[] = [];
    for (const deletion of deletions) {
      if (this.#targetMismatch(record, deletion)) continue;
      const authority = this.#deletionAuthority(record, deletion);
      if (authority.allowed) return { status: 'deleted', deletions: [deletion] };
      blocked.push(deletion);
    }
    if (blocked.length > 0) {
      return { status: 'blocked', deletions: blocked, reason: 'enterprise-no-exemption-delete-blocked' };
    }
    return { status: 'live', deletions: [] };
  }

  isActiveAt(record: PolicyRecord, at: number): boolean {
    if (record.notBefore !== undefined && at < record.notBefore) return false;
    return this.options.expiryBoundary === 'inclusive' ? at <= record.expiresAt : at < record.expiresAt;
  }

  collectCandidates(hostInput: DomainInput, partitionInput?: PartitionInput): {
    host: DnsName;
    partition: NetworkPartition;
    candidates: Candidate[];
  } {
    const host = normalizeDnsName(hostInput, { validate: this.options.validateDns });
    const partition = normalizePartition(partitionInput ?? DEFAULT_PARTITION);
    const ids = this.trie.pathValues(host);
    const candidates: Candidate[] = [];

    for (const id of ids) {
      const record = this.records.get(eventKey(id));
      if (!record || record.partitionKey !== partitionKey(partition)) continue;
      const relation = dnsRelation(host, record.host);
      if (relation === 'unrelated') continue;
      const match = relation === 'equal' ? 'exact' : 'subdomain';
      if (match === 'subdomain' && !record.includeSubDomains) continue;
      const deletion = this.deletionStatus(record);
      candidates.push({
        record,
        match,
        specificity: record.host.labels.length,
        active: true,
        deletion,
      });
    }
    return { host, partition, candidates };
  }

  #compareCandidates(a: Candidate, b: Candidate): number {
    if (this.options.comparePolicies) return this.options.comparePolicies(a, b);
    const precedence = this.options.precedence ?? 'source-first';
    if (precedence === 'specificity-first') {
      if (a.specificity !== b.specificity) return a.specificity - b.specificity;
      const source = this.sourceRank(a.record.source) - this.sourceRank(b.record.source);
      if (source !== 0) return source;
    } else {
      const source = this.sourceRank(a.record.source) - this.sourceRank(b.record.source);
      if (source !== 0) return source;
      if (a.specificity !== b.specificity) return a.specificity - b.specificity;
    }
    const event = compareEventId(a.record.eventId, b.record.eventId);
    if (event !== 0) return event;
    return b.record.expiresAt - a.record.expiresAt;
  }

  decideAt(
    hostInput: DomainInput,
    at: number | undefined,
    options: QueryOptions = {},
  ): DecisionExplanation {
    const partition = queryPartition(options);
    const { host, candidates: allCandidates } = this.collectCandidates(hostInput, partition);
    const issuerFiltered = options.issuer
      ? allCandidates.filter((candidate) => candidate.record.issuer === options.issuer)
      : allCandidates;
    const sourceFiltered = options.source
      ? issuerFiltered.filter((candidate) => candidate.record.source === options.source)
      : issuerFiltered;

    for (const candidate of sourceFiltered) {
      candidate.active = at === undefined ? true : this.isActiveAt(candidate.record, at);
    }

    const visible = sourceFiltered.filter((candidate) => candidate.deletion.status !== 'deleted');
    const active = options.includeInactive
      ? visible
      : visible.filter((candidate) => candidate.active);
    const lockedUpgrades = active.filter(
      (candidate) => !candidate.record.exemptionsAllowed && candidate.record.effect === 'upgrade',
    );
    const blocked = active.filter(
      (candidate) => candidate.record.effect === 'allow' && lockedUpgrades.length > 0,
    ).map((candidate) => candidate.record);

    let winner: Candidate | undefined;
    const reasonCodes = new Set<string>();
    let outcome: PointDecision['outcome'] = 'none';

    if (lockedUpgrades.length > 0) {
      lockedUpgrades.sort((a, b) => this.#compareCandidates(b, a));
      winner = lockedUpgrades[0];
      outcome = 'upgrade';
      reasonCodes.add('mandatory-enterprise-rule');
      if (blocked.length > 0) reasonCodes.add('user-exception-blocked');
    } else if (active.length > 0) {
      const sorted = [...active].sort((a, b) => this.#compareCandidates(b, a));
      winner = sorted[0];
      outcome = winner.record.effect === 'upgrade' ? 'upgrade' : 'allow';
      reasonCodes.add(winner.match === 'exact' ? 'exact-rule' : 'suffix-inheritance');
      reasonCodes.add(`source:${winner.record.source}`);
    }

    if (!winner) reasonCodes.add('no-active-policy');
    if (sourceFiltered.some((candidate) => candidate.deletion.status === 'blocked')) {
      reasonCodes.add('unauthorized-delete-blocked');
    }

    const scopeComplete = this.isScopeComplete(host, partition);
    const requireScope = options.requireCompleteScopeForNegative
      ?? this.options.requireCompleteScopeForNegative
      ?? false;
    let evidenceSufficient = true;
    if (!winner && outcome === 'none' && requireScope && !scopeComplete) {
      outcome = 'unknown';
      evidenceSufficient = false;
      reasonCodes.add('incomplete-sync-scope');
    }
    if (scopeComplete) reasonCodes.add('scope-complete');

    const suffixRules = sourceFiltered
      .map((candidate) => candidate.record.host.fqdn)
      .filter((value, index, values) => values.indexOf(value) === index)
      .sort((a, b) => a.split('.').length - b.split('.').length || a.localeCompare(b));

    const basis = sourceFiltered.map((candidate) => ({
      eventId: candidate.record.eventId,
      host: candidate.record.host.fqdn,
      source: candidate.record.source,
      issuer: candidate.record.issuer,
      effect: candidate.record.effect,
      includeSubDomains: candidate.record.includeSubDomains,
      expiresAt: candidate.record.expiresAt,
      match: candidate.match,
      active: candidate.active,
      deletionStatus: candidate.deletion.status,
    }));

    return {
      host,
      queryHost: host,
      partition,
      at,
      outcome,
      decision: outcome,
      shouldUpgrade: outcome === 'upgrade',
      upgrade: outcome === 'upgrade',
      requiresUpgrade: outcome === 'upgrade',
      action: outcome === 'upgrade'
        ? 'upgrade'
        : outcome === 'allow'
          ? 'allow'
          : outcome === 'unknown'
            ? 'unknown'
            : 'skip',
      evidenceSufficient,
      winner: winner?.record,
      candidates: sourceFiltered,
      blocked,
      scopeComplete,
      reasonCodes: [...reasonCodes],
      sourcePriority: this.sourcePriority,
      suffixRules,
      basis,
    };
  }

  evaluate(host: DomainInput, options: QueryOptions = {}): DecisionExplanation & { temporal?: TemporalDecision } {
    const atInput = options.at ?? this.options.now?.();
    const timeInput = options.time ?? options.possibleTime ?? options.trustedTime ?? options.timeSet;
    if (timeInput !== undefined) {
      const temporal = this.evaluateTemporal(host, timeInput, options);
      const middle = temporal.probes[Math.floor(temporal.probes.length / 2)];
      const at = middle?.at ?? (atInput === undefined ? undefined : toAbsoluteTime(atInput));
      const point = this.decideAt(host, at, options);
      return { ...point, temporal };
    }
    const at = atInput === undefined ? undefined : toAbsoluteTime(atInput);
    return this.decideAt(host, at, options);
  }

  explain(host: DomainInput, options: QueryOptions = {}): DecisionExplanation & { temporal?: TemporalDecision } {
    return this.evaluate(host, options);
  }

  query(host: DomainInput, options: QueryOptions = {}) {
    return this.evaluate(host, options);
  }

  shouldUpgrade(host: DomainInput, atOrOptions?: number | string | Date | QueryOptions): boolean {
    const options = isObject(atOrOptions)
      ? atOrOptions as QueryOptions
      : { at: atOrOptions as number | string | Date | undefined };
    return this.evaluate(host, options).shouldUpgrade;
  }

  evaluateTemporal(host: DomainInput, timeInput: TimeSetInput, options: QueryOptions = {}): TemporalDecision {
    const intervals = normalizeTimeSet(timeInput);
    const partition = queryPartition(options);
    const { candidates } = this.collectCandidates(host, partition);
    const expiries = candidates.map((candidate) => candidate.record.expiresAt);
    const probesTimes = new Set<number>();

    for (const interval of intervals) {
      probesTimes.add(interval.start);
      probesTimes.add(interval.end);
      for (const expiry of expiries) {
        if (expiry >= interval.start && expiry <= interval.end) probesTimes.add(expiry);
      }
      const critical = [...probesTimes]
        .filter((time) => time >= interval.start && time <= interval.end)
        .sort((a, b) => a - b);
      for (let i = 1; i < critical.length; i += 1) {
        const previous = critical[i - 1];
        const current = critical[i];
        if (current - previous > 0) probesTimes.add((previous + current) / 2);
      }
      if (critical.length === 0 && Number.isFinite(interval.start)) probesTimes.add(interval.start);
    }

    const probes: TemporalProbe[] = [...probesTimes]
      .filter((time) => timeSetContains(intervals, time))
      .sort((a, b) => a - b)
      .map((time) => {
        const decision = this.decideAt(host, time, { ...options, at: time });
        return {
          at: time,
          outcome: decision.outcome,
          shouldUpgrade: decision.shouldUpgrade,
          evidenceSufficient: decision.evidenceSufficient,
          winnerEventId: decision.winner?.eventId,
          reasonCodes: decision.reasonCodes,
        };
      });

    const classes = new Set(probes.map((probe) => {
      if (probe.shouldUpgrade) return 'upgrade';
      return probe.evidenceSufficient ? 'negative' : 'unknown';
    }));

    let verdict: TemporalVerdict;
    if (classes.size === 1 && classes.has('upgrade')) verdict = 'always-upgrade';
    else if (classes.size === 1 && classes.has('negative')) verdict = 'never-upgrade';
    else verdict = 'evidence-insufficient';

    return {
      verdict,
      classification: verdict === 'always-upgrade'
        ? 'must-upgrade'
        : verdict === 'never-upgrade'
          ? 'no-upgrade'
          : 'unknown',
      result: verdict,
      mustUpgradeAtAllPossibleTimes: verdict === 'always-upgrade',
      noUpgradeAtAnyPossibleTime: verdict === 'never-upgrade',
      evidenceInsufficient: verdict === 'evidence-insufficient',
      intervals,
      probes,
    };
  }

  isScopeComplete(hostInput: DomainInput, partitionInput?: PartitionInput): boolean {
    const host = normalizeDnsName(hostInput, { validate: this.options.validateDns });
    const partition = normalizePartition(partitionInput ?? DEFAULT_PARTITION);
    return this.scopeSummaries.some((summary) => {
      if (summary.partitionKey !== partitionKey(partition)) return false;
      if (summary.complete !== true) return false;
      return isSubdomainOf(host, summary.prefix);
    });
  }

  addScopeSummary(summary: SignedScopeSummary): void {
    this.scopeSummaries.push(summary);
    for (const version of summary.confirmedVersions ?? []) this.confirmVersion(version);
  }

  toJSON(): SerializedLibrary {
    return {
      format: 'hsts-policy-library',
      version: 1,
      options: this.options,
      events: this.events.map((event) => serializeEvent(event)),
      confirmedVersions: [...this.confirmedVersions].map((key) => key.slice(key.indexOf(':') + 1)),
      scopeSummaries: this.scopeSummaries,
    };
  }

  snapshot(): SerializedLibrary {
    return this.toJSON();
  }

  static fromJSON(json: SerializedLibrary | string, options?: HSTSLibraryOptions): HSTSPolicyLibrary {
    const parsed = typeof json === 'string' ? JSON.parse(json) as SerializedLibrary : json;
    const library = new HSTSPolicyLibrary(options ?? parsed.options);
    for (const event of parsed.events) library.applyEvent(event as EventInput);
    for (const version of parsed.confirmedVersions ?? []) library.confirmVersion(version);
    for (const summary of parsed.scopeSummaries ?? []) library.addScopeSummary(summary);
    return library;
  }

  static replay(events: readonly EventInput[], options?: HSTSLibraryOptions): HSTSPolicyLibrary {
    return replayPolicyEvents(events, options);
  }
}

function isDnsName(value: unknown): value is DnsName {
  return isObject(value) && Array.isArray((value as DnsName).labels) && typeof (value as DnsName).fqdn === 'string';
}

export function serializeEvent(event: PolicyEvent): Record<string, unknown> {
  if (event.kind === 'add') {
    return {
      type: 'add',
      eventId: event.eventId,
      host: event.host.fqdn,
      issuer: event.issuer,
      partition: event.partition,
      source: event.source,
      includeSubDomains: event.includeSubDomains,
      effect: event.effect,
      expiresAt: event.expiresAt,
      notBefore: event.notBefore,
      exemptionsAllowed: event.exemptionsAllowed,
      deletableBy: event.deletableBy,
      metadata: event.metadata,
      confirmed: event.confirmed,
    };
  }
  return {
    type: 'delete',
    eventId: event.eventId,
    observedEventId: event.targetVersion,
    host: event.host?.fqdn,
    issuer: event.issuer,
    partition: event.partition,
    source: event.source,
    includeSubDomains: event.includeSubDomains,
    reason: event.reason,
    authoritative: event.authoritative,
    confirmed: event.confirmed,
    metadata: event.metadata,
  };
}

export function replayPolicyEvents(
  events: readonly EventInput[],
  options?: HSTSLibraryOptions,
): HSTSPolicyLibrary {
  const library = new HSTSPolicyLibrary(options);
  const normalized = events.map((event) => {
    const kind = eventKind(event);
    return kind === 'delete'
      ? normalizeDelete(event, library.options)
      : normalizePolicy(event, library.options);
  });
  normalized.sort((a, b) => compareEventId(a.eventId, b.eventId));
  for (const event of normalized) library.applyEvent(event as EventInput);
  return library;
}

function getWebCrypto(): Crypto {
  const cryptoObject = globalThis.crypto as Crypto | undefined;
  if (!cryptoObject?.subtle) throw new Error('WebCrypto subtle API is required for signed digests');
  return cryptoObject;
}

function bytesToBase64(bytes: Uint8Array): string {
  let binary = '';
  const chunkSize = 0x8000;
  for (let i = 0; i < bytes.length; i += chunkSize) {
    binary += String.fromCharCode(...bytes.subarray(i, i + chunkSize));
  }
  return btoa(binary);
}

export async function sha256Hex(value: unknown): Promise<string> {
  const subtle = getWebCrypto().subtle;
  const bytes = new TextEncoder().encode(typeof value === 'string' ? value : stableStringify(value));
  const digest = await subtle.digest('SHA-256', bytes);
  return [...new Uint8Array(digest)].map((byte) => byte.toString(16).padStart(2, '0')).join('');
}

export async function hmacSha256Base64(secret: string, message: string): Promise<string> {
  const subtle = getWebCrypto().subtle;
  const key = await subtle.importKey(
    'raw',
    new TextEncoder().encode(secret),
    { name: 'HMAC', hash: 'SHA-256' },
    false,
    ['sign'],
  );
  const signature = await subtle.sign('HMAC', key, new TextEncoder().encode(message));
  return bytesToBase64(new Uint8Array(signature));
}

export async function verifyHmacSha256(secret: string, message: string, signature: string): Promise<boolean> {
  const expected = await hmacSha256Base64(secret, message);
  if (expected.length !== signature.length) return false;
  let mismatch = 0;
  for (let i = 0; i < expected.length; i += 1) mismatch |= expected.charCodeAt(i) ^ signature.charCodeAt(i);
  return mismatch === 0;
}

export interface ScopeSummaryInput {
  prefix: DomainInput;
  root?: DomainInput;
  subtree?: DomainInput;
  rangePrefix?: DomainInput;
  partition?: PartitionInput;
  throughEventId?: EventId;
  eventId?: EventId;
  signer: string;
  events?: readonly unknown[];
  digest?: string;
  confirmedVersions?: readonly EventId[];
  confirmedTombstones?: readonly EventId[];
  tombstones?: readonly EventId[];
  issuedAt?: number;
  expiresAt?: number | string | Date;
  metadata?: PolicyMetadata;
}

function summaryEventHost(event: unknown): DnsName | undefined {
  if (!isObject(event)) return undefined;
  const host = pick<DomainInput>(event, ['host', 'name', 'domain']);
  return host === undefined ? undefined : normalizeDnsName(host);
}

function summaryEventPartitionKey(event: unknown): string | undefined {
  if (!isObject(event)) return undefined;
  const partition = pick<PartitionInput>(event, ['partition', 'networkPartition']);
  return partition === undefined ? undefined : partitionKey(partition);
}

export function selectScopeEvents(input: Pick<ScopeSummaryInput, 'prefix' | 'partition' | 'events'>): unknown[] {
  const prefix = normalizeDnsName(input.prefix);
  const partition = normalizePartition(input.partition ?? DEFAULT_PARTITION);
  return (input.events ?? []).filter((event) => {
    const host = summaryEventHost(event);
    const eventPartition = summaryEventPartitionKey(event);
    if (eventPartition !== undefined && eventPartition !== partitionKey(partition)) return false;
    if (!host) return true;
    return isSubdomainOf(host, prefix);
  });
}

export function buildScopeSummary(input: ScopeSummaryInput): ScopeSummary {
  const prefixInput = input.prefix ?? input.root ?? input.subtree ?? input.rangePrefix;
  if (prefixInput === undefined) throw new PolicyValidationError('scope summary requires a prefix');
  const prefix = normalizeDnsName(prefixInput);
  const partition = normalizePartition(input.partition ?? DEFAULT_PARTITION);
  const events = selectScopeEvents(input);
  const sortedEvents = [...events].sort((a, b) => {
    const aid = isObject(a) ? pick<EventId>(a, ['eventId', 'id']) : undefined;
    const bid = isObject(b) ? pick<EventId>(b, ['eventId', 'id']) : undefined;
    if (aid === undefined || bid === undefined) return 0;
    return compareEventId(aid, bid);
  });
  const digest = input.digest ?? stableDigest(sortedEvents);
  const throughEventId = input.throughEventId ?? input.eventId;
  const confirmedVersions = [
    ...(input.confirmedVersions ?? []),
    ...(input.confirmedTombstones ?? []),
    ...(input.tombstones ?? []),
  ];
  return {
    kind: 'hsts-scope-summary',
    version: 1,
    signer: input.signer,
    prefix,
    partition,
    partitionKey: partitionKey(partition),
    complete: true,
    throughEventId,
    digest,
    confirmedVersions: confirmedVersions.length ? confirmedVersions : undefined,
    issuedAt: input.issuedAt,
    expiresAt: input.expiresAt === undefined ? undefined : toAbsoluteTime(input.expiresAt),
    metadata: input.metadata,
  };
}

export async function createSignedScopeSummary(
  input: ScopeSummaryInput,
  secret: string,
): Promise<SignedScopeSummary> {
  const summary = buildScopeSummary(input);
  if (input.events && input.digest === undefined) {
    summary.digest = await sha256Hex(stableStringify(selectScopeEvents(input)));
  }
  const unsigned = { ...summary, algorithm: 'HMAC-SHA256' as const };
  const signature = await hmacSha256Base64(secret, stableStringify(unsigned));
  return { ...unsigned, signature };
}

export async function verifySignedScopeSummary(
  summary: SignedScopeSummary,
  secret: string,
  expected: { partition?: PartitionInput; host?: DomainInput; now?: number } = {},
): Promise<{ valid: boolean; reason?: string }> {
  const { signature, ...unsigned } = summary;
  const validSignature = await verifyHmacSha256(secret, stableStringify(unsigned), signature);
  if (!validSignature) return { valid: false, reason: 'bad-signature' };
  if (summary.complete !== true) return { valid: false, reason: 'scope-not-complete' };
  if (expected.partition !== undefined && summary.partitionKey !== partitionKey(expected.partition)) {
    return { valid: false, reason: 'partition-generation-mismatch' };
  }
  if (expected.host !== undefined && !isSubdomainOf(expected.host, summary.prefix)) {
    return { valid: false, reason: 'host-outside-scope-prefix' };
  }
  if (summary.expiresAt !== undefined && (expected.now ?? Date.now()) >= summary.expiresAt) {
    return { valid: false, reason: 'scope-summary-expired' };
  }
  return { valid: true };
}

export interface SubtreeDecisionSummary {
  outcome: PointDecision['outcome'];
  shouldUpgrade: boolean;
  verdict?: TemporalVerdict;
  winnerEventId?: EventId;
  reasonCodes: string[];
}

export interface AffectedSubtree {
  root: string;
  labels: string[];
  partition: NetworkPartition;
  includesDescendants: boolean;
  eventIds: EventId[];
  observedEventIds: EventId[];
  sources: string[];
  issuers: string[];
  effects: PolicyEffect[];
  expiresAt: number[];
  reasonCodes: string[];
  basis: Array<Record<string, unknown>>;
  decision?: SubtreeDecisionSummary;
}

export interface AffectedSubtreesExport {
  subtrees: AffectedSubtree[];
  minimalRoots: AffectedSubtree[];
  entries: AffectedSubtree[];
  count: number;
}

export interface ExportAffectedOptions extends QueryOptions {
  events?: readonly EventInput[];
  includeDecisions?: boolean;
}

function canonicalEventsFromSource(
  source?: HSTSPolicyLibrary | readonly EventInput[],
  options: HSTSLibraryOptions = {},
): { library?: HSTSPolicyLibrary; events: PolicyEvent[] } {
  if (source instanceof HSTSPolicyLibrary) return { library: source, events: source.events };
  if (Array.isArray(source)) {
    const library = HSTSPolicyLibrary.replay(source, options);
    return { library, events: library.events };
  }
  const library = new HSTSPolicyLibrary(options);
  return { library, events: [] };
}

export function exportAffectedSubtrees(
  source?: HSTSPolicyLibrary | readonly EventInput[] | ExportAffectedOptions,
  maybeOptions: ExportAffectedOptions = {},
): AffectedSubtreesExport {
  let library: HSTSPolicyLibrary | undefined;
  let rawEvents: readonly EventInput[] | undefined;
  let options: ExportAffectedOptions = maybeOptions;

  if (source instanceof HSTSPolicyLibrary) {
    library = source;
  } else if (Array.isArray(source)) {
    rawEvents = source;
  } else if (isObject(source)) {
    options = { ...source, ...maybeOptions };
    rawEvents = options.events;
  }
  if (options.events) rawEvents = options.events;

  if (!library) {
    const replayOptions = options as HSTSLibraryOptions;
    library = rawEvents ? HSTSPolicyLibrary.replay(rawEvents, replayOptions) : new HSTSPolicyLibrary(replayOptions);
  }

  const entries: AffectedSubtree[] = [];
  for (const event of library.events) {
    let host: DnsName | undefined;
    let partition: NetworkPartition | undefined;
    let includesDescendants = false;
    let effect: PolicyEffect | undefined;
    let expiresAt: number | undefined;
    let observedEventIds: EventId[] = [];
    const reasonCodes: string[] = [];

    if (event.kind === 'add') {
      host = event.host;
      partition = event.partition;
      includesDescendants = event.includeSubDomains;
      effect = event.effect;
      expiresAt = event.expiresAt;
      reasonCodes.push(event.includeSubDomains ? 'subtree-policy' : 'exact-name-policy');
    } else {
      const target = library.records.get(eventKey(event.targetVersion));
      host = event.host ?? target?.host;
      partition = event.partition ?? target?.partition;
      includesDescendants = event.includeSubDomains ?? target?.includeSubDomains ?? false;
      effect = target?.effect;
      expiresAt = target?.expiresAt;
      observedEventIds = [event.targetVersion];
      reasonCodes.push('versioned-delete');
      if (library.deletionStatus(target as PolicyRecord).status === 'blocked') {
        reasonCodes.push('delete-blocked');
      }
    }
    if (!host || !partition) continue;
    const partitionFilter = options.partition ?? options.networkPartition ?? options.partitionId;
    if (partitionFilter !== undefined && partitionKey(partition) !== partitionKey(partitionFilter)) continue;

    entries.push({
      root: host.fqdn,
      labels: host.labels,
      partition,
      includesDescendants,
      eventIds: [event.eventId],
      observedEventIds,
      sources: [event.source ?? 'unknown'].filter((value): value is string => Boolean(value)),
      issuers: [event.kind === 'add' ? event.issuer : event.issuer ?? 'unspecified'],
      effects: effect ? [effect] : [],
      expiresAt: expiresAt === undefined ? [] : [expiresAt],
      reasonCodes,
      basis: [serializeEvent(event)],
    });
  }

  const grouped = new Map<string, AffectedSubtree>();
  for (const entry of entries) {
    const groupKey = stableStringify({
      root: entry.root,
      partition: partitionKey(entry.partition),
      includesDescendants: entry.includesDescendants,
    });
    const existing = grouped.get(groupKey);
    if (!existing) {
      grouped.set(groupKey, entry);
      continue;
    }
    existing.eventIds.push(...entry.eventIds);
    existing.observedEventIds.push(...entry.observedEventIds);
    existing.sources.push(...entry.sources);
    existing.issuers.push(...entry.issuers);
    existing.effects.push(...entry.effects);
    existing.expiresAt.push(...entry.expiresAt);
    existing.basis.push(...entry.basis);
    for (const reason of entry.reasonCodes) if (!existing.reasonCodes.includes(reason)) existing.reasonCodes.push(reason);
  }

  const normalizedEntries = [...grouped.values()].map((entry) => ({
    ...entry,
    sources: [...new Set(entry.sources)],
    issuers: [...new Set(entry.issuers)],
    effects: [...new Set(entry.effects)],
    eventIds: [...new Set(entry.eventIds)],
    observedEventIds: [...new Set(entry.observedEventIds)],
  }));

  normalizedEntries.sort(
    (a, b) =>
      Number(b.includesDescendants) - Number(a.includesDescendants) ||
      a.labels.length - b.labels.length ||
      a.root.localeCompare(b.root),
  );

  const minimalRoots: AffectedSubtree[] = [];
  for (const entry of normalizedEntries) {
    const covered = minimalRoots.some((selected) => {
      if (partitionKey(selected.partition) !== partitionKey(entry.partition)) return false;
      if (selected.includesDescendants) return isSubdomainOf(entry.root, selected.root);
      return selected.root === entry.root;
    });
    if (!covered) minimalRoots.push(entry);
  }

  if (options.includeDecisions) {
    for (const subtree of minimalRoots) {
      const decision = library.evaluate(subtree.root, { ...options, partition: subtree.partition });
      subtree.decision = {
        outcome: decision.outcome,
        shouldUpgrade: decision.shouldUpgrade,
        verdict: decision.temporal?.verdict,
        winnerEventId: decision.winner?.eventId,
        reasonCodes: decision.reasonCodes,
      };
    }
  }

  return { subtrees: minimalRoots, minimalRoots, entries: normalizedEntries, count: minimalRoots.length };
}

export interface BuildCheckpointOptions extends QueryOptions {
  host?: DomainInput;
  throughEventId?: EventId;
  createdAt?: number;
  includeSnapshot?: boolean;
  secret?: string;
}

export function computeSuffixRules(
  library: HSTSPolicyLibrary,
  hostInput: DomainInput | undefined,
  partitionInput?: PartitionInput,
): Array<Record<string, unknown>> {
  const partition = normalizePartition(partitionInput ?? DEFAULT_PARTITION);
  let records: PolicyRecord[];
  if (hostInput === undefined) {
    records = [...library.records.values()].filter((record) => record.partitionKey === partitionKey(partition));
  } else {
    const host = normalizeDnsName(hostInput);
    const ids = library.trie.pathValues(host);
    records = ids
      .map((id) => library.records.get(eventKey(id)))
      .filter((record): record is PolicyRecord => Boolean(record))
      .filter((record) => record.partitionKey === partitionKey(partition))
      .filter((record) => dnsRelation(host, record.host) !== 'unrelated');
  }
  return records
    .map((record) => ({
      ...serializeEvent(record),
      deletionStatus: library.deletionStatus(record).status,
    }))
    .sort((a, b) => compareEventId(a.eventId as EventId, b.eventId as EventId));
}

export function buildCheckpoint(library: HSTSPolicyLibrary, options: BuildCheckpointOptions = {}): PolicyCheckpoint {
  const partition = queryPartition(options);
  const host = options.host === undefined ? undefined : normalizeDnsName(options.host);
  const suffixRules = computeSuffixRules(library, host, partition);
  const suffixRulesDigest = stableDigest(suffixRules);
  const stateForDigest = {
    events: library.events.map(serializeEvent),
    confirmedVersions: [...library.confirmedVersions],
    scopeSummaries: library.scopeSummaries.map((summary) => stableDigest(summary)),
  };
  const eventIds = library.events.map((event) => event.eventId);
  const checkpoint: PolicyCheckpoint = {
    kind: 'hsts-policy-checkpoint',
    formatVersion: 1,
    createdAt: options.createdAt ?? Date.now(),
    host: host?.fqdn,
    partition,
    partitionGeneration: String(partition.generation),
    appliedThroughEventId: options.throughEventId ?? maxEventId(eventIds),
    eventCount: library.events.length,
    stateDigest: stableDigest(stateForDigest),
    suffixRules,
    suffixRulesDigest,
    confirmedVersions: [...library.confirmedVersions]
      .map((key) => key.slice(key.indexOf(':') + 1)),
    scopeSummaryDigests: library.scopeSummaries.map((summary) => stableDigest(summary)),
    snapshot: options.includeSnapshot === false ? undefined : library.toJSON(),
  };
  return checkpoint;
}

export async function signCheckpoint(checkpoint: PolicyCheckpoint, secret: string): Promise<PolicyCheckpoint> {
  const unsigned = { ...checkpoint, algorithm: 'HMAC-SHA256' as const };
  const signature = await hmacSha256Base64(secret, stableStringify(unsigned));
  return { ...unsigned, signature };
}

export async function createSignedCheckpoint(
  library: HSTSPolicyLibrary,
  options: BuildCheckpointOptions = {},
): Promise<PolicyCheckpoint> {
  const checkpoint = buildCheckpoint(library, options);
  if (!options.secret) return checkpoint;
  return signCheckpoint(checkpoint, options.secret);
}

export async function verifyCheckpointSignature(checkpoint: PolicyCheckpoint, secret: string): Promise<boolean> {
  if (!checkpoint.signature) return false;
  const { signature, ...unsigned } = checkpoint;
  return verifyHmacSha256(secret, stableStringify(unsigned), signature);
}

export interface RestoreCheckpointOptions {
  expectedPartition?: PartitionInput;
  expectedGeneration?: number | string;
  partitionRegistry?: PartitionRegistry;
  libraryOptions?: HSTSLibraryOptions;
  library?: HSTSPolicyLibrary;
  verifyStateDigest?: boolean;
}

export function restoreCheckpoint(
  checkpointInput: PolicyCheckpoint | string,
  options: RestoreCheckpointOptions = {},
): HSTSPolicyLibrary {
  const checkpoint = typeof checkpointInput === 'string'
    ? JSON.parse(checkpointInput) as PolicyCheckpoint
    : checkpointInput;
  if (checkpoint.kind !== 'hsts-policy-checkpoint') {
    throw new CheckpointValidationError('Not an HSTS policy checkpoint');
  }

  const expectedPartition = options.expectedPartition === undefined
    ? undefined
    : normalizePartition(options.expectedPartition);
  if (expectedPartition && partitionKey(checkpoint.partition) !== partitionKey(expectedPartition)) {
    throw new CheckpointValidationError('Checkpoint network partition generation does not match expected partition');
  }
  if (options.expectedGeneration !== undefined && String(options.expectedGeneration) !== checkpoint.partitionGeneration) {
    throw new CheckpointValidationError('Checkpoint partition generation does not match recovered partition generation');
  }
  if (options.partitionRegistry) {
    const current = options.partitionRegistry.current(checkpoint.partition.id);
    if (String(current.generation) !== checkpoint.partitionGeneration) {
      throw new CheckpointValidationError('Device reinstall changed partition generation; refusing old checkpoint');
    }
  }

  const library = options.library
    ?? (checkpoint.snapshot ? HSTSPolicyLibrary.fromJSON(checkpoint.snapshot, options.libraryOptions) : undefined);
  if (!library) throw new CheckpointValidationError('Checkpoint has no snapshot and no target library was provided');

  const suffixRules = computeSuffixRules(
    library,
    checkpoint.host,
    checkpoint.partition,
  );
  if (stableDigest(suffixRules) !== checkpoint.suffixRulesDigest) {
    throw new CheckpointValidationError('Checkpoint is not bound to the current suffix rules');
  }

  if (options.verifyStateDigest !== false && checkpoint.snapshot) {
    const stateDigest = stableDigest({
      events: library.events.map(serializeEvent),
      confirmedVersions: [...library.confirmedVersions],
      scopeSummaries: library.scopeSummaries.map((summary) => stableDigest(summary)),
    });
    if (stateDigest !== checkpoint.stateDigest) {
      throw new CheckpointValidationError('Checkpoint state digest does not match recovered event log');
    }
  }

  return library;
}

export async function verifyAndRestoreCheckpoint(
  checkpoint: PolicyCheckpoint | string,
  secret: string,
  options: RestoreCheckpointOptions = {},
): Promise<HSTSPolicyLibrary> {
  const parsed = typeof checkpoint === 'string' ? JSON.parse(checkpoint) as PolicyCheckpoint : checkpoint;
  if (!await verifyCheckpointSignature(parsed, secret)) {
    throw new CheckpointValidationError('Checkpoint signature verification failed');
  }
  return restoreCheckpoint(parsed, options);
}

export interface HSTSPolicyLibrary {
  exportAffectedSubtrees(options?: ExportAffectedOptions): AffectedSubtreesExport;
  buildCheckpoint(options?: BuildCheckpointOptions): PolicyCheckpoint;
  createCheckpoint(options?: BuildCheckpointOptions): Promise<PolicyCheckpoint>;
  restoreCheckpoint(checkpoint: PolicyCheckpoint, options?: RestoreCheckpointOptions): HSTSPolicyLibrary;
}

HSTSPolicyLibrary.prototype.exportAffectedSubtrees = function exportAffectedSubtreesMethod(
  this: HSTSPolicyLibrary,
  options?: ExportAffectedOptions,
) {
  return exportAffectedSubtrees(this, options);
};

HSTSPolicyLibrary.prototype.buildCheckpoint = function buildCheckpointMethod(
  this: HSTSPolicyLibrary,
  options?: BuildCheckpointOptions,
) {
  return buildCheckpoint(this, options);
};

HSTSPolicyLibrary.prototype.createCheckpoint = async function createCheckpointMethod(
  this: HSTSPolicyLibrary,
  options: BuildCheckpointOptions = {},
) {
  return createSignedCheckpoint(this, options);
};

HSTSPolicyLibrary.prototype.restoreCheckpoint = function restoreCheckpointMethod(
  this: HSTSPolicyLibrary,
  checkpoint: PolicyCheckpoint,
  options: RestoreCheckpointOptions = {},
) {
  return restoreCheckpoint(checkpoint, { library: this, ...options });
};

export const HSTSStore = HSTSPolicyLibrary;
export const HSTSEngine = HSTSPolicyLibrary;
export const PolicyLibrary = HSTSPolicyLibrary;
export const PolicyStore = HSTSPolicyLibrary;
export const HSTSManager = HSTSPolicyLibrary;
export const HSTSPolicyManager = HSTSPolicyLibrary;
export const PolicyEngine = HSTSPolicyLibrary;
export function createHSTSStore(options?: HSTSLibraryOptions): HSTSPolicyLibrary {
  return new HSTSPolicyLibrary(options);
}

export function evaluateEvents(
  events: readonly EventInput[],
  host: DomainInput,
  options: QueryOptions & HSTSLibraryOptions = {},
): DecisionExplanation & { temporal?: TemporalDecision } {
  const library = HSTSPolicyLibrary.replay(events, options);
  return library.evaluate(host, options);
}

export const evaluateHSTS = evaluateEvents;
export const evaluatePolicy = evaluateEvents;

export function classifyTrustedTime(
  events: readonly EventInput[],
  host: DomainInput,
  time: TimeSetInput,
  options: QueryOptions & HSTSLibraryOptions = {},
): TemporalDecision {
  const library = HSTSPolicyLibrary.replay(events, options);
  return library.evaluateTemporal(host, time, options);
}

export default HSTSPolicyLibrary;
