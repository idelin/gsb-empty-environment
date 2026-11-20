import { readWeight } from "./weight.ts";

/*
 * Privacy admission component.
 *
 * Two adjacent datasets are described by finite stateful query mechanisms:
 * every (query, hidden state) emits a pair (public answer, successor hidden
 * state) with an exact rational probability. An analyst issues at most eight
 * rounds of queries, choosing each query from the public transcript so far,
 * and the same transcript must select the same query on both datasets. A
 * public-history predicate may stop the interaction early; stopping is a
 * visible transcript event.
 *
 * Worst-case two-direction hockey-stick divergence
 *   sum_t max(P(t) - L Q(t), 0)
 * over all adaptive analysts is solved by memoized dynamic programming on
 * posterior-belief states rather than by enumerating adaptive strategy trees.
 */

// ---------------------------------------------------------------------------
// Exact rational arithmetic (canonical fractions, BigInt)
// ---------------------------------------------------------------------------

export interface Fraction { readonly n: bigint; readonly d: bigint }

function gcd(a: bigint, b: bigint): bigint {
  let x = a < 0n ? -a : a;
  let y = b < 0n ? -b : b;
  while (y !== 0n) { const r = x % y; x = y; y = r; }
  return x;
}

const ZERO: Fraction = Object.freeze({ n: 0n, d: 1n });
const ONE: Fraction = Object.freeze({ n: 1n, d: 1n });

function frac(n: bigint, d: bigint): Fraction {
  if (d === 0n) throw new Error("fraction with zero denominator");
  if (d < 0n) { n = -n; d = -d; }
  if (n === 0n) return ZERO;
  const g = gcd(n, d);
  return Object.freeze({ n: n / g, d: d / g });
}

function parseFraction(text: string): Fraction {
  const m = /^(0|[1-9][0-9]*)\/([1-9][0-9]*)$/.exec(text);
  if (!m || m[0] !== text) throw new Error("invalid rational: " + text);
  return frac(BigInt(m[1]), BigInt(m[2]));
}

function fracWire(f: Fraction): string {
  // Canonical rational byte form: non-negative reduced n/d. Values here are
  // divergences or probabilities and therefore never negative; unlike
  // wireWeight this form is not restricted to n <= d.
  if (f.n < 0n) throw new Error("cannot serialize negative rational");
  const text = f.n.toString() + "/" + f.d.toString();
  if (!/^(0|[1-9][0-9]*)\/[1-9][0-9]*$/.test(text) || gcd(f.n, f.d) !== 1n) {
    throw new Error("noncanonical rational");
  }
  return text;
}

function fracAdd(a: Fraction, b: Fraction): Fraction {
  return frac(a.n * b.d + b.n * a.d, a.d * b.d);
}

function fracSub(a: Fraction, b: Fraction): Fraction {
  return frac(a.n * b.d - b.n * a.d, a.d * b.d);
}

function fracMul(a: Fraction, b: Fraction): Fraction {
  return frac(a.n * b.n, a.d * b.d);
}

// a / b for non-negative exact fractions.
function fracDiv(a: Fraction, b: Fraction): Fraction {
  if (b.n === 0n) throw new Error("division by zero fraction");
  return frac(a.n * b.d, a.d * b.n);
}

function fracMax0(a: Fraction): Fraction {
  return a.n < 0n ? ZERO : a;
}

function fracGE(a: Fraction, b: Fraction): boolean {
  return a.n * b.d >= b.n * a.d;
}

// ---------------------------------------------------------------------------
// Public wire types
// ---------------------------------------------------------------------------

export interface TransitionEntry {
  readonly answer: string;
  readonly nextState: string;
  readonly probability: string; // canonical probability byte format, see weight.ts
}

type TransitionTable =
  Readonly<Record<string, Readonly<Record<string, readonly TransitionEntry[]>>>>;

export interface MechanismSpec {
  readonly rounds: number;                       // at most 8
  readonly states: readonly string[];
  readonly answers: readonly string[];
  readonly queries: readonly string[];
  readonly allowedQueries: readonly (readonly string[])[]; // length === rounds
  readonly initial: readonly string[];          // length === states, probabilities sum to 1
  readonly multiplier: string;                  // L >= 1, canonical fraction
  readonly stopHistories: readonly (readonly (readonly [string, string])[])[];
  readonly tables: readonly [TransitionTable, TransitionTable];
  readonly delta?: string; // optional admission budget
}

