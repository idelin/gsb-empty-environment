import { createHash, createPrivateKey, createPublicKey, generateKeyPairSync, sign as nodeSign, verify as nodeVerify } from "node:crypto";
import { compareDots, formatDot, parseDot, type Dot } from "./dot.ts";

/**
 * Document key admission for an offline-first collaboration host.
 *
 * Two independent entry points share one verdict core:
 *  - KeyAdmissionMerger keeps only the minimum evidence needed while merging
 *    online (events, sealed frontiers, gaps).
 *  - verifyKeyLog recomputes every principal's claimable key handles from
 *    uncompressed raw events.
 *
 * Ordering invariants:
 *  - compareDots is a stable storage order only, never a causal order.
 *  - Causality comes exclusively from each author's contiguous local dot
 *    sequence and the signed `observed` sparse prefix.
 *  - A known gap is an unknown fact ("pending"), never proof that an author
 *    never wrote anything.
 */

// ---------------------------------------------------------------------------
// Rights and event shapes
// ---------------------------------------------------------------------------

export type Right = "grant" | "delegate" | "revoke" | "delete" | "rebuild";

export const ALL_RIGHTS: readonly Right[] = ["grant", "delegate", "revoke", "delete", "rebuild"];

/** Distinct signature permission per event class. */
export type EventKind = "grant" | "delegate" | "revoke" | "delete" | "rebuild";

const KIND_BYTE: Record<EventKind, number> = {
  grant: 1,
  delegate: 2,
  revoke: 3,
  delete: 4,
  rebuild: 5,
};

/** Pluggable signature suite. The default is Ed25519 (pure, no prehash). */
export interface SignatureSuite {
  readonly algorithm: string;
  verify(pk: JsonWebKey | string | Uint8Array, payload: Uint8Array, signature: Uint8Array): boolean;
  sign(sk: JsonWebKey | string | Uint8Array, payload: Uint8Array): Uint8Array;
}

export interface IdentityRecord {
  /** Raw public key: JWK, PEM, SPKI DER, or raw Ed25519 point. */
  readonly publicKey: JsonWebKey | string | Uint8Array;
  readonly rights: readonly Right[];
}

export interface TrustRegistry {
  /** Permanent object id -> owner identity. */
  ownerOf(objectId: string): string;
  readonly identity: (actor: string) => IdentityRecord | undefined;
  /** Optional enumeration of all known principals. */
  readonly actors?: () => readonly string[];
}

export interface SignedEvent {
  /** Author identity, also the actor component of `dot`. */
  readonly author: string;
  /** Permanent object id. Alias document names never appear here. */
  readonly objectId: string;
  /** Encryption generation, uint32. */
  readonly generation: number;
  /** Author's local dot; sequences are contiguous per author. */
  readonly dot: Dot;
  /** Sparse observed prefix: actor -> highest contiguous sequence observed. */
  readonly observed: Readonly<Record<string, bigint>>;
  readonly kind: EventKind;
  readonly payload: EventPayload;
  readonly signature: Uint8Array;
}

export type EventPayload =
  | GrantPayload
  | DelegatePayload
  | RevokePayload
  | DeletePayload
  | RebuildPayload;

export interface GrantPayload {
  readonly subject: string;
  readonly handle: string;
}

export interface DelegatePayload {
  /** Dot of the edge this delegation extends. */
  readonly parent: Dot;
  readonly subject: string;
  readonly handle: string;
}

export interface RevokePayload {
  /** Edge this tombstone kills, together with everything below it. */
  readonly target: Dot;
  /** Generation the target edge lives in, if known to the revoker. */
  readonly targetGeneration?: number;
}

export interface DeletePayload {
  readonly reason?: string;
}

export interface RebuildPayload {
  readonly keyContext: string;
}

// ---------------------------------------------------------------------------
// Canonical byte representation (the persisted event identity)
// ---------------------------------------------------------------------------

class Writer {
  private chunks: number[][] = [];

  byte(value: number): this {
    this.chunks.push([value & 0xff]);
    return this;
  }

  bytes(value: Uint8Array): this {
    this.chunks.push(Array.from(value));
    return this;
  }

  u32(value: number): this {
    if (!Number.isInteger(value) || value < 0 || value > 0xffffffff) throw new Error("u32 overflow");
    this.chunks.push([
      Math.floor(value / 0x1000000) & 0xff,
      Math.floor(value / 0x10000) & 0xff,
      Math.floor(value / 0x100) & 0xff,
      value & 0xff,
    ]);
    return this;
  }

  u64(value: bigint): this {
    if (typeof value !== "bigint" || value < 0n || value > 0xffffffffffffffffn) throw new Error("u64 overflow");
    const out = new Array<number>(8);
    let rest = value;
    for (let index = 7; index >= 0; index -= 1) {
      out[index] = Number(rest & 0xffn);
      rest >>= 8n;
    }
    this.chunks.push(out);
    return this;
  }

