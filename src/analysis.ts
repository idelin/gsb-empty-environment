import { rat } from "./rational.ts";
import type { Rational, RationalInput } from "./rational.ts";
import {
  assertSignature,
  completenessDigest,
  completenessPayload,
  completenessRoot,
  deviceKey,
  earlyReturnPayload,
  leasePayload,
  markerToJSON,
  normalizeEarlyReturn,
  normalizeLease,
  sameDevice,
} from "./model.ts";
import type {
  ClockObservation,
  CompletenessProof,
  DeviceClock,
  DeviceIdentity,
  EarlyReturn,
  Lease,
  NormalizedCompleteness,
  NormalizedLease,
  NormalizedReturn,
  SignaturePolicy,
  MarkerJson,
} from "./model.ts";
import {
  describeResourceInterval,
  intersectResourceIntervals,
  intersectResourceScopes,
  normalizeResourceScope,
  partitionScopeByCuts,
  resourceCutList,
  resourceScopeToJSON,
} from "./resources.ts";
import type {
  ResourceInterval,
  ResourceScope,
  ResourceScopeInput,
} from "./resources.ts";
import { ClockSolver, clockWitnessToJSON } from "./clock.ts";
import type {
  ClockWitness,
  LocalEvent,
} from "./clock.ts";
import {
  emptyWindow,
  intersectWindows,
  markerIsEmptyLowerUpper,
  markerToString,
  safeAfterWindow,
  unboundedLower,
  unboundedUpper,
  windowDefiniteAt,
  windowIsEmpty,
  windowPossibleAt,
} from "./time.ts";
import type {
  TimeMarker,
  ValidityWindow,
} from "./time.ts";

export interface AnalysisInput extends SignaturePolicy {
  leases: Lease[];
  returns?: EarlyReturn[];
  completenessProofs?: CompletenessProof[];
  clocks?: DeviceClock[];
  observations?: ClockObservation[];
  /** Resource regions to decide.  Default: the whole string-prefix universe. */
  queries?: ResourceScopeInput;
  /** Server time at which the status field is evaluated. */
  now?: RationalInput;
}

export interface LeaseSummary {
  id: string;
  deviceKey: string;
  deviceId: string;
  generation: string;
  resources: ResourceInterval[];
  term: {
    start: string;
    end: string;
    startInclusive: boolean;
    endInclusive: boolean;
  };
  parentId: string | null;
  parentDigest: string | null;
  digest: string;
}

export type BlockReason =
  | "direct-lease-term"
  | "early-returned-lease"
  | "parent-term-cap"
  | "unseen-subtree"
  | "unresolved-parent";

export interface BlockingChain {
  blockerId: string;
  leaseIds: string[];
  leases: LeaseSummary[];
  reason: BlockReason;
  resource: ResourceInterval;
  possibleFrom: MarkerJson;
  possibleUntil: MarkerJson;
  definiteFrom: MarkerJson | null;
  definiteUntil: MarkerJson | null;
  completeness: "proved" | "missing" | "not-needed";
  notes: string[];
  clockWitnesses: Array<Record<string, unknown>>;
}

export interface BoundCertificate {
  kind: "lower" | "upper";
  claim: string;
  time: string | null;
  inclusive: boolean;
  infinite: "negative" | "positive" | null;
  resource: ResourceInterval;
  blockers: BlockingChain[];
  clockWitnesses: Array<Record<string, unknown>>;
  notes: string[];
}

export type RegrantStatus = "reclaimed" | "definitely-valid" | "possibly-valid";

export interface RegrantDecision {
  resource: ResourceInterval;
  resourceDescription: string;
  status: RegrantStatus;
  /** Earliest server-time boundary from which re-grant is safe. */
  safeFrom: {
    time: string | null;
    inclusive: boolean;
    infinite: "negative" | "positive" | null;
    text: string;
  };
  /** Latest possible validity instant of the last blocking authorization. */
  lastPossibleUse: {
    time: string | null;
    inclusive: boolean;
    infinite: "negative" | "positive" | null;
    text: string;
  };
  safeAtNow: boolean;
  hasFutureUse: boolean;
  blockers: BlockingChain[];
  lowerBoundCertificate: BoundCertificate;
  upperBoundCertificate: BoundCertificate;
}

export interface AnalysisResult {
  now: string;
  decisions: RegrantDecision[];
  ignoredReturns: Array<{ id: string; leaseId: string; reason: string }>;
  ignoredCompletenessProofs: Array<{ id: string; leaseId: string; reason: string }>;
  leases: LeaseSummary[];
  notes: string[];
}

interface GraphLease extends NormalizedLease {
  parent: GraphLease | null;
  children: GraphLease[];
  unresolvedParent: boolean;
  originalWindow: ValidityWindow | null;
  depth: number;
}

