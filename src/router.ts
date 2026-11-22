import {
  clonePartition,
  makePartition,
  normalizeScope,
  partitionKey,
  partitionsEqual,
  scopeAllows,
} from "./partition.ts";
import type {
  PartitionIdentity,
  PartitionInput,
  SubscriptionScope,
  SubscriptionScopeInput,
} from "./partition.ts";

export type DocumentState = "prerender" | "active" | "bfcache" | "closed";

export interface DocumentOptions {
  readonly instanceId?: string;
  readonly displayUrl: string;
  readonly partition: PartitionInput;
  readonly ancestorChain?: readonly string[];
  readonly state?: Exclude<DocumentState, "closed">;
  readonly channelNames?: readonly string[];
}

export interface NavigateOptions {
  readonly instanceId?: string;
  readonly displayUrl?: string;
  readonly keepDisplayUrl?: boolean;
  readonly channelNames?: readonly string[];
  readonly keepChannelNames?: boolean;
  readonly partition?: PartitionInput;
  readonly ancestorChain?: readonly string[];
  readonly state?: Exclude<DocumentState, "closed">;
}

export interface ChannelSession {
  readonly sessionId: string;
  readonly instanceId: string;
  readonly lifecycleId: number;
  readonly channelName: string;
  readonly generation: number;
  readonly sequence: number;
  readonly open: boolean;
  readonly partition: PartitionIdentity;
  readonly phase: number;
}

export interface DocumentSnapshot {
  readonly instanceId: string;
  readonly lifecycleId: number;
  readonly displayUrl: string;
  readonly partition: PartitionIdentity;
  readonly ancestorChain: readonly string[];
  readonly state: DocumentState;
  readonly phase: number;
  readonly declaredChannelNames: readonly string[];
  readonly subscriptionIds: readonly string[];
  readonly channelSessions: readonly ChannelSession[];
}

export interface SubscribeOptions {
  readonly id?: string;
  readonly scope?: SubscriptionScopeInput;
  readonly visibleOrigins?: readonly string[];
  readonly visiblePartitions?: readonly PartitionInput[];
  readonly paused?: boolean;
  readonly onMessage?: ReceiveHandler;
  readonly onDelivery?: ReceiveHandler;
  readonly callback?: ReceiveHandler;
}

export interface Subscription {
  readonly id: string;
  readonly instanceId: string;
  readonly lifecycleId: number;
  readonly channelName: string;
  readonly partition: PartitionIdentity;
  readonly scope: SubscriptionScope;
  readonly phase: number;
  readonly active: boolean;
  readonly paused: boolean;
}

export type ReceiveHandler = (
  delivery: BroadcastDelivery,
) => void | Promise<void>;

export interface MessageEnvelope {
  readonly messageId: string;
  readonly senderInstanceId: string;
  readonly senderLifecycleId: number;
  readonly channelSessionId: string;
  readonly channelName: string;
  readonly channelGeneration: number;
  readonly sequence: number;
  readonly payload: unknown;
  readonly sendSnapshot: PartitionIdentity;
  readonly sentAt: number;
}

export interface DeliverySnapshot {
  readonly partition: PartitionIdentity;
  readonly instanceId: string;
  readonly lifecycleId: number;
  readonly phase: number;
  readonly subscriptionId: string;
  readonly capturedAt: number;
}

export interface BroadcastDelivery {
  readonly deliveryId: string;
  readonly messageId: string;
  readonly dedupeKey: string;
  readonly senderInstanceId: string;
  readonly senderLifecycleId: number;
  readonly recipientInstanceId: string;
  readonly recipientLifecycleId: number;
  readonly subscriptionId: string;
  readonly channelName: string;
  readonly channelGeneration: number;
  readonly sequence: number;
  readonly payload: unknown;
  readonly sendSnapshot: PartitionIdentity;
  readonly deliverySnapshot: DeliverySnapshot;
  readonly receivedAt: number;
}

export type DeliveryStatus = "queued" | "delivered" | "dropped";

export interface DeliveryRecord extends BroadcastDelivery {
  readonly status: DeliveryStatus;
  readonly error?: string;
}

export type IngestStatus = "accepted" | "duplicate" | "rejected";

export interface IngestSkip {
  readonly instanceId: string;
  readonly subscriptionId?: string;
  readonly reason: string;
}

export interface IngestResult {
  readonly status: IngestStatus;
  readonly dedupeKey: string;
  readonly reason?: string;
  readonly deliveries: readonly BroadcastDelivery[];
  readonly skipped: readonly IngestSkip[];
}

export interface IngestAuditRecord extends IngestResult {
  readonly receivedAt: number;
}

export interface SendOptions {
  readonly messageId?: string;
  /** Defaults to true. Set false to put the envelope on a simulated wire. */
  readonly deliver?: boolean;
}

export interface SendResult {
  readonly envelope: MessageEnvelope;
  readonly ingest?: IngestResult;
}

export interface TransmitOptions {
  readonly deliverySnapshot?: PartitionInput | { readonly partition: PartitionInput };
  readonly deliveryPartition?: PartitionInput;
  readonly recipientInstanceId?: string;
  readonly includeSender?: boolean;
  readonly allowInvalidSender?: boolean;
}

export type DeliveryDecisionStatus = "confirmed" | "closed";

export interface DeliveryDecision {
  readonly deliveryId: string;
  readonly messageId: string;
  readonly dedupeKey: string;
  readonly status: DeliveryDecisionStatus;
  readonly by: "ack" | "close";
  readonly at: number;
  readonly reason?: string;
}

export interface DecisionAttempt {
  readonly attempt: number;
  readonly requested: "ack" | "close";
  readonly accepted: boolean;
  readonly at: number;
  readonly reason?: string;
}

