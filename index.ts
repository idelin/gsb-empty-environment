/**
 * Deterministic Web Locks-style coordination for browser hosts.
 *
 * A lock identity is the tuple
 *   (storage partition, origin, raw UTF-8 lock name, host policy generation)
 * and is deliberately never encoded with an ambiguous delimiter.
 */

export const LockMode = Object.freeze({
  Shared: "shared",
  Exclusive: "exclusive",
} as const);

export type LockMode = (typeof LockMode)[keyof typeof LockMode];

export type LockName = string | Uint8Array | ArrayBuffer | ArrayBufferView;

export interface LockIdentity {
  readonly storagePartition: string;
  readonly origin: string;
  /** A defensive copy of the exact UTF-8 bytes used for identity. */
  readonly lockName: Uint8Array;
  readonly lockNameHex: string;
  readonly hostPolicyGeneration: number;
}

export interface LockRequestOptions {
  storagePartition?: string;
  /** Alias for storagePartition. */
  partition?: string;
  origin: string;
  lockName?: LockName;
  /** Web Locks-style alias for lockName. */
  name?: LockName;
  mode?: LockMode;
  documentGeneration: number;
  requestID: string;
  /** Absolute deadline in milliseconds on the manager clock. */
  deadline?: number | Date;
  /** Alias for deadline. */
  deadlineMs?: number | Date;
  ifAvailable?: boolean;
  signal?: AbortSignal;
  /**
   * Optional assertion of the caller's policy generation. If supplied and
   * stale, the request is rejected instead of being placed in an old queue.
   */
  hostPolicyGeneration?: number;
}

export type LockCallback<T> = (lease: WebLockLease) => T | PromiseLike<T>;

export type LeaseReleaseReason =
  | "callback"
  | "explicit"
  | "abort"
  | "deadline"
  | "navigation"
  | "policy"
  | "shutdown";

export interface LeaseReleaseInfo {
  readonly leaseID: string;
  readonly reason: LeaseReleaseReason;
  readonly releasedAt: number;
  readonly documentGeneration: number;
  readonly lockNameHex: string;
}

export interface WebLockLease {
  readonly leaseID: string;
  readonly mode: LockMode;
  readonly requestID: string;
  readonly requestSequence: number;
  readonly documentGeneration: number;
  readonly storagePartition: string;
  readonly origin: string;
  readonly lockName: Uint8Array;
  readonly lockNameHex: string;
  readonly identity: LockIdentity;
  readonly released: boolean;
  readonly releaseInfo: LeaseReleaseInfo | undefined;
  /** Resolves exactly once, when this particular lease stops being held. */
  readonly whenReleased: Promise<LeaseReleaseInfo>;
  /** Idempotent. Releasing an old lease can never release a newer lease. */
  release(): void;
}

export interface LockView {
  readonly storagePartition: string;
  readonly origin: string;
  readonly name: string;
  readonly lockName: Uint8Array;
  readonly lockNameHex: string;
  readonly mode: LockMode;
  readonly documentGeneration: number;
  readonly requestID: string;
  readonly requestSequence: number;
  readonly hostPolicyGeneration: number;
  readonly state: "held" | "pending";
  readonly leaseID?: string;
  readonly deadline?: number;
  readonly ifAvailable: boolean;
}

export interface LockQueryScope {
  storagePartition?: string;
  /** Alias for storagePartition. */
  partition?: string;
  origin: string;
  /**
   * The calling document. A host can use canViewLock to prevent one document
   * from observing another document's lock activity.
   */
  documentGeneration?: number;
  includePending?: boolean;
}

export interface LockQueryResult {
  readonly held: readonly LockView[];
  readonly pending: readonly LockView[];
}

export interface DocumentScope {
  storagePartition?: string;
  partition?: string;
  origin: string;
  documentGeneration: number;
}

export interface DocumentReleaseResult {
  readonly pendingRejected: number;
  readonly leasesReleased: number;
}

export type GrantedLeaseRevocationPolicy =
  | boolean
  | ((lease: WebLockLease) => boolean);

export interface HostPolicyChangeOptions {
  /**
   * Whether grants from the old generation are revoked immediately.
   * Defaults to true. A callback can selectively retain grants.
   */
  revokeGranted?: GrantedLeaseRevocationPolicy;
}

export interface HostPolicyChangeResult {
  readonly storagePartition: string;
  readonly previousGeneration: number;
  readonly generation: number;
  readonly pendingRejected: number;
  readonly leasesRevoked: number;
}

export interface LeaseReleasedEvent {
  readonly lease: WebLockLease;
  readonly info: LeaseReleaseInfo;
}

export interface LockManagerOptions {
  clock?: LockClock;
  /** Initial default policy generation for partitions not listed below. */
  policyGeneration?: number;
  /** Optional per-partition initial generations. */
  policyGenerations?: Readonly<Record<string, number>>;
  createLeaseID?: () => string;
  /**
   * Invoked only after the partition/origin filter. Returning false hides an
   * otherwise in-scope lock from this document.
   */
  canViewLock?: (viewer: LockQueryScope, lock: LockView) => boolean;
  onLeaseReleased?: (event: LeaseReleasedEvent) => void;
}

