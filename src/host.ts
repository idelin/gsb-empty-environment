import { DeterministicEventLoop } from "./event-loop.ts";
import type { ScheduledTask } from "./event-loop.ts";
import { isWellFormedAuthEpochEvent } from "./auth.ts";
import { InMemoryDom } from "./dom.ts";
import type { DomAdapter, DomSnapshotLike } from "./dom.ts";
import { TimerRegistry } from "./timers.ts";
import type {
  AuditEvent,
  AuthEpochAuthority,
  AuthEpochEvent,
  AuthEpochResult,
  AuthEpochState,
  DeliveryResult,
  DiscardReason,
  DocumentCapability,
  DocumentIdentity,
  DocumentState,
  DocumentView,
  FormSubmitResult,
  FreezeKind,
  NavigationRequest,
  NetworkRequestView,
  PagehideOptions,
  PagehideResult,
  PageshowRequest,
  Partition,
  PolicyAuthority,
  PolicySnapshot,
  PolicyUpdateResult,
  ReadResult,
  ResumeResult,
  RunOptions,
} from "./types.ts";

export class BfcacheSecurityError extends Error {
  readonly code: string;

  constructor(code: string, message: string) {
    super(message);
    this.name = "BfcacheSecurityError";
    this.code = code;
  }
}

export class CapabilityDeniedError extends BfcacheSecurityError {
  constructor(code: string, message: string) {
    super(code, message);
    this.name = "CapabilityDeniedError";
  }
}

export class DocumentUnavailableError extends BfcacheSecurityError {
  constructor(message: string) {
    super("document-unavailable", message);
    this.name = "DocumentUnavailableError";
  }
}

/**
 * Switches used only by the differential trace explorer.
 *
 * Production hosts always use SAFE_GUARDS.  A review run may disable one guard
 * to model a candidate implementation and prove that the missing check has a
 * concrete, shortest failing trace.
 */
export interface ReviewGuards {
  readonly revalidateOnResume: boolean;
  readonly quarantineOnRevokedEvent: boolean;
  readonly purgeOnRevokedEvent: boolean;
  readonly checkDeliveryAgainstAuth: boolean;
  readonly authorizeSensitiveEffects: boolean;
  readonly dropDeferredOnFailedResume: boolean;
}

export const SAFE_GUARDS: ReviewGuards = Object.freeze({
  revalidateOnResume: true,
  quarantineOnRevokedEvent: true,
  purgeOnRevokedEvent: true,
  checkDeliveryAgainstAuth: true,
  authorizeSensitiveEffects: true,
  dropDeferredOnFailedResume: true,
});

export interface BfcacheHostOptions {
  readonly browserSessionID: string;
  readonly authAuthority?: AuthEpochAuthority;
  readonly auth?: AuthEpochAuthority;
  readonly authService?: AuthEpochAuthority;
  readonly policyAuthority?: PolicyAuthority;
  readonly policy?: PolicyAuthority;
  readonly policyService?: PolicyAuthority;
  readonly createDom?: (
    identity: DocumentIdentity,
    initialSensitiveDom?: Readonly<Record<string, string>>,
  ) => DomAdapter;
  readonly eventLoop?: DeterministicEventLoop;
  readonly maxDeferredEvents?: number;
  /** Test/review only.  Omit for a production host. */
  readonly reviewGuards?: Partial<ReviewGuards>;
}

export interface PageLifecycleEvent {
  readonly type: "pagehide" | "pageshow";
  readonly persisted: boolean;
  readonly historyEntryID: string;
  readonly documentGeneration: number;
}

export type PageLifecycleListener = (event: PageLifecycleEvent) => void;

export interface DocumentPort {
  readonly id: string;
  readonly documentGeneration: number;
  onmessage: ((data: unknown) => void) | null;
  close(): void;
}

interface DeferredEvent {
  readonly arrivalSeq: number;
  readonly documentGeneration: number;
  readonly kind: FreezeKind;
  readonly capability?: DocumentCapability;
  readonly label: string;
  run?: () => void | Promise<void>;
}

interface NetworkRequestRecord {
  readonly id: string;
  readonly documentGeneration: number;
  readonly url: string;
  state: "pending" | "completed" | "delivered" | "dropped";
  handler?: (body: string) => void | Promise<void>;
  body?: string;
  dropReason?: string;
}

interface PortRecord {
  readonly id: string;
  readonly documentGeneration: number;
  onmessage: ((data: unknown) => void) | null;
  closed: boolean;
}

interface PromiseCallbackRecord {
  readonly onFulfilled?: (value: unknown) => unknown;
  readonly onRejected?: (reason: unknown) => unknown;
  readonly child: GuardedPromise<unknown>;
}

interface PromiseRecord {
  readonly id: string;
  readonly documentGeneration: number;
  state: "pending" | "settled" | "delivered" | "dropped";
  outcome?: "fulfilled" | "rejected";
  value?: unknown;
  readonly callbacks: PromiseCallbackRecord[];
}

interface DocumentRecord {
  readonly identity: DocumentIdentity;
  readonly historyEntryID: string;
  state: DocumentState;
  readonly dom: DomAdapter;
  readonly timers: TimerRegistry;
  readonly tokens: Map<string, string>;
  readonly listeners: {
    pagehide: Set<PageLifecycleListener>;
    pageshow: Set<PageLifecycleListener>;
  };
  readonly deferred: DeferredEvent[];
  readonly requests: Map<string, NetworkRequestRecord>;
  readonly ports: Map<string, PortRecord>;
  readonly promises: Map<string, PromiseRecord>;
  droppedDeferredCount: number;
  freezing: boolean;
  snapshot?: DomSnapshotLike;
  policy?: PolicySnapshot;
  policyStale: boolean;
  authorizationLost: boolean;
  freezeStartedAt?: number;
  frozenArrivalSeq?: number;
}

interface SessionRecord {
  readonly accountID: string;
  readonly authSessionID: string;
  epoch: number;
  state: AuthEpochState;
  readonly event: AuthEpochEvent;
  maxEpochSeen: number;
}

interface AuthReadResult {
  event?: AuthEpochEvent;
  error?: unknown;
}

interface PolicyReadResult {
  policy?: PolicySnapshot;
  error?: unknown;
}

function partitionKey(partition: Partition): string {
  return JSON.stringify([
    partition.topLevelSite,
    partition.origin,
    partition.browserSessionID,
  ]);
}

function samePartition(left: Partition, right: Partition): boolean {
  return partitionKey(left) === partitionKey(right);
}

function policyFingerprint(policy: PolicySnapshot | undefined): string {
  if (!policy) {
    return "none";
  }
  return JSON.stringify([
    policy.allowSensitiveDom,
    policy.allowNetwork,
    policy.allowFormSubmission,
    policy.allowMessagePort,
  ]);
}

function copyPolicy(policy: PolicySnapshot): PolicySnapshot {
  return Object.freeze({ ...policy });
}

function copyAuthEvent(event: AuthEpochEvent): AuthEpochEvent {
  return Object.freeze({ ...event });
}

/**
 * Merge two snapshots of the same policy generation.  A denial always wins:
 * an authority rollback can never re-open a capability the host already saw
 * denied.
 */
function stricterPolicy(
  left: PolicySnapshot,
  right: PolicySnapshot,
): PolicySnapshot {
  return Object.freeze({
    generation: Math.max(left.generation, right.generation),
    allowSensitiveDom: left.allowSensitiveDom && right.allowSensitiveDom,
    allowNetwork: left.allowNetwork && right.allowNetwork,
    allowFormSubmission:
      left.allowFormSubmission && right.allowFormSubmission,
    allowMessagePort: left.allowMessagePort && right.allowMessagePort,
    ...(left.label && right.label && left.label === right.label
      ? { label: left.label }
      : {}),
  });
}

function assertNonEmpty(value: unknown, name: string): string {
  if (typeof value !== "string" || value.length === 0) {
    throw new BfcacheSecurityError(
      "invalid-input",
      `${name} must be a non-empty string`,
    );
  }
  return value;
}

function isPolicySnapshot(policy: unknown): policy is PolicySnapshot {
  if (!policy || typeof policy !== "object") {
    return false;
  }
  const candidate = policy as Partial<PolicySnapshot>;
  return (
    Number.isSafeInteger(candidate.generation) &&
    (candidate.generation as number) >= 0 &&
    typeof candidate.allowSensitiveDom === "boolean" &&
    typeof candidate.allowNetwork === "boolean" &&
    typeof candidate.allowFormSubmission === "boolean" &&
    typeof candidate.allowMessagePort === "boolean"
  );
}

/**
 * Browser-host page freeze/restore state machine.
 *
 * The host owns every sensitive capability.  Page code never touches raw
 * timers, fetch, form submission, ports or DOM directly; it uses the guarded
 * methods below.  Consequently a discarded document cannot run an old task
 * even if browser-level BFCache bookkeeping is wrong.
 */
export class BfcacheHost {
  readonly #browserSessionID: string;
  readonly #auth: AuthEpochAuthority;
  readonly #policy: PolicyAuthority;
  readonly #loop: DeterministicEventLoop;
  readonly #guards: ReviewGuards;
  readonly #maxDeferredEvents: number;
  readonly #createDom?: BfcacheHostOptions["createDom"];

  readonly #documents = new Map<number, DocumentRecord>();
  readonly #frozenByEntry = new Map<string, number>();
  readonly #sessions = new Map<string, SessionRecord>();
  readonly #policies = new Map<string, PolicySnapshot>();
  readonly #timerOwners = new Map<number, { generation: number; localID: number }>();
  readonly #audit: AuditEvent[] = [];
  readonly #violations: AuditEvent[] = [];

  #currentEntryID: string | undefined;
  #nextGeneration = 1;
  #nextArrival = 1;
  #nextRequest = 1;
  #nextPort = 1;
  #nextPromise = 1;
  #nextTimer = 1;
  #auditSeq = 1;
  #now = 0;