export interface AdmissionResult {
  readonly admitted: boolean;
  readonly worstDivergence: string;
  readonly direction01: string; // sup over analysts of sum max(P0 - L P1, 0)
  readonly direction10: string; // sup over analysts of sum max(P1 - L P0, 0)
  readonly multiplier: string;
  readonly rounds: number;
  readonly distinctStates: number;
  readonly certificate: Certificate;
}

export interface Certificate {
  readonly version: 1;
  readonly multiplier: string;
  readonly rounds: number;
  readonly stopAutomaton: StopAutomaton;
  readonly nodes: Readonly<Record<string, CertificateNode>>;
  readonly rootKey: string;
}

export interface CertificateNode {
  readonly round: number;
  readonly stopNode: string;
  readonly mass0: string;
  readonly mass1: string;
  readonly belief0: readonly string[];
  readonly belief1: readonly string[];
  readonly terminal: boolean;
  readonly value01: string;
  readonly value10: string;
  // Non-terminal nodes: per query the public-answer branches (hidden branches
  // with equal answers are merged) and the reached posterior child state.
  readonly branches?: Readonly<Record<string, readonly CertificateEdge[]>>;
  readonly chosen01?: string;
  readonly chosen10?: string;
}

export interface CertificateEdge {
  readonly answer: string;
  readonly prob0: string;
  readonly prob1: string;
  // Absent exactly when this answer terminates the transcript (round cap or
  // stop rule): the edge itself is the terminal public event.
  readonly childKey?: string;
}

interface StopAutomaton {
  readonly initial: string;
  readonly accepting: readonly string[];
  readonly transitions: Readonly<Record<string, Readonly<Record<string, string>>>>;
}

// ---------------------------------------------------------------------------
// Specification validation and indexing
// ---------------------------------------------------------------------------

interface AnswerDist {
  readonly total: Fraction;
  readonly next: Fraction[]; // joint probability mass per successor state
}

interface PreparedStop {
  readonly initial: string;
  readonly accepting: ReadonlySet<string>;
  readonly transitions: ReadonlyMap<string, ReadonlyMap<string, string>>;
}

interface PreparedSpec {
  readonly spec: MechanismSpec;
  readonly stateCount: number;
  readonly answerCount: number;
  readonly queryCount: number;
  readonly allowed: readonly (readonly number[])[];
  readonly initial0: Fraction[];
  readonly initial1: Fraction[];
  readonly multiplier: Fraction;
  readonly delta?: Fraction;
  // table[side][query][state][answerIndex]
  readonly table: AnswerDist[][][];
  readonly stop: PreparedStop;
}

function requireUnique(labels: readonly string[], kind: string): void {
  const seen = new Set<string>();
  for (const label of labels) {
    if (typeof label !== "string" || label.length === 0) {
      throw new Error("invalid " + kind + " identifier");
    }
    if (seen.has(label)) throw new Error("duplicate " + kind + " identifier: " + label);
    seen.add(label);
  }
}

function indexMap(labels: readonly string[]): Map<string, number> {
  const map = new Map<string, number>();
  labels.forEach((label, i) => map.set(label, i));
  return map;
}

