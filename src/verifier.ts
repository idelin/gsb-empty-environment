import { BroadcastRouter } from "./router.ts";
import { makePartition, partitionKey } from "./partition.ts";
import type {
  BroadcastDelivery,
  DocumentState,
  MessageEnvelope,
} from "./router.ts";
import type {
  PartitionInput,
  SubscriptionScopeInput,
} from "./partition.ts";

export interface CreateDocumentAction {
  readonly type: "createDocument";
  readonly document: string;
  readonly displayUrl: string;
  readonly partition: PartitionInput;
  readonly ancestorChain?: readonly string[];
  readonly state?: Exclude<DocumentState, "closed">;
  readonly channelNames?: readonly string[];
}

export interface ActivateDocumentAction {
  readonly type: "activateDocument";
  readonly document: string;
}

export interface EnterBackForwardCacheAction {
  readonly type: "enterBackForwardCache";
  readonly document: string;
}

export interface RestoreDocumentAction {
  readonly type: "restoreDocument";
  readonly document: string;
}

export interface CloseDocumentAction {
  readonly type: "closeDocument";
  readonly document: string;
}

export interface NavigateDocumentAction {
  readonly type: "navigateDocument";
  readonly document: string;
  readonly nextDocument?: string;
  readonly displayUrl?: string;
  readonly keepDisplayUrl?: boolean;
  readonly channelNames?: readonly string[];
  readonly keepChannelNames?: boolean;
  readonly partition?: PartitionInput;
  readonly ancestorChain?: readonly string[];
  readonly state?: Exclude<DocumentState, "closed">;
}

export interface OpenChannelAction {
  readonly type: "openChannel";
  readonly document: string;
  readonly channel: string;
}

export interface CloseChannelAction {
  readonly type: "closeChannel";
  readonly sessionId: string;
}

export interface SubscribeAction {
  readonly type: "subscribe";
  readonly subscription: string;
  readonly document: string;
  readonly channel: string;
  readonly scope?: SubscriptionScopeInput;
  readonly visibleOrigins?: readonly string[];
  readonly visiblePartitions?: readonly PartitionInput[];
  readonly paused?: boolean;
}

export interface UnsubscribeAction {
  readonly type: "unsubscribe";
  readonly subscription: string;
}

export interface PauseAction {
  readonly type: "pause";
  readonly document: string;
  readonly subscription?: string;
}

export interface ResumeAction {
  readonly type: "resume";
  readonly document: string;
  readonly subscription?: string;
}

export interface SendAction {
  readonly type: "send";
  readonly messageId?: string;
  readonly document: string;
  readonly channel: string;
  readonly payload: unknown;
  /** Defaults to true; false leaves the envelope for a later transmit event. */
  readonly deliver?: boolean;
}

export interface TransmitAction {
  readonly type: "transmit";
  readonly envelopeFrom: string;
  readonly deliverySnapshot?: PartitionInput | { readonly partition: PartitionInput };
  readonly recipient?: string;
}

export interface ConfirmDeliveryAction {
  readonly type: "confirmDelivery";
  readonly deliveryId?: string;
  readonly messageId?: string;
  readonly subscription?: string;
  readonly recipient?: string;
  readonly reason?: string;
}

export interface CloseDeliveryAction {
  readonly type: "closeDelivery";
  readonly deliveryId?: string;
  readonly messageId?: string;
  readonly subscription?: string;
  readonly recipient?: string;
  readonly reason?: string;
}

export type ScenarioAction =
  | CreateDocumentAction
  | ActivateDocumentAction
  | EnterBackForwardCacheAction
  | RestoreDocumentAction
  | CloseDocumentAction
  | NavigateDocumentAction
  | OpenChannelAction
  | CloseChannelAction
  | SubscribeAction
  | UnsubscribeAction
  | PauseAction
  | ResumeAction
  | SendAction
  | TransmitAction
  | ConfirmDeliveryAction
  | CloseDeliveryAction;

/**
 * A node in a partial order. Only dependencies are significant; the array
 * order in the source file is not an additional ordering constraint.
 */
export interface PartialOrderEvent {
  readonly id: string;
  readonly action: ScenarioAction;
  readonly dependsOn?: readonly string[];
  readonly after?: readonly string[];
  readonly predecessors?: readonly string[];
  readonly causalPredecessors?: readonly string[];
  /** This event must run before each event named here. */
  readonly before?: readonly string[];
}