export interface DecisionAudit {
  readonly deliveryId: string;
  readonly decision: DeliveryDecision;
  readonly attempts: readonly DecisionAttempt[];
}

interface InternalDocument {
  readonly instanceId: string;
  readonly lifecycleId: number;
  readonly displayUrl: string;
  readonly partition: PartitionIdentity;
  readonly ancestorChain: readonly string[];
  readonly declaredChannelNames: Set<string>;
  readonly sessions: Map<string, string>;
  readonly subscriptionIds: Set<string>;
  state: DocumentState;
  phase: number;
  pausedAll: boolean;
  receiveHandler?: ReceiveHandler;
}

interface InternalSession {
  readonly sessionId: string;
  readonly instanceId: string;
  readonly lifecycleId: number;
  readonly channelName: string;
  readonly generation: number;
  readonly partition: PartitionIdentity;
  sequence: number;
  open: boolean;
  phase: number;
}

interface InternalSubscription {
  readonly id: string;
  readonly instanceId: string;
  readonly lifecycleId: number;
  readonly channelName: string;
  readonly partition: PartitionIdentity;
  readonly scope: SubscriptionScope;
  phase: number;
  active: boolean;
  paused: boolean;
  handler?: ReceiveHandler;
  readonly queue: InternalDelivery[];
  pumping: boolean;
  currentPump?: Promise<void>;
}

interface InternalDelivery extends BroadcastDelivery {
  status: DeliveryStatus;
  error?: string;
}

interface InternalDecision {
  readonly decision: DeliveryDecision;
  readonly attempts: DecisionAttempt[];
}

function isThenable(value: unknown): value is PromiseLike<unknown> {
  return (
    typeof value === "object" &&
    value !== null &&
    typeof (value as { then?: unknown }).then === "function"
  );
}

function cloneSession(session: InternalSession): ChannelSession {
  return Object.freeze({
    sessionId: session.sessionId,
    instanceId: session.instanceId,
    lifecycleId: session.lifecycleId,
    channelName: session.channelName,
    generation: session.generation,
    sequence: session.sequence,
    open: session.open,
    partition: clonePartition(session.partition),
    phase: session.phase,
  });
}

function cloneDocument(document: InternalDocument, sessions: readonly InternalSession[]): DocumentSnapshot {
  return Object.freeze({
    instanceId: document.instanceId,
    lifecycleId: document.lifecycleId,
    displayUrl: document.displayUrl,
    partition: clonePartition(document.partition),
    ancestorChain: Object.freeze([...document.ancestorChain]),
    state: document.state,
    phase: document.phase,
    declaredChannelNames: Object.freeze([...document.declaredChannelNames].sort()),
    subscriptionIds: Object.freeze([...document.subscriptionIds].sort()),
    channelSessions: Object.freeze(sessions.map(cloneSession)),
  });
}

function cloneSubscription(subscription: InternalSubscription): Subscription {
  return Object.freeze({
    id: subscription.id,
    instanceId: subscription.instanceId,
    lifecycleId: subscription.lifecycleId,
    channelName: subscription.channelName,
    partition: clonePartition(subscription.partition),
    scope: subscription.scope,
    phase: subscription.phase,
    active: subscription.active,
    paused: subscription.paused,
  });
}

function cloneDeliverySnapshot(snapshot: DeliverySnapshot): DeliverySnapshot {
  return Object.freeze({
    ...snapshot,
    partition: clonePartition(snapshot.partition),
  });
}

function cloneDelivery(delivery: InternalDelivery): DeliveryRecord {
  return Object.freeze({
    ...delivery,
    sendSnapshot: clonePartition(delivery.sendSnapshot),
    deliverySnapshot: cloneDeliverySnapshot(delivery.deliverySnapshot),
  });
}

function clonePublicDelivery(delivery: InternalDelivery): BroadcastDelivery {
  return Object.freeze({
    deliveryId: delivery.deliveryId,
    messageId: delivery.messageId,
    dedupeKey: delivery.dedupeKey,
    senderInstanceId: delivery.senderInstanceId,
    senderLifecycleId: delivery.senderLifecycleId,
    recipientInstanceId: delivery.recipientInstanceId,
    recipientLifecycleId: delivery.recipientLifecycleId,
    subscriptionId: delivery.subscriptionId,
    channelName: delivery.channelName,
    channelGeneration: delivery.channelGeneration,
    sequence: delivery.sequence,
    payload: delivery.payload,
    sendSnapshot: clonePartition(delivery.sendSnapshot),
    deliverySnapshot: cloneDeliverySnapshot(delivery.deliverySnapshot),
    receivedAt: delivery.receivedAt,
  });
}

function cloneDecision(decision: DeliveryDecision): DeliveryDecision {
  return Object.freeze({ ...decision });
}

/**
 * The only dedupe identity promised by the wire protocol.
 *
 * A lifecycle identity is part of senderInstanceId at the call site. Keeping
 * the tuple explicit here makes it impossible to accidentally dedupe messages
 * from a new channel generation against an old one.
 */
export function dedupeKeyFor(
  senderInstanceId: string,
  channelGeneration: number,
  sequence: number,
): string {
  return [senderInstanceId, String(channelGeneration), String(sequence)].join(
    "\u001f",
  );
}

/**
 * An in-process model of the browser broadcast path. The router also accepts
 * envelopes from another process; those envelopes may be delivered more than
 * once and are deduplicated by the tuple above.
 */