  text(value: string): this {
    const encoded = new TextEncoder().encode(value);
    this.u32(encoded.length);
    this.bytes(encoded);
    return this;
  }

  finish(): Uint8Array {
    const total = this.chunks.reduce((sum, chunk) => sum + chunk.length, 0);
    const out = new Uint8Array(total);
    let offset = 0;
    for (const chunk of this.chunks) {
      out.set(chunk, offset);
      offset += chunk.length;
    }
    return out;
  }
}

function writeObserved(writer: Writer, observed: Readonly<Record<string, bigint>>): void {
  const entries = Object.entries(observed).sort(([left], [right]) => (left < right ? -1 : left > right ? 1 : 0));
  writer.u32(entries.length);
  for (const [actor, sequence] of entries) {
    writer.text(actor);
    writer.u64(sequence);
  }
}

/** Canonical bytes the signature covers. The event id is their sha256 hash. */
export function canonicalEventBytes(event: Omit<SignedEvent, "signature">): Uint8Array {
  const writer = new Writer();
  writer.byte(KIND_BYTE[event.kind]);
  writer.text(event.author);
  writer.text(event.objectId);
  writer.u32(event.generation);
  writer.text(event.dot.actor);
  writer.u64(event.dot.sequence);
  writeObserved(writer, event.observed);
  switch (event.kind) {
    case "grant": {
      const payload = event.payload as GrantPayload;
      writer.text(payload.subject);
      writer.text(payload.handle);
      break;
    }
    case "delegate": {
      const payload = event.payload as DelegatePayload;
      writer.text(payload.parent.actor);
      writer.u64(payload.parent.sequence);
      writer.text(payload.subject);
      writer.text(payload.handle);
      break;
    }
    case "revoke": {
      const payload = event.payload as RevokePayload;
      writer.text(payload.target.actor);
      writer.u64(payload.target.sequence);
      if (payload.targetGeneration === undefined) writer.byte(0);
      else writer.byte(1).u32(payload.targetGeneration);
      break;
    }
    case "delete": {
      const payload = event.payload as DeletePayload;
      writer.text(payload.reason ?? "");
      break;
    }
    case "rebuild": {
      const payload = event.payload as RebuildPayload;
      writer.text(payload.keyContext);
      break;
    }
  }
  return writer.finish();
}

/** Persistent content id: sha256 hex of the canonical signed bytes. */
export function eventId(event: Omit<SignedEvent, "signature">): string {
  return createHash("sha256").update(canonicalEventBytes(event)).digest("hex");
}

// ---------------------------------------------------------------------------
// Default Ed25519 suite
// ---------------------------------------------------------------------------

function toKeyObject(key: JsonWebKey | string | Uint8Array) {
  if (typeof key === "string") return createPublicKey(key);
  if (key instanceof Uint8Array) return createPublicKey({ key: Buffer.from(key), format: "der", type: "spki" });
  return createPublicKey({ key, format: "jwk" });
}

function toPrivateKeyObject(key: JsonWebKey | string | Uint8Array) {
  if (typeof key === "string") return createPrivateKey(key);
  if (key instanceof Uint8Array) return createPrivateKey({ key: Buffer.from(key), format: "der", type: "pkcs8" });
  return createPrivateKey({ key, format: "jwk" });
}

export const ed25519Suite: SignatureSuite = {
  algorithm: "Ed25519",
  verify(publicKey, payload, signature) {
    try {
      return nodeVerify(null, Buffer.from(payload), toKeyObject(publicKey), Buffer.from(signature));
    } catch {
      return false;
    }
  },
  sign(privateKey, payload) {
    return new Uint8Array(nodeSign(null, Buffer.from(payload), toPrivateKeyObject(privateKey)));
  },
};

export interface KeyPair {
  readonly publicKey: JsonWebKey;
  readonly privateKey: JsonWebKey;
}

/** Test/host helper for minting identity keys. */
export function generateIdentityKeyPair(): KeyPair {
  const { publicKey, privateKey } = generateKeyPairSync("ed25519");
  return {
    publicKey: publicKey.export({ format: "jwk" }) as JsonWebKey,
    privateKey: privateKey.export({ format: "jwk" }) as JsonWebKey,
  };
}

/** Sign a prepared event body. */
export function signEvent(
  body: Omit<SignedEvent, "signature">,
  privateKey: JsonWebKey | string | Uint8Array,
  suite: SignatureSuite = ed25519Suite,
): SignedEvent {
  return { ...body, signature: suite.sign(privateKey, canonicalEventBytes(body)) };
}

// ---------------------------------------------------------------------------
// Sealed compressed frontiers
// ---------------------------------------------------------------------------

/**
 * Compressed replica summary: per actor, the highest sequence the attesting
 * replica has evidence for. A receiver learns that a suffix exists even when
 * the events themselves are missing, so gaps can never be mistaken for
 * "author never wrote".
 */
