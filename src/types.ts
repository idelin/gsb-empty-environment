export interface Subject {
  readonly id: string;
}

export type AttrKey = string;
export type Version = string;

export interface FetchRequest {
  readonly subject: Subject;
  readonly permission: string;
  readonly keys: readonly AttrKey[];
}

export interface FetchOutcome {
  readonly version: Version;
  readonly values: ReadonlyMap<AttrKey, unknown>;
  readonly failures?: ReadonlyMap<AttrKey, Error>;
}

export interface AttributeSource {
  readonly name: string;
  readonly attrs: ReadonlySet<AttrKey>;
  readonly batchCost: number;
  readonly attrCost: number;
  fetchBatch(req: FetchRequest): Promise<FetchOutcome>;
}

export interface EvidenceEntry {
  readonly key: AttrKey;
  readonly value: unknown;
  readonly version: Version;
  readonly source: string;
}

export interface ReadSession {
  readonly subject: Subject;
  readonly permission: string;
  readonly signal: AbortSignal;
  readonly versions: Map<string, Version>;
  readonly values: Map<AttrKey, unknown>;
  readonly pendingReads: Map<AttrKey, Promise<unknown>>;
  readonly evidence: EvidenceEntry[];
}

export interface DecisionRecord {
  readonly decision: 'allow' | 'deny';
  readonly subjectId: string;
  readonly permission: string;
  readonly evidence: readonly EvidenceEntry[];
  readonly attempts: number;
  readonly seq: number;
}

export class CancelledError extends Error {
  constructor() {
    super('evaluation cancelled');
    this.name = 'CancelledError';
  }
}

export class FetchTimeoutError extends Error {
  readonly timeoutMs: number;
  constructor(timeoutMs: number) {
    super(`fetch timed out after ${timeoutMs}ms`);
    this.name = 'FetchTimeoutError';
    this.timeoutMs = timeoutMs;
  }
}

export class AttrFetchError extends Error {
  readonly key: AttrKey;
  readonly cause: Error;
  constructor(key: AttrKey, cause: Error) {
    super(`failed to fetch attribute "${key}": ${cause.message}`);
    this.name = 'AttrFetchError';
    this.key = key;
    this.cause = cause;
  }
}

export class VersionConflictError extends Error {
  readonly source: string;
  readonly expected: Version;
  readonly actual: Version;
  constructor(source: string, expected: Version, actual: Version) {
    super(`mixed versions from source "${source}": saw ${expected} then ${actual}`);
    this.name = 'VersionConflictError';
    this.source = source;
    this.expected = expected;
    this.actual = actual;
  }
}

export class PolicyError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'PolicyError';
  }
}