interface BlockerInternal {
  lease: GraphLease;
  chain: GraphLease[];
  blockerId: string;
  reason: BlockReason;
  window: ValidityWindow;
  completeness: "proved" | "missing" | "not-needed";
  notes: string[];
}

interface ReturnInternal extends NormalizedReturn {
  trusted: boolean;
  ignoredReason?: string;
  effectiveResources: ResourceScope;
}

interface ProofInternal extends NormalizedCompleteness {
  trusted: boolean;
  ignoredReason?: string;
}

export function analyzeAuthorizations(input: AnalysisInput): AnalysisResult {
  const now = rat(input.now ?? 0);
  const notes: string[] = [];
  const normalizedLeases = input.leases.map(normalizeLease);
  const leaseById = indexLeases(normalizedLeases);
  const digestToLease = indexDigests(normalizedLeases);

  const graph = buildLeaseGraph(normalizedLeases, leaseById, digestToLease, input);
  const roots = graph.filter((lease) => lease.parent === null);
  const ordered = topologicallyOrder(graph, roots);
  computeEffectiveScopesAndWindows(ordered);

  const { returns, ignoredReturns } = prepareReturns(input.returns ?? [], leaseById, input);
  const { proofs, ignoredCompletenessProofs } = prepareProofs(
    input.completenessProofs ?? [],
    leaseById,
    graph,
    input,
  );
  const returnsByLease = groupReturns(returns);
  const proofsByLease = groupProofs(proofs);

  const events = new Map<string, LocalEvent[]>();
  for (const lease of graph) {
    addEvent(events, lease.deviceKey, lease.term.start, `lease ${lease.id} start`);
    addEvent(events, lease.deviceKey, lease.term.end, `lease ${lease.id} end`);
  }
  for (const earlyReturn of returns) {
    if (!earlyReturn.trusted) continue;
    const lease = leaseById.get(earlyReturn.leaseId);
    if (!lease) continue;
    addEvent(
      events,
      lease.deviceKey,
      earlyReturn.returnedAt,
      `return ${earlyReturn.id} for ${lease.id}`,
    );
  }
  const solver = new ClockSolver(input.clocks ?? [], input.observations ?? []);
  for (const lease of graph) solver.getSystem(lease.device);
  solver.prepare(events);

  // Projecting a window needs the clock solver, so windows are filled in after
  // the graph has been solved.
  projectOriginalWindows(ordered, solver);
  computeCappedOriginalWindows(ordered);

  const cuts = collectCuts(ordered, returns);
  const queryScopes =
    input.queries === undefined ||
    (Array.isArray(input.queries) && input.queries.length === 0)
      ? [{ prefix: "" }]
      : normalizeResourceScope(input.queries);
  const decisions: RegrantDecision[] = [];
  for (const query of queryScopes) {
    const normalizedQuery = normalizeResourceScope(query);
    const cells = partitionScopeByCuts(normalizedQuery, cuts);
    for (const cell of cells) {
      decisions.push(
        decideCell(cell, ordered, returnsByLease, proofsByLease, solver, now),
      );
    }
  }

  decisions.sort(compareDecisions);
  notes.push(
    "All time values are exact rationals.  Open bounds are strict: an open lower bound at T means safe re-grant begins strictly after T.",
  );
  notes.push(
    "Without a valid subtree-completeness proof, an early-returned lease is modeled as an opaque subtree whose unseen descendants may remain valid through the lease's original true term end.",
  );
  notes.push(
    "A return signed by a different device generation is ignored; generation is part of cryptographic identity.",
  );

  return {
    now: now.toString(),
    decisions,
    ignoredReturns,
    ignoredCompletenessProofs,
    leases: ordered.map(toLeaseSummary),
    notes,
  };
}

/** Convenience wrapper for a single resource query. */
export function decideRegrant(
  input: Omit<AnalysisInput, "queries"> & { resource: ResourceInterval },
): RegrantDecision {
  const { resource, ...rest } = input;
  const result = analyzeAuthorizations({ ...rest, queries: [resource] });
  if (result.decisions.length !== 1) {
    throw new Error(`expected one decision cell, got ${result.decisions.length}`);
  }
  return result.decisions[0];
}

function indexLeases(leases: NormalizedLease[]): Map<string, NormalizedLease> {
  const map = new Map<string, NormalizedLease>();
  for (const lease of leases) {
    if (map.has(lease.id)) throw new Error(`duplicate lease id: ${lease.id}`);
    map.set(lease.id, lease);
  }
  return map;
}

function indexDigests(leases: NormalizedLease[]): Map<string, NormalizedLease> {
  const map = new Map<string, NormalizedLease>();
  for (const lease of leases) {
    const existing = map.get(lease.digest);
    if (existing && existing.id !== lease.id) {
      throw new Error(`leases ${existing.id} and ${lease.id} have the same digest`);
    }
    map.set(lease.digest, lease);
  }
  return map;
}