export interface FrontierSummary {
  readonly objectId: string;
  readonly frontier: Readonly<Record<string, bigint>>;
  readonly attester: string;
  readonly signature: Uint8Array;
}

export function canonicalSummaryBytes(summary: Omit<FrontierSummary, "signature">): Uint8Array {
  const writer = new Writer();
  writer.text("FRONTIER-V1");
  writer.text(summary.objectId);
  writeObserved(writer, summary.frontier);
  writer.text(summary.attester);
  return writer.finish();
}

export function signSummary(
  summary: Omit<FrontierSummary, "signature">,
  privateKey: JsonWebKey | string | Uint8Array,
  suite: SignatureSuite = ed25519Suite,
): FrontierSummary {
  return { ...summary, signature: suite.sign(privateKey, canonicalSummaryBytes(summary)) };
}

// ---------------------------------------------------------------------------
// Validation and ingestion results
// ---------------------------------------------------------------------------

export type RejectReason =
  | "bad-shape"
  | "bad-dot"
  | "bad-observed"
  | "bad-generation"
  | "unknown-author"
  | "bad-signature"
  | "missing-right"
  | "not-owner"
  | "dot-misattribution"
  | "target-generation-mismatch"
  | "unknown-summary-signer";

export interface IngestResult {
  readonly status: "stored" | "duplicate" | "rejected";
  readonly reason?: RejectReason;
  readonly id?: string;
}

const U32_MAX = 0xffffffff;
const U64_MAX = 0xffffffffffffffffn;

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null;
}

function dotFromUnknown(value: unknown): Dot | undefined {
  if (!isRecord(value) || typeof value.actor !== "string" || typeof value.sequence !== "bigint") return undefined;
  try {
    return parseDot(formatDot(value as Dot));
  } catch {
    return undefined;
  }
}

function payloadOf(kind: EventKind, value: unknown): EventPayload | undefined {
  if (!isRecord(value)) return undefined;
  if (kind === "grant" || kind === "delegate") {
    if (typeof value.subject !== "string" || typeof value.handle !== "string") return undefined;
    if (kind === "grant") return { subject: value.subject, handle: value.handle };
    const parent = dotFromUnknown(value.parent);
    if (!parent) return undefined;
    return { parent, subject: value.subject, handle: value.handle };
  }
  if (kind === "revoke") {
    const target = dotFromUnknown(value.target);
    if (!target) return undefined;
    const targetGeneration = value.targetGeneration;
    if (
      targetGeneration !== undefined &&
      (!Number.isInteger(targetGeneration) || targetGeneration < 0 || targetGeneration > U32_MAX)
    ) {
      return undefined;
    }
    return targetGeneration === undefined ? { target } : { target, targetGeneration };
  }
  if (kind === "delete") {
    if (value.reason !== undefined && typeof value.reason !== "string") return undefined;
    return value.reason === undefined ? {} : { reason: value.reason };
  }
  if (typeof value.keyContext !== "string") return undefined;
  return { keyContext: value.keyContext };
}

function validateShape(input: unknown): { event?: SignedEvent; reason?: RejectReason } {
  if (!isRecord(input)) return { reason: "bad-shape" };
  const kind = input.kind;
  if (typeof kind !== "string" || !(kind in KIND_BYTE)) return { reason: "bad-shape" };
  if (typeof input.author !== "string" || typeof input.objectId !== "string") return { reason: "bad-shape" };
  if (!Number.isInteger(input.generation) || input.generation < 0 || input.generation > U32_MAX) {
    return { reason: "bad-generation" };
  }
  const dot = dotFromUnknown(input.dot);
  if (!dot) return { reason: "bad-dot" };
  if (!isRecord(input.observed)) return { reason: "bad-observed" };
  const observed: Record<string, bigint> = {};
  for (const [actor, sequence] of Object.entries(input.observed)) {
    if (typeof sequence !== "bigint" || sequence < 0n || sequence > U64_MAX) return { reason: "bad-observed" };
    try {
      parseDot(`${sequence.toString()}@${actor}`);
    } catch {
      return { reason: "bad-observed" };
    }
    observed[actor] = sequence;
  }
  if (!(input.signature instanceof Uint8Array)) return { reason: "bad-shape" };
  const payload = payloadOf(kind as EventKind, input.payload);
  if (!payload) return { reason: "bad-shape" };
  return {
    event: {
      author: input.author,
      objectId: input.objectId,
      generation: input.generation,
      dot,
      observed,
      kind: kind as EventKind,
      payload,
      signature: input.signature,
    },
  };
}

/**
 * Full admission-time validation. Shape, signature and permission checks are
 * local; causal relationships (parent/target reachability) are evaluated by
 * the verdict core instead of the storage layer.
 */