export class BroadcastRouter {
  private readonly documents = new Map<string, InternalDocument>();
  private readonly subscriptions = new Map<string, InternalSubscription>();
  private readonly sessions = new Map<string, InternalSession>();
  private readonly currentSession = new Map<string, string>();
  private readonly channelGenerations = new Map<string, number>();
  private readonly deliveries = new Map<string, InternalDelivery>();
  private readonly deliveryOrder: string[] = [];
  private readonly seenKeys = new Map<string, IngestAuditRecord>();
  private readonly ingestAudit: IngestAuditRecord[] = [];
  private readonly senderSequenceWatermarks = new Map<string, number>();
  private readonly senderGenerationWatermarks = new Map<string, number>();
  private readonly decisions = new Map<string, InternalDecision>();

  private nextLifecycle = 0;
  private nextSession = 0;
  private nextSubscription = 0;
  private nextDelivery = 0;
  private clock = 0;

  private tick(): number {
    this.clock += 1;
    return this.clock;
  }

  private nextLifecycleId(): number {
    this.nextLifecycle += 1;
    return this.nextLifecycle;
  }

  private newDocumentId(lifecycleId: number): string {
    const base = `doc-${lifecycleId}`;
    if (!this.documents.has(base)) return base;
    let suffix = 1;
    while (this.documents.has(`${base}-${suffix}`)) suffix += 1;
    return `${base}-${suffix}`;
  }

  private newSubscriptionId(): string {
    let candidate: string;
    do {
      this.nextSubscription += 1;
      candidate = `sub-${this.nextSubscription}`;
    } while (this.subscriptions.has(candidate));
    return candidate;
  }

  private newSessionId(): string {
    this.nextSession += 1;
    return `chan-${this.nextSession}`;
  }

  private newDeliveryId(): string {
    this.nextDelivery += 1;
    return `delivery-${this.nextDelivery}`;
  }

  private requireDocument(instanceId: string): InternalDocument {
    const document = this.documents.get(instanceId);
    if (document === undefined) {
      throw new Error(`unknown document instance: ${instanceId}`);
    }
    return document;
  }

  createDocument(options: DocumentOptions): DocumentSnapshot {
    const lifecycleId = this.nextLifecycleId();
    const instanceId = options.instanceId ?? this.newDocumentId(lifecycleId);
    if (instanceId.trim().length === 0) {
      throw new TypeError("document instanceId must not be empty");
    }
    if (this.documents.has(instanceId)) {
      throw new Error(`document instanceId already exists: ${instanceId}`);
    }

    const state = options.state ?? "prerender";
    const document: InternalDocument = {
      instanceId,
      lifecycleId,
      displayUrl: options.displayUrl,
      partition: makePartition(options.partition),
      ancestorChain: Object.freeze([...(options.ancestorChain ?? [])]),
      declaredChannelNames: new Set(options.channelNames ?? []),
      sessions: new Map(),
      subscriptionIds: new Set(),
      state,
      phase: 0,
      pausedAll: false,
    };
    this.documents.set(instanceId, document);
    return this.snapshotDocument(document);
  }

  getDocument(instanceId: string): DocumentSnapshot {
    return this.snapshotDocument(this.requireDocument(instanceId));
  }

  listDocuments(): DocumentSnapshot[] {
    return [...this.documents.values()].map((document) =>
      this.snapshotDocument(document),
    );
  }

  private snapshotDocument(document: InternalDocument): DocumentSnapshot {
    const sessions = [...document.sessions.values()]
      .map((sessionId) => this.sessions.get(sessionId))
      .filter((session): session is InternalSession => session !== undefined);
    return cloneDocument(document, sessions);
  }

  activateDocument(instanceId: string): DocumentSnapshot {
    const document = this.requireDocument(instanceId);
    if (document.state !== "prerender") {
      throw new Error(
        `document ${instanceId} cannot activate from ${document.state}`,
      );
    }
    this.advancePhase(document, "activation");
    document.state = "active";
    this.rearmSubscriptions(document);
    return this.snapshotDocument(document);
  }

  enterBackForwardCache(instanceId: string): DocumentSnapshot {
    const document = this.requireDocument(instanceId);
    if (document.state !== "active") {
      throw new Error(
        `document ${instanceId} cannot enter bfcache from ${document.state}`,
      );
    }
    this.advancePhase(document, "bfcache-entry");
    document.state = "bfcache";
    return this.snapshotDocument(document);
  }

  enterBfcache(instanceId: string): DocumentSnapshot {
    return this.enterBackForwardCache(instanceId);
  }

  restoreDocument(instanceId: string): DocumentSnapshot {
    const document = this.requireDocument(instanceId);
    if (document.state !== "bfcache") {
      throw new Error(
        `document ${instanceId} cannot restore from ${document.state}`,
      );
    }
    this.advancePhase(document, "bfcache-restore");
    document.state = "active";
    this.rearmSubscriptions(document);
    return this.snapshotDocument(document);
  }

  restore(instanceId: string): DocumentSnapshot {
    return this.restoreDocument(instanceId);
  }

  closeDocument(instanceId: string): DocumentSnapshot {
    const document = this.requireDocument(instanceId);
    if (document.state === "closed") return this.snapshotDocument(document);

    this.advancePhase(document, "document-close");
    document.state = "closed";
    document.pausedAll = true;
    for (const subscriptionId of document.subscriptionIds) {
      const subscription = this.subscriptions.get(subscriptionId);
      if (subscription !== undefined) subscription.active = false;
    }
    return this.snapshotDocument(document);
  }