function buildLeaseGraph(
  leases: NormalizedLease[],
  byId: Map<string, NormalizedLease>,
  byDigest: Map<string, NormalizedLease>,
  input: AnalysisInput,
): GraphLease[] {
  const graph: GraphLease[] = leases.map((lease) => ({
    ...lease,
    parent: null,
    children: [],
    unresolvedParent: false,
    originalWindow: null,
    depth: 0,
  }));
  const graphById = new Map(graph.map((lease) => [lease.id, lease]));

  for (const lease of graph) {
    let parent: GraphLease | null = null;
    if (lease.parentDigest) {
      const found = byDigest.get(lease.parentDigest);
      if (!found) {
        lease.unresolvedParent = true;
      } else {
        parent = graphById.get(found.id) ?? null;
      }
    }
    if (lease.parentId) {
      const found = byId.get(lease.parentId);
      if (!found) {
        lease.unresolvedParent = true;
      } else {
        const byIdParent = graphById.get(found.id) ?? null;
        if (parent && parent.id !== byIdParent?.id) {
          throw new Error(
            `lease ${lease.id}: parentId and parentDigest refer to different leases`,
          );
        }
        parent = byIdParent;
      }
    }
    lease.parent = parent;
    if (parent) {
      parent.children.push(lease);
    }
  }

  const propagated = new Set<string>();
  const propagateUnresolved = (lease: GraphLease, inherited: boolean): void => {
    if (propagated.has(lease.id)) return;
    propagated.add(lease.id);
    lease.unresolvedParent = lease.unresolvedParent || inherited;
    for (const child of lease.children) {
      propagateUnresolved(child, lease.unresolvedParent);
    }
  };
  for (const lease of graph) {
    if (!lease.parent) propagateUnresolved(lease, false);
  }

  for (const lease of graph) {
    const valid = assertSignature(
      leasePayload(lease as Lease),
      lease.signature,
      lease.device,
      input,
      `lease ${lease.id}`,
    );
    if (!valid && input.verifier) {
      throw new Error(`lease ${lease.id}: signature verification failed`);
    }
  }

  const visiting = new Set<string>();
  const visited = new Set<string>();
  const visit = (lease: GraphLease, depth: number): void => {
    if (visiting.has(lease.id)) {
      throw new Error(`authorization cycle involving lease ${lease.id}`);
    }
    if (visited.has(lease.id)) return;
    visiting.add(lease.id);
    lease.depth = depth;
    for (const child of lease.children) visit(child, depth + 1);
    visiting.delete(lease.id);
    visited.add(lease.id);
  };
  for (const lease of graph) {
    if (!lease.parent) visit(lease, 0);
  }
  if (visited.size !== graph.length) {
    throw new Error("authorization graph contains a cycle");
  }
  return graph;
}

function topologicallyOrder(graph: GraphLease[], roots: GraphLease[]): GraphLease[] {
  const output: GraphLease[] = [];
  const stack = [...roots].sort((a, b) => (a.id < b.id ? -1 : a.id > b.id ? 1 : 0));
  while (stack.length > 0) {
    const lease = stack.pop()!;
    output.push(lease);
    const children = [...lease.children].sort((a, b) =>
      a.id < b.id ? 1 : a.id > b.id ? -1 : 0,
    );
    stack.push(...children);
  }
  return output;
}

function computeEffectiveScopesAndWindows(ordered: GraphLease[]): void {
  for (const lease of ordered) {
    if (lease.parent) {
      lease.resources = intersectResourceScopes(lease.declaredResources, lease.parent.resources);
    } else {
      lease.resources = normalizeResourceScope(lease.declaredResources);
    }
  }
}