export function validateEvent(
  input: unknown,
  registry: TrustRegistry,
  suite: SignatureSuite = ed25519Suite,
): { event?: SignedEvent; reason?: RejectReason } {
  const shaped = validateShape(input);
  if (!shaped.event) return shaped;
  const event = shaped.event;
  if (event.dot.actor !== event.author) return { reason: "dot-misattribution" };
  const identity = registry.identity(event.author);
  if (!identity) return { reason: "unknown-author" };
  if (!suite.verify(identity.publicKey, canonicalEventBytes(event), event.signature)) {
    return { reason: "bad-signature" };
  }
  if (event.kind === "rebuild" && registry.ownerOf(event.objectId) !== event.author) {
    return { reason: "not-owner" };
  }
  if (!identity.rights.includes(event.kind)) return { reason: "missing-right" };
  if (event.kind === "revoke") {
    const payload = event.payload as RevokePayload;
    if (
      payload.targetGeneration !== undefined &&
      payload.targetGeneration !== event.generation
    ) {
      return { reason: "target-generation-mismatch" };
    }
  }
  return { event };
}

// ---------------------------------------------------------------------------
// Evidence model
// ---------------------------------------------------------------------------

/**
 * Minimum retained evidence. The online merger keeps exactly these facts;
 * the offline verifier reconstructs the same shape from raw events.
 */
export interface AdmissionEvidence {
  readonly objectId: string;
  /** Keyed by formatDot(dot); equivocating dots retain every fork. */
  readonly events: ReadonlyMap<string, EvRecord[]>;
  /** Highest sequence attested (by own dots or sealed frontiers) per actor. */
  readonly frontier: ReadonlyMap<string, bigint>;
  /** Dots observed with two or more distinct signed contents. */
  readonly equivocations: ReadonlySet<string>;
}

export interface EvRecord {
  readonly dot: Dot;
  readonly id: string;
  readonly author: string;
  readonly generation: number;
  readonly kind: EventKind;
  readonly payload: EventPayload;
  readonly observed: Readonly<Record<string, bigint>>;
}

function dotKey(dot: Dot): string {
  return formatDot(dot);
}

function allRecords(evidence: AdmissionEvidence): EvRecord[] {
  return [...evidence.events.values()].flat();
}

function firstRecord(evidence: AdmissionEvidence, dot: Dot): EvRecord | undefined {
  return evidence.events.get(formatDot(dot))?.[0];
}

function toRecord(event: SignedEvent): EvRecord {
  return {
    dot: event.dot,
    id: eventId(event),
    author: event.author,
    generation: event.generation,
    kind: event.kind,
    payload: event.payload,
    observed: { ...event.observed },
  };
}

/**
 * Causal-past test using only the author's contiguous dot sequence and the
 * signed sparse observed prefix. compareDots storage order is never consulted.
 */
function causallyBefore(candidate: EvRecord, event: EvRecord): boolean {
  if (candidate.dot.actor === event.dot.actor && candidate.dot.sequence <= event.dot.sequence) return true;
  const observed = event.observed[candidate.dot.actor];
  return observed !== undefined && candidate.dot.sequence <= observed;
}

/**
 * Known gaps per actor: sequences below the attested frontier whose dots are
 * absent. A gap is "unknown evidence", retained for later judgement.
 */
export function gapsOf(evidence: AdmissionEvidence): ReadonlyMap<string, bigint> {
  const gaps = new Map<string, bigint>();
  const present = new Map<string, bigint>();
  for (const records of evidence.events.values()) {
    for (const record of records) present.set(record.dot.actor, (present.get(record.dot.actor) ?? 0n) + 1n);
  }
  for (const [actor, frontier] of evidence.frontier) {
    // Sequences are contiguous 1..frontier; count arithmetically so a frontier
    // at 2^64-1 never requires iterating over every sequence.
    const missing = frontier - (present.get(actor) ?? 0n);
    if (missing > 0n) gaps.set(actor, missing);
  }
  return gaps;
}

// ---------------------------------------------------------------------------
// Online entry: minimum-evidence merger
// ---------------------------------------------------------------------------

export class KeyAdmissionMerger {
  private readonly store = new Map<string, EvRecord[]>();
  private readonly frontier = new Map<string, bigint>();
  private readonly equivocations = new Set<string>();
  readonly objectId: string;
  private readonly registry: TrustRegistry;
  private readonly suite: SignatureSuite;

  constructor(
    objectId: string,
    registry: TrustRegistry,
    suite: SignatureSuite = ed25519Suite,
  ) {
    this.objectId = objectId;
    this.registry = registry;
    this.suite = suite;
  }