  constructor(options: BfcacheHostOptions) {
    this.#browserSessionID = assertNonEmpty(
      options.browserSessionID,
      "browserSessionID",
    );
    const auth =
      options.authAuthority ?? options.auth ?? options.authService;
    const policy =
      options.policyAuthority ?? options.policy ?? options.policyService;
    if (!auth) {
      throw new BfcacheSecurityError(
        "invalid-configuration",
        "an AuthEpochAuthority is required",
      );
    }
    if (!policy) {
      throw new BfcacheSecurityError(
        "invalid-configuration",
        "a PolicyAuthority is required",
      );
    }
    this.#auth = auth;
    this.#policy = policy;
    this.#loop = options.eventLoop ?? new DeterministicEventLoop();
    this.#guards = { ...SAFE_GUARDS, ...(options.reviewGuards ?? {}) };
    this.#maxDeferredEvents = options.maxDeferredEvents ?? 1024;
    this.#createDom = options.createDom;
  }

  get browserSessionID(): string {
    return this.#browserSessionID;
  }

  get eventLoop(): DeterministicEventLoop {
    return this.#loop;
  }

  get currentEntryID(): string | undefined {
    return this.#currentEntryID;
  }

  get state(): DocumentState | undefined {
    return this.currentDocument()?.state;
  }

  get identity(): DocumentIdentity | undefined {
    return this.currentDocument()?.identity;
  }

  get audit(): readonly AuditEvent[] {
    return this.#audit;
  }

  get violations(): readonly AuditEvent[] {
    return this.#violations;
  }

  get now(): number {
    return this.#now;
  }

  /**
   * Create and make visible a new document.  This is a normal navigation, not
   * a BFCache restore; the authentication authority is read and its signature
   * is verified before any sensitive capability is exposed.
   */
  async navigate(request: NavigationRequest): Promise<DocumentView> {
    const historyEntryID = assertNonEmpty(
      request.historyEntryID,
      "historyEntryID",
    );
    const topLevelSite = assertNonEmpty(request.topLevelSite, "topLevelSite");
    const origin = assertNonEmpty(request.origin, "origin");
    const authSessionID = assertNonEmpty(
      request.authSessionID,
      "authSessionID",
    );
    const browserSessionID = assertNonEmpty(
      request.browserSessionID ?? this.#browserSessionID,
      "browserSessionID",
    );
    const partition: Partition = {
      topLevelSite,
      origin,
      browserSessionID,
    };

    const authRead = await this.#readAuth(authSessionID);
    if (authRead.error) {
      throw new BfcacheSecurityError(
        "authority-read-failed",
        "could not read the current auth epoch",
      );
    }
    const authEvent = authRead.event;
    if (!authEvent) {
      throw new BfcacheSecurityError(
        "auth-session-missing",
        `auth session ${authSessionID} is not known to the authority`,
      );
    }
    if (!(await this.#verifyAuthEvent(authEvent))) {
      throw new BfcacheSecurityError(
        "signature-invalid",
        "auth epoch event signature verification failed",
      );
    }
    if (authEvent.state !== "Active") {
      throw new BfcacheSecurityError(
        "auth-state-not-active",
        "cannot open a sensitive document without an Active session",
      );
    }
    if (
      request.authEpoch !== undefined &&
      request.authEpoch !== authEvent.epoch
    ) {
      throw new BfcacheSecurityError(
        "auth-epoch-mismatch",
        "navigation auth epoch does not match the authority",
      );
    }
    this.#observeAuthView(authEvent);
    const effectiveSession = this.#sessions.get(authSessionID);
    if (
      effectiveSession &&
      effectiveSession.accountID !== authEvent.accountID
    ) {
      throw new BfcacheSecurityError(
        "account-mismatch",
        "the auth session is bound to a different account",
      );
    }
    if (effectiveSession?.state === "Revoked") {
      throw new BfcacheSecurityError(
        "auth-session-revoked",
        "the host has already observed a Revoked epoch for this session",
      );
    }
    if (
      effectiveSession &&
      effectiveSession.epoch !== authEvent.epoch
    ) {
      throw new BfcacheSecurityError(
        "auth-epoch-mismatch",
        "the authority returned a stale auth epoch",
      );
    }

    const policyRead = await this.#readPolicy(partition);
    if (policyRead.error || !policyRead.policy) {
      throw new BfcacheSecurityError(
        "policy-unavailable",
        "could not read the partition policy",
      );
    }
    this.#observePolicyView(partition, policyRead.policy);
    const effectivePolicy =
      this.#policies.get(partitionKey(partition)) ??
      copyPolicy(policyRead.policy);

    if (
      this.#currentEntryID !== undefined &&
      this.#currentEntryID !== historyEntryID
    ) {
      const previous = this.#findLiveDocument(this.#currentEntryID);
      if (previous) {
        this.pagehide({
          historyEntryID: this.#currentEntryID,
          persisted: true,
        });
      }
    }

    for (const old of [...this.#documents.values()]) {
      if (
        old.historyEntryID !== historyEntryID ||
        old.state === "discarded" ||
        old.state === "disposed"
      ) {
        continue;
      }
      if (old.state === "frozen") {
        this.#discardDocument(old, "document-generation-mismatch", [
          "entry-replaced-by-navigation",
        ]);
      } else {
        this.#disposeDocument(old, "entry-replaced-by-navigation");
      }
    }

    const existingFrozen = this.#frozenByEntry.get(historyEntryID);
    if (existingFrozen !== undefined) {
      const old = this.#documents.get(existingFrozen);
      if (old) {
        this.#discardDocument(old, "document-generation-mismatch", [
          "entry-replaced-by-navigation",
        ]);
      }
      this.#frozenByEntry.delete(historyEntryID);
    }

    const identity = this.#newIdentity(
      partition,
      authEvent,
      effectivePolicy,
    );
    const dom = this.#createDom
      ? this.#createDom(identity, request.sensitiveDom)
      : new InMemoryDom(request.sensitiveDom);
    const document: DocumentRecord = {
      identity,
      historyEntryID,
      state: "live",
      dom,
      timers: new TimerRegistry(),
      tokens: new Map(),
      listeners: { pagehide: new Set(), pageshow: new Set() },
      deferred: [],
      requests: new Map(),
      ports: new Map(),
      promises: new Map(),
      droppedDeferredCount: 0,
      freezing: false,
      policy: effectivePolicy,
      policyStale: false,
      authorizationLost: false,
    };
    this.#documents.set(identity.documentGeneration, document);
    this.#currentEntryID = historyEntryID;
    this.#auditEvent("document-navigate", "info", document, {
      topLevelSite,
      origin,
      authEpoch: authEvent.epoch,
      policyGeneration: effectivePolicy.generation,
    });
    return this.#view(document);
  }

  /**
   * Synchronous `pagehide` handler.
   *
   * Page listeners run first (the document is still alive), then the host
   * snapshots DOM into memory and freezes timers, network, forms and ports
   * before returning.  When `persisted` is false the document is disposed and
   * all sensitive state is wiped immediately.
   */
  pagehide(options: PagehideOptions = {}): PagehideResult {
    const historyEntryID =
      options.historyEntryID ??
      this.#currentEntryID ??
      (() => {
        throw new DocumentUnavailableError(
          "pagehide requires a current history entry",
        );
      })();
    const document = this.#findEntryDocument(historyEntryID);
    if (!document) {
      return {
        accepted: false,
        historyEntryID,
        documentGeneration: 0,
        persisted: options.persisted !== false,
        state: "discarded",
        reason: "no-live-document",
      };
    }

    const generation = document.identity.documentGeneration;
    if (document.state === "frozen") {
      return {
        accepted: false,
        historyEntryID,
        documentGeneration: generation,
        persisted: true,
        state: document.state,
        reason: "already-frozen",
      };
    }
    if (document.freezing) {
      return {
        accepted: false,
        historyEntryID,
        documentGeneration: generation,
        persisted: options.persisted !== false,
        state: document.state,
        reason: "already-freezing",
      };
    }

    const persisted = options.persisted !== false;
    if (document.state === "locked") {
      this.#disposeDocument(document, "revoked-document-unloaded");
      return {
        accepted: true,
        historyEntryID,
        documentGeneration: generation,
        persisted: false,
        state: document.state,
        reason: "revoked-document-disposed",
      };
    }

    document.freezing = true;
    try {
      this.#dispatchLifecycle(document, "pagehide", persisted);
    } finally {
      document.freezing = false;
    }

    if (!persisted) {
      this.#disposeDocument(document, "pagehide-not-persisted");
      return {
        accepted: true,
        historyEntryID,
        documentGeneration: generation,
        persisted: false,
        state: document.state,
      };
    }

    document.state = "frozen";
    document.freezeStartedAt = this.#now;
    document.frozenArrivalSeq = this.#nextArrival - 1;
    document.timers.pause(this.#now);
    document.snapshot = document.dom.snapshot();
    this.#frozenByEntry.set(historyEntryID, generation);
    this.#auditEvent("bfcache-freeze", "info", document, {
      snapshotBytes: document.snapshot.bytes,
      snapshotStorage: "memory",
    });
    return {
      accepted: true,
      historyEntryID,
      documentGeneration: generation,
      persisted: true,
      state: document.state,
      snapshotBytes: document.snapshot.bytes,
    };
  }

  /**
   * Host-level `pageshow` entry point.
   *
   * Validation is queued on the control lane.  Document tasks cannot run
   * while the target is in `restoring`, and control tasks always run first.
   * The returned promise resolves when the queued validation task runs.
   */
  /**
   * Queue a `pageshow` validation task without running it.  Use this when a
   * test or embedding host needs to inject more auth/policy events before the
   * control lane is drained.
   */
  enqueuePageshow(request: PageshowRequest): Promise<ResumeResult> {
    return new Promise<ResumeResult>((resolve) => {
      this.#loop.enqueue(
        "control",
        `pageshow:${request.historyEntryID}`,
        async () => {
          try {
            resolve(await this.#handlePageshow(request));
          } catch (error) {
            const generation = this.#frozenByEntry.get(
              request.historyEntryID,
            );
            const document =
              generation === undefined
                ? undefined
                : this.#documents.get(generation);
            let freshGeneration: number | undefined;
            if (document) {
              this.#discardDocument(document, "authority-read-failed", [
                error instanceof Error ? error.message : String(error),
              ]);
              this.#frozenByEntry.delete(request.historyEntryID);
              try {
                const fresh = await this.#freshNavigationAfterDiscard(
                  document,
                  request.currentPartition,
                  undefined,
                  undefined,
                );
                freshGeneration = fresh.identity.documentGeneration;
              } catch {
                freshGeneration = undefined;
              }
            }
            resolve({
              outcome: "discarded",
              historyEntryID: request.historyEntryID,
              ...(document
                ? {
                    previousDocumentGeneration:
                      document.identity.documentGeneration,
                  }
                : {}),
              ...(freshGeneration !== undefined
                ? { documentGeneration: freshGeneration }
                : {}),
              reason: "authority-read-failed",
              details: [
                error instanceof Error ? error.message : String(error),
              ],
            });
          }
        },
      );
    });
  }

  /**
   * Validate and restore a BFCache document.
   *
   * The call drains the control lane (never the document lane), so the result
   * is available without a separate `runUntilIdle` call while queued document
   * work stays ordered and observable.
   */
  async pageshow(request: PageshowRequest): Promise<ResumeResult> {
    const result = this.enqueuePageshow(request);
    await this.#loop.runControlUntilIdle();
    return result;
  }

  pageshowNow(request: PageshowRequest): Promise<ResumeResult> {
    return this.pageshow(request);
  }

  /**
   * Push a signed AuthEpoch event into the host.
   *
   * Verification and epoch monotonicity are checked on the control lane.  A
   * Revoked event quarantines every document bound to that session before any
   * queued document task can run.
   */
  enqueueAuthEpoch(event: AuthEpochEvent): Promise<AuthEpochResult> {
    const candidate =
      event && typeof event === "object"
        ? Object.freeze({ ...event })
        : event;
    return new Promise<AuthEpochResult>((resolve) => {
      this.#loop.enqueue(
        "control",
        `auth-epoch:${candidate?.authSessionID ?? ""}:${candidate?.epoch ?? ""}`,
        async () => {
          try {
            resolve(await this.#applyAuthEpoch(candidate));
          } catch (error) {
            resolve({
              accepted: false,
              reason:
                error instanceof Error ? error.message : String(error),
              authSessionID:
                typeof candidate?.authSessionID === "string"
                  ? candidate.authSessionID
                  : "",
            });
          }
        },
      );
    });
  }

  async receiveAuthEpoch(event: AuthEpochEvent): Promise<AuthEpochResult> {
    const result = this.enqueueAuthEpoch(event);
    await this.#loop.runControlUntilIdle();
    return result;
  }

  receiveAuthEpochNow(event: AuthEpochEvent): Promise<AuthEpochResult> {
    return this.receiveAuthEpoch(event);
  }

  enqueuePolicy(
    policy: PolicySnapshot,
    partition?: Partition,
  ): Promise<PolicyUpdateResult> {
    const candidate =
      policy && typeof policy === "object"
        ? copyPolicy(policy)
        : policy;
    return new Promise<PolicyUpdateResult>((resolve) => {
      this.#loop.enqueue(
        "control",
        `policy:${candidate?.generation ?? ""}`,
        async () => {
          try {
            resolve(this.#applyPolicy(candidate, partition));
          } catch (error) {
            resolve({
              accepted: false,
              generation: Number(candidate?.generation ?? 0),
              reason:
                error instanceof Error ? error.message : String(error),
            });
          }
        },
      );
    });
  }

  async receivePolicy(
    policy: PolicySnapshot,
    partition?: Partition,
  ): Promise<PolicyUpdateResult> {
    const result = this.enqueuePolicy(policy, partition);
    await this.#loop.runControlUntilIdle();
    return result;
  }

  receivePolicyNow(
    policy: PolicySnapshot,
    partition?: Partition,
  ): Promise<PolicyUpdateResult> {
    return this.receivePolicy(policy, partition);
  }

  runUntilIdle(options: RunOptions = {}): Promise<number> {
    return this.#loop.runUntilIdle(options);
  }

  runNext(): ReturnType<DeterministicEventLoop["runNext"]> {
    return this.#loop.runNext();
  }

  getDocument(
    reference: number | string = this.#currentEntryID ?? -1,
  ): DocumentView | undefined {
    let document: DocumentRecord | undefined;
    if (typeof reference === "number") {
      document = this.#documents.get(reference);
    } else {
      document =
        this.#findLiveDocument(reference) ??
        (() => {
          const generation = this.#frozenByEntry.get(reference);
          return generation === undefined
            ? undefined
            : this.#documents.get(generation);
        })() ??
        [...this.#documents.values()].find(
          (candidate) => candidate.historyEntryID === reference,
        );
    }
    return document ? this.#view(document) : undefined;
  }

  currentDocument(): DocumentView | undefined {
    return this.#currentEntryID
      ? this.getDocument(this.#currentEntryID)
      : undefined;
  }

  authSessionView(authSessionID: string):
    | {
        accountID: string;
        epoch: number;
        state: AuthEpochState;
        maxEpochSeen: number;
      }
    | undefined {
    const session = this.#sessions.get(authSessionID);
    return session
      ? {
          accountID: session.accountID,
          epoch: session.epoch,
          state: session.state,
          maxEpochSeen: session.maxEpochSeen,
        }
      : undefined;
  }

  documents(): DocumentView[] {
    return [...this.#documents.values()].map((document) =>
      this.#view(document),
    );
  }

  history(): Array<{
    historyEntryID: string;
    currentGeneration?: number;
    frozenGeneration?: number;
  }> {
    const entries = new Set<string>([
      ...this.#frozenByEntry.keys(),
      ...[...this.#documents.values()].map(
        (document) => document.historyEntryID,
      ),
    ]);
    return [...entries].map((historyEntryID) => {
      const current = [...this.#documents.values()]
        .filter(
          (document) =>
            document.historyEntryID === historyEntryID &&
            (document.state === "live" || document.state === "locked"),
        )
        .at(-1);
      return {
        historyEntryID,
        ...(current
          ? { currentGeneration: current.identity.documentGeneration }
          : {}),
        ...(this.#frozenByEntry.has(historyEntryID)
          ? { frozenGeneration: this.#frozenByEntry.get(historyEntryID) }
          : {}),
      };
    });
  }

  addPageListener(
    type: "pagehide" | "pageshow",
    listener: PageLifecycleListener,
    reference: number | string = this.#currentEntryID ?? -1,
  ): () => void {
    const view = this.getDocument(reference);
    const document = view
      ? this.#documents.get(view.identity.documentGeneration)
      : undefined;
    if (!document) {
      throw new DocumentUnavailableError("no document for lifecycle listener");
    }
    document.listeners[type].add(listener);
    return () => {
      document.listeners[type].delete(listener);
    };
  }

  // ---------------------------------------------------------------------
  // Guarded capabilities
  // ---------------------------------------------------------------------

  readSensitive(selector: string, reference?: number | string): string {
    const document = this.#requireDocument(reference);
    const gate = this.#sensitiveGate(document, "sensitive-dom");
    if (!gate.ok) {
      this.#auditEvent("sensitive-dom-read", "denied", document, {
        selector,
        reason: gate.reason,
      }, { afterRevocation: gate.afterRevocation });
      throw new CapabilityDeniedError(
        gate.reason ?? "capability-denied",
        `sensitive DOM read denied: ${gate.reason}`,
      );
    }
    const value = document.dom.read(selector);
    if (value === undefined) {
      throw new DocumentUnavailableError(`no DOM node for selector ${selector}`);
    }
    this.#auditEvent(
      "sensitive-dom-read",
      "allowed",
      document,
      { selector, bytes: value.length },
      {
        afterRevocation: gate.afterRevocation,
        violation: gate.afterRevocation,
      },
    );
    return value;
  }

  tryReadSensitive(
    selector: string,
    reference?: number | string,
  ): ReadResult {
    try {
      return { ok: true, value: this.readSensitive(selector, reference) };
    } catch (error) {
      return {
        ok: false,
        reason: error instanceof BfcacheSecurityError ? error.code : "error",
      };
    }
  }

  readSensitiveToken(name: string, reference?: number | string): string {
    const document = this.#requireDocument(reference);
    const gate = this.#sensitiveGate(document, "sensitive-dom");
    if (!gate.ok) {
      this.#auditEvent("sensitive-token-read", "denied", document, {
        name,
        reason: gate.reason,
      }, { afterRevocation: gate.afterRevocation });
      throw new CapabilityDeniedError(
        gate.reason ?? "capability-denied",
        `sensitive token read denied: ${gate.reason}`,
      );
    }
    const value = document.tokens.get(name);
    if (value === undefined) {
      throw new DocumentUnavailableError(`no in-memory token named ${name}`);
    }
    this.#auditEvent(
      "sensitive-token-read",
      "allowed",
      document,
      { name, bytes: value.length },
      {
        afterRevocation: gate.afterRevocation,
        violation: gate.afterRevocation,
      },
    );
    return value;
  }

  submitForm(action: string, reference?: number | string): FormSubmitResult {
    const document = this.#requireDocument(reference);
    const gate = this.#sensitiveGate(document, "form-submission");
    if (!gate.ok) {
      this.#auditEvent("form-submit", "denied", document, {
        action,
        reason: gate.reason,
      }, { afterRevocation: gate.afterRevocation });
      return { ok: false, reason: gate.reason };
    }
    this.#auditEvent(
      "form-submit",
      "allowed",
      document,
      { action },
      {
        afterRevocation: gate.afterRevocation,
        violation: gate.afterRevocation,
      },
    );
    return { ok: true };
  }

  /**
   * Start a network request.  The response is always delivered through the
   * generation-fenced queue, never by native promise timing.
   */
  startNetworkRequest(
    url: string,
    handler?: (body: string) => void | Promise<void>,
    reference?: number | string,
  ): string {
    const document = this.#requireDocument(reference);
    const gate = this.#sensitiveGate(document, "network");
    if (!gate.ok) {
      this.#auditEvent("network-submit", "denied", document, {
        url,
        reason: gate.reason,
      }, { afterRevocation: gate.afterRevocation });
      throw new CapabilityDeniedError(
        gate.reason ?? "capability-denied",
        `network request denied: ${gate.reason}`,
      );
    }
    const id = `req-${this.#nextRequest}`;
    this.#nextRequest += 1;
    document.requests.set(id, {
      id,
      documentGeneration: document.identity.documentGeneration,
      url,
      state: "pending",
      ...(handler ? { handler } : {}),
    });
    this.#auditEvent(
      "network-submit",
      "allowed",
      document,
      { url, requestID: id },
      {
        afterRevocation: gate.afterRevocation,
        violation: gate.afterRevocation,
      },
    );
    return id;
  }

  deliverNetworkResponse(
    requestID: string,
    body: string,
  ): DeliveryResult {
    const request = this.#findRequest(requestID);
    if (!request) {
      return { accepted: false, queued: false, deferred: false, reason: "unknown-request" };
    }
    if (request.state !== "pending") {
      return {
        accepted: false,
        queued: false,
        deferred: false,
        reason: `request-${request.state}`,
      };
    }
    const document = this.#documents.get(request.documentGeneration);
    if (!document) {
      request.state = "dropped";
      return {
        accepted: false,
        queued: false,
        deferred: false,
        reason: "document-missing",
      };
    }
    request.state = "completed";
    request.body = body;
    const arrivalSeq = this.#nextArrival;
    this.#nextArrival += 1;
    const event: DeferredEvent = {
      arrivalSeq,
      documentGeneration: document.identity.documentGeneration,
      kind: "network-response",
      capability: "network",
      label: `network-response:${requestID}`,
      run: async () => {
        request.state = "delivered";
        const payload = request.body;
        request.body = undefined;
        if (request.handler) {
          await request.handler(payload ?? "");
        }
      },
    };
    const disposition = this.#submit(document, event);
    this.#auditEvent("network-response-arrived", "info", document, {
      requestID,
      arrivalSeq,
      disposition,
    });
    if (disposition === "dropped") {
      request.state = "dropped";
      request.body = "";
      request.dropReason = "document-not-deliverable";
      return {
        accepted: false,
        queued: false,
        deferred: false,
        reason: request.dropReason,
      };
    }
    return {
      accepted: true,
      queued: disposition === "queued",
      deferred: disposition === "deferred",
    };
  }

  createMessagePort(reference?: number | string): DocumentPort {
    const document = this.#requireDocument(reference);
    const gate = this.#sensitiveGate(document, "message-port");
    if (!gate.ok) {
      this.#auditEvent(
        "message-port-created",
        "denied",
        document,
        { reason: gate.reason },
        { afterRevocation: gate.afterRevocation },
      );
      throw new CapabilityDeniedError(
        gate.reason ?? "capability-denied",
        "cannot create a MessagePort for this document",
      );
    }
    const id = `port-${this.#nextPort}`;
    this.#nextPort += 1;
    const record: PortRecord = {
      id,
      documentGeneration: document.identity.documentGeneration,
      onmessage: null,
      closed: false,
    };
    document.ports.set(id, record);
    const port: DocumentPort = {
      id,
      documentGeneration: record.documentGeneration,
      get onmessage() {
        return record.onmessage;
      },
      set onmessage(handler) {
        record.onmessage = typeof handler === "function" ? handler : null;
      },
      close: () => {
        record.closed = true;
        record.onmessage = null;
        const owner = this.#documents.get(record.documentGeneration);
        owner?.ports.delete(id);
        this.#auditEvent("message-port-closed", "info", owner, { portID: id });
      },
    };
    this.#auditEvent("message-port-created", "info", document, { portID: id });
    return port;
  }

  deliverMessage(portID: string, data: unknown): DeliveryResult {
    const found = this.#findPort(portID);
    if (!found) {
      return {
        accepted: false,
        queued: false,
        deferred: false,
        reason: "unknown-port",
      };
    }
    const { document, port } = found;
    if (port.closed) {
      return {
        accepted: false,
        queued: false,
        deferred: false,
        reason: "port-closed",
      };
    }
    const arrivalSeq = this.#nextArrival;
    this.#nextArrival += 1;
    const event: DeferredEvent = {
      arrivalSeq,
      documentGeneration: document.identity.documentGeneration,
      kind: "message",
      capability: "message-port",
      label: `message:${portID}`,
      run: () => {
        const handler = port.onmessage;
        if (handler) {
          handler(data);
        }
      },
    };
    const disposition = this.#submit(document, event);
    this.#auditEvent("message-arrived", "info", document, {
      portID,
      arrivalSeq,
      disposition,
    });
    return {
      accepted: disposition !== "dropped",
      queued: disposition === "queued",
      deferred: disposition === "deferred",
      ...(disposition === "dropped" ? { reason: "document-not-deliverable" } : {}),
    };
  }

  createPromise<T>(
    reference?: number | string,
  ): GuardedPromise<T> {
    const document = this.#requireDocument(reference);
    const auth = this.#authState(document);
    if (
      !this.#isLive(document) ||
      (this.#guards.authorizeSensitiveEffects && !auth.active)
    ) {
      this.#auditEvent(
        "promise-created",
        "denied",
        document,
        {
          reason: !this.#isLive(document)
            ? `document-${document.state}`
            : auth.reason,
        },
        { afterRevocation: auth.afterRevocation },
      );
      throw new CapabilityDeniedError(
        auth.reason ?? "document-not-live",
        "cannot create a document promise in this state",
      );
    }
    const id = `promise-${this.#nextPromise}`;
    this.#nextPromise += 1;
    const record: PromiseRecord = {
      id,
      documentGeneration: document.identity.documentGeneration,
      state: "pending",
      callbacks: [],
    };
    document.promises.set(id, record);
    return new GuardedPromise<T>(this, id, document.identity.documentGeneration);
  }

  /**
   * Route a promise settlement callback through the generation fence without
   * constructing a full promise.  This is the adapter hook for code that
   * already owns a promise but must not let native microtask timing bypass
   * BFCache freeze/restore ordering.
   */
  queuePromiseCallback(
    callback: () => void | Promise<void>,
    label = "promise-callback",
    reference?: number | string,
  ): DeliveryResult {
    const document = this.#requireDocument(reference);
    const arrivalSeq = this.#nextArrival;
    this.#nextArrival += 1;
    const disposition = this.#submit(document, {
      arrivalSeq,
      documentGeneration: document.identity.documentGeneration,
      kind: "promise-callback",
      capability: "promise-callback",
      label,
      run: callback,
    });
    this.#auditEvent("promise-callback-arrived", "info", document, {
      label,
      arrivalSeq,
      disposition,
    });
    return {
      accepted: disposition !== "dropped",
      queued: disposition === "queued",
      deferred: disposition === "deferred",
      ...(disposition === "dropped"
        ? { reason: "document-not-deliverable" }
        : {}),
    };
  }

  /** @internal Used by GuardedPromise. */
  registerPromiseCallback(
    promiseID: string,
    onFulfilled: ((value: unknown) => unknown) | undefined,
    onRejected: ((reason: unknown) => unknown) | undefined,
    child: GuardedPromise<unknown>,
  ): void {
    const record = this.#findPromise(promiseID);
    if (!record || record.state === "dropped") {
      return;
    }
    const callback: PromiseCallbackRecord = {
      ...(onFulfilled ? { onFulfilled } : {}),
      ...(onRejected ? { onRejected } : {}),
      child,
    };
    if (record.state === "delivered") {
      this.#replayPromiseCallback(record, callback);
    } else {
      record.callbacks.push(callback);
    }
  }

  /** @internal Used by GuardedPromise. */
  settlePromise(
    promiseID: string,
    outcome: "fulfilled" | "rejected",
    value: unknown,
  ): DeliveryResult {
    const record = this.#findPromise(promiseID);
    if (!record || record.state !== "pending") {
      return {
        accepted: false,
        queued: false,
        deferred: false,
        reason: "promise-not-pending",
      };
    }
    const document = this.#documents.get(record.documentGeneration);
    if (!document) {
      record.state = "dropped";
      return {
        accepted: false,
        queued: false,
        deferred: false,
        reason: "document-missing",
      };
    }
    record.state = "settled";
    record.outcome = outcome;
    record.value = value;
    if (record.callbacks.length === 0) {
      // No observer is registered yet.  Keep the settlement value for a later
      // `.then` but do not enqueue an empty document task.
      record.state = "delivered";
      return { accepted: true, queued: false, deferred: false };
    }
    const arrivalSeq = this.#nextArrival;
    this.#nextArrival += 1;
    const event: DeferredEvent = {
      arrivalSeq,
      documentGeneration: record.documentGeneration,
      kind: "promise-callback",
      capability: "promise-callback",
      label: `promise:${promiseID}`,
      run: () => this.#deliverPromiseCallbacks(record),
    };
    const disposition = this.#submit(document, event);
    this.#auditEvent("promise-settled", "info", document, {
      promiseID,
      outcome,
      arrivalSeq,
      disposition,
    });
    if (disposition === "dropped") {
      record.state = "dropped";
      record.callbacks.length = 0;
      return {
        accepted: false,
        queued: false,
        deferred: false,
        reason: "document-not-deliverable",
      };
    }
    return {
      accepted: true,
      queued: disposition === "queued",
      deferred: disposition === "deferred",
    };
  }

  // ---------------------------------------------------------------------
  // Timers
  // ---------------------------------------------------------------------

  setTimeout(callback: () => void, delayMs: number): number {
    return this.#scheduleTimer(callback, delayMs, false);
  }

  setInterval(callback: () => void, delayMs: number): number {
    return this.#scheduleTimer(callback, delayMs, true);
  }

  clearTimeout(timerID: number): boolean {
    const owner = this.#timerOwners.get(timerID);
    if (!owner) {
      return false;
    }
    this.#timerOwners.delete(timerID);
    const document = this.#documents.get(owner.generation);
    return document ? document.timers.clear(owner.localID) : false;
  }

  advanceTime(deltaMs: number): number {
    if (!Number.isFinite(deltaMs) || deltaMs < 0) {
      throw new BfcacheSecurityError(
        "invalid-input",
        "delta must be a non-negative finite number",
      );
    }
    this.#now += deltaMs;
    let fired = 0;
    for (const document of this.#documents.values()) {
      if (document.state !== "live") {
        continue;
      }
      for (const due of document.timers.due(this.#now)) {
        const arrivalSeq = this.#nextArrival;
        this.#nextArrival += 1;
        this.#submit(document, {
          arrivalSeq,
          documentGeneration: document.identity.documentGeneration,
          kind: "timer",
          capability: "timer",
          label: `timer:${due.id}`,
          run: due.callback,
        });
        fired += 1;
      }
    }
    return fired;
  }

  // ---------------------------------------------------------------------
  // DOM / tokens for the scenario harness
  // ---------------------------------------------------------------------

  writeDom(selector: string, value: string, reference?: number | string): void {
    const document = this.#requireDocument(reference);
    const gate = this.#sensitiveGate(document, "sensitive-dom");
    if (!gate.ok) {
      throw new CapabilityDeniedError(
        gate.reason ?? "capability-denied",
        "DOM writes require an authorized live document",
      );
    }
    document.dom.write(selector, value);
  }

  setSensitiveToken(
    name: string,
    value: string,
    reference?: number | string,
  ): void {
    const document = this.#requireDocument(reference);
    const gate = this.#sensitiveGate(document, "sensitive-dom");
    if (!gate.ok) {
      throw new CapabilityDeniedError(
        gate.reason ?? "capability-denied",
        "token writes require an authorized live document",
      );
    }
    document.tokens.set(name, value);
  }

  requestView(requestID: string): NetworkRequestView | undefined {
    const request = this.#findRequest(requestID);
    return request
      ? {
          id: request.id,
          documentGeneration: request.documentGeneration,
          url: request.url,
          state: request.state,
        }
      : undefined;
  }

  /** @internal Review helper: promise lifecycle state without data. */
  promiseState(promiseID: string): string {
    return this.#findPromise(promiseID)?.state ?? "none";
  }

  /** @internal Review helper: port lifecycle state without messages. */
  portState(portID: string): string {
    const found = this.#findPort(portID);
    return found ? (found.port.closed ? "closed" : "open") : "none";
  }

  /**
   * @internal Test/review helper.  Returns only the presence and size of a DOM
   * value, never the value itself.  It performs no authorization check and
   * emits no audit record; callers must not expose it to page code.
   */
  domFingerprint(
    selector: string,
    reference?: number | string,
  ): string {
    try {
      const document = this.#requireDocument(reference);
      const value = document.dom.read(selector);
      return value === undefined ? "absent" : `present:${value.length}`;
    } catch {
      return "none";
    }
  }

  // ---------------------------------------------------------------------
  // Pageshow / auth / policy internals
  // ---------------------------------------------------------------------

  async #handlePageshow(request: PageshowRequest): Promise<ResumeResult> {
    const persisted = request.persisted !== false;
    const generation = this.#frozenByEntry.get(request.historyEntryID);
    if (generation === undefined) {
      return {
        outcome: "ignored",
        historyEntryID: request.historyEntryID,
        reason: "no-frozen-document",
      };
    }
    const document = this.#documents.get(generation);
    if (!document) {
      this.#frozenByEntry.delete(request.historyEntryID);
      return {
        outcome: "ignored",
        historyEntryID: request.historyEntryID,
        reason: "no-frozen-document",
      };
    }
    if (!persisted) {
      this.#discardDocument(document, "no-frozen-document", [
        "non-persisted-pageshow",
      ]);
      this.#frozenByEntry.delete(request.historyEntryID);
      const authRead = await this.#readAuth(document.identity.authSessionID);
      const authEvent =
        authRead.event && (await this.#verifyAuthEvent(authRead.event))
          ? authRead.event
          : undefined;
      if (authEvent) {
        this.#observeAuthView(authEvent);
      }
      const policyRead = await this.#readPolicy(request.currentPartition);
      const fresh = await this.#freshNavigationAfterDiscard(
        document,
        request.currentPartition,
        authEvent,
        policyRead.policy,
      );
      return {
        outcome: "fresh-navigation",
        historyEntryID: request.historyEntryID,
        previousDocumentGeneration: generation,
        documentGeneration: fresh.identity.documentGeneration,
        reason: "non-persisted-pageshow",
      };
    }
    if (document.state !== "frozen") {
      return {
        outcome: "ignored",
        historyEntryID: request.historyEntryID,
        previousDocumentGeneration: generation,
        reason: `document-${document.state}`,
      };
    }

    this.#quiesceOtherLiveDocuments(request.historyEntryID);
    document.state = "restoring";
    this.#auditEvent("bfcache-restore-start", "info", document, {
      currentPartition: request.currentPartition,
    });

    if (!this.#guards.revalidateOnResume) {
      return this.#resumeDocument(document, {
        details: ["review-guard:revalidateOnResume=false"],
        authEvent: this.#sessions.get(document.identity.authSessionID)?.event,
      });
    }

    const cachedPolicyBefore = this.#policies.get(
      partitionKey(request.currentPartition),
    );
    const authRead = await this.#readAuth(document.identity.authSessionID);
    const policyRead = await this.#readPolicy(request.currentPartition);
    const details: string[] = [];
    let authEvent = authRead.event;
    let authVerified = false;

    if (authRead.error) {
      details.push("authority-read-failed");
    } else if (!authEvent) {
      details.push("auth-session-missing");
    } else if (!(await this.#verifyAuthEvent(authEvent))) {
      details.push("signature-invalid");
    } else {
      authVerified = true;
      this.#observeAuthView(authEvent);
      if (authEvent.accountID !== document.identity.accountID) {
        details.push("account-mismatch");
      }
      if (authEvent.authSessionID !== document.identity.authSessionID) {
        details.push("auth-session-missing");
      }
      const cached = this.#sessions.get(document.identity.authSessionID);
      if (authEvent.state === "Revoked") {
        this.#quarantineSession(authEvent.authSessionID, authEvent.epoch);
      }
      if (document.authorizationLost) {
        details.push("authorization-lost");
      }
      if (
        cached?.state === "Revoked" ||
        authEvent.state === "Revoked"
      ) {
        details.push("auth-session-revoked");
      }
      if (
        cached?.maxEpochSeen !== undefined &&
        authEvent.epoch < cached.maxEpochSeen
      ) {
        details.push("auth-epoch-rollback");
      }
      if (authEvent.epoch !== document.identity.authEpoch) {
        details.push("auth-epoch-mismatch");
      }
      if (authEvent.state !== "Active") {
        details.push("auth-state-not-active");
      }
    }

    if (request.currentPartition.topLevelSite !== document.identity.topLevelSite) {
      details.push("top-level-site-mismatch");
    }
    if (request.currentPartition.origin !== document.identity.origin) {
      details.push("origin-mismatch");
    }
    if (
      request.currentPartition.browserSessionID !==
      document.identity.browserSessionID
    ) {
      details.push("browser-session-mismatch");
    }

    const policy = policyRead.policy;
    if (policyRead.error) {
      details.push("authority-read-failed");
    } else if (!policy) {
      details.push("policy-generation-mismatch");
    } else {
      this.#observePolicyView(request.currentPartition, policy);
      if (policy.generation !== document.identity.policyGeneration) {
        details.push("policy-generation-mismatch");
      }
      if (policyFingerprint(policy) !== policyFingerprint(document.policy)) {
        details.push("policy-fingerprint-mismatch");
      }
      if (cachedPolicyBefore) {
        if (cachedPolicyBefore.generation > policy.generation) {
          details.push("policy-generation-mismatch");
        }
        if (
          cachedPolicyBefore.generation === policy.generation &&
          policyFingerprint(cachedPolicyBefore) !== policyFingerprint(policy)
        ) {
          details.push("policy-fingerprint-mismatch");
        }
      }
    }
    if (document.policyStale) {
      details.push("policy-generation-mismatch");
    }

    if (
      this.#documents.get(generation) !== document ||
      document.state !== "restoring"
    ) {
      return {
        outcome: "ignored",
        historyEntryID: request.historyEntryID,
        previousDocumentGeneration: generation,
        reason: `document-${document.state}`,
      };
    }

    if (details.length > 0) {
      const reason = this.#chooseDiscardReason(details);
      const dropped = this.#discardDocument(document, reason, details);
      this.#frozenByEntry.delete(request.historyEntryID);
      const fresh = await this.#freshNavigationAfterDiscard(
        document,
        request.currentPartition,
        authVerified ? authEvent : undefined,
        policy,
      );
      return {
        outcome: "discarded",
        historyEntryID: request.historyEntryID,
        previousDocumentGeneration: generation,
        documentGeneration: fresh.identity.documentGeneration,
        reason,
        details,
        droppedDeferred: dropped,
      };
    }

    return this.#resumeDocument(document, { details, authEvent });
  }

  #resumeDocument(
    document: DocumentRecord,
    options: { details: string[]; authEvent?: AuthEpochEvent },
  ): ResumeResult {
    this.#quiesceOtherLiveDocuments(document.historyEntryID);
    document.state = "live";
    document.authorizationLost = false;
    document.timers.resume(this.#now);
    if (document.snapshot) {
      document.snapshot.wipe();
      document.snapshot = undefined;
    }
    this.#frozenByEntry.delete(document.historyEntryID);
    this.#currentEntryID = document.historyEntryID;
    this.#dispatchLifecycle(document, "pageshow", true);
    const flushed = this.#flushDeferred(document);
    this.#auditEvent("bfcache-resume", "info", document, {
      flushedDeferred: flushed,
      authEpoch: options.authEvent?.epoch ?? document.identity.authEpoch,
      notes: options.details,
    });
    return {
      outcome: "resumed",
      historyEntryID: document.historyEntryID,
      previousDocumentGeneration: document.identity.documentGeneration,
      documentGeneration: document.identity.documentGeneration,
      details: options.details,
    };
  }

  async #freshNavigationAfterDiscard(
    old: DocumentRecord,
    currentPartition: Partition,
    _authEvent: AuthEpochEvent | undefined,
    _policy: PolicySnapshot | undefined,
  ): Promise<DocumentRecord> {
    this.#quiesceOtherLiveDocuments(old.historyEntryID);
    const partition: Partition = {
      topLevelSite: old.identity.topLevelSite,
      origin: old.identity.origin,
      browserSessionID: currentPartition.browserSessionID,
    };
    const policyRead = await this.#readPolicy(partition);
    let freshPolicy = policyRead.policy;
    if (freshPolicy) {
      this.#observePolicyView(partition, freshPolicy);
      freshPolicy =
        this.#policies.get(partitionKey(partition)) ?? copyPolicy(freshPolicy);
    }
    // A fresh navigation must not inherit authorization from the failed
    // restore attempt.  Ask the authority again and fail closed on error.
    const authRead = await this.#readAuth(old.identity.authSessionID);
    const latestAuth =
      authRead.event && (await this.#verifyAuthEvent(authRead.event))
        ? authRead.event
        : undefined;
    if (latestAuth) {
      this.#observeAuthView(latestAuth);
    }
    const cachedSession = latestAuth
      ? this.#sessions.get(latestAuth.authSessionID)
      : undefined;
    const usableAuth =
      latestAuth &&
      latestAuth.state === "Active" &&
      latestAuth.authSessionID === old.identity.authSessionID &&
      latestAuth.accountID === old.identity.accountID &&
      cachedSession?.accountID === latestAuth.accountID &&
      cachedSession?.state !== "Revoked" &&
      (cachedSession?.epoch ?? latestAuth.epoch) === latestAuth.epoch
        ? latestAuth
        : undefined;
    const identity: DocumentIdentity = Object.freeze({
      ...partition,
      documentGeneration: this.#nextGeneration,
      authSessionID:
        latestAuth?.authSessionID ?? old.identity.authSessionID,
      accountID: latestAuth?.accountID ?? old.identity.accountID,
      authEpoch: latestAuth?.epoch ?? old.identity.authEpoch,
      policyGeneration: freshPolicy?.generation ?? 0,
    });
    this.#nextGeneration += 1;
    const freshDom = this.#createDom
      ? this.#createDom(identity)
      : new InMemoryDom();
    const fresh: DocumentRecord = {
      identity,
      historyEntryID: old.historyEntryID,
      state: usableAuth && freshPolicy ? "live" : "locked",
      dom: freshDom,
      timers: new TimerRegistry(),
      tokens: new Map(),
      listeners: { pagehide: new Set(), pageshow: new Set() },
      deferred: [],
      requests: new Map(),
      ports: new Map(),
      promises: new Map(),
      droppedDeferredCount: 0,
      freezing: false,
      ...(freshPolicy ? { policy: freshPolicy } : {}),
      policyStale: false,
      authorizationLost: !(usableAuth && freshPolicy),
    };
    this.#documents.set(identity.documentGeneration, fresh);
    this.#currentEntryID = fresh.historyEntryID;
    this.#auditEvent("fresh-navigation", "info", fresh, {
      replacedGeneration: old.identity.documentGeneration,
      authorized: Boolean(usableAuth),
      policyGeneration: freshPolicy?.generation ?? 0,
    });
    return fresh;
  }

  async #applyAuthEpoch(event: AuthEpochEvent): Promise<AuthEpochResult> {
    const authSessionID =
      typeof event?.authSessionID === "string" ? event.authSessionID : "";
    if (!(await this.#verifyAuthEvent(event))) {
      this.#auditEvent("auth-epoch", "denied", undefined, {
        authSessionID,
        epoch: event?.epoch,
        reason: "signature-invalid",
      });
      return {
        accepted: false,
        reason: "signature-invalid",
        authSessionID,
      };
    }

    const current = this.#sessions.get(event.authSessionID);
    if (current) {
      if (current.accountID !== event.accountID) {
        this.#auditEvent("auth-epoch", "denied", undefined, {
          authSessionID: event.authSessionID,
          epoch: event.epoch,
          reason: "account-mismatch",
        });
        return {
          accepted: false,
          reason: "account-mismatch",
          authSessionID: event.authSessionID,
          epoch: event.epoch,
          state: event.state,
        };
      }
      if (event.epoch < current.epoch) {
        this.#auditEvent("auth-epoch", "denied", undefined, {
          authSessionID: event.authSessionID,
          epoch: event.epoch,
          reason: "stale-epoch",
        });
        return {
          accepted: false,
          reason: "stale-epoch",
          authSessionID: event.authSessionID,
          epoch: event.epoch,
          state: event.state,
        };
      }
      if (event.epoch === current.epoch) {
        if (
          current.event.signature === event.signature &&
          current.state === event.state
        ) {
          return {
            accepted: true,
            duplicate: true,
            authSessionID: event.authSessionID,
            epoch: event.epoch,
            state: event.state,
          };
        }
        this.#auditEvent("auth-epoch", "denied", undefined, {
          authSessionID: event.authSessionID,
          epoch: event.epoch,
          reason: "epoch-conflict",
        });
        return {
          accepted: false,
          reason: "epoch-conflict",
          authSessionID: event.authSessionID,
          epoch: event.epoch,
          state: event.state,
        };
      }
      if (current.state === "Revoked" && event.state === "Active") {
        this.#auditEvent("auth-epoch", "denied", undefined, {
          authSessionID: event.authSessionID,
          epoch: event.epoch,
          reason: "revoked-session-terminal",
        });
        return {
          accepted: false,
          reason: "revoked-session-terminal",
          authSessionID: event.authSessionID,
          epoch: event.epoch,
          state: event.state,
        };
      }
    }

    const previousEpoch = current?.epoch;
    const record: SessionRecord = {
      accountID: event.accountID,
      authSessionID: event.authSessionID,
      epoch: event.epoch,
      state: event.state,
      event: copyAuthEvent(event),
      maxEpochSeen: Math.max(current?.maxEpochSeen ?? 0, event.epoch),
    };
    this.#sessions.set(event.authSessionID, record);
    if (
      previousEpoch !== undefined &&
      event.epoch > previousEpoch &&
      event.state === "Active" &&
      this.#guards.quarantineOnRevokedEvent
    ) {
      this.#quarantineEpochMismatch(event.authSessionID, event.epoch);
    }
    if (event.state === "Revoked" && this.#guards.quarantineOnRevokedEvent) {
      this.#quarantineSession(event.authSessionID, event.epoch);
    }
    this.#auditEvent("auth-epoch", "info", undefined, {
      authSessionID: event.authSessionID,
      epoch: event.epoch,
      state: event.state,
      reason: event.reason,
    });
    return {
      accepted: true,
      authSessionID: event.authSessionID,
      epoch: event.epoch,
      state: event.state,
    };
  }

  #applyPolicy(
    policy: PolicySnapshot,
    partition?: Partition,
  ): PolicyUpdateResult {
    if (!isPolicySnapshot(policy)) {
      return {
        accepted: false,
        generation: Number(policy?.generation ?? 0),
        reason: "invalid-policy",
      };
    }
    const nextPolicy = copyPolicy(policy);
    const candidateKeys = partition
      ? [partitionKey(partition)]
      : [...this.#policies.keys()];
    for (const key of candidateKeys) {
      const cached = this.#policies.get(key);
      if (cached && nextPolicy.generation < cached.generation) {
        this.#auditEvent("policy-update", "denied", undefined, {
          reason: "policy-generation-rollback",
          generation: nextPolicy.generation,
        });
        return {
          accepted: false,
          generation: nextPolicy.generation,
          reason: "policy-generation-rollback",
        };
      }
    }
    for (const document of this.#documents.values()) {
      if (document.state === "discarded" || document.state === "disposed") {
        continue;
      }
      if (partition && !samePartition(document.identity, partition)) {
        continue;
      }
      const key = partitionKey(document.identity);
      const cached = this.#policies.get(key);
      const changedGeneration =
        nextPolicy.generation > document.identity.policyGeneration;
      const changedFingerprint =
        policyFingerprint(nextPolicy) !== policyFingerprint(document.policy);
      const cachedConflict =
        cached !== undefined &&
        (cached.generation !== nextPolicy.generation ||
          policyFingerprint(cached) !== policyFingerprint(nextPolicy));
      if (changedGeneration || changedFingerprint || cachedConflict) {
        document.policyStale = true;
        if (document.state === "live") {
          this.#lockDocument(document, "policy-tightened");
        } else if (document.state === "frozen" || document.state === "restoring") {
          this.#purgeFrozenSensitiveState(document, "policy-tightened");
        }
      }
    }
    const partitions = partition
      ? new Set([partitionKey(partition)])
      : new Set(
          [...this.#documents.values()].map((document) =>
            partitionKey(document.identity),
          ),
        );
    for (const key of partitions) {
      this.#policies.set(key, nextPolicy);
    }
    this.#auditEvent("policy-update", "info", undefined, {
      generation: nextPolicy.generation,
      fingerprint: policyFingerprint(nextPolicy),
    });
    return { accepted: true, generation: nextPolicy.generation };
  }

  // ---------------------------------------------------------------------
  // Internal guards and delivery
  // ---------------------------------------------------------------------

  #submit(
    document: DocumentRecord,
    event: DeferredEvent,
  ): "queued" | "deferred" | "dropped" {
    if (
      this.#documents.get(event.documentGeneration) !== document ||
      document.identity.documentGeneration !== event.documentGeneration
    ) {
      this.#auditEvent("deferred-dropped", "dropped", document, {
        arrivalSeq: event.arrivalSeq,
        reason: "generation-mismatch",
      });
      return "dropped";
    }
    if (document.state === "frozen" || document.state === "restoring") {
      if (document.authorizationLost) {
        this.#auditEvent("deferred-dropped", "dropped", document, {
          arrivalSeq: event.arrivalSeq,
          reason: "authorization-lost",
        });
        return "dropped";
      }
      if (document.deferred.length >= this.#maxDeferredEvents) {
        this.#auditEvent("deferred-dropped", "dropped", document, {
          arrivalSeq: event.arrivalSeq,
          reason: "deferred-capacity",
        });
        return "dropped";
      }
      document.deferred.push(event);
      return "deferred";
    }
    if (document.state !== "live") {
      this.#auditEvent("deferred-dropped", "dropped", document, {
        arrivalSeq: event.arrivalSeq,
        reason: `document-${document.state}`,
      });
      return "dropped";
    }
    this.#loop.enqueue(
      "document",
      `doc:${event.documentGeneration}:${event.kind}:${event.arrivalSeq}`,
      () => this.#executeWork(document, event),
    );
    return "queued";
  }

  async #executeWork(
    document: DocumentRecord,
    event: DeferredEvent,
  ): Promise<void> {
    if (
      this.#documents.get(document.identity.documentGeneration) !== document ||
      event.documentGeneration !== document.identity.documentGeneration
    ) {
      this.#auditEvent("deferred-dropped", "dropped", document, {
        arrivalSeq: event.arrivalSeq,
        reason: "generation-mismatch",
      });
      return;
    }
    if (document.state === "frozen" || document.state === "restoring") {
      if (document.authorizationLost) {
        this.#auditEvent("deferred-dropped", "dropped", document, {
          arrivalSeq: event.arrivalSeq,
          reason: "authorization-lost",
        });
        return;
      }
      // A pagehide may have raced ahead of an already queued callback.
      if (document.deferred.length < this.#maxDeferredEvents) {
        document.deferred.push(event);
      } else {
        this.#auditEvent("deferred-dropped", "dropped", document, {
          arrivalSeq: event.arrivalSeq,
          reason: "deferred-capacity",
        });
      }
      return;
    }
    if (document.state !== "live") {
      this.#auditEvent("deferred-dropped", "dropped", document, {
        arrivalSeq: event.arrivalSeq,
        reason: `document-${document.state}`,
      });
      return;
    }

    const auth = this.#authState(document);
    if (this.#guards.checkDeliveryAgainstAuth && !auth.active) {
      this.#auditEvent(
        "document-task",
        "denied",
        document,
        {
          kind: event.kind,
          arrivalSeq: event.arrivalSeq,
          reason: auth.reason,
        },
        { afterRevocation: auth.afterRevocation },
      );
      return;
    }
    if (
      event.capability &&
      !this.#policyAllows(document, event.capability)
    ) {
      this.#auditEvent(
        "document-task",
        "denied",
        document,
        {
          kind: event.kind,
          arrivalSeq: event.arrivalSeq,
          reason: "policy-denied",
        },
        { afterRevocation: auth.afterRevocation },
      );
      return;
    }

    try {
      if (!event.run) {
        return;
      }
      await event.run();
      this.#auditEvent(
        "document-task",
        "allowed",
        document,
        { kind: event.kind, arrivalSeq: event.arrivalSeq },
        {
          afterRevocation: auth.afterRevocation,
          violation: auth.afterRevocation,
        },
      );
    } catch (error) {
      this.#auditEvent("document-task", "denied", document, {
        kind: event.kind,
        arrivalSeq: event.arrivalSeq,
        error: error instanceof Error ? error.message : String(error),
      }, { afterRevocation: auth.afterRevocation });
    }
  }

  #flushDeferred(document: DocumentRecord): number {
    if (document.deferred.length === 0) {
      return 0;
    }
    const sorted = document.deferred
      .slice()
      .sort((left, right) => left.arrivalSeq - right.arrivalSeq);
    document.deferred.length = 0;
    let queued = 0;
    for (const event of sorted) {
      if (event.documentGeneration !== document.identity.documentGeneration) {
        this.#auditEvent("deferred-dropped", "dropped", document, {
          arrivalSeq: event.arrivalSeq,
          reason: "generation-mismatch",
        });
        continue;
      }
      this.#loop.enqueue(
        "document",
        `doc:${event.documentGeneration}:${event.kind}:${event.arrivalSeq}`,
        () => this.#executeWork(document, event),
      );
      queued += 1;
    }
    this.#auditEvent("deferred-flushed", "info", document, {
      count: queued,
    });
    return queued;
  }

  #dropDeferred(document: DocumentRecord, reason: string): number {
    const count = document.deferred.length;
    if (count > 0) {
      for (const event of document.deferred) {
        event.run = undefined;
      }
      document.deferred.length = 0;
      document.droppedDeferredCount += count;
      this.#auditEvent("deferred-dropped", "dropped", document, {
        count,
        reason,
      });
    }
    return count;
  }

  #discardDocument(
    document: DocumentRecord,
    reason: DiscardReason | string,
    details: readonly string[],
  ): number {
    this.#dropDeferred(document, String(reason));
    const dropped = document.droppedDeferredCount;
    document.state = "discarded";
    document.authorizationLost = true;
    document.policyStale = true;
    this.#loop.cancelWhere((task: ScheduledTask) =>
      task.label.startsWith(`doc:${document.identity.documentGeneration}:`),
    );
    this.#wipeDocumentSensitiveState(document);
    this.#frozenByEntry.delete(document.historyEntryID);
    this.#auditEvent("document-discarded", "info", document, {
      reason,
      details,
      droppedDeferred: dropped,
    });
    return dropped;
  }

  #disposeDocument(document: DocumentRecord, reason: string): void {
    document.state = "disposed";
    document.authorizationLost = true;
    document.policyStale = true;
    this.#loop.cancelWhere((task: ScheduledTask) =>
      task.label.startsWith(`doc:${document.identity.documentGeneration}:`),
    );
    const dropped = this.#dropDeferred(document, reason);
    this.#wipeDocumentSensitiveState(document);
    this.#frozenByEntry.delete(document.historyEntryID);
    if (this.#currentEntryID === document.historyEntryID) {
      this.#currentEntryID = undefined;
    }
    this.#auditEvent("document-disposed", "info", document, {
      reason,
      droppedDeferred: dropped,
    });
  }

  #lockDocument(document: DocumentRecord, reason: string): void {
    document.state = "locked";
    document.authorizationLost = true;
    if (document.snapshot) {
      document.snapshot.wipe();
      document.snapshot = undefined;
    }
    this.#loop.cancelWhere((task: ScheduledTask) =>
      task.label.startsWith(`doc:${document.identity.documentGeneration}:`),
    );
    this.#dropDeferred(document, reason);
    document.timers.clearAll();
    this.#dropDocumentRequests(document, reason);
    for (const port of document.ports.values()) {
      port.closed = true;
      port.onmessage = null;
    }
    document.tokens.clear();
    document.dom.wipe();
    document.listeners.pagehide.clear();
    document.listeners.pageshow.clear();
    this.#dropDocumentPromises(document);
    this.#auditEvent("document-locked", "info", document, { reason });
  }

  #quarantineSession(authSessionID: string, revokedEpoch: number): void {
    for (const document of this.#documents.values()) {
      if (
        document.identity.authSessionID !== authSessionID ||
        document.state === "discarded" ||
        document.state === "disposed"
      ) {
        continue;
      }
      document.authorizationLost = true;
      this.#loop.cancelWhere((task: ScheduledTask) =>
        task.label.startsWith(`doc:${document.identity.documentGeneration}:`),
      );
      if (document.state === "live") {
        this.#lockDocument(document, "auth-epoch-revoked");
      } else if (
        document.state === "frozen" ||
        document.state === "restoring"
      ) {
        if (!this.#guards.purgeOnRevokedEvent) {
          this.#auditEvent("document-quarantined", "info", document, {
            revokedEpoch,
            state: document.state,
            purged: false,
          });
          continue;
        }
        this.#purgeFrozenSensitiveState(document, "auth-epoch-revoked");
      }
      this.#auditEvent("document-quarantined", "info", document, {
        revokedEpoch,
        state: document.state,
      });
    }
  }

  #quarantineEpochMismatch(
    authSessionID: string,
    newEpoch: number,
  ): void {
    for (const document of this.#documents.values()) {
      if (
        document.identity.authSessionID !== authSessionID ||
        document.identity.authEpoch === newEpoch ||
        document.state === "discarded" ||
        document.state === "disposed"
      ) {
        continue;
      }
      document.authorizationLost = true;
      this.#loop.cancelWhere((task: ScheduledTask) =>
        task.label.startsWith(`doc:${document.identity.documentGeneration}:`),
      );
      if (document.state === "live") {
        this.#lockDocument(document, "auth-epoch-advanced");
      } else if (
        (document.state === "frozen" || document.state === "restoring") &&
        this.#guards.purgeOnRevokedEvent
      ) {
        this.#purgeFrozenSensitiveState(document, "auth-epoch-advanced");
      }
      this.#auditEvent("document-quarantined", "info", document, {
        reason: "auth-epoch-advanced",
        newEpoch,
      });
    }
  }

  #purgeFrozenSensitiveState(
    document: DocumentRecord,
    reason: string,
  ): void {
    this.#dropDeferred(document, reason);
    document.timers.clearAll();
    this.#dropDocumentRequests(document, reason);
    for (const port of document.ports.values()) {
      port.closed = true;
      port.onmessage = null;
    }
    document.tokens.clear();
    if (document.snapshot) {
      document.snapshot.wipe();
      document.snapshot = undefined;
    }
    document.dom.wipe();
    document.listeners.pagehide.clear();
    document.listeners.pageshow.clear();
    this.#dropDocumentPromises(document);
  }

  #wipeDocumentSensitiveState(document: DocumentRecord): void {
    if (document.snapshot) {
      document.snapshot.wipe();
      document.snapshot = undefined;
    }
    document.dom.wipe();
    document.listeners.pagehide.clear();
    document.listeners.pageshow.clear();
    for (const name of document.tokens.keys()) {
      document.tokens.set(name, "");
    }
    document.tokens.clear();
    document.timers.clearAll();
    this.#dropDocumentRequests(document, "document-discarded");
    for (const port of document.ports.values()) {
      port.closed = true;
      port.onmessage = null;
    }
    document.ports.clear();
    this.#dropDocumentPromises(document);
  }

  #dropDocumentPromises(document: DocumentRecord): void {
    for (const promise of document.promises.values()) {
      promise.state = "dropped";
      promise.callbacks.length = 0;
      promise.value = undefined;
    }
    document.promises.clear();
  }

  #dropDocumentRequests(document: DocumentRecord, reason: string): void {
    for (const request of document.requests.values()) {
      if (request.state === "pending" || request.state === "completed") {
        request.state = "dropped";
        request.dropReason = reason;
        request.body = "";
        request.handler = undefined;
      }
    }
    document.requests.clear();
  }

  #dispatchLifecycle(
    document: DocumentRecord,
    type: "pagehide" | "pageshow",
    persisted: boolean,
  ): void {
    const event: PageLifecycleEvent = Object.freeze({
      type,
      persisted,
      historyEntryID: document.historyEntryID,
      documentGeneration: document.identity.documentGeneration,
    });
    for (const listener of [...document.listeners[type]]) {
      try {
        listener(event);
      } catch (error) {
        this.#auditEvent("lifecycle-listener-error", "denied", document, {
          type,
          error: error instanceof Error ? error.message : String(error),
        });
      }
    }
  }

  #sensitiveGate(
    document: DocumentRecord,
    capability: Exclude<DocumentCapability, "timer" | "promise-callback">,
  ): {
    ok: boolean;
    afterRevocation: boolean;
    reason?: string;
  } {
    const auth = this.#authState(document);
    if (!this.#isLive(document)) {
      return {
        ok: false,
        afterRevocation: auth.afterRevocation,
        reason: `document-${document.state}`,
      };
    }
    if (!this.#policyAllows(document, capability)) {
      return {
        ok: false,
        afterRevocation: auth.afterRevocation,
        reason: "policy-denied",
      };
    }
    if (this.#guards.authorizeSensitiveEffects && !auth.active) {
      return {
        ok: false,
        afterRevocation: auth.afterRevocation,
        reason: auth.reason,
      };
    }
    return { ok: true, afterRevocation: auth.afterRevocation };
  }

  #authState(document: DocumentRecord): {
    active: boolean;
    afterRevocation: boolean;
    reason?: string;
  } {
    const session = this.#sessions.get(document.identity.authSessionID);
    const afterRevocation =
      Boolean(document.authorizationLost) ||
      session?.state === "Revoked" ||
      (session !== undefined && session.epoch !== document.identity.authEpoch);
    if (!session) {
      return {
        active: false,
        afterRevocation,
        reason: "auth-session-missing",
      };
    }
    if (session.state === "Revoked") {
      return {
        active: false,
        afterRevocation: true,
        reason: "auth-session-revoked",
      };
    }
    if (session.epoch !== document.identity.authEpoch) {
      return {
        active: false,
        afterRevocation: true,
        reason: "auth-epoch-mismatch",
      };
    }
    if (document.authorizationLost) {
      return {
        active: false,
        afterRevocation: true,
        reason: "authorization-lost",
      };
    }
    return { active: true, afterRevocation };
  }

  #policyAllows(
    document: DocumentRecord,
    capability: DocumentCapability,
  ): boolean {
    if (!document.policy || document.policyStale) {
      return false;
    }
    const cached = this.#policies.get(partitionKey(document.identity));
    if (
      cached &&
      (cached.generation !== document.policy.generation ||
        policyFingerprint(cached) !== policyFingerprint(document.policy))
    ) {
      return false;
    }
    switch (capability) {
      case "sensitive-dom":
        return document.policy.allowSensitiveDom;
      case "network":
        return document.policy.allowNetwork;
      case "form-submission":
        return document.policy.allowFormSubmission;
      case "message-port":
        return document.policy.allowMessagePort;
      case "timer":
      case "promise-callback":
        return true;
      default:
        return false;
    }
  }

  #isLive(document: DocumentRecord): boolean {
    return document.state === "live";
  }

  #requireDocument(reference?: number | string): DocumentRecord {
    const resolved =
      reference ??
      this.#currentEntryID ??
      (() => {
        throw new DocumentUnavailableError(
          "no current document; pass a generation or history entry",
        );
      })();
    const document =
      typeof resolved === "number"
        ? this.#documents.get(resolved)
        : this.#findLiveDocument(resolved) ??
          this.#documents.get(this.#frozenByEntry.get(resolved) ?? -1) ??
          [...this.#documents.values()]
            .filter((candidate) => candidate.historyEntryID === resolved)
            .at(-1);
    if (!document) {
      throw new DocumentUnavailableError(
        `document ${String(resolved)} does not exist`,
      );
    }
    return document;
  }

  #findLiveDocument(historyEntryID: string): DocumentRecord | undefined {
    return [...this.#documents.values()]
      .filter(
        (document) =>
          document.historyEntryID === historyEntryID &&
          (document.state === "live" || document.state === "locked"),
      )
      .at(-1);
  }

  #quiesceOtherLiveDocuments(exceptHistoryEntryID: string): void {
    if (
      this.#currentEntryID === undefined ||
      this.#currentEntryID === exceptHistoryEntryID
    ) {
      return;
    }
    const previous = this.#findLiveDocument(this.#currentEntryID);
    if (previous) {
      this.pagehide({
        historyEntryID: this.#currentEntryID,
        persisted: true,
      });
    }
  }

  #findEntryDocument(historyEntryID: string): DocumentRecord | undefined {
    return [...this.#documents.values()]
      .filter(
        (document) =>
          document.historyEntryID === historyEntryID &&
          (document.state === "live" ||
            document.state === "locked" ||
            document.state === "frozen"),
      )
      .at(-1);
  }

  #findRequest(requestID: string): NetworkRequestRecord | undefined {
    for (const document of this.#documents.values()) {
      const request = document.requests.get(requestID);
      if (request) {
        return request;
      }
    }
    return undefined;
  }

  #findPort(
    portID: string,
  ): { document: DocumentRecord; port: PortRecord } | undefined {
    for (const document of this.#documents.values()) {
      const port = document.ports.get(portID);
      if (port) {
        return { document, port };
      }
    }
    return undefined;
  }

  #findPromise(promiseID: string): PromiseRecord | undefined {
    for (const document of this.#documents.values()) {
      const promise = document.promises.get(promiseID);
      if (promise) {
        return promise;
      }
    }
    return undefined;
  }

  #deliverPromiseCallbacks(record: PromiseRecord): void {
    record.state = "delivered";
    const callbacks = record.callbacks.slice();
    record.callbacks.length = 0;
    for (const callback of callbacks) {
      if (record.outcome === "fulfilled") {
        if (callback.onFulfilled) {
          try {
            const result = callback.onFulfilled(record.value);
            callback.child._resolveFromCallback(result);
          } catch (error) {
            callback.child._rejectFromCallback(error);
          }
        } else {
          callback.child._resolveFromCallback(record.value);
        }
      } else if (callback.onRejected) {
        try {
          const result = callback.onRejected(record.value);
          callback.child._resolveFromCallback(result);
        } catch (error) {
          callback.child._rejectFromCallback(error);
        }
      } else {
        callback.child._rejectFromCallback(record.value);
      }
    }
  }

  #replayPromiseCallback(
    record: PromiseRecord,
    callback: PromiseCallbackRecord,
  ): void {
    const document = this.#documents.get(record.documentGeneration);
    if (!document) {
      return;
    }
    const arrivalSeq = this.#nextArrival;
    this.#nextArrival += 1;
    this.#submit(document, {
      arrivalSeq,
      documentGeneration: record.documentGeneration,
      kind: "promise-callback",
      capability: "promise-callback",
      label: `promise-late:${record.id}`,
      run: () => {
        const single: PromiseRecord = {
          ...record,
          state: "delivered",
          callbacks: [callback],
        };
        this.#deliverPromiseCallbacks(single);
      },
    });
  }

  async #readAuth(authSessionID: string): Promise<AuthReadResult> {
    try {
      const event = await this.#auth.readCurrent(authSessionID);
      return {
        event:
          event && typeof event === "object"
            ? copyAuthEvent(event)
            : event,
      };
    } catch (error) {
      return { error };
    }
  }

  async #readPolicy(partition: Partition): Promise<PolicyReadResult> {
    try {
      const policy = await this.#policy.read(partition);
      return isPolicySnapshot(policy)
        ? { policy: copyPolicy(policy) }
        : { error: new Error("malformed policy snapshot") };
    } catch (error) {
      return { error };
    }
  }

  async #verifyAuthEvent(event: AuthEpochEvent): Promise<boolean> {
    if (!isWellFormedAuthEpochEvent(event)) {
      return false;
    }
    try {
      return await this.#auth.verify(event);
    } catch {
      return false;
    }
  }

  #observeAuthView(event: AuthEpochEvent): void {
    const current = this.#sessions.get(event.authSessionID);
    if (current && current.accountID !== event.accountID) {
      return;
    }
    if (!current) {
      this.#sessions.set(event.authSessionID, {
        accountID: event.accountID,
        authSessionID: event.authSessionID,
        epoch: event.epoch,
        state: event.state,
        event: copyAuthEvent(event),
        maxEpochSeen: event.epoch,
      });
      if (
        event.state === "Revoked" &&
        this.#guards.quarantineOnRevokedEvent
      ) {
        this.#quarantineSession(event.authSessionID, event.epoch);
      }
      return;
    }
    const previousEpoch = current.epoch;
    if (current.state === "Revoked") {
      if (event.state === "Revoked" && event.epoch >= current.epoch) {
        current.epoch = event.epoch;
        current.maxEpochSeen = Math.max(current.maxEpochSeen, event.epoch);
        current.event = copyAuthEvent(event);
        if (this.#guards.quarantineOnRevokedEvent) {
          this.#quarantineSession(event.authSessionID, event.epoch);
        }
      }
      return;
    }
    if (event.epoch >= current.epoch) {
      current.epoch = event.epoch;
      current.state = event.state;
      current.event = copyAuthEvent(event);
      current.maxEpochSeen = Math.max(current.maxEpochSeen, event.epoch);
      if (
        event.state === "Revoked" &&
        this.#guards.quarantineOnRevokedEvent
      ) {
        this.#quarantineSession(event.authSessionID, event.epoch);
      } else if (
        event.state === "Active" &&
        event.epoch > previousEpoch &&
        this.#guards.quarantineOnRevokedEvent
      ) {
        this.#quarantineEpochMismatch(
          event.authSessionID,
          event.epoch,
        );
      }
    }
  }

  #observePolicyView(partition: Partition, policy: PolicySnapshot): void {
    const key = partitionKey(partition);
    const cached = this.#policies.get(key);
    if (!cached) {
      this.#policies.set(key, copyPolicy(policy));
      return;
    }
    if (policy.generation > cached.generation) {
      this.#policies.set(key, copyPolicy(policy));
      return;
    }
    if (
      policy.generation === cached.generation &&
      policyFingerprint(policy) !== policyFingerprint(cached)
    ) {
      this.#policies.set(key, stricterPolicy(cached, policy));
    }
  }

  #newIdentity(
    partition: Partition,
    authEvent: AuthEpochEvent,
    policy: PolicySnapshot,
  ): DocumentIdentity {
    const identity: DocumentIdentity = Object.freeze({
      ...partition,
      documentGeneration: this.#nextGeneration,
      authSessionID: authEvent.authSessionID,
      accountID: authEvent.accountID,
      authEpoch: authEvent.epoch,
      policyGeneration: policy.generation,
    });
    this.#nextGeneration += 1;
    return identity;
  }

  #scheduleTimer(
    callback: () => void,
    delayMs: number,
    interval: boolean,
  ): number {
    if (!Number.isFinite(delayMs) || delayMs < 0) {
      throw new BfcacheSecurityError(
        "invalid-input",
        "timer delay must be a non-negative finite number",
      );
    }
    const document = this.#requireDocument();
    const auth = this.#authState(document);
    if (!this.#isLive(document) || (this.#guards.authorizeSensitiveEffects && !auth.active)) {
      this.#auditEvent("timer-schedule", "denied", document, {
        reason: !this.#isLive(document)
          ? `document-${document.state}`
          : auth.reason,
      }, { afterRevocation: auth.afterRevocation });
      throw new CapabilityDeniedError(
        auth.reason ?? "document-not-live",
        "timer scheduling denied",
      );
    }
    const localID = interval
      ? document.timers.scheduleInterval(delayMs, callback, this.#now)
      : document.timers.schedule(delayMs, callback, this.#now);
    const globalID = this.#nextTimer;
    this.#nextTimer += 1;
    this.#timerOwners.set(globalID, {
      generation: document.identity.documentGeneration,
      localID,
    });
    return globalID;
  }

  #chooseDiscardReason(details: readonly string[]): DiscardReason {
    const priority: DiscardReason[] = [
      "signature-invalid",
      "auth-session-revoked",
      "auth-state-not-active",
      "auth-session-missing",
      "account-mismatch",
      "auth-epoch-rollback",
      "auth-epoch-mismatch",
      "authorization-lost",
      "top-level-site-mismatch",
      "origin-mismatch",
      "browser-session-mismatch",
      "policy-generation-mismatch",
      "policy-fingerprint-mismatch",
      "authority-read-failed",
    ];
    for (const reason of priority) {
      if (details.includes(reason)) {
        return reason;
      }
    }
    return "document-generation-mismatch";
  }

  #view(document: DocumentRecord): DocumentView {
    const auth = this.#authState(document);
    return {
      identity: document.identity,
      state: document.state,
      historyEntryID: document.historyEntryID,
      authorized: auth.active,
      authorizationLost: document.authorizationLost,
      policyStale: document.policyStale,
      hasInMemorySnapshot: document.snapshot !== undefined,
      deferredEvents: document.deferred.length,
      deferredKinds: document.deferred.map((event) => event.kind),
      pendingRequests: [...document.requests.values()].filter(
        (request) => request.state === "pending" || request.state === "completed",
      ).length,
      openPorts: [...document.ports.values()].filter((port) => !port.closed)
        .length,
      activeTimers: document.timers.activeCount,
    };
  }

  #auditEvent(
    type: string,
    result: AuditEvent["result"],
    document: DocumentRecord | undefined,
    detail?: Record<string, unknown>,
    options: { afterRevocation?: boolean; violation?: boolean } = {},
  ): AuditEvent {
    const event: AuditEvent = Object.freeze({
      sequence: this.#auditSeq,
      time: this.#now,
      type,
      result,
      ...(document
        ? {
            documentGeneration: document.identity.documentGeneration,
            historyEntryID: document.historyEntryID,
            authSessionID: document.identity.authSessionID,
            authEpoch: document.identity.authEpoch,
          }
        : {}),
      ...(options.afterRevocation !== undefined
        ? { afterRevocation: options.afterRevocation }
        : {}),
      ...(options.violation ? { violation: true } : {}),
      ...(detail ? { detail: Object.freeze({ ...detail }) } : {}),
    });
    this.#auditSeq += 1;
    this.#audit.push(event);
    if (event.violation) {
      this.#violations.push(event);
    }
    return event;
  }
}