function prepareReturns(
  raw: EarlyReturn[],
  leases: Map<string, NormalizedLease>,
  input: AnalysisInput,
): { returns: ReturnInternal[]; ignoredReturns: AnalysisResult["ignoredReturns"] } {
  const returns: ReturnInternal[] = [];
  const ignoredReturns: AnalysisResult["ignoredReturns"] = [];
  const seen = new Set<string>();
  for (const value of raw) {
    const earlyReturn = normalizeEarlyReturn(value);
    if (seen.has(earlyReturn.id)) throw new Error(`duplicate early return id: ${earlyReturn.id}`);
    seen.add(earlyReturn.id);
    const lease = leases.get(earlyReturn.leaseId);
    if (!lease) throw new Error(`return ${earlyReturn.id}: unknown lease ${earlyReturn.leaseId}`);
    if (earlyReturn.leaseDigest && earlyReturn.leaseDigest !== lease.digest) {
      throw new Error(`return ${earlyReturn.id}: lease digest mismatch`);
    }

    let ignoredReason: string | undefined;
    if (!sameDevice(earlyReturn.device, lease.device)) {
      ignoredReason = `return signed by ${earlyReturn.deviceKey}, lease belongs to ${lease.deviceKey}`;
    } else if (lease.unresolvedParent) {
      ignoredReason = "return belongs to a lease with an unresolved parent";
    }

    const trusted =
      ignoredReason === undefined &&
      assertSignature(
        earlyReturnPayload(value, lease.digest),
        earlyReturn.signature,
        earlyReturn.device,
        input,
        `return ${earlyReturn.id}`,
      );
    if (ignoredReason === undefined && !trusted && input.requireSignatures) {
      throw new Error(`return ${earlyReturn.id}: signature rejected`);
    }
    if (ignoredReason === undefined && !trusted && input.verifier) {
      ignoredReason = "signature verification failed";
    }

    let effectiveResources: ResourceScope = [];
    if (ignoredReason === undefined) {
      effectiveResources =
        earlyReturn.resources === null
          ? lease.resources
          : intersectResourceScopes(earlyReturn.resources, lease.resources);
    }

    if (ignoredReason !== undefined) {
      ignoredReturns.push({
        id: earlyReturn.id,
        leaseId: earlyReturn.leaseId,
        reason: ignoredReason,
      });
    }
    returns.push({ ...earlyReturn, trusted: ignoredReason === undefined, ignoredReason, effectiveResources });
  }
  return { returns, ignoredReturns };
}

function prepareProofs(
  raw: CompletenessProof[],
  leaseById: Map<string, NormalizedLease>,
  graph: GraphLease[],
  input: AnalysisInput,
): {
  proofs: ProofInternal[];
  ignoredCompletenessProofs: AnalysisResult["ignoredCompletenessProofs"];
} {
  const proofs: ProofInternal[] = [];
  const ignored: AnalysisResult["ignoredCompletenessProofs"] = [];
  const graphById = new Map(graph.map((lease) => [lease.id, lease]));
  const seen = new Set<string>();
  for (const value of raw) {
    const proof: NormalizedCompleteness = {
      id: value.id,
      leaseId: value.leaseId,
      leaseDigest: value.leaseDigest ?? null,
      device: value.device,
      deviceKey: deviceKey(value.device),
      at: rat(value.at),
      descendants: [...value.descendants].sort(),
      root: value.root ?? "",
      signature: value.signature ?? null,
      digest: "",
    };
    if (seen.has(proof.id)) {
      throw new Error(`duplicate completeness proof id: ${proof.id}`);
    }
    seen.add(proof.id);
    const rootLease = leaseById.get(proof.leaseId);
    if (!rootLease) {
      throw new Error(`completeness proof ${proof.id}: unknown lease ${proof.leaseId}`);
    }
    if (proof.leaseDigest && proof.leaseDigest !== rootLease.digest) {
      throw new Error(`completeness proof ${proof.id}: lease digest mismatch`);
    }

    let ignoredReason: string | undefined;
    if (!sameDevice(proof.device, rootLease.device)) {
      ignoredReason = `proof signed by ${proof.deviceKey}, lease belongs to ${rootLease.deviceKey}`;
    }

    const knownDescendants = collectDescendants(graphById.get(rootLease.id)!);
    const expected = [...knownDescendants].sort();
    if (ignoredReason === undefined && !arrayEquals(expected, proof.descendants)) {
      ignoredReason = `proof descendant set does not equal known subtree (${expected.join(", ")})`;
    }

    const descendantDigests = proof.descendants
      .map((id) => leaseById.get(id)?.digest)
      .filter((digest): digest is string => Boolean(digest))
      .sort();
    const computedRoot = completenessRoot(rootLease.digest, descendantDigests);
    if (ignoredReason === undefined && proof.root && proof.root !== computedRoot) {
      ignoredReason = "proof root does not match the known subtree commitment";
    }
    proof.root = proof.root || computedRoot;
    proof.digest = completenessDigest(value, descendantDigests);

    const trusted =
      ignoredReason === undefined &&
      assertSignature(
        completenessPayload(value, descendantDigests),
        proof.signature,
        proof.device,
        input,
        `completeness proof ${proof.id}`,
      );
    if (ignoredReason === undefined && !trusted && input.requireSignatures) {
      throw new Error(`completeness proof ${proof.id}: signature rejected`);
    }
    if (ignoredReason === undefined && !trusted && input.verifier) {
      ignoredReason = "signature verification failed";
    }

    if (ignoredReason !== undefined) {
      ignored.push({ id: proof.id, leaseId: proof.leaseId, reason: ignoredReason });
    }
    proofs.push({ ...proof, trusted: ignoredReason === undefined, ignoredReason });
  }
  return { proofs, ignoredCompletenessProofs: ignored };
}