  /** Ingest one event, in any legal delivery order. */
  ingest(input: unknown): IngestResult {
    const checked = validateEvent(input, this.registry, this.suite);
    if (!checked.event) return { status: "rejected", reason: checked.reason };
    const event = checked.event;
    if (event.objectId !== this.objectId) return { status: "rejected", reason: "bad-shape" };
    const key = dotKey(event.dot);
    const id = eventId(event);
    const existing = this.store.get(key);
    if (existing) {
      if (existing.some((record) => record.id === id)) return { status: "duplicate", id };
      // Same dot, different signed content: quarantine this author's claims.
      this.equivocations.add(key);
      existing.push(toRecord(event));
      return { status: "stored", id };
    }
    this.store.set(key, [toRecord(event)]);
    this.raiseFrontier(event.dot.actor, event.dot.sequence);
    for (const [actor, sequence] of Object.entries(event.observed)) this.raiseFrontier(actor, sequence);
    return { status: "stored", id };
  }

  /** Ingest a compressed summary plus whatever suffix events accompany it. */
  ingestSummary(summary: FrontierSummary): IngestResult {
    if (summary.objectId !== this.objectId) return { status: "rejected", reason: "bad-shape" };
    const identity = this.registry.identity(summary.attester);
    if (!identity) return { status: "rejected", reason: "unknown-summary-signer" };
    if (!this.suite.verify(identity.publicKey, canonicalSummaryBytes(summary), summary.signature)) {
      return { status: "rejected", reason: "bad-signature" };
    }
    for (const [actor, sequence] of Object.entries(summary.frontier)) this.raiseFrontier(actor, sequence);
    return { status: "stored" };
  }

  /** Current three-valued admission view, computed from retained evidence. */
  view(): AdmissionView {
    return computeView(this.snapshot(), this.registry);
  }

  snapshot(): AdmissionEvidence {
    return {
      objectId: this.objectId,
      events: new Map(this.store),
      frontier: new Map(this.frontier),
      equivocations: new Set(this.equivocations),
    };
  }

  /**
   * Dots of revoke records that are safe to discard without changing any
   * current or future verdict.
   */
  discardableRevocations(): string[] {
    return discardableRevocationDots(this.snapshot());
  }

  private raiseFrontier(actor: string, sequence: bigint): void {
    const current = this.frontier.get(actor);
    if (current === undefined || sequence > current) this.frontier.set(actor, sequence);
  }
}

// ---------------------------------------------------------------------------
// Offline entry: recompute from uncompressed raw events
// ---------------------------------------------------------------------------

/**
 * Independent offline verifier. It receives full, uncompressed events and
 * rebuilds the evidence graph itself before applying the same verdict core.
 */
export function verifyKeyLog(
  objectId: string,
  events: readonly unknown[],
  registry: TrustRegistry,
  suite: SignatureSuite = ed25519Suite,
): { view: AdmissionView; rejected: ReadonlyArray<{ reason: RejectReason }> } {
  const store = new Map<string, EvRecord[]>();
  const frontier = new Map<string, bigint>();
  const equivocations = new Set<string>();
  const rejected: { reason: RejectReason }[] = [];

  for (const input of events) {
    const checked = validateEvent(input, registry, suite);
    if (!checked.event) {
      if (checked.reason) rejected.push({ reason: checked.reason });
      continue;
    }
    const event = checked.event;
    if (event.objectId !== objectId) {
      rejected.push({ reason: "bad-shape" });
      continue;
    }
    const key = dotKey(event.dot);
    const existing = store.get(key);
    if (existing) {
      if (!existing.some((record) => record.id === eventId(event))) {
        equivocations.add(key);
        existing.push(toRecord(event));
      }
      continue;
    }
    store.set(key, [toRecord(event)]);
    raise(frontier, event.dot.actor, event.dot.sequence);
    for (const [actor, sequence] of Object.entries(event.observed)) {
      raise(frontier, actor, sequence);
    }
  }

  const view = computeView({ objectId, events: store, frontier, equivocations }, registry);
  return { view, rejected };
}

function raise(map: Map<string, bigint>, actor: string, sequence: bigint): void {
  if (sequence > (map.get(actor) ?? 0n)) map.set(actor, sequence);
}

// ---------------------------------------------------------------------------
// Verdict core (shared by both entries)
// ---------------------------------------------------------------------------

export type Verdict = "admitted" | "denied" | "pending";

export interface KeyClaim {
  readonly subject: string;
  readonly handle: string;
  readonly generation: number;
  readonly edge: Dot;
}

export interface DecidedClaim extends KeyClaim {
  readonly verdict: Verdict;
  readonly reasons: ReadonlyArray<string>;
}

export interface GenerationStatus {
  readonly generation: number;
  /** pending | conflicted | enabled | closed */
  readonly state: "pending" | "conflicted" | "enabled" | "closed";
  readonly reasons: ReadonlyArray<string>;
}

export interface AdmissionView {
  readonly objectId: string;
  readonly complete: boolean;
  readonly generations: GenerationStatus[];
  readonly claims: DecidedClaim[];
  readonly admitted: ReadonlyArray<KeyClaim>;
  readonly equivocatingAuthors: readonly string[];
}

type EdgeState = "admitted" | "denied" | "pending";

interface EdgeInfo {
  record: EvRecord;
  kind: "grant" | "delegate";
  parent?: Dot;
  subject: string;
  handle: string;
}