export interface LockClock {
  now(): number;
  setTimeout(callback: () => void, delayMs: number): unknown;
  clearTimeout(handle: unknown): void;
}

export class SystemClock implements LockClock {
  now(): number {
    return Date.now();
  }

  setTimeout(callback: () => void, delayMs: number): unknown {
    return globalThis.setTimeout(callback, Math.max(0, delayMs));
  }

  clearTimeout(handle: unknown): void {
    globalThis.clearTimeout(handle as ReturnType<typeof setTimeout>);
  }
}

interface ManualTimer {
  readonly id: number;
  readonly due: number;
  readonly order: number;
  readonly callback: () => void;
}

/**
 * A deterministic clock/timer queue for tests. Timers at the same instant run
 * in installation order. No wall-clock time is consulted.
 */
export class ManualClock implements LockClock {
  #now: number;
  #nextID = 1;
  #nextOrder = 1;
  #timers = new Map<number, ManualTimer>();

  constructor(start = 0) {
    if (!Number.isFinite(start)) {
      throw new TypeError("ManualClock start must be finite");
    }
    this.#now = start;
  }

  now(): number {
    return this.#now;
  }

  setTimeout(callback: () => void, delayMs: number): unknown {
    const delay = Math.max(0, delayMs);
    const timer: ManualTimer = {
      id: this.#nextID++,
      due: this.#now + delay,
      order: this.#nextOrder++,
      callback,
    };
    this.#timers.set(timer.id, timer);
    return timer.id;
  }

  clearTimeout(handle: unknown): void {
    if (typeof handle === "number") {
      this.#timers.delete(handle);
    }
  }

  /** Advances time, firing all due timers in deterministic order. */
  advance(ms: number): number {
    if (!Number.isFinite(ms) || ms < 0) {
      throw new RangeError(
        "ManualClock.advance requires a finite non-negative delay",
      );
    }

    const target = this.#now + ms;
    let fired = 0;

    while (true) {
      let next: ManualTimer | undefined;
      for (const timer of this.#timers.values()) {
        if (timer.due > target) continue;
        if (
          next === undefined ||
          timer.due < next.due ||
          (timer.due === next.due && timer.order < next.order)
        ) {
          next = timer;
        }
      }
      if (next === undefined) break;

      this.#now = Math.max(this.#now, next.due);
      this.#timers.delete(next.id);
      next.callback();
      fired += 1;
    }

    this.#now = target;
    return fired;
  }

  advanceTo(time: number): number {
    if (!Number.isFinite(time) || time < this.#now) {
      throw new RangeError(
        "ManualClock.advanceTo requires a time at or after now",
      );
    }
    return this.advance(time - this.#now);
  }

  get pendingTimerCount(): number {
    return this.#timers.size;
  }
}

export class LockManagerError extends Error {
  readonly code: string;