  navigateDocument(
    instanceId: string,
    options: NavigateOptions = {},
  ): DocumentSnapshot {
    const previous = this.requireDocument(instanceId);
    if (previous.state === "closed") {
      throw new Error(`document ${instanceId} is closed`);
    }

    const displayUrl =
      options.displayUrl ??
      (options.keepDisplayUrl === false ? "about:blank" : previous.displayUrl);
    const channelNames =
      options.channelNames ??
      (options.keepChannelNames === false
        ? []
        : [...previous.declaredChannelNames]);
    const partition = options.partition ?? previous.partition;
    const ancestorChain = options.ancestorChain ?? previous.ancestorChain;

    this.closeDocument(instanceId);
    return this.createDocument({
      instanceId: options.instanceId,
      displayUrl,
      partition,
      ancestorChain,
      state: options.state ?? "active",
      channelNames,
    });
  }

  navigate(instanceId: string, options: NavigateOptions = {}): DocumentSnapshot {
    return this.navigateDocument(instanceId, options);
  }

  private advancePhase(document: InternalDocument, reason: string): void {
    document.phase += 1;
    for (const sessionId of document.sessions.values()) {
      const session = this.sessions.get(sessionId);
      if (session !== undefined) session.open = false;
    }
    document.sessions.clear();

    for (const subscriptionId of document.subscriptionIds) {
      const subscription = this.subscriptions.get(subscriptionId);
      if (subscription === undefined) continue;
      const queued = subscription.queue.splice(0);
      for (const delivery of queued) {
        this.dropQueuedDelivery(delivery, `lifecycle:${reason}`);
      }
    }
  }

  private rearmSubscriptions(document: InternalDocument): void {
    for (const subscriptionId of document.subscriptionIds) {
      const subscription = this.subscriptions.get(subscriptionId);
      if (subscription === undefined || !subscription.active) continue;
      subscription.phase = document.phase;
      subscription.paused = document.pausedAll || subscription.paused;
    }
    this.pumpDocument(document);
  }

  openChannel(instanceId: string, channelName: string): ChannelSession {
    const document = this.requireDocument(instanceId);
    if (document.state !== "active") {
      throw new Error(
        `document ${instanceId} cannot open a channel while ${document.state}`,
      );
    }
    if (channelName.trim().length === 0) {
      throw new TypeError("channelName must not be empty");
    }

    const currentSessionId = document.sessions.get(channelName);
    if (currentSessionId !== undefined) {
      const current = this.sessions.get(currentSessionId);
      if (current !== undefined) current.open = false;
    }

    const generationKey = `${instanceId}\u001f${channelName}`;
    const generation = (this.channelGenerations.get(generationKey) ?? 0) + 1;
    this.channelGenerations.set(generationKey, generation);

    const session: InternalSession = {
      sessionId: this.newSessionId(),
      instanceId,
      lifecycleId: document.lifecycleId,
      channelName,
      generation,
      partition: document.partition,
      sequence: 0,
      open: true,
      phase: document.phase,
    };
    this.sessions.set(session.sessionId, session);
    this.currentSession.set(
      `${instanceId}\u001f${channelName}`,
      session.sessionId,
    );
    document.sessions.set(channelName, session.sessionId);
    document.declaredChannelNames.add(channelName);
    return cloneSession(session);
  }

  closeChannel(sessionId: string): ChannelSession {
    const session = this.sessions.get(sessionId);
    if (session === undefined) throw new Error(`unknown channel session: ${sessionId}`);
    session.open = false;
    return cloneSession(session);
  }

  getChannelSession(
    instanceId: string,
    channelName: string,
  ): ChannelSession | undefined {
    const sessionId = this.currentSession.get(
      `${instanceId}\u001f${channelName}`,
    );
    if (sessionId === undefined) return undefined;
    const session = this.sessions.get(sessionId);
    return session === undefined ? undefined : cloneSession(session);
  }

  subscribe(
    instanceId: string,
    channelName: string,
    options: SubscribeOptions = {},
  ): Subscription {
    const document = this.requireDocument(instanceId);
    if (document.state === "closed") {
      throw new Error(`document ${instanceId} is closed`);
    }
    if (channelName.trim().length === 0) {
      throw new TypeError("channelName must not be empty");
    }

    const id = options.id ?? this.newSubscriptionId();
    if (this.subscriptions.has(id)) {
      throw new Error(`subscription id already exists: ${id}`);
    }

    const scope = normalizeScope(
      options.scope ??
        (options.visibleOrigins !== undefined || options.visiblePartitions !== undefined
          ? {
              visibleOrigins: options.visibleOrigins,
              visiblePartitions: options.visiblePartitions,
            }
          : undefined),
    );
    const subscription: InternalSubscription = {
      id,
      instanceId,
      lifecycleId: document.lifecycleId,
      channelName,
      partition: document.partition,
      scope,
      phase: document.phase,
      active: true,
      paused: options.paused ?? document.pausedAll,
      handler: options.onMessage ?? options.onDelivery ?? options.callback,
      queue: [],
      pumping: false,
    };
    this.subscriptions.set(id, subscription);
    document.subscriptionIds.add(id);
    document.declaredChannelNames.add(channelName);
    return cloneSubscription(subscription);
  }

  getSubscription(id: string): Subscription {
    const subscription = this.subscriptions.get(id);
    if (subscription === undefined) throw new Error(`unknown subscription: ${id}`);
    return cloneSubscription(subscription);
  }

  listSubscriptions(instanceId?: string): Subscription[] {
    return [...this.subscriptions.values()]
      .filter(
        (subscription) =>
          instanceId === undefined || subscription.instanceId === instanceId,
      )
      .map(cloneSubscription);
  }

  unsubscribe(subscriptionId: string): void {
    const subscription = this.subscriptions.get(subscriptionId);
    if (subscription === undefined) {
      throw new Error(`unknown subscription: ${subscriptionId}`);
    }
    subscription.active = false;
    subscription.paused = true;
    const queued = subscription.queue.splice(0);
    for (const delivery of queued) {
      this.dropQueuedDelivery(delivery, "subscription-revoked");
    }
  }