function stableJSON(value: unknown): string {
  return JSON.stringify(sortJSON(value));
}

function sortJSON(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(sortJSON);
  if (value !== null && typeof value === "object") {
    const object = value as Record<string, unknown>;
    const output: Record<string, unknown> = {};
    for (const key of Object.keys(object).sort()) {
      if (object[key] !== undefined) output[key] = sortJSON(object[key]);
    }
    return output;
  }
  return value;
}

function collectDescendants(lease: GraphLease): string[] {
  const output: string[] = [];
  const stack = [...lease.children];
  while (stack.length > 0) {
    const child = stack.pop()!;
    output.push(child.id);
    stack.push(...child.children);
  }
  return output;
}

function groupReturns(returns: ReturnInternal[]): Map<string, ReturnInternal[]> {
  const map = new Map<string, ReturnInternal[]>();
  for (const earlyReturn of returns) {
    if (!earlyReturn.trusted) continue;
    const list = map.get(earlyReturn.leaseId) ?? [];
    list.push(earlyReturn);
    map.set(earlyReturn.leaseId, list);
  }
  return map;
}

function groupProofs(proofs: ProofInternal[]): Map<string, ProofInternal[]> {
  const map = new Map<string, ProofInternal[]>();
  for (const proof of proofs) {
    if (!proof.trusted) continue;
    const list = map.get(proof.leaseId) ?? [];
    list.push(proof);
    map.set(proof.leaseId, list);
  }
  return map;
}

function addEvent(
  events: Map<string, LocalEvent[]>,
  key: string,
  local: Rational,
  source: string,
): void {
  const list = events.get(key) ?? [];
  list.push({ local, source });
  events.set(key, list);
}

function projectOriginalWindows(ordered: GraphLease[], solver: ClockSolver): void {
  for (const lease of ordered) {
    lease.originalWindow = projectWindow(
      lease,
      lease.term.start,
      lease.term.startInclusive,
      lease.term.end,
      lease.term.endInclusive,
      solver,
    );
  }
}

function computeCappedOriginalWindows(ordered: GraphLease[]): void {
  for (const lease of ordered) {
    if (lease.unresolvedParent) {
      lease.originalWindow = unresolvedWindow(lease.originalWindow!);
      continue;
    }
    let window = lease.originalWindow!;
    let ancestor = lease.parent;
    while (ancestor) {
      window = intersectWindows(window, ancestor.originalWindow!);
      ancestor = ancestor.parent;
    }
    lease.originalWindow = window;
  }
}

function unresolvedWindow(window: ValidityWindow): ValidityWindow {
  return {
    possibleFrom: window.possibleFrom,
    possibleUntil: unboundedUpper(false),
    definiteFrom: null,
    definiteUntil: null,
  };
}

function projectWindow(
  lease: GraphLease,
  startLocal: Rational,
  startInclusive: boolean,
  endLocal: Rational,
  endInclusive: boolean,
  solver: ClockSolver,
): ValidityWindow {
  if (
    startLocal.gt(endLocal) ||
    (startLocal.eq(endLocal) && (!startInclusive || !endInclusive))
  ) {
    return emptyWindow();
  }
  const start = solver.localBounds(lease.device, startLocal);
  const end = solver.localBounds(lease.device, endLocal);
  const window: ValidityWindow = {
    possibleFrom: { time: start.min, inclusive: startInclusive },
    possibleUntil: { time: end.max, inclusive: endInclusive },
    definiteFrom: { time: start.max, inclusive: startInclusive },
    definiteUntil: { time: end.min, inclusive: endInclusive },
  };
  if (
    markerIsEmptyLowerUpper(window.definiteFrom, window.definiteUntil) &&
    window.definiteFrom !== null &&
    window.definiteUntil !== null
  ) {
    window.definiteFrom = null;
    window.definiteUntil = null;
  }
  return windowIsEmpty(window) ? emptyWindow() : window;
}

function collectCuts(leases: GraphLease[], returns: ReturnInternal[]): ResourceScope {
  const cuts: ResourceScope = [];
  for (const lease of leases) cuts.push(...lease.resources);
  for (const earlyReturn of returns) {
    if (earlyReturn.trusted) cuts.push(...earlyReturn.effectiveResources);
  }
  return resourceCutList(cuts);
}

