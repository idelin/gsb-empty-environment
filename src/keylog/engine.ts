import { compareDots, formatDot, parseDot, type Dot } from "../dot.ts";
import { createHash } from "node:crypto";
import {
  canonicalBytes,
  dotId,
  toHex,
  type DelegateEvent,
  type DeleteEvent,
  type GrantEvent,
  type KeyEvent,
  type KeyHandle,
  type RebuildEvent,
  type RevokeEvent,
  type SignedEvent,
  type Verifier,
} from "./events.ts";

export type EngineMode = "online" | "offline";

export type IngestOutcome = "accepted" | "duplicate" | "equivocation" | "rejected";

export interface IngestResult {
  outcome: IngestOutcome;
  id: string;
  reason?: string;
}

interface StoredVariant {
  hash: string;
  event: KeyEvent;
  envelope?: SignedEvent;
}

export type EdgeStatus =
  | "admitted"
  | "pending"
  | "closed"
  | "revoked"
  | "tainted"
  | "denied";

export interface HandleDecision {
  handle: KeyHandle;
  subject: string;
  status: EdgeStatus;
  edgeId: string;
  reasons: string[];
}

export interface AdmissionReport {
  subject: string;
  claimable: KeyHandle[];
  pending: HandleDecision[];
  decidedDenials: HandleDecision[];
}

/**
 * Compressed replica summary. `frontier` is an authoritative per-actor high
 * water mark even when the matching events are absent, so a delivery gap can
 * never be mistaken for "that actor never wrote anything".
 */
export interface CompressedSummary {
  version: 1;
  objectId: string;
  frontier: Record<string, string>;
  records: CompactRecord[];
  equivocated: Record<string, string[]>;
}

export type CompactRecord =
  | { kind: "rebuild"; id: string; author: string; generation: string; handle: string; observed: string[]; hash: string }
  | { kind: "delete"; id: string; author: string; generation: string; observed: string[]; hash: string }
  | { kind: "grant"; id: string; author: string; generation: string; subject: string; handle: string; observed: string[]; hash: string }
  | { kind: "delegate"; id: string; author: string; generation: string; subject: string; maxDepth: number; observed: string[]; hash: string }
  | { kind: "revoke"; id: string; author: string; generation: string; targetDot: string; observed: string[]; hash: string };

function contentHash(event: KeyEvent): string {
  return toHex(new Uint8Array(createHash("sha256").update(canonicalBytes(event)).digest()));
}

function validateEvent(event: KeyEvent): void {
  if (!event || typeof event.author !== "string" || event.author.length === 0) {
    throw new Error("missing author");
  }
  if (typeof event.objectId !== "string" || event.objectId.length === 0) {
    throw new Error("missing objectId");
  }
  if (typeof event.generation !== "bigint" || event.generation < 0n) {
    throw new Error("bad generation");
  }
  if (!event.dot || typeof event.dot.sequence !== "bigint" || event.dot.sequence < 1n) {
    throw new Error("bad dot");
  }
  if (event.dot.actor !== event.author) {
    throw new Error("dot actor must equal signing author");
  }
  if (!Array.isArray(event.observed)) throw new Error("observed must be an array");
  for (const dot of event.observed) {
    if (typeof dot.sequence !== "bigint") throw new Error("bad observed dot");
  }
  switch (event.kind) {
    case "rebuild":
      if (!event.handle) throw new Error("rebuild requires handle");
      break;
    case "grant":
      if (!event.subject || !event.handle) throw new Error("grant requires subject and handle");
      break;
    case "delegate":
      if (!event.subject) throw new Error("delegate requires subject");
      if (!Number.isInteger(event.maxDepth) || event.maxDepth < 0 || event.maxDepth > 0xffffffff) {
        throw new Error("bad maxDepth");
      }
      break;
    case "revoke":
      if (!event.targetDot) throw new Error("revoke requires targetDot");
      break;
    case "delete":
      break;
    default:
      throw new Error("unknown event kind");
  }
}

export class KeyAdmissionEngine {
  readonly mode: EngineMode;
  #objectId: string | undefined;
  readonly #variants = new Map<string, StoredVariant[]>();
  readonly #knownMax = new Map<string, bigint>();
  readonly #received = new Map<string, Set<bigint>>();
  readonly #equivocated = new Set<string>();
  readonly #envelopes = new Map<string, SignedEvent>();