  setReceiveHandler(instanceId: string, handler: ReceiveHandler): void {
    const document = this.requireDocument(instanceId);
    document.receiveHandler = handler;
    this.pumpDocument(document);
  }

  setSubscriptionHandler(
    subscriptionId: string,
    handler: ReceiveHandler,
  ): void {
    const subscription = this.subscriptions.get(subscriptionId);
    if (subscription === undefined) {
      throw new Error(`unknown subscription: ${subscriptionId}`);
    }
    subscription.handler = handler;
    this.pumpSubscription(subscription);
  }

  pauseReceiving(instanceId: string, subscriptionId?: string): void {
    const document = this.requireDocument(instanceId);
    if (subscriptionId !== undefined) {
      const subscription = this.subscriptions.get(subscriptionId);
      if (subscription === undefined || subscription.instanceId !== instanceId) {
        throw new Error(
          `subscription ${subscriptionId} does not belong to ${instanceId}`,
        );
      }
      subscription.paused = true;
      return;
    }

    document.pausedAll = true;
    for (const id of document.subscriptionIds) {
      const subscription = this.subscriptions.get(id);
      if (subscription !== undefined) subscription.paused = true;
    }
  }

  async resumeReceiving(
    instanceId: string,
    subscriptionId?: string,
  ): Promise<void> {
    const document = this.requireDocument(instanceId);
    if (subscriptionId !== undefined) {
      const subscription = this.subscriptions.get(subscriptionId);
      if (subscription === undefined || subscription.instanceId !== instanceId) {
        throw new Error(
          `subscription ${subscriptionId} does not belong to ${instanceId}`,
        );
      }
      subscription.paused = false;
      this.pumpSubscription(subscription);
    } else {
      document.pausedAll = false;
      for (const id of document.subscriptionIds) {
        const subscription = this.subscriptions.get(id);
        if (subscription !== undefined) subscription.paused = false;
      }
      this.pumpDocument(document);
    }
    await this.flush();
  }

  send(
    instanceId: string,
    channelName: string,
    payload: unknown,
    options: SendOptions = {},
  ): SendResult {
    const document = this.requireDocument(instanceId);
    if (document.state !== "active") {
      throw new Error(
        `document ${instanceId} cannot send while ${document.state}`,
      );
    }

    let session = this.currentOpenSession(document, channelName);
    if (session === undefined) {
      this.openChannel(instanceId, channelName);
      session = this.currentOpenSession(document, channelName);
    }
    if (session === undefined) {
      throw new Error(`failed to open channel ${channelName}`);
    }

    session.sequence += 1;
    const sentAt = this.tick();
    const messageId =
      options.messageId ??
      `${instanceId}:${document.lifecycleId}:${channelName}:${session.generation}:${session.sequence}`;
    const envelope: MessageEnvelope = Object.freeze({
      messageId,
      senderInstanceId: instanceId,
      senderLifecycleId: document.lifecycleId,
      channelSessionId: session.sessionId,
      channelName,
      channelGeneration: session.generation,
      sequence: session.sequence,
      payload,
      sendSnapshot: clonePartition(document.partition),
      sentAt,
    });

    if (options.deliver === false) {
      return Object.freeze({ envelope });
    }
    return Object.freeze({ envelope, ingest: this.transmit(envelope) });
  }

  broadcast(
    instanceId: string,
    channelName: string,
    payload: unknown,
    options: SendOptions = {},
  ): SendResult {
    return this.send(instanceId, channelName, payload, options);
  }

  postMessage(
    instanceId: string,
    channelName: string,
    payload: unknown,
    options: SendOptions = {},
  ): SendResult {
    return this.send(instanceId, channelName, payload, options);
  }

  private currentOpenSession(
    document: InternalDocument,
    channelName: string,
  ): InternalSession | undefined {
    const sessionId = document.sessions.get(channelName);
    if (sessionId === undefined) return undefined;
    const session = this.sessions.get(sessionId);
    if (
      session === undefined ||
      !session.open ||
      session.phase !== document.phase ||
      session.lifecycleId !== document.lifecycleId
    ) {
      return undefined;
    }
    return session;
  }