export function prepareSpec(spec: MechanismSpec): PreparedSpec {
  if (!spec || typeof spec !== "object") throw new Error("missing mechanism spec");
  const rounds = spec.rounds;
  if (!Number.isInteger(rounds) || rounds < 1 || rounds > 8) {
    throw new Error("rounds must be an integer in [1, 8]");
  }
  requireUnique(spec.states, "state");
  requireUnique(spec.answers, "answer");
  requireUnique(spec.queries, "query");
  const nS = spec.states.length;
  const nA = spec.answers.length;
  const nQ = spec.queries.length;
  if (nS === 0 || nA === 0 || nQ === 0) throw new Error("empty alphabets");
  const stateIndex = indexMap(spec.states);
  const answerIndex = indexMap(spec.answers);
  const queryIndex = indexMap(spec.queries);

  if (spec.allowedQueries.length !== rounds) {
    throw new Error("allowedQueries must have one entry per round");
  }
  const allowed: number[][] = [];
  for (let r = 0; r < rounds; r++) {
    const row: number[] = [];
    for (const q of spec.allowedQueries[r]) {
      const qi = queryIndex.get(q);
      if (qi === undefined) throw new Error("unknown allowed query: " + q);
      if (row.includes(qi)) throw new Error("repeated allowed query: " + q);
      row.push(qi);
    }
    if (row.length === 0) throw new Error("no allowed query at round " + r);
    allowed.push(row);
  }

  if (spec.initial.length !== nS) throw new Error("initial must align with states");
  const initial0 = spec.initial.map((w) => readWeight(w));
  const initial1 = spec.initial.map((w) => readWeight(w));
  const isOne = (f: Fraction): boolean => f.n === f.d;
  if (!isOne(initial0.reduce(fracAdd, ZERO)) || !isOne(initial1.reduce(fracAdd, ZERO))) {
    throw new Error("initial distributions must sum to 1/1");
  }

  const multiplier = parseFraction(spec.multiplier);
  if (!fracGE(multiplier, ONE)) throw new Error("multiplier L must be >= 1/1");
  let delta: Fraction | undefined;
  if (spec.delta !== undefined) {
    delta = parseFraction(spec.delta);
    if (delta.n < 0n) throw new Error("delta must be non-negative");
  }

  if (spec.tables.length !== 2) throw new Error("tables must contain exactly two sides");
  const table: AnswerDist[][][] = [[], []];
  for (let side = 0; side < 2; side++) {
    const sideTable = spec.tables[side];
    for (let qi = 0; qi < nQ; qi++) {
      const perState: AnswerDist[] = [];
      const queryRows = sideTable[spec.queries[qi]];
      if (!queryRows) throw new Error("missing query " + spec.queries[qi]);
      for (let si = 0; si < nS; si++) {
        const entries = queryRows[spec.states[si]];
        if (!entries) throw new Error("missing rows for state " + spec.states[si]);
        const merged: AnswerDist[] = new Array(nA);
        for (let ai = 0; ai < nA; ai++) {
          merged[ai] = { total: ZERO, next: new Array(nS).fill(ZERO) };
        }
        let rowSum = ZERO;
        for (const entry of entries) {
          const ai = answerIndex.get(entry.answer);
          if (ai === undefined) throw new Error("unknown answer: " + entry.answer);
          const next = stateIndex.get(entry.nextState);
          if (next === undefined) throw new Error("unknown nextState: " + entry.nextState);
          const p = readWeight(entry.probability);
          rowSum = fracAdd(rowSum, p);
          const dist = merged[ai];
          merged[ai] = {
            total: fracAdd(dist.total, p),
            next: dist.next.map((v, i) => (i === next ? fracAdd(v, p) : v)),
          };
        }
        if (!isOne(rowSum)) {
          throw new Error("transition probabilities must sum to 1/1");
        }
        perState.push(merged);
      }
      table[side].push(perState);
    }
  }

  const stop = prepareStop(spec.stopHistories, queryIndex, answerIndex);
  return {
    spec, stateCount: nS, answerCount: nA, queryCount: nQ,
    allowed, initial0, initial1, multiplier, delta, table, stop,
  };
}

function prepareStop(
  histories: readonly (readonly (readonly [string, string])[]),
  queryIndex: ReadonlyMap<string, number>,
  answerIndex: ReadonlyMap<string, number>,
): PreparedStop {
  if (!Array.isArray(histories)) throw new Error("stopHistories must be a list");
  // Trie of stop histories plus a never-stop sink. Tokens are "queryIndex:answerIndex".
  const children = new Map<string, Map<string, string>>();
  const accepting = new Set<string>();
  const sink = "sink";
  children.set("t0", new Map());
  children.set(sink, new Map());
  for (const history of histories) {
    if (!Array.isArray(history)) throw new Error("each stop history must be a list");
    let node = "t0";
    for (const [q, a] of history) {
      const qi = queryIndex.get(q);
      const ai = answerIndex.get(a);
      if (qi === undefined) throw new Error("unknown stop query: " + q);
      if (ai === undefined) throw new Error("unknown stop answer: " + a);
      const token = qi + ":" + ai;
      const map = children.get(node)!;
      let nxt = map.get(token);
      if (nxt === undefined) {
        nxt = "t" + (children.size - 1);
        children.set(nxt, new Map());
        map.set(token, nxt);
      }
      node = nxt;
    }
    accepting.add(node);
  }
  return { initial: "t0", accepting, transitions: children };
}

function automatonWire(stop: PreparedStop): StopAutomaton {
  const transitions: Record<string, Record<string, string>> = {};
  for (const [node, map] of stop.transitions) transitions[node] = Object.fromEntries(map);
  return { initial: stop.initial, accepting: [...stop.accepting], transitions };
}