/**
 * Actors whose unseen events could negate a claim. The registry contributes
 * actors that currently have no stored event at all: an unknown suffix from a
 * principal holding revoke/delete/rebuild rights must keep verdicts pending.
 */
function collectNegatingActors(evidence: AdmissionEvidence, registry?: TrustRegistry): Set<string> {
  const actors = new Set<string>();
  for (const record of allRecords(evidence)) {
    if (record.kind === "revoke" || record.kind === "delete" || record.kind === "rebuild") {
      actors.add(record.dot.actor);
    }
  }
  if (registry) {
    const known = registry.actors ? registry.actors() : evidence.frontier.keys();
    for (const actor of known) {
      const identity = registry.identity(actor);
      if (identity && identity.rights.some((right) => right === "revoke" || right === "delete" || right === "rebuild")) {
        actors.add(actor);
      }
    }
  }
  return actors;
}

/**
 * Order-independent three-valued verdict. Every negative fact that is fully
 * present yields a definitive denial; any missing fact that could change the
 * outcome yields "pending". Nothing is ever admitted on missing evidence.
 */
export function computeView(evidence: AdmissionEvidence, registry?: TrustRegistry): AdmissionView {
  const records = allRecords(evidence);
  const gaps = gapsOf(evidence);
  const equivocatingAuthors = new Set<string>();
  for (const key of evidence.equivocations) equivocatingAuthors.add(parseDot(key).actor);
  const negatingActors = collectNegatingActors(evidence, registry);

  const generationStatus = computeGenerations(evidence, records, gaps, negatingActors);
  const edges = collectEdges(records);
  const tombstones = records.filter((record) => record.kind === "revoke");

  const edgeState = new Map<string, EdgeState>();
  const edgeReasons = new Map<string, string[]>();
  const directlyRevoked = new Set<string>();
  for (const tombstone of tombstones) {
    const payload = tombstone.payload as RevokePayload;
    const targets = evidence.events.get(formatDot(payload.target));
    if (targets) for (const target of targets) directlyRevoked.add(edgeKey(target));
  }

  const evaluate = (info: EdgeInfo): EdgeState => {
    const dot = info.record.dot;
    const key = edgeKey(info.record);
    const cached = edgeState.get(key);
    if (cached) return cached;
    edgeState.set(key, "pending"); // guard against pathological cycles
    const reasons: string[] = [];
    let state: EdgeState = "admitted";

    const generation = generationStatus.get(info.record.generation);
    if (!generation || generation.state === "pending") {
      state = "pending";
      reasons.push("generation-not-enabled");
    } else if (generation.state === "closed") {
      state = "denied";
      reasons.push("generation-deleted");
    } else if (generation.state === "conflicted") {
      state = "denied";
      reasons.push("generation-conflicted");
    }

    if (equivocatingAuthors.has(info.record.author)) {
      state = "denied";
      reasons.push("author-equivocation");
    }

    if (info.kind === "delegate") {
      const parentRecords = evidence.events.get(formatDot(info.parent!));
      if (!parentRecords || parentRecords.length === 0) {
        state = "pending";
        reasons.push("parent-pending");
      } else {
        const parentInfos = parentRecords
          .map((record) => edges.get(edgeKey(record)))
          .filter((candidate): candidate is EdgeInfo => candidate !== undefined);
        if (parentInfos.length === 0) {
          state = "pending";
          reasons.push("parent-pending");
        } else {
          const parentStates = parentInfos.map((parentInfo) => evaluate(parentInfo));
          const parentExplicitlyDead = parentInfos.some((parentInfo) => {
            if (directlyRevoked.has(edgeKey(parentInfo.record))) return true;
            const parentGeneration = generationStatus.get(parentInfo.record.generation);
            return parentGeneration?.state === "closed" || parentGeneration?.state === "conflicted";
          });
          if (parentStates.includes("denied") && parentExplicitlyDead) {
            state = "denied";
            reasons.push("parent-dead");
          } else if (parentStates.includes("denied")) {
            state = "pending";
            reasons.push("parent-pending");
          } else if (parentStates.includes("pending") && state !== "denied") {
            state = "pending";
            reasons.push("parent-pending");
          }
        }
      }
    }

    // Revocation semantics (causal, not storage order):
    //  - a tombstone kills its target edge only when the edge is in the
    //    tombstone's causal past (same-author sequence or observed prefix);
    //  - the whole delegation subtree below a killed edge dies with it;
    //  - a tombstone whose causal reach cannot be established stays pending,
    //    never denied and never ignored.
    for (const tombstone of tombstones) {
      const payload = tombstone.payload as RevokePayload;
      if (payload.targetGeneration !== undefined && payload.targetGeneration !== info.record.generation) continue;
      const targetRecords = evidence.events.get(formatDot(payload.target));
      const targetReachable = targetRecords !== undefined && targetRecords.length > 0;
      if (sameDot(payload.target, dot)) {
        // The tombstone is explicitly addressed at this dot, so the edge's
        // existence is all that is required; arrival order is irrelevant.
        state = "denied";
        reasons.push(`revoked:${formatDot(tombstone.dot)}`);
        continue;
      }
      const below = edgeIsBelow(info, payload.target, evidence);
      if (below === "unknown") {
        if (state !== "denied") state = "pending";
        reasons.push(`revoke-chain-missing:${formatDot(tombstone.dot)}`);
      } else if (below === "below") {
        if (!targetReachable) {
          if (state !== "denied") state = "pending";
          reasons.push(`revoke-target-missing:${formatDot(tombstone.dot)}`);
        } else if (!causallyBefore(info.record, tombstone)) {
          // Concurrent fork: neither event sees the other, so the tombstone
          // cannot establish that this branch was dead yet. Stay pending.
          if (state !== "denied") state = "pending";
          reasons.push(`revoke-concurrent:${formatDot(tombstone.dot)}`);
        } else if (!targetRecords!.some((target) => causallyBefore(target, tombstone))) {
          if (state !== "denied") state = "pending";
          reasons.push(`revoke-target-concurrent:${formatDot(tombstone.dot)}`);
        } else {
          state = "denied";
          reasons.push(`revoked-subtree:${formatDot(tombstone.dot)}`);
        }
      }
    }

    // Gap gate: only missing events of actors able to negate this claim, or
    // actors on its causal chain, can hold it pending. Gaps of unrelated
    // pure-delegation authors do not block it.
    const relevantGap = [...gaps.keys()].some((actor) =>
      actorCanNegate(actor, info, evidence, equivocatingAuthors, negatingActors),
    );
    if (relevantGap && state !== "denied") {
      state = "pending";
      reasons.push("evidence-gap");
    }

    edgeState.set(key, state);
    edgeReasons.set(key, reasons);
    return state;
  };

  const claims: DecidedClaim[] = [];
  for (const [key, info] of edges) {
    const verdict = evaluate(info);
    claims.push({
      subject: info.subject,
      handle: info.handle,
      generation: info.record.generation,
      edge: info.record.dot,
      verdict,
      reasons: edgeReasons.get(key) ?? [],
    });
  }
  claims.sort((left, right) => compareDots(left.edge, right.edge));

  const generations = [...generationStatus.values()].sort((left, right) => left.generation - right.generation);
  return {
    objectId: evidence.objectId,
    complete: gaps.size === 0 && evidence.equivocations.size === 0,
    generations,
    claims,
    admitted: claims.filter((claim) => claim.verdict === "admitted"),
    equivocatingAuthors: [...equivocatingAuthors].sort(),
  };
}