export interface DeliveryClassification {
  readonly key: string;
  readonly messageId: string;
  readonly recipientDocument: string;
  readonly subscriptionId: string;
  readonly channelName: string;
  readonly channelGeneration: number;
  readonly sequences: readonly number[];
  readonly occurrenceCount: number;
  readonly classification: "always" | "order-dependent";
}

export interface VerificationOrderResult {
  readonly eventIds: readonly string[];
  readonly deliveryKeys: readonly string[];
}

export interface VerificationReport {
  readonly eventCount: number;
  readonly orderCount: number;
  /** Deliveries that occur under every legal topological order. */
  readonly always: readonly DeliveryClassification[];
  /** Deliveries that occur under at least one, but not every, legal order. */
  readonly orderDependent: readonly DeliveryClassification[];
  readonly allDeliveries: readonly DeliveryClassification[];
  readonly orders: readonly VerificationOrderResult[];
  /** Convenience aliases for callers that use must/may terminology. */
  readonly must: readonly DeliveryClassification[];
  readonly may: readonly DeliveryClassification[];
}

export interface VerifyOptions {
  readonly maxOrders?: number;
  readonly includeOrders?: boolean;
}

export class PartialOrderTooLargeError extends Error {
  readonly eventCount: number;
  readonly maxOrders: number;

  constructor(eventCount: number, maxOrders: number) {
    super(
      `partial order has more than ${maxOrders} legal orders (${eventCount} events); raise maxOrders or add dependencies`,
    );
    this.name = "PartialOrderTooLargeError";
    this.eventCount = eventCount;
    this.maxOrders = maxOrders;
  }
}

interface ExecutedOrder {
  readonly eventIds: readonly string[];
  readonly deliveries: ReadonlyMap<string, DeliveryObservation>;
}

interface DeliveryObservation {
  readonly key: string;
  readonly messageId: string;
  readonly recipientDocument: string;
  readonly subscriptionId: string;
  readonly channelName: string;
  readonly channelGeneration: number;
  readonly sequences: Set<number>;
}

export function deliveryOutcomeKey(delivery: BroadcastDelivery): string {
  return [
    delivery.messageId,
    delivery.subscriptionId,
    delivery.recipientInstanceId,
  ].join("\u001f");
}

function canonicalParents(event: PartialOrderEvent): Set<string> {
  const parents = new Set<string>();
  for (const parent of [
    ...(event.dependsOn ?? []),
    ...(event.after ?? []),
    ...(event.predecessors ?? []),
    ...(event.causalPredecessors ?? []),
  ]) {
    parents.add(parent);
  }
  return parents;
}

export function enumerateLegalOrders(
  events: readonly PartialOrderEvent[],
  maxOrders = 2_000,
): string[][] {
  if (events.length === 0) return [[]];
  if (maxOrders <= 0) {
    throw new RangeError("maxOrders must be positive");
  }

  const byId = new Map<string, PartialOrderEvent>();
  for (const event of events) {
    if (event.id.trim().length === 0) {
      throw new TypeError("event id must not be empty");
    }
    if (byId.has(event.id)) {
      throw new Error(`duplicate event id: ${event.id}`);
    }
    byId.set(event.id, event);
  }

  const children = new Map<string, Set<string>>();
  const indegree = new Map<string, number>();
  for (const event of events) {
    children.set(event.id, new Set());
    indegree.set(event.id, 0);
  }

  const addEdge = (parent: string, child: string): void => {
    const targets = children.get(parent);
    if (targets === undefined) {
      throw new Error(`event ${child} depends on unknown event ${parent}`);
    }
    if (targets.has(child)) return;
    targets.add(child);
    indegree.set(child, (indegree.get(child) ?? 0) + 1);
  };

  for (const event of events) {
    for (const parent of canonicalParents(event)) {
      addEdge(parent, event.id);
    }
    for (const child of event.before ?? []) {
      addEdge(event.id, child);
    }
  }

  const orders: string[][] = [];
  const current: string[] = [];
  const mutableIndegree = new Map(indegree);

  const visit = (): void => {
    if (orders.length >= maxOrders) {
      throw new PartialOrderTooLargeError(events.length, maxOrders);
    }
    if (current.length === events.length) {
      orders.push([...current]);
      return;
    }

    const available = [...byId.keys()]
      .filter((id) => (mutableIndegree.get(id) ?? 0) === 0)
      .sort();
    if (available.length === 0) {
      throw new Error("partial order contains a cycle");
    }

    for (const id of available) {
      current.push(id);
      mutableIndegree.set(id, -1);
      for (const child of children.get(id) ?? []) {
        mutableIndegree.set(child, (mutableIndegree.get(child) ?? 0) - 1);
      }
      visit();
      for (const child of children.get(id) ?? []) {
        mutableIndegree.set(child, (mutableIndegree.get(child) ?? 0) + 1);
      }
      mutableIndegree.set(id, 0);
      current.pop();
    }
  };

  visit();
  return orders;
}