function decideCell(
  resource: ResourceInterval,
  leases: GraphLease[],
  returnsByLease: Map<string, ReturnInternal[]>,
  proofsByLease: Map<string, ProofInternal[]>,
  solver: ClockSolver,
  now: Rational,
): RegrantDecision {
  const blockers: BlockerInternal[] = [];

  for (const lease of leases) {
    if (!scopeIntersectsCell(lease.resources, resource)) continue;
    const applicableReturns = (returnsByLease.get(lease.id) ?? []).filter((earlyReturn) =>
      scopeIntersectsCell(earlyReturn.effectiveResources, resource),
    );
    const returned = applicableReturns.length > 0;
    const latestReturn = returned
      ? applicableReturns.reduce((max, value) =>
          value.returnedAt.gt(max) ? value.returnedAt : max,
        applicableReturns[0].returnedAt)
      : null;
    const proof = findApplicableProof(proofsByLease.get(lease.id) ?? [], latestReturn);
    const complete = proof !== null;

    if (lease.unresolvedParent) {
      blockers.push({
        lease,
        chain: chainFor(lease),
        blockerId: `unresolved-parent:${lease.id}`,
        reason: "unresolved-parent",
        window: {
          possibleFrom: lease.originalWindow!.possibleFrom,
          possibleUntil: unboundedUpper(false),
          definiteFrom: null,
          definiteUntil: null,
        },
        completeness: "missing",
        notes: ["the parent lease/digest is unavailable, so no finite parent deadline can be certified"],
      });
      continue;
    }

    const effectiveEnd =
      latestReturn !== null && latestReturn.lte(lease.term.end)
        ? { local: latestReturn, inclusive: false }
        : { local: lease.term.end, inclusive: lease.term.endInclusive };
    const rawDirectWindow = projectWindow(
      lease,
      lease.term.start,
      lease.term.startInclusive,
      effectiveEnd.local,
      effectiveEnd.inclusive,
      solver,
    );
    const directWindow = capWindow(rawDirectWindow, lease);
    const parentCapApplied =
      !markerEquals(rawDirectWindow.possibleFrom, directWindow.possibleFrom) ||
      !markerEquals(rawDirectWindow.possibleUntil, directWindow.possibleUntil);
    const directNotes = returned
      ? [
          `early return observed at local ${latestReturn}`,
          complete
            ? "a valid subtree-completeness proof covers this return"
            : "no valid subtree-completeness proof covers this return",
        ]
      : ["no early return; the original true term controls reclamation"];
    if (parentCapApplied) {
      directNotes.push("the lease is capped by an original parent authorization term");
    }
    if (!windowIsEmpty(directWindow)) {
      blockers.push({
        lease,
        chain: chainFor(lease),
        blockerId: lease.id,
        reason: parentCapApplied
          ? "parent-term-cap"
          : returned
            ? "early-returned-lease"
            : "direct-lease-term",
        window: directWindow,
        completeness: returned ? (complete ? "proved" : "missing") : "not-needed",
        notes: directNotes,
      });
    }

    if (returned && !complete && latestReturn !== null && latestReturn.gt(lease.term.start)) {
      // Unseen children may have been issued before the return and may live to
      // the parent's original true term end.  This is only possible validity,
      // never definite validity.
      blockers.push({
        lease,
        chain: chainFor(lease),
        blockerId: `unseen-subtree:${lease.id}`,
        reason: "unseen-subtree",
        window: {
          possibleFrom: lease.originalWindow!.possibleFrom,
          possibleUntil: lease.originalWindow!.possibleUntil,
          definiteFrom: null,
          definiteUntil: null,
        },
        completeness: "missing",
        notes: [
          "without a completeness proof, unobserved subleases cannot be treated as nonexistent",
          "an unseen descendant can remain valid through the original parent term end",
        ],
      });
    }
  }

  const activeBlockers = blockers.filter((blocker) => !windowIsEmpty(blocker.window));
  const lastPossibleUse = lastPossibleUseOf(activeBlockers);
  const safeFrom = safeFromBlockers(activeBlockers);
  const definite = activeBlockers.some((blocker) => windowDefiniteAt(blocker.window, now));
  const possible = activeBlockers.some((blocker) => windowPossibleAt(blocker.window, now));
  const status: RegrantStatus = definite
    ? "definitely-valid"
    : possible
      ? "possibly-valid"
      : "reclaimed";
  const safeAtNow =
    safeFrom.time === null
      ? safeFrom.infinite === "negative"
      : now.gt(safeFrom.time) || (now.eq(safeFrom.time) && safeFrom.inclusive);
  const hasFutureUse = activeBlockers.some(
    (blocker) =>
      blocker.window.possibleFrom.time === null || blocker.window.possibleFrom.time.gte(now),
  );

  const lowerChains = activeBlockers
    .filter((blocker) => markerEquals(safeAfterWindow(blocker.window), safeFrom))
    .map((blocker) => toBlockingChain(blocker, resource, solver));
  const upperChains = activeBlockers.map((blocker) =>
    toBlockingChain(blocker, resource, solver),
  );

  const lowerWitnesses = uniqueWitnesses(lowerChains);
  const upperWitnesses = uniqueWitnesses(upperChains);

  const lowerCertificate: BoundCertificate = {
    kind: "lower",
    claim:
      activeBlockers.length === 0
        ? "no authorization can overlap this resource; its safe lower bound is -infinity"
        : safeFrom.time === null && safeFrom.infinite === "positive"
          ? "a permanent/external authorization blocks reclamation forever"
          : safeFrom.inclusive
            ? `re-grant is not safe for server times < ${safeFrom.time}`
            : `re-grant is not safe for server times <= ${safeFrom.time}`,
    time: safeFrom.time === null ? null : safeFrom.time.toString(),
    inclusive: safeFrom.inclusive,
    infinite: safeFrom.time === null ? (safeFrom.infinite ?? null) : null,
    resource,
    blockers: lowerChains,
    clockWitnesses: lowerWitnesses,
    notes:
      activeBlockers.length === 0
        ? ["the resource is already free in the possible-validity model"]
        : [
            "each listed chain is a single coherent clock assignment; values from different observations of one device are never mixed",
            safeFrom.inclusive
              ? `the last possible end is open, so ${markerToString(safeFrom, "lower")} itself is safe`
              : `the last possible end is closed, so re-grant must be strictly after the boundary`,
          ],
  };

  const upperCertificate: BoundCertificate = {
    kind: "upper",
    claim:
      activeBlockers.length === 0
        ? "no authorization overlaps this resource; it is free at all server times"
        : safeFrom.time === null
          ? "no finite upper bound can be certified"
          : `re-grant is safe from ${markerToString(safeFrom, "lower")}`,
    time: safeFrom.time === null ? null : safeFrom.time.toString(),
    inclusive: safeFrom.inclusive,
    infinite: safeFrom.time === null ? (safeFrom.infinite ?? null) : null,
    resource,
    blockers: upperChains,
    clockWitnesses: upperWitnesses,
    notes: [
      "every blocker is either a signed early termination, an original true term, a parent term cap, or an explicitly unresolved/unseen authorization",
      "a permanently offline device is reclaimed from its true term bound, not from an assumption that it has already expired",
    ],
  };

  return {
    resource,
    resourceDescription: describeResourceInterval(resource),
    status,
    safeFrom: {
      time: safeFrom.time === null ? null : safeFrom.time.toString(),
      inclusive: safeFrom.inclusive,
      infinite: safeFrom.time === null ? (safeFrom.infinite ?? null) : null,
      text: markerToString(safeFrom, "lower"),
    },
    lastPossibleUse: {
      time: lastPossibleUse.time === null ? null : lastPossibleUse.time.toString(),
      inclusive: lastPossibleUse.inclusive,
      infinite:
        lastPossibleUse.time === null ? (lastPossibleUse.infinite ?? null) : null,
      text: markerToString(lastPossibleUse, "upper"),
    },
    safeAtNow,
    hasFutureUse,
    blockers: upperChains,
    lowerBoundCertificate: lowerCertificate,
    upperBoundCertificate: upperCertificate,
  };
}