  constructor(message: string, code: string, name = "LockManagerError") {
    super(message);
    this.name = name;
    this.code = code;
  }
}

export class LockAbortError extends LockManagerError {
  constructor(message = "The lock request was aborted") {
    super(message, "ABORT_ERR", "AbortError");
  }
}

export class LockTimeoutError extends LockManagerError {
  constructor(message = "The lock request deadline expired") {
    super(message, "TIMEOUT_ERR", "TimeoutError");
  }
}

export class LockNavigationError extends LockManagerError {
  constructor(message = "The requesting document was navigated away") {
    super(message, "NAVIGATION_ERR", "NavigationError");
  }
}

export class LockPolicyError extends LockManagerError {
  constructor(message = "The lock request is stale under the host policy") {
    super(message, "POLICY_ERR", "PolicyError");
  }
}

export class LockClosedError extends LockManagerError {
  constructor(message = "The lock manager has been closed") {
    super(message, "CLOSED_ERR", "InvalidStateError");
  }
}

const UTF8_ENCODER = new TextEncoder();
const UTF8_DECODER = new TextDecoder("utf-8", { fatal: true });

export function lockNameBytes(name: LockName): Uint8Array {
  if (typeof name === "string") {
    return UTF8_ENCODER.encode(name);
  }
  if (name instanceof Uint8Array) {
    return new Uint8Array(name);
  }
  if (name instanceof ArrayBuffer) {
    return new Uint8Array(name.slice(0));
  }
  if (ArrayBuffer.isView(name)) {
    return new Uint8Array(name.buffer, name.byteOffset, name.byteLength).slice();
  }
  throw new TypeError("lockName must be a string or byte buffer");
}

export function bytesToHex(bytes: Uint8Array): string {
  let result = "";
  for (const byte of bytes) {
    result += byte.toString(16).padStart(2, "0");
  }
  return result;
}

/** Decodes only valid UTF-8; invalid byte sequences return undefined. */
export function decodeLockName(name: LockName): string | undefined {
  try {
    return UTF8_DECODER.decode(lockNameBytes(name));
  } catch {
    return undefined;
  }
}

function assertPartition(value: unknown): string {
  if (typeof value !== "string" || value.length === 0) {
    throw new TypeError("storagePartition must be a non-empty string");
  }
  return value;
}

function assertOrigin(value: unknown): string {
  if (typeof value !== "string" || value.length === 0) {
    throw new TypeError("origin must be a non-empty string");
  }
  return value;
}

function assertGeneration(value: unknown, label: string): number {
  if (!Number.isSafeInteger(value) || (value as number) < 0) {
    throw new TypeError(`${label} must be a non-negative safe integer`);
  }
  return value as number;
}

function assertRequestID(value: unknown): string {
  if (typeof value !== "string" || value.length === 0) {
    throw new TypeError("requestID must be a non-empty string");
  }
  return value;
}

function normalizeMode(mode: LockMode | undefined): LockMode {
  if (mode === undefined) return LockMode.Exclusive;
  if (typeof mode !== "string") {
    throw new TypeError("mode must be Shared or Exclusive");
  }
  const normalized = mode.toLowerCase();
  if (normalized === LockMode.Shared) return LockMode.Shared;
  if (normalized === LockMode.Exclusive) return LockMode.Exclusive;
  throw new TypeError("mode must be Shared or Exclusive");
}

function normalizeDeadline(
  value: number | Date | undefined,
): number | undefined {
  if (value === undefined) return undefined;
  const deadline = value instanceof Date ? value.getTime() : value;
  if (!Number.isFinite(deadline)) {
    throw new TypeError("deadline must be a finite absolute time");
  }
  return deadline;
}

function partitionOf(value: {
  storagePartition?: string;
  partition?: string;
}): string {
  return assertPartition(value.storagePartition ?? value.partition);
}

function deadlineOf(options: LockRequestOptions): number | undefined {
  return normalizeDeadline(options.deadline ?? options.deadlineMs);
}

function nameOf(options: LockRequestOptions): LockName {
  const name = options.lockName ?? options.name;
  if (name === undefined) {
    throw new TypeError("lockName is required");
  }
  return name;
}

/**
 * Injective canonical key for a lock identity. JSON array encoding preserves
 * every UTF-16 code unit and the byte string is hex, so no component can
 * escape into a neighboring field.
 */
export function lockIdentityKey(
  storagePartition: string,
  origin: string,
  lockName: LockName,
  hostPolicyGeneration: number,
): string;
export function lockIdentityKey(identity: {
  storagePartition: string;
  origin: string;
  lockName: LockName;
  hostPolicyGeneration: number;
}): string;
export function lockIdentityKey(
  partitionOrIdentity:
    | string
    | {
        storagePartition: string;
        origin: string;
        lockName: LockName;
        hostPolicyGeneration: number;
      },
  origin?: string,
  lockName?: LockName,
  hostPolicyGeneration?: number,
): string {
  let partition: string;
  let lockOrigin: string;
  let name: LockName;
  let generation: number;

  if (typeof partitionOrIdentity === "string") {
    partition = assertPartition(partitionOrIdentity);
    lockOrigin = assertOrigin(origin);
    if (lockName === undefined) throw new TypeError("lockName is required");
    name = lockName;
    generation = assertGeneration(
      hostPolicyGeneration,
      "hostPolicyGeneration",
    );
  } else {
    partition = assertPartition(partitionOrIdentity.storagePartition);
    lockOrigin = assertOrigin(partitionOrIdentity.origin);
    name = partitionOrIdentity.lockName;
    generation = assertGeneration(
      partitionOrIdentity.hostPolicyGeneration,
      "hostPolicyGeneration",
    );
  }

  return JSON.stringify([
    "web-lock-v1",
    partition,
    lockOrigin,
    bytesToHex(lockNameBytes(name)),
    generation,
  ]);
}

export function createLockIdentity(
  storagePartition: string,
  origin: string,
  lockName: LockName,
  hostPolicyGeneration: number,
): LockIdentity {
  const partition = assertPartition(storagePartition);
  const lockOrigin = assertOrigin(origin);
  const generation = assertGeneration(
    hostPolicyGeneration,
    "hostPolicyGeneration",
  );
  const bytes = lockNameBytes(lockName);
  return Object.freeze({
    storagePartition: partition,
    origin: lockOrigin,
    lockName: bytes,
    lockNameHex: bytesToHex(bytes),
    hostPolicyGeneration: generation,
  });
}

function defaultLeaseID(): string {
  const cryptoObject = globalThis.crypto;
  if (typeof cryptoObject?.randomUUID === "function") {
    return cryptoObject.randomUUID();
  }
  if (typeof cryptoObject?.getRandomValues === "function") {
    const bytes = new Uint8Array(16);
    cryptoObject.getRandomValues(bytes);
    return bytesToHex(bytes);
  }
  return `lease-${Date.now().toString(36)}-${Math.random()
    .toString(36)
    .slice(2)}`;
}

function abortReason(signal: AbortSignal): unknown {
  const reason = (signal as AbortSignal & { reason?: unknown }).reason;
  return reason === undefined ? new LockAbortError() : reason;
}

function visibleName(bytes: Uint8Array): string {
  try {
    return UTF8_DECODER.decode(bytes);
  } catch {
    return `0x${bytesToHex(bytes)}`;
  }
}

interface IdentityState {
  readonly key: string;
  readonly identity: LockIdentity;
  readonly holders: HeldLease[];
  readonly queue: RequestRecord[];
  nextSequence: number;
}

interface HeldLease {
  readonly lease: LockLeaseImpl;
  readonly request: RequestRecord;
  readonly state: IdentityState;
  active: boolean;
}

type RequestStatus = "queued" | "held" | "finished";

interface RequestRecord {
  readonly state: IdentityState;
  readonly mode: LockMode;
  readonly documentGeneration: number;
  readonly requestID: string;
  readonly sequence: number;
  readonly ifAvailable: boolean;
  readonly deadline: number | undefined;
  readonly signal: AbortSignal | undefined;
  readonly callback: LockCallback<unknown> | undefined;
  readonly resolve: (value: unknown) => void;
  readonly reject: (reason: unknown) => void;
  status: RequestStatus;
  settled: boolean;
  held: HeldLease | undefined;
  deadlineHandle: unknown;
  abortHandler: (() => void) | undefined;
  callbackStarted: boolean;
}

const LEASE_INTERNAL = Symbol("web-lock-lease-internal");
const LEASE_MANAGER = Symbol("web-lock-lease-manager");

export class LockLeaseImpl implements WebLockLease {
  readonly leaseID: string;
  readonly mode: LockMode;
  readonly requestID: string;
  readonly requestSequence: number;
  readonly documentGeneration: number;
  readonly storagePartition: string;
  readonly origin: string;
  readonly lockNameHex: string;