async function executeOrder(
  eventsById: ReadonlyMap<string, PartialOrderEvent>,
  order: readonly string[],
): Promise<ExecutedOrder> {
  const router = new BroadcastRouter();
  const envelopes = new Map<string, MessageEnvelope>();
  const deliveries = new Map<string, DeliveryObservation>();

  for (const eventId of order) {
    const event = eventsById.get(eventId);
    if (event === undefined) throw new Error(`unknown event ${eventId}`);
    const action = event.action;

    switch (action.type) {
      case "createDocument": {
        router.createDocument({
          instanceId: action.document,
          displayUrl: action.displayUrl,
          partition: action.partition,
          ancestorChain: action.ancestorChain,
          state: action.state,
          channelNames: action.channelNames,
        });
        break;
      }
      case "activateDocument":
        router.activateDocument(action.document);
        break;
      case "enterBackForwardCache":
        router.enterBackForwardCache(action.document);
        break;
      case "restoreDocument":
        router.restoreDocument(action.document);
        break;
      case "closeDocument":
        router.closeDocument(action.document);
        break;
      case "navigateDocument":
        router.navigateDocument(action.document, {
          instanceId: action.nextDocument,
          displayUrl: action.displayUrl,
          keepDisplayUrl: action.keepDisplayUrl,
          channelNames: action.channelNames,
          keepChannelNames: action.keepChannelNames,
          partition: action.partition,
          ancestorChain: action.ancestorChain,
          state: action.state,
        });
        break;
      case "openChannel":
        router.openChannel(action.document, action.channel);
        break;
      case "closeChannel":
        router.closeChannel(action.sessionId);
        break;
      case "subscribe":
        router.subscribe(action.document, action.channel, {
          id: action.subscription,
          scope: action.scope,
          visibleOrigins: action.visibleOrigins,
          visiblePartitions: action.visiblePartitions,
          paused: action.paused,
          onMessage: (delivery) => {
            const key = deliveryOutcomeKey(delivery);
            const existing = deliveries.get(key);
            if (existing === undefined) {
              deliveries.set(key, {
                key,
                messageId: delivery.messageId,
                recipientDocument: delivery.recipientInstanceId,
                subscriptionId: delivery.subscriptionId,
                channelName: delivery.channelName,
                channelGeneration: delivery.channelGeneration,
                sequences: new Set([delivery.sequence]),
              });
            } else {
              existing.sequences.add(delivery.sequence);
            }
          },
        });
        break;
      case "unsubscribe":
        router.unsubscribe(action.subscription);
        break;
      case "pause":
        router.pauseReceiving(action.document, action.subscription);
        break;
      case "resume":
        await router.resumeReceiving(action.document, action.subscription);
        break;
      case "send": {
        const sent = router.send(action.document, action.channel, action.payload, {
          messageId: action.messageId ?? event.id,
          deliver: action.deliver,
        });
        envelopes.set(eventId, sent.envelope);
        envelopes.set(sent.envelope.messageId, sent.envelope);
        break;
      }
      case "transmit": {
        const envelope = envelopes.get(action.envelopeFrom);
        if (envelope === undefined) {
          throw new Error(`transmit references unknown envelope ${action.envelopeFrom}`);
        }
        router.transmit(envelope, {
          deliverySnapshot: action.deliverySnapshot,
          recipientInstanceId: action.recipient,
        });
        break;
      }
      case "confirmDelivery": {
        const delivery = findReferencedDelivery(router, action);
        router.confirmDelivery(delivery.deliveryId, action.reason);
        break;
      }
      case "closeDelivery": {
        const delivery = findReferencedDelivery(router, action);
        router.closeDelivery(delivery.deliveryId, action.reason);
        break;
      }
    }

    await router.flush();
  }

  return Object.freeze({ eventIds: Object.freeze([...order]), deliveries });
}