  constructor(mode: EngineMode = "offline") {
    this.mode = mode;
  }

  get objectId(): string | undefined {
    return this.#objectId;
  }

  #allEvents(): KeyEvent[] {
    const out: KeyEvent[] = [];
    for (const list of this.#variants.values()) {
      for (const variant of list) out.push(variant.event);
    }
    return out;
  }

  #present(dot: Dot): boolean {
    return this.#received.get(dot.actor)?.has(dot.sequence) ?? false;
  }

  #bumpFrontier(actor: string, sequence: bigint): void {
    const current = this.#knownMax.get(actor);
    if (current === undefined || sequence > current) this.#knownMax.set(actor, sequence);
    let set = this.#received.get(actor);
    if (!set) {
      set = new Set<bigint>();
      this.#received.set(actor, set);
    }
    set.add(sequence);
  }

  /**
   * Merge one signed envelope. The reducer is confluent: evidence is keyed by
   * persistent event id and never mutated in an arrival-order dependent way,
   * so every legal delivery order converges to the same report.
   */
  async ingest(envelope: SignedEvent, verify: Verifier): Promise<IngestResult> {
    const { event, signature } = envelope;
    validateEvent(event);
    if (!(await verify(event, signature))) {
      return { outcome: "rejected", id: dotId(event.dot), reason: "bad signature" };
    }
    if (this.#objectId === undefined) {
      this.#objectId = event.objectId;
    } else if (this.#objectId !== event.objectId) {
      return { outcome: "rejected", id: dotId(event.dot), reason: "cross-object envelope" };
    }

    const id = dotId(event.dot);
    const hash = contentHash(event);
    const existing = this.#variants.get(id);
    if (existing) {
      if (existing.some((variant) => variant.hash === hash)) {
        return { outcome: "duplicate", id };
      }
      this.#equivocated.add(event.author);
      existing.push({ hash, event, envelope: this.mode === "offline" ? envelope : undefined });
      if (this.mode === "offline") this.#envelopes.set(id, envelope);
      this.#bumpFrontier(event.author, event.dot.sequence);
      return { outcome: "equivocation", id, reason: "same dot, different signed content" };
    }

    this.#variants.set(id, [{ hash, event, envelope: this.mode === "offline" ? envelope : undefined }]);
    if (this.mode === "offline") this.#envelopes.set(id, envelope);
    this.#bumpFrontier(event.author, event.dot.sequence);
    return { outcome: "accepted", id };
  }

  /**
   * Merge a compressed summary without full events. The frontier alone proves
   * missing sequences exist; the gap stays explicit instead of being read as
   * "the author never wrote anything".
   */
  ingestSummary(summary: CompressedSummary): void {
    if (this.#objectId === undefined) this.#objectId = summary.objectId;
    if (this.#objectId !== summary.objectId) throw new Error("cross-object summary");
    for (const [actor, maxSeq] of Object.entries(summary.frontier)) {
      const value = BigInt(maxSeq);
      const current = this.#knownMax.get(actor);
      if (current === undefined || value > current) this.#knownMax.set(actor, value);
    }
    for (const [actor, hashes] of Object.entries(summary.equivocated)) {
      if (hashes.length > 0) this.#equivocated.add(actor);
    }
    for (const record of summary.records) this.#absorbRecord(record);
  }

  #absorbRecord(record: CompactRecord): void {
    const dot = parseDot(record.id);
    this.#bumpFrontier(dot.actor, dot.sequence);
    const existing = this.#variants.get(record.id);
    if (existing && existing.some((variant) => variant.hash === record.hash)) return;
    const observed = record.observed.map((id) => parseDot(id));
    const base = {
      author: record.author,
      objectId: this.#objectId as string,
      generation: BigInt(record.generation),
      dot,
      observed,
    };
    let event: KeyEvent;
    switch (record.kind) {
      case "rebuild":
        event = { ...base, kind: "rebuild", handle: record.handle } as RebuildEvent;
        break;
      case "delete":
        event = { ...base, kind: "delete" } as DeleteEvent;
        break;
      case "grant":
        event = { ...base, kind: "grant", subject: record.subject, handle: record.handle } as GrantEvent;
        break;
      case "delegate":
        event = { ...base, kind: "delegate", subject: record.subject, maxDepth: record.maxDepth } as DelegateEvent;
        break;
      case "revoke":
        event = { ...base, kind: "revoke", targetDot: parseDot(record.targetDot) } as RevokeEvent;
        break;
    }
    if (existing) {
      this.#equivocated.add(record.author);
      existing.push({ hash: record.hash, event });
    } else {
      this.#variants.set(record.id, [{ hash: record.hash, event }]);
    }
  }

  /** Offline access to the retained uncompressed log. */
  envelopes(): SignedEvent[] {
    return [...this.#envelopes.values()].sort((a, b) => compareDots(a.event.dot, b.event.dot));
  }

  equivocatedActors(): string[] {
    return [...this.#equivocated].sort();
  }

  isEquivocated(actor: string): boolean {
    return this.#equivocated.has(actor);
  }

  /**
   * Explicit gaps: every sequence up to the known frontier that we do not hold.
   * Rendered as dot ids so a gap is never confused with a non-emission.
   */
  gaps(): string[] {
    const out: string[] = [];
    const actors = [...this.#knownMax.keys()].sort();
    for (const actor of actors) {
      const maxSeq = this.#knownMax.get(actor) as bigint;
      const held = this.#received.get(actor) ?? new Set<bigint>();
      for (let seq = 1n; seq <= maxSeq; seq++) {
        if (!held.has(seq)) out.push(formatDot({ actor, sequence: seq }));
      }
    }
    return out;
  }

  frontier(): Record<string, string> {
    const out: Record<string, string> = {};
    for (const actor of [...this.#knownMax.keys()].sort()) {
      out[actor] = (this.#knownMax.get(actor) as bigint).toString(10);
    }
    return out;
  }

  // ---- Pure derivation over accumulated evidence (order independent) ----

  /**
   * Build a grounding oracle for one derivation. Memoization is local to the
   * call so a provisional `false` seen while breaking a causal cycle can never
   * be cached and misread later. An event is grounded only when every observed
   * predecessor is present and itself grounded; causality comes exclusively
   * from the observed prefix (never from compareDots or storage order).
   */
  #grounding(): (id: string) => boolean {
    const memo = new Map<string, boolean>();
    const visiting = new Set<string>();
    const isGrounded = (id: string): boolean => {
      const cached = memo.get(id);
      if (cached !== undefined) return cached;
      if (visiting.has(id)) return false; // causal cycle: cannot establish
      const variants = this.#variants.get(id);
      let ok = Boolean(variants);
      if (variants) {
        visiting.add(id);
        const event = variants[0].event;
        for (const observedDot of event.observed) {
          if (!this.#present(observedDot) || !isGrounded(dotId(observedDot))) {
            ok = false;
            break;
          }
        }
        visiting.delete(id);
      }
      memo.set(id, ok);
      return ok;
    };
    return isGrounded;
  }

  #eventsOf(kind: KeyEvent["kind"]): KeyEvent[] {
    return this.#allEvents().filter((event) => event.kind === kind);
  }

  /** Privilege events are quarantined for an equivocating author; denies are not. */
  #tainted(event: KeyEvent): boolean {
    return this.#equivocated.has(event.author);
  }

  #resolveOwner(isGrounded: (id: string) => boolean): { owner: string | undefined; ambiguous: boolean } {
    const authors = new Set<string>();
    for (const event of this.#eventsOf("rebuild")) {
      if (this.#tainted(event)) continue;
      if (isGrounded(dotId(event.dot))) authors.add(event.author);
    }
    if (authors.size === 0) return { owner: undefined, ambiguous: false };
    if (authors.size > 1) return { owner: undefined, ambiguous: true };
    return { owner: [...authors][0], ambiguous: false };
  }

  /**
   * Delete tombstones close exactly one generation. They remain effective even
   * when concurrent with or causally after the old envelopes, and even if the
   * delete author later equivocates (a deny must never be erased by a fork).
   */
  #closedGenerations(owner: string | undefined, isGrounded: (id: string) => boolean): {
    closed: Set<bigint>;
    pendingClose: Set<bigint>;
  } {
    const closed = new Set<bigint>();
    const pendingClose = new Set<bigint>();
    if (!owner) return { closed, pendingClose };
    for (const event of this.#eventsOf("delete")) {
      if (event.author !== owner) continue;
      if (isGrounded(dotId(event.dot))) closed.add(event.generation);
      else pendingClose.add(event.generation);
    }
    return { closed, pendingClose };
  }

  #enabledGenerations(owner: string | undefined, isGrounded: (id: string) => boolean): {
    enabled: Set<bigint>;
    handleByGen: Map<bigint, string>;
  } {
    const enabled = new Set<bigint>();
    const handleByGen = new Map<bigint, string>();
    if (!owner) return { enabled, handleByGen };
    for (const event of this.#eventsOf("rebuild")) {
      if (event.author !== owner || this.#tainted(event)) continue;
      if (!isGrounded(dotId(event.dot))) continue;
      enabled.add(event.generation);
      handleByGen.set(event.generation, event.handle);
    }
    return { enabled, handleByGen };
  }

  /**
   * Revoke tombstones that are presently in force. A revoke is fail-closed: it
   * is applied whenever its signature is valid, independent of whether its
   * observed predecessors have arrived. `hard` marks revokes whose revoker's
   * authority is already established, so callers can separate a firm deny from
   * one that could still be reclassified as pending.
   */
  #activeRevokes(owner: string | undefined, reachable: (actor: string, gen: bigint) => boolean): {
    hard: Map<string, RevokeEvent>;
    tentative: Map<string, RevokeEvent>;
  } {
    const hard = new Map<string, RevokeEvent>();
    const tentative = new Map<string, RevokeEvent>();
    for (const event of this.#eventsOf("revoke")) {
      const targetId = dotId(event.targetDot);
      const authoritative = event.author === owner || reachable(event.author, event.generation);
      if (authoritative) {
        if (!hard.has(targetId)) hard.set(targetId, event);
      } else if (!hard.has(targetId) && !tentative.has(targetId)) {
        tentative.set(targetId, event);
      }
    }
    return { hard, tentative };
  }

  #privilegeEvents(isGrounded: (id: string) => boolean): {
    delegates: DelegateEvent[];
    grants: GrantEvent[];
    ungroundedGrants: GrantEvent[];
  } {
    const delegates: DelegateEvent[] = [];
    const grants: GrantEvent[] = [];
    const ungroundedGrants: GrantEvent[] = [];
    for (const event of this.#allEvents()) {
      if (this.#tainted(event)) continue; // forked author cannot confer authority
      const grounded = isGrounded(dotId(event.dot));
      if (event.kind === "delegate") {
        if (grounded) delegates.push(event);
      } else if (event.kind === "grant") {
        if (grounded) grants.push(event);
        else ungroundedGrants.push(event);
      }
    }
    return { delegates, grants, ungroundedGrants };
  }

  /**
   * Recompute every subject's claimable handle set from the retained evidence.
   * Pure and confluent: no mutable admission state exists between calls, so the
   * online merger and offline verifier agree for identical logical evidence.
   */
  computeReport(subject: string): AdmissionReport {
    const claimable: KeyHandle[] = [];
    const pending: HandleDecision[] = [];
    const decidedDenials: HandleDecision[] = [];
    const objectId = this.#objectId ?? "";

    const isGrounded = this.#grounding();
    const { owner, ambiguous } = this.#resolveOwner(isGrounded);
    const { closed, pendingClose } = this.#closedGenerations(owner, isGrounded);
    const { enabled, handleByGen } = this.#enabledGenerations(owner, isGrounded);
    const { delegates, grants, ungroundedGrants } = this.#privilegeEvents(isGrounded);

    const reachableIn = (gen: bigint): Set<string> => {
      const reached = new Set<string>();
      if (!owner) return reached;
      // BFS over delegated authority. Each hop consumes one depth unit.
      const queue: Array<{ actor: string; depth: number }> = [{ actor: owner, depth: 0xffffffff }];
      reached.add(owner);
      while (queue.length > 0) {
        const current = queue.shift() as { actor: string; depth: number };
        if (current.depth <= 0) continue;
        for (const edge of delegates) {
          if (edge.generation !== gen || edge.author !== current.actor) continue;
          if (reached.has(edge.subject)) continue;
          reached.add(edge.subject);
          queue.push({ actor: edge.subject, depth: Math.min(current.depth - 1, edge.maxDepth) });
        }
      }
      return reached;
    };

    const isReachable = (actor: string, gen: bigint): boolean => reachableIn(gen).has(actor);
    const { hard, tentative } = this.#activeRevokes(owner, isReachable);

    const push = (bucket: HandleDecision[], decision: HandleDecision): void => {
      if (decision.subject !== subject) return;
      bucket.push(decision);
    };

    // Tainted grants are a firm safety denial: the granting author forked.
    for (const event of this.#eventsOf("grant")) {
      if (event.subject !== subject || !this.#tainted(event)) continue;
      push(decidedDenials, {
        handle: { objectId, generation: event.generation, handle: event.handle },
        subject: event.subject,
        status: "tainted",
        edgeId: dotId(event.dot),
        reasons: ["granting author equivocated; author-related authority quarantined"],
      });
    }

    // Ungrounded grants cannot be decided yet: a predecessor is still in a gap.
    for (const event of ungroundedGrants) {
      if (event.subject !== subject) continue;
      push(pending, {
        handle: { objectId, generation: event.generation, handle: event.handle },
        subject: event.subject,
        status: "pending",
        edgeId: dotId(event.dot),
        reasons: ["grant event is not grounded; observed predecessor missing"],
      });
    }

    // The owner may claim the rebuild handle of every enabled generation.
    if (owner === subject) {
      for (const gen of [...enabled].sort((a, b) => (a < b ? -1 : a > b ? 1 : 0))) {
        if (closed.has(gen)) continue;
        claimable.push({ objectId, generation: gen, handle: handleByGen.get(gen) as string });
      }
    }

    for (const grant of grants) {
      if (grant.subject !== subject) continue;
      const edgeId = dotId(grant.dot);
      const handle: KeyHandle = { objectId, generation: grant.generation, handle: grant.handle };
      const base = { handle, subject: grant.subject, edgeId };

      if (!enabled.has(grant.generation)) {
        const reasons = [`generation ${grant.generation.toString(10)} has no owner rebuild`];
        if (ambiguous) reasons.push("ambiguous owner from distinct rebuild authors");
        push(pending, { ...base, status: "pending", reasons });
        continue;
      }
      if (closed.has(grant.generation)) {
        push(decidedDenials, {
          ...base,
          status: "closed",
          reasons: [`generation ${grant.generation.toString(10)} was deleted`],
        });
        continue;
      }
      if (pendingClose.has(grant.generation)) {
        push(pending, {
          ...base,
          status: "pending",
          reasons: ["delete tombstone for this generation is not yet grounded"],
        });
        continue;
      }
      if (hard.has(edgeId)) {
        push(decidedDenials, { ...base, status: "revoked", reasons: ["edge revoked by an authoritative actor"] });
        continue;
      }
      if (tentative.has(edgeId)) {
        push(pending, { ...base, status: "pending", reasons: ["revoke present but revoker authority unconfirmed"] });
        continue;
      }
      if (grant.author !== owner && !isReachable(grant.author, grant.generation)) {
        // Blocked by a missing predecessor => undecidable, never a silent allow.
        if (this.#blockedByGap(grant.author, grant.generation, delegates, isGrounded)) {
          push(pending, { ...base, status: "pending", reasons: ["delegation chain crosses a delivery gap"] });
        } else {
          push(decidedDenials, { ...base, status: "denied", reasons: ["granting actor lacks delegated authority"] });
        }
        continue;
      }
      claimable.push(handle);
    }

    return {
      subject,
      claimable: sortHandles(claimable),
      pending: sortDecisions(pending),
      decidedDenials: sortDecisions(decidedDenials),
    };
  }

  /**
   * True when an actor's missing authority is plausibly hidden behind an
   * ungrounded delegate edge rather than absent outright. Keeps such cases
   * pending instead of producing a firm denial.
   */
  #blockedByGap(
    actor: string,
    gen: bigint,
    delegates: DelegateEvent[],
    isGrounded: (id: string) => boolean,
  ): boolean {
    for (const edge of delegates) {
      if (edge.generation !== gen || edge.subject !== actor) continue;
      if (!isGrounded(dotId(edge.dot))) return true;
      if (this.#blockedByGap(edge.author, gen, delegates, isGrounded)) return true;
    }
    return false;
  }

  /** Subjects that currently hold at least one claimable handle. */
  subjects(): string[] {
    const subjects = new Set<string>();
    const owner = this.#resolveOwner(this.#grounding()).owner;
    if (owner) subjects.add(owner);
    for (const event of this.#eventsOf("grant")) subjects.add(event.subject);
    return [...subjects].sort();
  }

  /** Convenience: claimable handles for every known subject. */
  reportAll(): AdmissionReport[] {
    return this.subjects().map((subject) => this.computeReport(subject));
  }

  /**
   * Produce the online merger's minimal evidence. Records are compacted to
   * fingerprints plus the fields needed for admission; signatures and bodies
   * are dropped, while the frontier preserves the authoritative high water mark.
   */
  toSummary(): CompressedSummary {
    const records: CompactRecord[] = [];
    const equivocated: Record<string, string[]> = {};
    for (const [id, variants] of this.#variants) {
      for (const variant of variants) {
        records.push(toCompactRecord(id, variant.hash, variant.event));
        if (variants.length > 1) {
          const actor = variant.event.author;
          (equivocated[actor] ??= []).push(variant.hash);
        }
      }
    }
    return {
      version: 1,
      objectId: this.#objectId ?? "",
      frontier: this.frontier(),
      records: records.sort((a, b) => (a.id < b.id ? -1 : a.id > b.id ? 1 : 0)),
      equivocated,
    };
  }

  /**
   * Exact condition under which an old revoke tombstone may be discarded.
   *
   * It is safe iff every edge the revoke could ever suppress has already lost
   * decrypt eligibility for a reason the receiving side cannot roll back:
   *   1. the edge's generation is durably closed by a grounded owner delete, or
   *   2. the edge's generation is no longer enabled by any grounded rebuild.
   * An open generation never qualifies: a replica that never comes online may
   * still replay the original grant, so dropping the revoke would wrongly
   * re-admit it. This predicate needs no quorum or liveness assumption.
   */
  canDiscardRevoke(revokeId: string): boolean {
    const variants = this.#variants.get(revokeId);
    if (!variants || variants[0].event.kind !== "revoke") return false;
    const revoke = variants[0].event as RevokeEvent;
    const isGrounded = this.#grounding();
    const { owner } = this.#resolveOwner(isGrounded);
    if (!owner) return false;
    const { closed } = this.#closedGenerations(owner, isGrounded);
    const { enabled } = this.#enabledGenerations(owner, isGrounded);
    const generation = revoke.generation;
    if (closed.has(generation)) return true;
    if (!enabled.has(generation)) return true;
    return false;
  }

  /** Ids of revoke tombstones that currently satisfy the discard condition. */
  discardableRevokes(): string[] {
    const out: string[] = [];
    for (const [id, variants] of this.#variants) {
      if (variants[0].event.kind === "revoke" && this.canDiscardRevoke(id)) out.push(id);
    }
    return out.sort();
  }
}

function toCompactRecord(id: string, hash: string, event: KeyEvent): CompactRecord {
  const observed = event.observed.map((dot) => dotId(dot));
  const common = {
    id,
    author: event.author,
    generation: event.generation.toString(10),
    observed,
    hash,
  };
  switch (event.kind) {
    case "rebuild":
      return { ...common, kind: "rebuild", handle: event.handle };
    case "delete":
      return { ...common, kind: "delete" };
    case "grant":
      return { ...common, kind: "grant", subject: event.subject, handle: event.handle };
    case "delegate":
      return { ...common, kind: "delegate", subject: event.subject, maxDepth: event.maxDepth };
    case "revoke":
      return { ...common, kind: "revoke", targetDot: dotId(event.targetDot) };
    default: {
      const exhaustive: never = event;
      throw new Error(`unsupported event kind: ${String(exhaustive)}`);
    }
  }
}

function sortHandles(handles: KeyHandle[]): KeyHandle[] {
  return [...handles].sort((a, b) => {
    if (a.objectId !== b.objectId) return a.objectId < b.objectId ? -1 : 1;
    if (a.generation !== b.generation) return a.generation < b.generation ? -1 : 1;
    return a.handle < b.handle ? -1 : a.handle > b.handle ? 1 : 0;
  });
}

function sortDecisions(decisions: HandleDecision[]): HandleDecision[] {
  return [...decisions].sort((a, b) => (a.edgeId < b.edgeId ? -1 : a.edgeId > b.edgeId ? 1 : 0));
}