function findApplicableProof(
  proofs: ProofInternal[],
  latestReturn: Rational | null,
): ProofInternal | null {
  if (latestReturn === null) return null;
  for (const proof of proofs) {
    if (proof.at.gte(latestReturn)) return proof;
  }
  return null;
}

function capWindow(window: ValidityWindow, lease: GraphLease): ValidityWindow {
  let result = window;
  let ancestor = lease.parent;
  while (ancestor) {
    result = intersectWindows(result, ancestor.originalWindow!);
    ancestor = ancestor.parent;
  }
  return result;
}

function chainFor(lease: GraphLease): GraphLease[] {
  const chain: GraphLease[] = [];
  let current: GraphLease | null = lease;
  while (current) {
    chain.unshift(current);
    current = current.parent;
  }
  return chain;
}

function scopeIntersectsCell(scope: ResourceScope, cell: ResourceInterval): boolean {
  return normalizeResourceScope(scope).some(
    (interval) => intersectResourceIntervals(interval, cell) !== null,
  );
}

function lastPossibleUseOf(blockers: BlockerInternal[]): TimeMarker {
  let result: TimeMarker = unboundedLower(false);
  for (const blocker of blockers) {
    const candidate = blocker.window.possibleUntil;
    const cmp = compareNullableMarkers(candidate, result);
    if (cmp > 0) result = candidate;
    else if (cmp === 0) {
      result = { time: result.time, inclusive: result.inclusive || candidate.inclusive };
    }
  }
  return result;
}

function safeFromBlockers(blockers: BlockerInternal[]): TimeMarker {
  if (blockers.length === 0) return unboundedLower(true);
  let result: TimeMarker | null = null;
  for (const blocker of blockers) {
    const candidate = safeAfterWindow(blocker.window);
    if (result === null) {
      result = candidate;
      continue;
    }
    if (candidate.time === null) {
      result = candidate;
      continue;
    }
    if (result.time === null) continue;
    const cmp = candidate.time.cmp(result.time);
    if (cmp > 0) result = candidate;
    else if (cmp === 0) {
      result = {
        time: result.time,
        inclusive: result.inclusive && candidate.inclusive,
      };
    }
  }
  return result ?? unboundedLower(true);
}