  readonly #manager: WebLockManager;
  readonly #name: Uint8Array;
  readonly #hostPolicyGeneration: number;
  readonly #whenReleased: Promise<LeaseReleaseInfo>;
  #resolveReleased!: (info: LeaseReleaseInfo) => void;
  #released = false;
  #releaseInfo: LeaseReleaseInfo | undefined;

  constructor(
    manager: WebLockManager,
    request: RequestRecord,
    leaseID: string,
  ) {
    this.#manager = manager;
    this.leaseID = leaseID;
    this.mode = request.mode;
    this.requestID = request.requestID;
    this.requestSequence = request.sequence;
    this.documentGeneration = request.documentGeneration;
    this.storagePartition = request.state.identity.storagePartition;
    this.origin = request.state.identity.origin;
    this.#name = request.state.identity.lockName.slice();
    this.lockNameHex = request.state.identity.lockNameHex;
    this.#hostPolicyGeneration =
      request.state.identity.hostPolicyGeneration;
    this.#whenReleased = new Promise<LeaseReleaseInfo>((resolve) => {
      this.#resolveReleased = resolve;
    });
    // Public fields cannot be rebound or copied into another manager by
    // mutating the object. The private state remains writable internally.
    Object.freeze(this);
  }

  get lockName(): Uint8Array {
    return this.#name.slice();
  }

  get identity(): LockIdentity {
    return createLockIdentity(
      this.storagePartition,
      this.origin,
      this.#name,
      this.#hostPolicyGeneration,
    );
  }

  get released(): boolean {
    return this.#released;
  }

  get releaseInfo(): LeaseReleaseInfo | undefined {
    return this.#releaseInfo;
  }

  get whenReleased(): Promise<LeaseReleaseInfo> {
    return this.#whenReleased;
  }

  release(): void {
    this.#manager.releaseLease(this, "explicit");
  }

  [LEASE_MANAGER](): WebLockManager {
    return this.#manager;
  }

  [LEASE_INTERNAL](
    reason: LeaseReleaseReason,
    releasedAt: number,
  ): LeaseReleaseInfo | undefined {
    if (this.#released) return undefined;
    this.#released = true;
    const info: LeaseReleaseInfo = Object.freeze({
      leaseID: this.leaseID,
      reason,
      releasedAt,
      documentGeneration: this.documentGeneration,
      lockNameHex: this.lockNameHex,
    });
    this.#releaseInfo = info;
    this.#resolveReleased(info);
    return info;
  }
}

/**
 * Origin/partition/policy-scoped lock manager. All scheduling is synchronous
 * and deterministic; only promise settlement and callback start use
 * microtasks.
 */
export class WebLockManager {
  readonly clock: LockClock;

  #states = new Map<string, IdentityState>();
  #leases = new Map<string, HeldLease>();
  #usedLeaseIDs = new Set<string>();
  #policyGenerations = new Map<string, number>();
  #defaultPolicyGeneration: number;
  #createLeaseID: () => string;
  #canViewLock:
    | ((viewer: LockQueryScope, lock: LockView) => boolean)
    | undefined;
  #onLeaseReleased:
    | ((event: LeaseReleasedEvent) => void)
    | undefined;
  #closed = false;

