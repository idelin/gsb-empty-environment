import { createHmac } from "node:crypto";
import { parseDot, formatDot, type Dot } from "../dot.ts";
import {
  concat,
  u32,
  uBig,
  lpString,
  tag,
  encodeObserved,
  timingSafeEqual,
  toHex,
  eventId,
} from "./bytes.ts";

/**
 * The five signature permissions. Each key event must be signed under exactly
 * one permission; a signature made for one kind is never valid for another
 * because the kind is part of the signed, domain-tagged payload.
 */
export type EventKind = "rebuild" | "delete" | "grant" | "delegate" | "revoke";

export const EVENT_KINDS: readonly EventKind[] = [
  "rebuild",
  "delete",
  "delegate",
  "revoke",
  "grant",
];

/** A key handle is meaningful only within a single object generation. */
export interface KeyHandle {
  objectId: string;
  generation: bigint;
  /** Opaque identifier of the wrapped data key, e.g. a KMS resource name. */
  handle: string;
}

interface EventBase {
  /** Signing author identity (an actor name). */
  author: string;
  /** Permanent object identifier. Display names/aliases never appear here. */
  objectId: string;
  /** Encryption generation the event belongs to / acts upon. */
  generation: bigint;
  /** This event's local dot (actor, per-actor monotonic sequence). */
  dot: Dot;
  /** Sparse version vector: causal predecessors the author had observed. */
  observed: readonly Dot[];
}

/** Owner-only. Enables a generation (genesis or a rebuild after delete). */
export interface RebuildEvent extends EventBase {
  kind: "rebuild";
  /** Handle that becomes claimable in the newly enabled generation. */
  handle: string;
}

/** Owner-only. Closes one generation; it cannot touch any other generation. */
export interface DeleteEvent extends EventBase {
  kind: "delete";
}

/** Authorizes `subject` to claim `handle` inside the event's generation. */
export interface GrantEvent extends EventBase {
  kind: "grant";
  subject: string;
  handle: string;
}

/**
 * Extends the author's own reachable authority to `subject`, capped at
 * `maxDepth` additional hops, for the event's generation.
 */
export interface DelegateEvent extends EventBase {
  kind: "delegate";
  subject: string;
  maxDepth: number;
}

/** Removes an edge identified by the dot of its creating event. */
export interface RevokeEvent extends EventBase {
  kind: "revoke";
  /** Dot (rendered via the persistent ID API) of the grant/delegate edge. */
  targetDot: Dot;
}

export type KeyEvent =
  | RebuildEvent
  | DeleteEvent
  | GrantEvent
  | DelegateEvent
  | RevokeEvent;

/** A signed envelope as it travels between replicas. */
export interface SignedEvent {
  event: KeyEvent;
  /** Raw signature bytes verifiable against `event` under `event.author`. */
  signature: Uint8Array;
}

/**
 * Host-supplied signature check. A real host implements this with asymmetric
 * signatures; the engine itself never assumes a concrete cryptosystem. The
 * returned promise resolves false on any invalid/forged signature.
 */
export type Verifier = (event: KeyEvent, signature: Uint8Array) => Promise<boolean> | boolean;

/** Deterministic canonical bytes for the signed payload (excludes signature). */
export function canonicalBytes(event: KeyEvent): Uint8Array {
  const head: number[] = [];
  tag(head, "keylog-event/v1");
  lpString(head, event.kind);
  lpString(head, event.author);
  lpString(head, event.objectId);
  uBig(head, event.generation);
  lpString(head, eventId(event.dot));
  encodeObserved(head, event.observed);

  const body: number[] = [];
  switch (event.kind) {
    case "rebuild":
      tag(body, "rebuild");
      lpString(body, event.handle);
      break;
    case "delete":
      tag(body, "delete");
      break;
    case "grant":
      tag(body, "grant");
      lpString(body, event.subject);
      lpString(body, event.handle);
      break;
    case "delegate":
      tag(body, "delegate");
      lpString(body, event.subject);
      u32(body, event.maxDepth);
      break;
    case "revoke":
      tag(body, "revoke");
      lpString(body, formatDot(event.targetDot));
      break;
  }
  return concat([head, body]);
}

/**
 * The identity used for dedup and equivocation is the persistent event ID
 * (the dot). Two envelopes with the same ID but different canonical bytes are
 * an equivocation by that author.
 */
export function signedEventId(envelope: SignedEvent): string {
  return eventId(envelope.event.dot);
}

/** Reference equality for dots by their persistent textual representation. */
export function sameDot(left: Dot, right: Dot): boolean {
  return left.actor === right.actor && left.sequence === right.sequence;
}

/** Render a dot the same way it is stored on the wire and in summaries. */
export function dotId(dot: Dot): string {
  return formatDot(dot);
}

export function parseEventId(id: string): Dot {
  return parseDot(id);
}

/**
 * Reference symmetric-key signer used by tests and by hosts that authenticate
 * replicas with per-actor MAC keys. Each actor has an independent secret, and
 * the MAC covers the domain-tagged canonical bytes including the kind, so a
 * MAC cannot be moved between permission kinds.
 */
export class HmacSigner {
  readonly #keys: ReadonlyMap<string, Uint8Array>;

  constructor(keys: ReadonlyMap<string, Uint8Array>) {
    this.#keys = keys;
  }

  sign(event: KeyEvent): SignedEvent {
    const key = this.#keys.get(event.author);
    if (!key) throw new Error(`no signing key for actor ${event.author}`);
    const mac = createHmac("sha256", key).update(canonicalBytes(event)).digest();
    return { event, signature: new Uint8Array(mac) };
  }

  verify: Verifier = (event, signature) => {
    const key = this.#keys.get(event.author);
    if (!key) return false;
    const expected = createHmac("sha256", key).update(canonicalBytes(event)).digest();
    return timingSafeEqual(signature, new Uint8Array(expected));
  };
}

/** Hex helper exported so summaries can carry compact content fingerprints. */
export { toHex };
export { fromHex } from "./bytes.ts";