function compareNullableMarkers(a: TimeMarker, b: TimeMarker): number {
  if (a.time === null && b.time === null) {
    if (a.infinite === b.infinite) return 0;
    return a.infinite === "negative" ? -1 : 1;
  }
  if (a.time === null) return a.infinite === "negative" ? -1 : 1;
  if (b.time === null) return b.infinite === "negative" ? 1 : -1;
  return a.time.cmp(b.time);
}

function markerEquals(a: TimeMarker, b: TimeMarker): boolean {
  if (a.time === null || b.time === null) {
    return a.time === b.time && a.infinite === b.infinite;
  }
  return a.time.eq(b.time) && a.inclusive === b.inclusive;
}

function toBlockingChain(
  blocker: BlockerInternal,
  resource: ResourceInterval,
  solver: ClockSolver,
): BlockingChain {
  const witnessKeys = new Set(blocker.chain.map((lease) => lease.deviceKey));
  const witnesses: Array<Record<string, unknown>> = [];
  for (const key of witnessKeys) {
    const lease = blocker.chain.find((value) => value.deviceKey === key)!;
    witnesses.push(clockWitnessToJSON(solver.witness(lease.device)));
  }
  return {
    blockerId: blocker.blockerId,
    leaseIds: blocker.chain.map((lease) => lease.id),
    leases: blocker.chain.map(toLeaseSummary),
    reason: blocker.reason,
    resource,
    possibleFrom: markerToJSON(blocker.window.possibleFrom),
    possibleUntil: markerToJSON(blocker.window.possibleUntil),
    definiteFrom:
      blocker.window.definiteFrom && markerToJSON(blocker.window.definiteFrom),
    definiteUntil:
      blocker.window.definiteUntil && markerToJSON(blocker.window.definiteUntil),
    completeness: blocker.completeness,
    notes: [...blocker.notes],
    clockWitnesses: witnesses,
  };
}

function uniqueWitnesses(chains: BlockingChain[]): Array<Record<string, unknown>> {
  const seen = new Set<string>();
  const output: Array<Record<string, unknown>> = [];
  for (const chain of chains) {
    for (const witness of chain.clockWitnesses) {
      const key = stableJSON(witness);
      if (seen.has(key)) continue;
      seen.add(key);
      output.push(witness);
    }
  }
  return output;
}

function toLeaseSummary(lease: GraphLease): LeaseSummary {
  return {
    id: lease.id,
    deviceKey: lease.deviceKey,
    deviceId: lease.device.id,
    generation: String(lease.device.generation),
    resources: resourceScopeToJSON(lease.resources),
    term: {
      start: lease.term.start.toString(),
      end: lease.term.end.toString(),
      startInclusive: lease.term.startInclusive,
      endInclusive: lease.term.endInclusive,
    },
    parentId: lease.parent?.id ?? lease.parentId,
    parentDigest: lease.parentDigest,
    digest: lease.digest,
  };
}

function compareDecisions(a: RegrantDecision, b: RegrantDecision): number {
  const prefixCmp =
    a.resource.prefix < b.resource.prefix ? -1 : a.resource.prefix > b.resource.prefix ? 1 : 0;
  if (prefixCmp !== 0) return prefixCmp;
  const ea = (a.resource.excludes ?? []).join("\u0000");
  const eb = (b.resource.excludes ?? []).join("\u0000");
  return ea < eb ? -1 : ea > eb ? 1 : 0;
}

function arrayEquals(a: string[], b: string[]): boolean {
  return a.length === b.length && a.every((value, index) => value === b[index]);
}

export function analysisToJSON(result: AnalysisResult): string {
  return JSON.stringify(result, null, 2);
}

export function explainDecision(decision: RegrantDecision): string {
  return [
    `${decision.resourceDescription}: ${decision.status}`,
    `safe from ${decision.safeFrom.text}`,
    `last possible use ${decision.lastPossibleUse.text}`,
    ...decision.blockers.map(
      (blocker) =>
        `- ${blocker.blockerId} (${blocker.reason}) via ${blocker.leaseIds.join(" -> ")}; possible through ${markerToString(
          {
            time: blocker.possibleUntil.time === null ? null : rat(blocker.possibleUntil.time),
            inclusive: blocker.possibleUntil.inclusive,
            infinite: blocker.possibleUntil.infinite ?? undefined,
          },
          "upper",
        )}`,
    ),
  ].join("\n");
}

export const analyzeRegrants = analyzeAuthorizations;
export const computeRegrant = decideRegrant;