  /**
   * Injects an envelope received from a wire. The caller may supply the
   * partition snapshot captured by the receiving process. Without it, each
   * candidate document's partition at receipt is used as its snapshot.
   */
  transmit(
    input: MessageEnvelope,
    options: TransmitOptions = {},
  ): IngestResult {
    const envelope = this.normalizeEnvelope(input);
    const dedupeKey = dedupeKeyFor(
      envelope.senderInstanceId,
      envelope.channelGeneration,
      envelope.sequence,
    );
    const receivedAt = this.tick();

    const duplicate = this.seenKeys.get(dedupeKey);
    if (duplicate !== undefined) {
      const result: IngestAuditRecord = Object.freeze({
        status: "duplicate",
        dedupeKey,
        reason: "at-least-once redelivery",
        deliveries: Object.freeze([]),
        skipped: Object.freeze([]),
        receivedAt,
      });
      this.ingestAudit.push(result);
      return result;
    }

    const senderProblem = this.validateSender(envelope, options);
    if (senderProblem !== undefined) {
      return this.rejectIngest(dedupeKey, senderProblem, receivedAt);
    }

    const deliveryPartition = this.resolveDeliveryPartition(options);
    const candidates = this.findRecipients(envelope, options, deliveryPartition);
    const acceptedDeliveries: InternalDelivery[] = [];
    const skipped: IngestSkip[] = [];

    for (const candidate of candidates) {
      if (candidate.skipReason !== undefined) {
        skipped.push({
          instanceId: candidate.document.instanceId,
          subscriptionId: candidate.subscription?.id,
          reason: candidate.skipReason,
        });
        continue;
      }
      const subscription = candidate.subscription;
      if (subscription === undefined) continue;

      const deliverySnapshot: DeliverySnapshot = Object.freeze({
        partition: clonePartition(
          deliveryPartition ?? candidate.document.partition,
        ),
        instanceId: candidate.document.instanceId,
        lifecycleId: candidate.document.lifecycleId,
        phase: candidate.document.phase,
        subscriptionId: subscription.id,
        capturedAt: receivedAt,
      });

      const delivery: InternalDelivery = {
        deliveryId: this.newDeliveryId(),
        messageId: envelope.messageId,
        dedupeKey,
        senderInstanceId: envelope.senderInstanceId,
        senderLifecycleId: envelope.senderLifecycleId,
        recipientInstanceId: candidate.document.instanceId,
        recipientLifecycleId: candidate.document.lifecycleId,
        subscriptionId: subscription.id,
        channelName: envelope.channelName,
        channelGeneration: envelope.channelGeneration,
        sequence: envelope.sequence,
        payload: envelope.payload,
        sendSnapshot: clonePartition(envelope.sendSnapshot),
        deliverySnapshot,
        receivedAt,
        status: "queued",
      };
      this.deliveries.set(delivery.deliveryId, delivery);
      this.deliveryOrder.push(delivery.deliveryId);
      subscription.queue.push(delivery);
      acceptedDeliveries.push(delivery);
      if (!subscription.paused && !candidate.document.pausedAll) {
        this.pumpSubscription(subscription);
      }
    }

    this.recordSenderWatermarks(envelope);
    const result: IngestAuditRecord = Object.freeze({
      status: "accepted",
      dedupeKey,
      deliveries: Object.freeze(
        acceptedDeliveries.map((delivery) => clonePublicDelivery(delivery)),
      ),
      skipped: Object.freeze(skipped),
      receivedAt,
    });
    this.seenKeys.set(dedupeKey, result);
    this.ingestAudit.push(result);
    return result;
  }

  receiveTransportMessage(
    input: MessageEnvelope,
    options: TransmitOptions = {},
  ): IngestResult {
    return this.transmit(input, options);
  }

  private normalizeEnvelope(input: MessageEnvelope): MessageEnvelope {
    if (!Number.isFinite(input.sequence) || input.sequence <= 0) {
      throw new TypeError("envelope sequence must be a positive finite number");
    }
    if (
      !Number.isFinite(input.channelGeneration) ||
      input.channelGeneration <= 0
    ) {
      throw new TypeError(
        "envelope channelGeneration must be a positive finite number",
      );
    }
    if (input.senderInstanceId.trim().length === 0) {
      throw new TypeError("envelope senderInstanceId must not be empty");
    }
    if (input.channelName.trim().length === 0) {
      throw new TypeError("envelope channelName must not be empty");
    }
    return Object.freeze({
      ...input,
      sequence: Math.trunc(input.sequence),
      channelGeneration: Math.trunc(input.channelGeneration),
      sendSnapshot: makePartition(input.sendSnapshot),
    });
  }

  private validateSender(
    envelope: MessageEnvelope,
    options: TransmitOptions,
  ): string | undefined {
    const generationStream = `${envelope.senderInstanceId}\u001f${envelope.channelName}`;
    const sequenceStream = `${envelope.senderInstanceId}\u001f${envelope.channelGeneration}`;
    const highestGeneration = this.senderGenerationWatermarks.get(generationStream);
    if (
      highestGeneration !== undefined &&
      envelope.channelGeneration < highestGeneration
    ) {
      return "stale-channel-generation";
    }
    const highestSequence = this.senderSequenceWatermarks.get(sequenceStream);
    if (highestSequence !== undefined && envelope.sequence <= highestSequence) {
      return "stale-sequence";
    }

    if (options.allowInvalidSender === true) return undefined;
    const sender = this.documents.get(envelope.senderInstanceId);
    if (sender === undefined) return undefined;

    if (sender.lifecycleId !== envelope.senderLifecycleId) {
      return "stale-sender-lifecycle";
    }
    const session = this.sessions.get(envelope.channelSessionId);
    if (
      session === undefined ||
      session.instanceId !== envelope.senderInstanceId ||
      session.channelName !== envelope.channelName ||
      session.generation !== envelope.channelGeneration ||
      !session.open ||
      session.phase !== sender.phase
    ) {
      return "stale-channel-session";
    }
    return undefined;
  }

  private recordSenderWatermarks(envelope: MessageEnvelope): void {
    const generationStream = `${envelope.senderInstanceId}\u001f${envelope.channelName}`;
    const sequenceStream = `${envelope.senderInstanceId}\u001f${envelope.channelGeneration}`;
    const currentGeneration = this.senderGenerationWatermarks.get(generationStream);
    if (
      currentGeneration === undefined ||
      envelope.channelGeneration > currentGeneration
    ) {
      this.senderGenerationWatermarks.set(
        generationStream,
        envelope.channelGeneration,
      );
    }
    this.senderSequenceWatermarks.set(sequenceStream, envelope.sequence);
  }

  private resolveDeliveryPartition(
    options: TransmitOptions,
  ): PartitionIdentity | undefined {
    const raw = options.deliverySnapshot ?? options.deliveryPartition;
    if (raw === undefined) return undefined;
    if (
      typeof raw === "object" &&
      raw !== null &&
      "partition" in raw
    ) {
      return makePartition(
        (raw as { readonly partition: PartitionInput }).partition,
      );
    }
    return makePartition(raw as PartitionInput);
  }