  constructor(options: LockManagerOptions = {}) {
    this.clock = options.clock ?? new SystemClock();
    this.#defaultPolicyGeneration = assertGeneration(
      options.policyGeneration ?? 0,
      "policyGeneration",
    );
    if (options.policyGenerations !== undefined) {
      for (const [partition, generation] of Object.entries(
        options.policyGenerations,
      )) {
        this.#policyGenerations.set(
          assertPartition(partition),
          assertGeneration(generation, "policyGenerations"),
        );
      }
    }
    this.#createLeaseID = options.createLeaseID ?? defaultLeaseID;
    this.#canViewLock = options.canViewLock;
    this.#onLeaseReleased = options.onLeaseReleased;
  }

  getPolicyGeneration(storagePartition: string): number {
    const partition = assertPartition(storagePartition);
    return (
      this.#policyGenerations.get(partition) ??
      this.#defaultPolicyGeneration
    );
  }

  request(options: LockRequestOptions): Promise<WebLockLease | null>;
  request<T>(
    options: LockRequestOptions,
    callback: LockCallback<T>,
  ): Promise<T | null>;
  request<T>(
    options: LockRequestOptions,
    callback?: LockCallback<T>,
  ): Promise<WebLockLease | T | null> {
    try {
      this.#assertOpen();
      const partition = partitionOf(options);
      const origin = assertOrigin(options.origin);
      const name = nameOf(options);
      const mode = normalizeMode(options.mode);
      const documentGeneration = assertGeneration(
        options.documentGeneration,
        "documentGeneration",
      );
      const requestID = assertRequestID(options.requestID);
      const deadline = deadlineOf(options);
      const signal = options.signal;
      const ifAvailable = options.ifAvailable === true;

      if (signal?.aborted) {
        return Promise.reject(abortReason(signal));
      }
      if (deadline !== undefined && deadline <= this.clock.now()) {
        return Promise.reject(new LockTimeoutError());
      }

      const currentGeneration = this.getPolicyGeneration(partition);
      if (
        options.hostPolicyGeneration !== undefined &&
        options.hostPolicyGeneration !== currentGeneration
      ) {
        return Promise.reject(
          new LockPolicyError(
            `Request generation ${options.hostPolicyGeneration} is stale; current generation is ${currentGeneration}`,
          ),
        );
      }

      const identity = createLockIdentity(
        partition,
        origin,
        name,
        currentGeneration,
      );
      const state = this.#getState(identity);
      const conflict = this.#hasConflict(state, mode);
      const blockedByFifo = state.queue.length > 0;

      // ifAvailable is a pure probe of this identity. In particular, it never
      // inspects another storage partition.
      if (ifAvailable && (conflict || blockedByFifo)) {
        return Promise.resolve(null);
      }

      return new Promise<WebLockLease | T | null>((resolve, reject) => {
        const sequence = state.nextSequence++;
        const record: RequestRecord = {
          state,
          mode,
          documentGeneration,
          requestID,
          sequence,
          ifAvailable,
          deadline,
          signal,
          callback: callback as LockCallback<unknown> | undefined,
          resolve: resolve as (value: unknown) => void,
          reject,
          status: "queued",
          settled: false,
          held: undefined,
          deadlineHandle: undefined,
          abortHandler: undefined,
          callbackStarted: false,
        };

        this.#installDeadline(record);
        this.#installAbort(record);

        if (
          !conflict &&
          !blockedByFifo &&
          state.holders.length === 0 &&
          state.queue.length === 0
        ) {
          this.#grant(record);
        } else {
          state.queue.push(record);
          this.#dispatch(state);
        }
      });
    } catch (error) {
      return Promise.reject(error);
    }
  }

  query(scope: LockQueryScope): LockQueryResult {
    this.#assertOpen();
    const partition = partitionOf(scope);
    const origin = assertOrigin(scope.origin);
    const includePending = scope.includePending !== false;
    const held: LockView[] = [];
    const pending: LockView[] = [];

    for (const state of this.#states.values()) {
      if (
        state.identity.storagePartition !== partition ||
        state.identity.origin !== origin
      ) {
        continue;
      }

      for (const holder of state.holders) {
        if (!holder.active) continue;
        const view = this.#makeView(holder.request, "held", holder.lease);
        if (this.#isVisible(scope, view)) held.push(view);
      }

      if (includePending) {
        for (const request of state.queue) {
          if (request.status !== "queued") continue;
          const view = this.#makeView(request, "pending", undefined);
          if (this.#isVisible(scope, view)) pending.push(view);
        }
      }
    }

    const bySequence = (left: LockView, right: LockView): number =>
      left.requestSequence - right.requestSequence ||
      left.lockNameHex.localeCompare(right.lockNameHex) ||
      left.requestID.localeCompare(right.requestID);
    held.sort(bySequence);
    pending.sort(bySequence);
    return { held, pending };
  }

  queryLocks(scope: LockQueryScope): LockQueryResult {
    return this.query(scope);
  }

  releaseDocument(scope: DocumentScope): DocumentReleaseResult;
  releaseDocument(
    storagePartition: string,
    origin: string,
    documentGeneration: number,
  ): DocumentReleaseResult;
  releaseDocument(
    scopeOrPartition: DocumentScope | string,
    origin?: string,
    documentGeneration?: number,
  ): DocumentReleaseResult {
    this.#assertOpen();
    const scope: DocumentScope =
      typeof scopeOrPartition === "string"
        ? {
            storagePartition: scopeOrPartition,
            origin: assertOrigin(origin),
            documentGeneration: assertGeneration(
              documentGeneration,
              "documentGeneration",
            ),
          }
        : scopeOrPartition;
    const partition = partitionOf(scope);
    const lockOrigin = assertOrigin(scope.origin);
    const generation = assertGeneration(
      scope.documentGeneration,
      "documentGeneration",
    );

    let pendingRejected = 0;
    let leasesReleased = 0;
    for (const state of this.#states.values()) {
      if (
        state.identity.storagePartition !== partition ||
        state.identity.origin !== lockOrigin
      ) {
        continue;
      }

      for (const request of state.queue.slice()) {
        if (request.documentGeneration !== generation) continue;
        if (!this.#removeQueued(request)) continue;
        this.#cleanupRecord(request);
        this.#rejectRecord(
          request,
          new LockNavigationError(
            "The requesting document was navigated away",
          ),
        );
        pendingRejected += 1;
      }

      for (const holder of state.holders.slice()) {
        if (
          !holder.active ||
          holder.request.documentGeneration !== generation
        ) {
          continue;
        }
        if (
          this.#releaseHeldLease(
            holder,
            "navigation",
            new LockNavigationError(
              "The lease-owning document was navigated away",
            ),
          )
        ) {
          leasesReleased += 1;
        }
      }
      this.#dispatch(state);
    }

    return { pendingRejected, leasesReleased };
  }

  navigateDocument(scope: DocumentScope): DocumentReleaseResult {
    return this.releaseDocument(scope);
  }

  setHostPolicyGeneration(
    storagePartition: string,
    generation: number,
    options: HostPolicyChangeOptions = {},
  ): HostPolicyChangeResult {
    this.#assertOpen();
    const partition = assertPartition(storagePartition);
    const nextGeneration = assertGeneration(
      generation,
      "hostPolicyGeneration",
    );
    const previousGeneration = this.getPolicyGeneration(partition);
    if (nextGeneration < previousGeneration) {
      throw new RangeError(
        "hostPolicyGeneration must not move backwards",
      );
    }
    this.#policyGenerations.set(partition, nextGeneration);

    if (nextGeneration === previousGeneration) {
      return {
        storagePartition: partition,
        previousGeneration,
        generation: nextGeneration,
        pendingRejected: 0,
        leasesRevoked: 0,
      };
    }

    const revokeGranted = options.revokeGranted ?? true;
    let pendingRejected = 0;
    let leasesRevoked = 0;
    const pendingError = new LockPolicyError(
      `Host policy generation changed from ${previousGeneration} to ${nextGeneration}`,
    );

    for (const state of this.#states.values()) {
      if (
        state.identity.storagePartition !== partition ||
        state.identity.hostPolicyGeneration === nextGeneration
      ) {
        continue;
      }

      for (const request of state.queue.slice()) {
        if (!this.#removeQueued(request)) continue;
        this.#cleanupRecord(request);
        this.#rejectRecord(request, pendingError);
        pendingRejected += 1;
      }

      for (const holder of state.holders.slice()) {
        if (!holder.active) continue;
        const shouldRevoke =
          typeof revokeGranted === "function"
            ? revokeGranted(holder.lease)
            : revokeGranted;
        if (!shouldRevoke) continue;
        if (
          this.#releaseHeldLease(
            holder,
            "policy",
            new LockPolicyError(
              `Lease ${holder.lease.leaseID} was revoked by host policy`,
            ),
          )
        ) {
          leasesRevoked += 1;
        }
      }
      this.#dispatch(state);
    }

    return {
      storagePartition: partition,
      previousGeneration,
      generation: nextGeneration,
      pendingRejected,
      leasesRevoked,
    };
  }

  advanceHostPolicyGeneration(
    storagePartition: string,
    options: HostPolicyChangeOptions = {},
  ): HostPolicyChangeResult {
    const current = this.getPolicyGeneration(storagePartition);
    return this.setHostPolicyGeneration(
      storagePartition,
      current + 1,
      options,
    );
  }

  updateHostPolicy(
    storagePartition: string,
    generation: number,
    options: HostPolicyChangeOptions = {},
  ): HostPolicyChangeResult {
    return this.setHostPolicyGeneration(
      storagePartition,
      generation,
      options,
    );
  }

  releaseLease(
    lease: WebLockLease,
    reason: LeaseReleaseReason = "explicit",
  ): void {
    if (!(lease instanceof LockLeaseImpl)) return;
    if (lease[LEASE_MANAGER]() !== this) return;
    const held = this.#leases.get(lease.leaseID);
    if (held === undefined || held.lease !== lease || !held.active) {
      // This is the late-callback guard: an old lease can never address a
      // newer lease that happens to reuse the same lock name.
      return;
    }
    this.#releaseHeldLease(held, reason, undefined);
  }

  close(reason = "Lock manager closed"): void {
    if (this.#closed) return;
    this.#closed = true;
    const error = new LockClosedError(reason);

    for (const state of this.#states.values()) {
      for (const request of state.queue.slice()) {
        this.#removeQueued(request);
        this.#cleanupRecord(request);
        this.#rejectRecord(request, error);
      }
      for (const holder of state.holders.slice()) {
        if (!holder.active) continue;
        this.#releaseHeldLease(holder, "shutdown", error);
      }
    }
  }

  #assertOpen(): void {
    if (this.#closed) throw new LockClosedError();
  }

  #getState(identity: LockIdentity): IdentityState {
    const key = lockIdentityKey(identity);
    let state = this.#states.get(key);
    if (state === undefined) {
      state = {
        key,
        identity,
        holders: [],
        queue: [],
        nextSequence: 1,
      };
      this.#states.set(key, state);
    }
    return state;
  }

  #hasConflict(state: IdentityState, mode: LockMode): boolean {
    if (state.holders.length === 0) return false;
    if (mode === LockMode.Exclusive) return true;
    return state.holders.some(
      (holder) =>
        holder.active && holder.request.mode === LockMode.Exclusive,
    );
  }

  #dispatch(state: IdentityState): void {
    while (true) {
      if (
        state.holders.some(
          (holder) =>
            holder.active && holder.request.mode === LockMode.Exclusive,
        )
      ) {
        return;
      }

      const head = state.queue[0];
      if (head === undefined) return;

      if (head.mode === LockMode.Exclusive) {
        if (state.holders.length > 0) return;
        state.queue.shift();
        this.#grant(head);
        return;
      }

      // Grant the maximal contiguous Shared batch. An Exclusive behind this
      // batch remains a FIFO barrier.
      while (
        state.queue.length > 0 &&
        state.queue[0].mode === LockMode.Shared
      ) {
        const request = state.queue.shift() as RequestRecord;
        this.#grant(request);
      }
      return;
    }
  }

  #grant(request: RequestRecord): void {
    request.status = "held";
    const lease = new LockLeaseImpl(
      this,
      request,
      this.#allocateLeaseID(),
    );
    const held: HeldLease = {
      lease,
      request,
      state: request.state,
      active: true,
    };
    request.held = held;
    request.state.holders.push(held);
    this.#leases.set(lease.leaseID, held);

    if (request.callback === undefined) {
      request.settled = true;
      request.resolve(lease);
      return;
    }

    queueMicrotask(() => {
      this.#startCallback(request);
    });
  }

  #startCallback(request: RequestRecord): void {
    if (request.settled || request.status !== "held") return;
    const held = request.held;
    if (held === undefined || !held.active) return;

    request.callbackStarted = true;
    let result: unknown;
    try {
      result = request.callback!(held.lease);
    } catch (error) {
      this.#finishCallback(request, false, error);
      return;
    }
    Promise.resolve(result).then(
      (value) => this.#finishCallback(request, true, value),
      (error) => this.#finishCallback(request, false, error),
    );
  }

  #finishCallback(
    request: RequestRecord,
    fulfilled: boolean,
    value: unknown,
  ): void {
    if (request.settled) return;
    const held = request.held;
    if (held !== undefined && held.active) {
      this.#releaseHeldLease(held, "callback", undefined);
    }
    request.status = "finished";
    if (fulfilled) {
      request.settled = true;
      request.resolve(value);
    } else {
      this.#rejectRecord(request, value);
    }
  }

  #installDeadline(request: RequestRecord): void {
    if (request.deadline === undefined) return;
    const delay = Math.max(0, request.deadline - this.clock.now());
    request.deadlineHandle = this.clock.setTimeout(() => {
      this.#onDeadline(request);
    }, delay);
  }

  #installAbort(request: RequestRecord): void {
    if (request.signal === undefined) return;
    const handler = (): void => {
      this.#onAbort(request);
    };
    request.abortHandler = handler;
    request.signal.addEventListener("abort", handler, { once: true });
  }

  #cleanupRecord(request: RequestRecord): void {
    if (request.deadlineHandle !== undefined) {
      this.clock.clearTimeout(request.deadlineHandle);
      request.deadlineHandle = undefined;
    }
    if (request.abortHandler !== undefined && request.signal !== undefined) {
      request.signal.removeEventListener("abort", request.abortHandler);
      request.abortHandler = undefined;
    }
  }

  #onDeadline(request: RequestRecord): void {
    request.deadlineHandle = undefined;

    if (request.status === "queued") {
      if (!this.#removeQueued(request)) return;
      this.#cleanupRecord(request);
      this.#rejectRecord(request, new LockTimeoutError());
      this.#dispatch(request.state);
      return;
    }

    const held = request.held;
    if (
      request.status === "held" &&
      held !== undefined &&
      held.active
    ) {
      this.#releaseHeldLease(
        held,
        "deadline",
        new LockTimeoutError(),
      );
      return;
    }

    if (!request.settled) {
      this.#cleanupRecord(request);
      this.#rejectRecord(request, new LockTimeoutError());
    }
  }

  #onAbort(request: RequestRecord): void {
    const reason = request.signal
      ? abortReason(request.signal)
      : new LockAbortError();

    if (request.status === "queued") {
      if (!this.#removeQueued(request)) return;
      this.#cleanupRecord(request);
      this.#rejectRecord(request, reason);
      this.#dispatch(request.state);
      return;
    }

    const held = request.held;
    if (
      request.status === "held" &&
      held !== undefined &&
      held.active
    ) {
      this.#releaseHeldLease(held, "abort", reason);
      return;
    }

    if (!request.settled) {
      this.#cleanupRecord(request);
      this.#rejectRecord(request, reason);
    }
  }

  #removeQueued(request: RequestRecord): boolean {
    if (request.status !== "queued") return false;
    const index = request.state.queue.indexOf(request);
    if (index === -1) return false;
    request.state.queue.splice(index, 1);
    request.status = "finished";
    return true;
  }

  #rejectRecord(request: RequestRecord, reason: unknown): boolean {
    if (request.settled) return false;
    request.settled = true;
    request.status = "finished";
    request.reject(reason);
    return true;
  }

  #releaseHeldLease(
    held: HeldLease,
    reason: LeaseReleaseReason,
    error: unknown,
  ): boolean {
    if (!held.active) return false;
    const current = this.#leases.get(held.lease.leaseID);
    if (current !== held) return false;

    held.active = false;
    this.#leases.delete(held.lease.leaseID);
    const index = held.state.holders.indexOf(held);
    if (index !== -1) held.state.holders.splice(index, 1);

    this.#cleanupRecord(held.request);
    const info = held.lease[LEASE_INTERNAL](reason, this.clock.now());
    if (info === undefined) return false;

    if (
      reason !== "callback" &&
      reason !== "explicit" &&
      !held.request.settled
    ) {
      this.#rejectRecord(
        held.request,
        error ??
          (reason === "abort"
            ? new LockAbortError()
            : reason === "deadline"
              ? new LockTimeoutError()
              : reason === "navigation"
                ? new LockNavigationError()
                : reason === "policy"
                  ? new LockPolicyError()
                  : new LockClosedError()),
      );
    }

    try {
      this.#onLeaseReleased?.({ lease: held.lease, info });
    } catch {
      // A host notification must not corrupt lock state.
    }

    this.#dispatch(held.state);
    return true;
  }

  #allocateLeaseID(): string {
    const base = this.#createLeaseID();
    if (typeof base !== "string" || base.length === 0) {
      throw new TypeError("createLeaseID must return a non-empty string");
    }
    let candidate = base;
    let suffix = 1;
    while (
      this.#usedLeaseIDs.has(candidate) ||
      this.#leases.has(candidate)
    ) {
      candidate = `${base}:${suffix++}`;
    }
    this.#usedLeaseIDs.add(candidate);
    return candidate;
  }

  #makeView(
    request: RequestRecord,
    state: "held" | "pending",
    lease: LockLeaseImpl | undefined,
  ): LockView {
    const identity = request.state.identity;
    const base = {
      storagePartition: identity.storagePartition,
      origin: identity.origin,
      name: visibleName(identity.lockName),
      lockName: identity.lockName.slice(),
      lockNameHex: identity.lockNameHex,
      mode: request.mode,
      documentGeneration: request.documentGeneration,
      requestID: request.requestID,
      requestSequence: request.sequence,
      hostPolicyGeneration: identity.hostPolicyGeneration,
      state,
      ifAvailable: request.ifAvailable,
      ...(request.deadline === undefined
        ? {}
        : { deadline: request.deadline }),
      ...(lease === undefined ? {} : { leaseID: lease.leaseID }),
    };
    return Object.freeze(base) as LockView;
  }

  #isVisible(scope: LockQueryScope, view: LockView): boolean {
    if (this.#canViewLock === undefined) {
      // Least privilege by default: once the caller identifies its document,
      // it cannot observe another document's lock activity.
      return (
        scope.documentGeneration === undefined ||
        view.documentGeneration === scope.documentGeneration
      );
    }
    try {
      return this.#canViewLock(scope, view) === true;
    } catch {
      return false;
    }
  }
}

export function createLockManager(
  options: LockManagerOptions = {},
): WebLockManager {
  return new WebLockManager(options);
}

/** Descriptive alias for the deterministic timer queue. */
export const DeterministicScheduler = ManualClock;
/** Short alias used by some hosts. */
export { WebLockManager as LockManager };

export default WebLockManager;