// ---------------------------------------------------------------------------
// Belief-state dynamic programming
// ---------------------------------------------------------------------------

// Normalized subproblem: conditional distributions given the public prefix,
// i.e. both masses equal 1/1. Absolute masses enter only through the linear
// edge combination at the parent, so identical posteriors share one node no
// matter which transcript reached them.
interface NormState {
  readonly round: number;
  readonly stopNode: string;
  readonly belief0: Fraction[];
  readonly belief1: Fraction[];
}

interface DpResult {
  readonly value01: Fraction;
  readonly value10: Fraction;
  readonly node: CertificateNode;
}

function stateKey(s: NormState): string {
  return [
    s.round,
    s.stopNode,
    ...s.belief0.map(fracWire),
    ...s.belief1.map(fracWire),
  ].join("|");
}

export function admissionWorstCase(spec: MechanismSpec): AdmissionResult {
  const prep = prepareSpec(spec);
  const nS = prep.stateCount;
  const memo = new Map<string, DpResult>();
  const zeroBelief: Fraction[] = new Array(nS).fill(ZERO);

  function stopChild(node: string, qi: number, ai: number): string {
    const map = prep.stop.transitions.get(node)!;
    return map.get(qi + ":" + ai) ?? "sink";
  }

  function dp(state: NormState): DpResult {
    const key = stateKey(state);
    const cached = memo.get(key);
    if (cached) return cached;

    // A state reached by a stop-triggering transcript ends immediately:
    // stopping is itself the visible terminal event.
    if (prep.stop.accepting.has(state.stopNode)) {
      const node: CertificateNode = {
        round: state.round,
        stopNode: state.stopNode,
        mass0: fracWire(ONE),
        mass1: fracWire(ONE),
        belief0: state.belief0.map(fracWire),
        belief1: state.belief1.map(fracWire),
        terminal: true,
        value01: fracWire(fracMax0(fracSub(ONE, prep.multiplier))),
        value10: fracWire(ZERO),
      };
      const result = {
        value01: fracMax0(fracSub(ONE, prep.multiplier)),
        value10: ZERO,
        node,
      };
      memo.set(key, result);
      return result;
    }

    const branches: Record<string, CertificateEdge[]> = {};
    const queryValues: { qi: number; v01: Fraction; v10: Fraction }[] = [];
    const lastRound = state.round + 1 >= prep.spec.rounds;

    for (const qi of prep.allowed[state.round]) {
      const edges: CertificateEdge[] = [];
      let q01 = ZERO;
      let q10 = ZERO;
      for (let ai = 0; ai < prep.answerCount; ai++) {
        let p0 = ZERO;
        let p1 = ZERO;
        const joint0 = new Array<Fraction>(nS).fill(ZERO);
        const joint1 = new Array<Fraction>(nS).fill(ZERO);
        for (let si = 0; si < nS; si++) {
          const d0 = prep.table[0][qi][si][ai];
          const d1 = prep.table[1][qi][si][ai];
          p0 = fracAdd(p0, fracMul(state.belief0[si], d0.total));
          p1 = fracAdd(p1, fracMul(state.belief1[si], d1.total));
          for (let ni = 0; ni < nS; ni++) {
            joint0[ni] = fracAdd(joint0[ni], fracMul(state.belief0[si], d0.next[ni]));
            joint1[ni] = fracAdd(joint1[ni], fracMul(state.belief1[si], d1.next[ni]));
          }
        }
        if (p0.n === 0n && p1.n === 0n) continue;
        const belief0 = p0.n === 0n ? zeroBelief.slice() : joint0.map((j) => fracDiv(j, p0));
        const belief1 = p1.n === 0n ? zeroBelief.slice() : joint1.map((j) => fracDiv(j, p1));
        const nextStop = stopChild(state.stopNode, qi, ai);
        const childState: NormState = {
          round: state.round + 1,
          stopNode: nextStop,
          belief0,
          belief1,
        };
        const childStops = lastRound || prep.stop.accepting.has(nextStop);
        const edge01 = fracMax0(fracSub(p0, fracMul(prep.multiplier, p1)));
        const edge10 = fracMax0(fracSub(p1, fracMul(prep.multiplier, p0)));
        let v01: Fraction;
        let v10: Fraction;
        if (childStops) {
          // The transcript terminates on this answer: the divergence term is
          // counted here exactly once, weighted by the edge mass.
          v01 = edge01;
          v10 = edge10;
        } else {
          const child = dp(childState);
          v01 = fracMul(p0, child.value01);
          v10 = fracMul(p1, child.value10);
        }
        q01 = fracAdd(q01, v01);
        q10 = fracAdd(q10, v10);
        edges.push({
          answer: prep.spec.answers[ai],
          prob0: fracWire(p0),
          prob1: fracWire(p1),
          childKey: childStops ? undefined : stateKey(childState),
        });
      }
      branches[prep.spec.queries[qi]] = edges;
      queryValues.push({ qi, v01: q01, v10: q10 });
    }

    let best01 = queryValues[0];
    let best10 = queryValues[0];
    for (const qv of queryValues) {
      if (!fracGE(best01.v01, qv.v01)) best01 = qv;
      if (!fracGE(best10.v10, qv.v10)) best10 = qv;
    }
    const node: CertificateNode = {
      round: state.round,
      stopNode: state.stopNode,
      mass0: fracWire(ONE),
      mass1: fracWire(ONE),
      belief0: state.belief0.map(fracWire),
      belief1: state.belief1.map(fracWire),
      terminal: false,
      value01: fracWire(best01.v01),
      value10: fracWire(best10.v10),
      branches,
      chosen01: prep.spec.queries[best01.qi],
      chosen10: prep.spec.queries[best10.qi],
    };
    const result = { value01: best01.v01, value10: best10.v10, node };
    memo.set(key, result);
    return result;
  }

  const root: NormState = {
    round: 0,
    stopNode: prep.stop.initial,
    belief0: prep.initial0.slice(),
    belief1: prep.initial1.slice(),
  };
  const rootResult = dp(root);
  const rootKey = stateKey(root);
  const nodes: Record<string, CertificateNode> = {};
  for (const [key, value] of memo) nodes[key] = value.node;

  const certificate: Certificate = {
    version: 1,
    multiplier: spec.multiplier,
    rounds: prep.spec.rounds,
    stopAutomaton: automatonWire(prep.stop),
    nodes,
    rootKey,
  };
  const worst = fracGE(rootResult.value01, rootResult.value10)
    ? rootResult.value01
    : rootResult.value10;
  return {
    admitted: prep.delta === undefined ? true : fracGE(prep.delta, worst),
    worstDivergence: fracWire(worst),
    direction01: fracWire(rootResult.value01),
    direction10: fracWire(rootResult.value10),
    multiplier: spec.multiplier,
    rounds: prep.spec.rounds,
    distinctStates: memo.size,
    certificate,
  };
}