  private findRecipients(
    envelope: MessageEnvelope,
    options: TransmitOptions,
    deliveryPartition: PartitionIdentity | undefined,
  ): Array<{
    document: InternalDocument;
    subscription?: InternalSubscription;
    skipReason?: string;
  }> {
    const result: Array<{
      document: InternalDocument;
      subscription?: InternalSubscription;
      skipReason?: string;
    }> = [];

    for (const document of this.documents.values()) {
      if (
        options.recipientInstanceId !== undefined &&
        document.instanceId !== options.recipientInstanceId
      ) {
        continue;
      }
      if (
        options.includeSender !== true &&
        document.instanceId === envelope.senderInstanceId
      ) {
        continue;
      }
      if (document.state !== "active") {
        if (options.recipientInstanceId === document.instanceId) {
          result.push({ document, skipReason: `document-${document.state}` });
        }
        continue;
      }

      const matching = [...document.subscriptionIds]
        .map((id) => this.subscriptions.get(id))
        .filter(
          (subscription): subscription is InternalSubscription =>
            subscription !== undefined &&
            subscription.channelName === envelope.channelName,
        );

      if (matching.length === 0) continue;

      for (const subscription of matching) {
        const skipReason = this.recipientSkipReason(
          document,
          subscription,
          envelope,
          deliveryPartition,
        );
        result.push({
          document,
          subscription,
          ...(skipReason === undefined ? {} : { skipReason }),
        });
      }
    }
    return result;
  }

  private recipientSkipReason(
    document: InternalDocument,
    subscription: InternalSubscription,
    envelope: MessageEnvelope,
    deliveryPartition: PartitionIdentity | undefined,
  ): string | undefined {
    if (!subscription.active) return "subscription-revoked";
    if (subscription.phase !== document.phase) return "subscription-not-current";
    if (!partitionsEqual(document.partition, envelope.sendSnapshot)) {
      return "send-partition-mismatch";
    }
    if (!partitionsEqual(subscription.partition, document.partition)) {
      return "subscription-partition-mismatch";
    }
    if (
      deliveryPartition !== undefined &&
      !partitionsEqual(deliveryPartition, document.partition)
    ) {
      return "delivery-partition-mismatch";
    }
    if (!scopeAllows(subscription.scope, envelope.sendSnapshot)) {
      return "outside-visible-scope";
    }
    return undefined;
  }

  private rejectIngest(
    dedupeKey: string,
    reason: string,
    receivedAt: number,
  ): IngestResult {
    const result: IngestAuditRecord = Object.freeze({
      status: "rejected",
      dedupeKey,
      reason,
      deliveries: Object.freeze([]),
      skipped: Object.freeze([]),
      receivedAt,
    });
    this.ingestAudit.push(result);
    return result;
  }

  private pumpDocument(document: InternalDocument): void {
    for (const subscriptionId of document.subscriptionIds) {
      const subscription = this.subscriptions.get(subscriptionId);
      if (subscription !== undefined) this.pumpSubscription(subscription);
    }
  }

  private pumpSubscription(subscription: InternalSubscription): void {
    if (subscription.pumping || subscription.paused || !subscription.active) {
      return;
    }
    const document = this.documents.get(subscription.instanceId);
    if (document === undefined || document.state !== "active") return;
    if (document.pausedAll) return;

    const handler = subscription.handler ?? document.receiveHandler;
    if (handler === undefined) return;

    subscription.pumping = true;
    try {
      while (subscription.queue.length > 0) {
        if (
          subscription.paused ||
          !subscription.active ||
          document.pausedAll ||
          document.state !== "active"
        ) {
          break;
        }

        const delivery = subscription.queue[0];
        if (delivery === undefined) break;
        const invalidReason = this.queuedDeliveryProblem(
          delivery,
          subscription,
          document,
        );
        if (invalidReason !== undefined) {
          subscription.queue.shift();
          this.dropQueuedDelivery(delivery, invalidReason);
          continue;
        }

        subscription.queue.shift();
        let result: void | Promise<void>;
        try {
          result = handler(clonePublicDelivery(delivery));
          delivery.status = "delivered";
        } catch (error) {
          delivery.status = "dropped";
          delivery.error =
            error instanceof Error ? error.message : String(error);
          continue;
        }

        if (isThenable(result)) {
          const pump = Promise.resolve(result)
            .then(
              () => undefined,
              (error: unknown) => {
                delivery.error =
                  error instanceof Error ? error.message : String(error);
              },
            )
            .finally(() => {
              subscription.currentPump = undefined;
              subscription.pumping = false;
              this.pumpSubscription(subscription);
            });
          subscription.currentPump = pump;
          return;
        }
      }
      subscription.pumping = false;
    } catch (error) {
      subscription.pumping = false;
      throw error;
    }
  }

  private queuedDeliveryProblem(
    delivery: InternalDelivery,
    subscription: InternalSubscription,
    document: InternalDocument,
  ): string | undefined {
    if (!subscription.active) return "subscription-revoked";
    if (
      document.instanceId !== delivery.recipientInstanceId ||
      document.lifecycleId !== delivery.recipientLifecycleId ||
      document.state !== "active" ||
      document.phase !== delivery.deliverySnapshot.phase ||
      subscription.phase !== document.phase
    ) {
      return "stale-lifecycle";
    }
    if (
      !partitionsEqual(document.partition, delivery.sendSnapshot) ||
      !partitionsEqual(document.partition, delivery.deliverySnapshot.partition) ||
      !partitionsEqual(subscription.partition, document.partition)
    ) {
      return "partition-changed";
    }
    if (!scopeAllows(subscription.scope, delivery.sendSnapshot)) {
      return "outside-visible-scope";
    }
    return undefined;
  }