/**
 * A promise whose callbacks are dispatched by the host's deterministic queue.
 * Native microtasks cannot be frozen by a browser host, so document code must
 * use this wrapper (or the host's queue directly) for BFCache-sensitive work.
 */
export class GuardedPromise<T> {
  readonly id: string;
  readonly documentGeneration: number;
  readonly #host: BfcacheHost;

  constructor(host: BfcacheHost, id: string, documentGeneration: number) {
    this.#host = host;
    this.id = id;
    this.documentGeneration = documentGeneration;
  }

  then<R = T>(
    onFulfilled?: (value: T) => R | PromiseLike<R>,
    onRejected?: (reason: unknown) => R | PromiseLike<R>,
  ): GuardedPromise<R> {
    const child = this.#host.createPromise<R>(this.documentGeneration);
    this.#host.registerPromiseCallback(
      this.id,
      onFulfilled as ((value: unknown) => unknown) | undefined,
      onRejected as ((reason: unknown) => unknown) | undefined,
      child as GuardedPromise<unknown>,
    );
    return child;
  }

  catch<R = T>(
    onRejected?: (reason: unknown) => R | PromiseLike<R>,
  ): GuardedPromise<R> {
    return this.then<R>(undefined, onRejected);
  }

  resolve(value: T): DeliveryResult {
    return this.#host.settlePromise(this.id, "fulfilled", value);
  }

  reject(reason: unknown): DeliveryResult {
    return this.#host.settlePromise(this.id, "rejected", reason);
  }

  /** @internal */
  _resolveFromCallback(value: unknown): void {
    this.#host.settlePromise(this.id, "fulfilled", value);
  }

  /** @internal */
  _rejectFromCallback(reason: unknown): void {
    this.#host.settlePromise(this.id, "rejected", reason);
  }
}

export function createBfcacheHost(options: BfcacheHostOptions): BfcacheHost {
  return new BfcacheHost(options);
}

export const createHost = createBfcacheHost;