function computeGenerations(
  evidence: AdmissionEvidence,
  records: EvRecord[],
  gaps: ReadonlyMap<string, bigint>,
  negatingActors: ReadonlySet<string>,
): Map<number, GenerationStatus> {
  const result = new Map<number, GenerationStatus>();

  const evaluateGeneration = (generation: number): GenerationStatus => {
    const cached = result.get(generation);
    if (cached) return cached;
    const builds = records.filter((record) => record.kind === "rebuild" && record.generation === generation);
    const deletes = records.filter((record) => record.kind === "delete" && record.generation === generation);

    let state: GenerationStatus["state"];
    const reasons: string[] = [];
    if (builds.length > 1) {
      state = "conflicted";
      reasons.push("duplicate-rebuild");
    } else if (builds.length === 0) {
      state = "pending";
      reasons.push("rebuild-missing");
    } else {
      const build = builds[0];
      if (generation > 0) {
        const parent = evaluateGeneration(generation - 1);
        if (parent.state === "pending") {
          state = "pending";
          reasons.push("parent-generation-pending");
        } else if (parent.state === "conflicted") {
          state = "conflicted";
          reasons.push("parent-generation-conflicted");
        } else {
          state = "enabled";
        }
      } else {
        state = "enabled";
      }
      if (state === "enabled") {
        const dangerousGap = [...gaps.keys()].some((actor) => negatingActors.has(actor));
        if (dangerousGap) {
          state = "pending";
          reasons.push("negating-actor-prefix-gap");
        }
      }
    }
    if (state === "enabled" && deletes.length > 0) {
      state = "closed";
      reasons.push(...deletes.map((record) => `deleted:${formatDot(record.dot)}`));
    }
    const status: GenerationStatus = { generation, state, reasons };
    result.set(generation, status);
    return status;
  };

  const touched = new Set<number>();
  for (const record of records) touched.add(record.generation);
  for (const generation of touched) evaluateGeneration(generation);
  return result;
}