// ---------------------------------------------------------------------------
// Independent certificate verification
// ---------------------------------------------------------------------------

export interface VerificationReport {
  readonly valid: boolean;
  readonly errors: readonly string[];
  readonly rootValue01?: string;
  readonly rootValue10?: string;
}

export function verifyCertificate(
  spec: MechanismSpec,
  certificate: Certificate,
): VerificationReport {
  const errors: string[] = [];
  const fail = (message: string): void => { errors.push(message); };

  let prep: PreparedSpec;
  try {
    prep = prepareSpec(spec);
  } catch (err) {
    return { valid: false, errors: ["invalid spec: " + (err as Error).message] };
  }

  if (certificate.version !== 1) fail("unsupported certificate version");
  if (certificate.multiplier !== spec.multiplier) fail("multiplier mismatch");
  if (certificate.rounds !== spec.rounds) fail("rounds mismatch");

  const auto = certificate.stopAutomaton;
  if (!auto || auto.initial !== prep.stop.initial) fail("stop automaton initial mismatch");
  const acceptSet = new Set(auto.accepting);
  if (acceptSet.size !== auto.accepting.length) fail("duplicate accepting stop nodes");
  for (const node of prep.stop.accepting) {
    if (!acceptSet.has(node)) fail("missing accepting stop node " + node);
  }
  for (const node of acceptSet) {
    if (!prep.stop.accepting.has(node)) fail("unexpected accepting stop node " + node);
  }
  if (!auto.transitions || !auto.transitions[prep.stop.initial]) {
    fail("stop automaton missing initial transitions");
  }
  for (const [node, edges] of Object.entries(auto.transitions ?? {})) {
    const expected = prep.stop.transitions.get(node);
    if (!expected) { fail("unknown stop automaton node " + node); continue; }
    if (Object.keys(edges).length !== expected.size) {
      fail("stop automaton edge count mismatch at " + node);
    }
    for (const [token, target] of Object.entries(edges)) {
      if (expected.get(token) !== target) fail("stop automaton edge mismatch at " + node);
    }
  }

  const nodes = certificate.nodes;
  if (!nodes || !(certificate.rootKey in nodes)) fail("root key absent from certificate");

  const nS = prep.stateCount;
  const parseList = (values: readonly string[] | undefined, label: string): Fraction[] => {
    if (!values || values.length !== nS) { fail(label + " must align with states"); return []; }
    return values.map((text) => parseFraction(text));
  };
  const expectFrac = (actual: string, expected: Fraction, label: string): void => {
    let parsed: Fraction;
    try { parsed = parseFraction(actual); } catch { fail(label + " is not a canonical fraction"); return; }
    if (parsed.n !== expected.n || parsed.d !== expected.d) {
      fail(label + " mismatch: certificate " + actual + " expected " + fracWire(expected));
    }
  };
  const expectKey = (actual: string, expected: string, label: string): void => {
    if (actual !== expected) fail(label + " key mismatch");
  };

  const visited = new Set<string>();
  const queue = [certificate.rootKey];
  const rootState: NormState = {
    round: 0,
    stopNode: prep.stop.initial,
    belief0: prep.initial0.slice(),
    belief1: prep.initial1.slice(),
  };
  const stateByKey = new Map<string, NormState>([[certificate.rootKey, rootState]]);

  while (queue.length > 0) {
    const key = queue.shift()!;
    if (visited.has(key)) continue;
    visited.add(key);
    const node = nodes[key];
    if (!node) { fail("missing certificate node " + key); continue; }
    const state = stateByKey.get(key)!;
    if (node.round !== state.round) fail("round mismatch at " + key);
    if (node.stopNode !== state.stopNode) fail("stopNode mismatch at " + key);
    expectFrac(node.mass0, ONE, "mass0 at " + key);
    expectFrac(node.mass1, ONE, "mass1 at " + key);
    const belief0 = parseList(node.belief0, "belief0 at " + key);
    const belief1 = parseList(node.belief1, "belief1 at " + key);
    if (belief0.length === nS) {
      belief0.forEach((_, i) => expectFrac(node.belief0[i], state.belief0[i], "belief0 at " + key));
    }
    if (belief1.length === nS) {
      belief1.forEach((_, i) => expectFrac(node.belief1[i], state.belief1[i], "belief1 at " + key));
    }

    const terminal = prep.stop.accepting.has(state.stopNode);
    if (node.terminal !== terminal) fail("terminal flag mismatch at " + key);

    if (terminal) {
      expectFrac(node.value01, fracMax0(fracSub(ONE, prep.multiplier)), "terminal value01 at " + key);
      expectFrac(node.value10, ZERO, "terminal value10 at " + key);
      if (node.branches || node.chosen01 !== undefined || node.chosen10 !== undefined) {
        fail("terminal node must not carry branches at " + key);
      }
      continue;
    }

    if (!node.branches) { fail("non-terminal node missing branches at " + key); continue; }
    const queryValues: { query: string; v01: Fraction; v10: Fraction }[] = [];
    for (const qi of prep.allowed[state.round]) {
      const query = prep.spec.queries[qi];
      const edges = node.branches[query];
      if (!edges) { fail("missing branches for query " + query + " at " + key); continue; }
      let v01 = ZERO;
      let v10 = ZERO;
      const seenAnswers = new Set<string>();
      let edgeIndex = 0;
      for (let ai = 0; ai < prep.answerCount; ai++) {
        let p0 = ZERO;
        let p1 = ZERO;
        const joint0 = new Array<Fraction>(nS).fill(ZERO);
        const joint1 = new Array<Fraction>(nS).fill(ZERO);
        for (let si = 0; si < nS; si++) {
          const d0 = prep.table[0][qi][si][ai];
          const d1 = prep.table[1][qi][si][ai];
          p0 = fracAdd(p0, fracMul(state.belief0[si], d0.total));
          p1 = fracAdd(p1, fracMul(state.belief1[si], d1.total));
          for (let ni = 0; ni < nS; ni++) {
            joint0[ni] = fracAdd(joint0[ni], fracMul(state.belief0[si], d0.next[ni]));
            joint1[ni] = fracAdd(joint1[ni], fracMul(state.belief1[si], d1.next[ni]));
          }
        }
        if (p0.n === 0n && p1.n === 0n) continue;
        const edge = edges[edgeIndex++];
        if (!edge) { fail("missing edge at " + key + " " + query); break; }
        if (seenAnswers.has(edge.answer)) fail("duplicate edge answer at " + key);
        seenAnswers.add(edge.answer);
        if (edge.answer !== prep.spec.answers[ai]) {
          fail("edge answer/order mismatch at " + key + " " + query);
        }
        expectFrac(edge.prob0, p0, "edge prob0 at " + key);
        expectFrac(edge.prob1, p1, "edge prob1 at " + key);

        const childBelief0 = p0.n === 0n
          ? new Array<Fraction>(nS).fill(ZERO)
          : joint0.map((j) => fracDiv(j, p0));
        const childBelief1 = p1.n === 0n
          ? new Array<Fraction>(nS).fill(ZERO)
          : joint1.map((j) => fracDiv(j, p1));
        const stopMap = prep.stop.transitions.get(state.stopNode)!;
        const nextStop = stopMap.get(qi + ":" + ai) ?? "sink";
        const childState: NormState = {
          round: state.round + 1,
          stopNode: nextStop,
          belief0: childBelief0,
          belief1: childBelief1,
        };
        const edge01 = fracMax0(fracSub(p0, fracMul(prep.multiplier, p1)));
        const edge10 = fracMax0(fracSub(p1, fracMul(prep.multiplier, p0)));
        const childStops = state.round + 1 >= prep.spec.rounds ||
          prep.stop.accepting.has(nextStop);
        if (childStops) {
          if (edge.childKey !== undefined) fail("stopping edge must omit childKey at " + key);
          v01 = fracAdd(v01, edge01);
          v10 = fracAdd(v10, edge10);
          continue;
        }
        const childKey = stateKey(childState);
        expectKey(edge.childKey ?? "", childKey, "edge child at " + key);
        stateByKey.set(childKey, childState);
        queue.push(childKey);
        const childNode = nodes[childKey];
        if (childNode) {
          const cv01 = parseFraction(childNode.value01);
          const cv10 = parseFraction(childNode.value10);
          v01 = fracAdd(v01, fracMul(p0, cv01));
          v10 = fracAdd(v10, fracMul(p1, cv10));
        } else {
          fail("child node absent from certificate: " + childKey);
        }
      }
      if (edgeIndex !== edges.length) fail("unexpected extra edges at " + key + " " + query);
      queryValues.push({ query, v01, v10 });
    }
    if (Object.keys(node.branches).length !== queryValues.length) {
      fail("branches contain queries outside allowed set at " + key);
    }
    const best01 = queryValues.reduce((a, b) => (fracGE(a.v01, b.v01) ? a : b));
    const best10 = queryValues.reduce((a, b) => (fracGE(a.v10, b.v10) ? a : b));
    expectFrac(node.value01, best01.v01, "value01 at " + key);
    expectFrac(node.value10, best10.v10, "value10 at " + key);
    if (node.chosen01 !== best01.query) fail("chosen01 not attaining value at " + key);
    if (node.chosen10 !== best10.query) fail("chosen10 not attaining value at " + key);
  }

  if (visited.size !== Object.keys(nodes).length) {
    fail("certificate contains unreachable nodes");
  }
  const rootNode = nodes[certificate.rootKey];
  if (errors.length === 0 && rootNode) {
    return {
      valid: true,
      errors: [],
      rootValue01: rootNode.value01,
      rootValue10: rootNode.value10,
    };
  }
  return { valid: false, errors };
}

export interface AdmissionDecision {
  readonly admitted: boolean;
  readonly worstDivergence: string;
  readonly budget?: string;
  readonly verification: VerificationReport;
}

// One-call entry point for release hosts: solve, verify the produced
// certificate independently, then compare against the optional budget.
export function assessAdmission(spec: MechanismSpec): AdmissionDecision {
  const result = admissionWorstCase(spec);
  const verification = verifyCertificate(spec, result.certificate);
  if (!verification.valid) {
    throw new Error("internal certificate verification failed: " + verification.errors.join("; "));
  }
  const budget = spec.delta;
  const admitted = budget === undefined
    ? true
    : fracGE(parseFraction(budget), parseFraction(result.worstDivergence));
  return {
    admitted,
    worstDivergence: result.worstDivergence,
    budget,
    verification,
  };
}