  private dropQueuedDelivery(delivery: InternalDelivery, reason: string): void {
    if (delivery.status === "delivered") return;
    delivery.status = "dropped";
    delivery.error = reason;
    if (!this.decisions.has(delivery.deliveryId)) {
      this.setDecision(delivery, "closed", "close", reason, true);
    }
  }

  async flush(): Promise<void> {
    while (true) {
      const pending = [...this.subscriptions.values()]
        .map((subscription) => subscription.currentPump)
        .filter((pump): pump is Promise<void> => pump !== undefined);
      if (pending.length === 0) return;
      await Promise.allSettled(pending);
    }
  }

  getDeliveries(): DeliveryRecord[] {
    return this.deliveryOrder
      .map((id) => this.deliveries.get(id))
      .filter((delivery): delivery is InternalDelivery => delivery !== undefined)
      .map(cloneDelivery);
  }

  getDelivery(deliveryId: string): DeliveryRecord | undefined {
    const delivery = this.deliveries.get(deliveryId);
    return delivery === undefined ? undefined : cloneDelivery(delivery);
  }

  getIngestAudit(): IngestAuditRecord[] {
    return this.ingestAudit.map((record) =>
      Object.freeze({
        ...record,
        deliveries: Object.freeze(
          record.deliveries.map((delivery) =>
            Object.freeze({
              ...delivery,
              sendSnapshot: clonePartition(delivery.sendSnapshot),
              deliverySnapshot: Object.freeze({
                ...delivery.deliverySnapshot,
                partition: clonePartition(
                  delivery.deliverySnapshot.partition,
                ),
              }),
            }),
          ),
        ),
        skipped: Object.freeze(record.skipped.map((skip) => Object.freeze({ ...skip }))),
      }),
    );
  }

  getIngestRecord(dedupeKeyOrMessageId: string): IngestAuditRecord | undefined {
    const byKey = this.getIngestAudit().find(
      (candidate) =>
        candidate.dedupeKey === dedupeKeyOrMessageId &&
        candidate.status === "accepted",
    );
    if (byKey !== undefined) return byKey;

    return this.getIngestAudit().find(
      (candidate) =>
        candidate.status === "accepted" &&
        candidate.deliveries.some(
          (delivery) => delivery.messageId === dedupeKeyOrMessageId,
        ),
    );
  }

  confirmDelivery(deliveryId: string, reason?: string): DeliveryDecision {
    return this.decideDelivery(deliveryId, "ack", "confirmed", reason);
  }

  acknowledgeDelivery(deliveryId: string, reason?: string): DeliveryDecision {
    return this.confirmDelivery(deliveryId, reason);
  }

  acknowledge(deliveryId: string, reason?: string): DeliveryDecision {
    return this.confirmDelivery(deliveryId, reason);
  }

  closeDelivery(deliveryId: string, reason?: string): DeliveryDecision {
    return this.decideDelivery(deliveryId, "close", "closed", reason);
  }

  private decideDelivery(
    deliveryId: string,
    requested: "ack" | "close",
    status: DeliveryDecisionStatus,
    reason: string | undefined,
  ): DeliveryDecision {
    const delivery = this.deliveries.get(deliveryId);
    if (delivery === undefined) throw new Error(`unknown delivery: ${deliveryId}`);
    const existing = this.decisions.get(deliveryId);
    if (existing !== undefined) {
      existing.attempts.push(
        Object.freeze({
          attempt: existing.attempts.length + 1,
          requested,
          accepted: false,
          at: this.tick(),
          reason: reason ?? "already-decided",
        }),
      );
      return cloneDecision(existing.decision);
    }

    if (requested === "close" && delivery.status === "queued") {
      const subscription = this.subscriptions.get(delivery.subscriptionId);
      if (subscription !== undefined) {
        const index = subscription.queue.indexOf(delivery);
        if (index >= 0) subscription.queue.splice(index, 1);
      }
      delivery.status = "dropped";
      delivery.error = reason ?? "closed";
    }
    return this.setDecision(delivery, status, requested, reason, true);
  }

  private setDecision(
    delivery: InternalDelivery,
    status: DeliveryDecisionStatus,
    by: "ack" | "close",
    reason: string | undefined,
    accepted: boolean,
  ): DeliveryDecision {
    const decision: DeliveryDecision = Object.freeze({
      deliveryId: delivery.deliveryId,
      messageId: delivery.messageId,
      dedupeKey: delivery.dedupeKey,
      status,
      by,
      at: this.tick(),
      ...(reason === undefined ? {} : { reason }),
    });
    this.decisions.set(delivery.deliveryId, {
      decision,
      attempts: accepted
        ? [Object.freeze({ attempt: 1, requested: by, accepted: true, at: decision.at, ...(reason === undefined ? {} : { reason }) })]
        : [],
    });
    return decision;
  }

  getDecision(deliveryId: string): DeliveryDecision | undefined {
    const decision = this.decisions.get(deliveryId);
    return decision === undefined ? undefined : cloneDecision(decision.decision);
  }

  getDecisionAudit(deliveryId: string): DecisionAudit | undefined {
    const internal = this.decisions.get(deliveryId);
    if (internal === undefined) return undefined;
    return Object.freeze({
      deliveryId,
      decision: cloneDecision(internal.decision),
      attempts: Object.freeze(internal.attempts.map((attempt) => Object.freeze({ ...attempt }))),
    });
  }

  getClock(): number {
    return this.clock;
  }

  partitionKeyOf(instanceId: string): string {
    return partitionKey(this.requireDocument(instanceId).partition);
  }
}