function findReferencedDelivery(
  router: BroadcastRouter,
  action: ConfirmDeliveryAction | CloseDeliveryAction,
): BroadcastDelivery {
  const matches = router.getDeliveries().filter((delivery) => {
    if (action.deliveryId !== undefined) {
      return delivery.deliveryId === action.deliveryId;
    }
    if (action.messageId !== undefined && delivery.messageId !== action.messageId) {
      return false;
    }
    if (
      action.subscription !== undefined &&
      delivery.subscriptionId !== action.subscription
    ) {
      return false;
    }
    if (
      action.recipient !== undefined &&
      delivery.recipientInstanceId !== action.recipient
    ) {
      return false;
    }
    return true;
  });
  if (matches.length !== 1) {
    throw new Error(
      `delivery reference matched ${matches.length} deliveries; add messageId/subscription/recipient or deliveryId`,
    );
  }
  return matches[0];
}

/**
 * Replays every legal linearisation of a partial order and classifies
 * deliveries by whether they occur in all orders or only some of them.
 */
export async function verifyPartialOrder(
  events: readonly PartialOrderEvent[],
  options: VerifyOptions = {},
): Promise<VerificationReport> {
  const maxOrders = options.maxOrders ?? 2_000;
  const includeOrders = options.includeOrders ?? true;
  const orders = enumerateLegalOrders(events, maxOrders);
  const byId = new Map(events.map((event) => [event.id, event]));

  const executed: ExecutedOrder[] = [];
  for (const order of orders) {
    executed.push(await executeOrder(byId, order));
  }

  const observations = new Map<
    string,
    {
      messageId: string;
      recipientDocument: string;
      subscriptionId: string;
      channelName: string;
      channelGeneration: number;
      sequences: Set<number>;
      occurrenceCount: number;
    }
  >();

  for (const result of executed) {
    for (const observation of result.deliveries.values()) {
      const existing = observations.get(observation.key);
      if (existing === undefined) {
        observations.set(observation.key, {
          messageId: observation.messageId,
          recipientDocument: observation.recipientDocument,
          subscriptionId: observation.subscriptionId,
          channelName: observation.channelName,
          channelGeneration: observation.channelGeneration,
          sequences: new Set(observation.sequences),
          occurrenceCount: 1,
        });
      } else {
        existing.occurrenceCount += 1;
        for (const sequence of observation.sequences) {
          existing.sequences.add(sequence);
        }
        if (observation.channelGeneration > existing.channelGeneration) {
          existing.channelGeneration = observation.channelGeneration;
        }
      }
    }
  }

  const allDeliveries: DeliveryClassification[] = [...observations.entries()]
    .map(([key, observation]) => {
      const classification: "always" | "order-dependent" =
        observation.occurrenceCount === executed.length
          ? "always"
          : "order-dependent";
      return Object.freeze({
        key,
        messageId: observation.messageId,
        recipientDocument: observation.recipientDocument,
        subscriptionId: observation.subscriptionId,
        channelName: observation.channelName,
        channelGeneration: observation.channelGeneration,
        sequences: Object.freeze([...observation.sequences].sort((a, b) => a - b)),
        occurrenceCount: observation.occurrenceCount,
        classification,
      });
    })
    .sort((left, right) => left.key.localeCompare(right.key));

  const always = Object.freeze(
    allDeliveries.filter((delivery) => delivery.classification === "always"),
  );
  const orderDependent = Object.freeze(
    allDeliveries.filter(
      (delivery) => delivery.classification === "order-dependent",
    ),
  );

  const orderResults = includeOrders
    ? executed.map((result) =>
        Object.freeze({
          eventIds: result.eventIds,
          deliveryKeys: Object.freeze(
            [...result.deliveries.keys()].sort(),
          ),
        }),
      )
    : [];

  return Object.freeze({
    eventCount: events.length,
    orderCount: orders.length,
    always,
    orderDependent,
    allDeliveries: Object.freeze(allDeliveries),
    orders: Object.freeze(orderResults),
    must: always,
    may: orderDependent,
  });
}

export const verifyScenario = verifyPartialOrder;
export const verify = verifyPartialOrder;

/** Includes the canonical partition key in diagnostics without exposing internals. */
export function describePartition(input: PartitionInput): string {
  return partitionKey(makePartition(input));
}