function collectEdges(records: EvRecord[]): Map<string, EdgeInfo> {
  const edges = new Map<string, EdgeInfo>();
  for (const record of records) {
    if (record.kind === "grant") {
      const payload = record.payload as GrantPayload;
      edges.set(edgeKey(record), {
        record,
        kind: "grant",
        subject: payload.subject,
        handle: payload.handle,
      });
    } else if (record.kind === "delegate") {
      const payload = record.payload as DelegatePayload;
      edges.set(edgeKey(record), {
        record,
        kind: "delegate",
        parent: payload.parent,
        subject: payload.subject,
        handle: payload.handle,
      });
    }
  }
  return edges;
}

function edgeKey(record: EvRecord): string {
  return `${formatDot(record.dot)}#${record.id}`;
}

function sameDot(left: Dot, right: Dot): boolean {
  return left.actor === right.actor && left.sequence === right.sequence;
}

/**
 * Delegation-subtree check. Returns:
 *  - "below" when the ancestor sits on a present parent chain,
 *  - "unknown" when a parent dot on the walk is not currently evidenced,
 *  - "unrelated" when the chain terminates without the ancestor.
 */
function edgeIsBelow(edge: EdgeInfo, ancestor: Dot, evidence: AdmissionEvidence): "below" | "unknown" | "unrelated" {
  let current: EdgeInfo | undefined = edge;
  const seen = new Set<string>();
  while (current && current.kind === "delegate") {
    const key = formatDot(current.record.dot);
    if (seen.has(key)) return "unrelated";
    seen.add(key);
    if (sameDot(current.parent!, ancestor)) return "below";
    const parentEdge = firstRecord(evidence, current.parent!);
    if (!parentEdge) return "unknown";
    current = edgeFromRecord(parentEdge);
  }
  return "unrelated";
}

function edgeFromRecord(record: EvRecord): EdgeInfo | undefined {
  if (record.kind === "grant") {
    const payload = record.payload as GrantPayload;
    return { record, kind: "grant", subject: payload.subject, handle: payload.handle };
  }
  if (record.kind === "delegate") {
    const payload = record.payload as DelegatePayload;
    return { record, kind: "delegate", parent: payload.parent, subject: payload.subject, handle: payload.handle };
  }
  return undefined;
}

function actorCanNegate(
  actor: string,
  info: EdgeInfo,
  evidence: AdmissionEvidence,
  equivocatingAuthors: ReadonlySet<string>,
  negatingActors: ReadonlySet<string>,
): boolean {
  if (equivocatingAuthors.has(actor)) return true;
  if (negatingActors.has(actor)) return true;
  const canIssueNegative = allRecords(evidence).some(
    (record) => record.dot.actor === actor && (record.kind === "revoke" || record.kind === "delete" || record.kind === "rebuild"),
  );
  if (canIssueNegative) return true;
  // Actors on the edge's own delegation chain can break the chain.
  let current: EdgeInfo | undefined = info;
  while (current && current.kind === "delegate") {
    if (current.parent!.actor === actor) return true;
    const ancestorEdge = firstRecord(evidence, current.parent!);
    current = ancestorEdge ? edgeFromRecord(ancestorEdge) : undefined;
  }
  if (info.record.dot.actor === actor) return true;
  return false;
}

// ---------------------------------------------------------------------------
// Revocation garbage collection
// ---------------------------------------------------------------------------

/**
 * Exact condition for discarding a revoke record.
 *
 * A tombstone is discardable only when the generation it protects is already
 * closed by a retained delete: a closed generation can never be re-enabled,
 * and the delete itself is the durable kill. In particular, revokes against an
 * open generation are never discardable, even on a replica that may never
 * come online again, because nothing else stands between an old envelope and
 * a mistaken local admission.
 */
export function canDiscardRevoke(revokeDot: Dot, evidence: AdmissionEvidence): boolean {
  const record = firstRecord(evidence, revokeDot);
  if (!record || record.kind !== "revoke") return false;
  const deletePresent = allRecords(evidence).some(
    (candidate) => candidate.kind === "delete" && candidate.generation === record.generation,
  );
  if (!deletePresent) return false;
  // The closed generation can never reopen: the retained delete is the durable
  // kill, so hidden lifecycle events behind a gap cannot change any verdict.
  return true;
}

export function discardableRevocationDots(evidence: AdmissionEvidence): string[] {
  return allRecords(evidence)
    .filter((record) => record.kind === "revoke" && canDiscardRevoke(record.dot, evidence))
    .map((record) => formatDot(record.dot))
    .sort(compareDots);
}

// ---------------------------------------------------------------------------
// JSON helpers for persisted bigint dots/frontiers
// ---------------------------------------------------------------------------

export function stringifyAdmissionData(value: unknown): string {
  return JSON.stringify(value, (_key, nested) =>
    typeof nested === "bigint" ? { __bigint__: nested.toString() } : nested,
  );
}

export function parseAdmissionData(text: string): unknown {
  return JSON.parse(text, (_key, nested) =>
    isRecord(nested) && typeof nested.__bigint__ === "string" ? BigInt(nested.__bigint__) : nested,
  );
}
