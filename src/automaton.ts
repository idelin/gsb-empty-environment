import type {
  CandidateWitness,
  DfaEdge,
  Scalar,
  ScalarAutomaton,
} from "./types.ts";
import type { ConfusableProgram } from "./confusables.ts";
import { codePoints, domainScalars, foldAsciiCase } from "./unicode.ts";

export interface CandidateSearchOptions {
  /** Candidate domain length cap in Unicode scalars, at most 64. */
  readonly maxScalars?: number;
  readonly maxStates?: number;
  readonly provenDomains?: Iterable<string>;
  /** Filter accepted candidates that are not "allowed domains". */
  readonly isAllowedDomain?: (domain: string, scalars: readonly Scalar[]) => boolean;
}

export interface CandidateSearchResult {
  readonly ok: boolean;
  readonly truncated: boolean;
  readonly targetSkeleton: readonly number[];
  readonly provenDomains: readonly string[];
  readonly acceptedSameSkeleton: number;
  readonly provenSameSkeleton: number;
  readonly witness: CandidateWitness | null;
  readonly exploredStates: number;
  readonly reason: string;
}

export interface AcceptedWordsResult {
  readonly words: readonly string[];
  readonly truncated: boolean;
}

export function assertDfa(dfa: ScalarAutomaton): void {
  if (!dfa || !Number.isInteger(dfa.initial) || dfa.initial < 0) {
    throw new TypeError("automaton needs a non-negative integer initial state");
  }
  if (!Array.isArray(dfa.accepting) || !Array.isArray(dfa.edges)) {
    throw new TypeError("automaton accepting and edges must be arrays");
  }
  let maxState = dfa.initial;
  for (const state of dfa.accepting) {
    if (!Number.isInteger(state) || state < 0) {
      throw new TypeError("automaton accepting states must be non-negative integers");
    }
    maxState = Math.max(maxState, state);
  }

  const byFrom = new Map<number, DfaEdge[]>();
  for (const edge of dfa.edges) {
    if (
      !Number.isInteger(edge.from) ||
      edge.from < 0 ||
      !Number.isInteger(edge.to) ||
      edge.to < 0
    ) {
      throw new TypeError("automaton edge states must be non-negative integers");
    }
    if (
      !Number.isInteger(edge.start) ||
      !Number.isInteger(edge.end) ||
      edge.start < 0 ||
      edge.end > 0x10ffff ||
      edge.start > edge.end
    ) {
      throw new TypeError(
        `automaton edge ${edge.from}->${edge.to} has an invalid scalar range`,
      );
    }
    maxState = Math.max(maxState, edge.from, edge.to);
    const list = byFrom.get(edge.from);
    if (list) list.push(edge);
    else byFrom.set(edge.from, [edge]);
  }

  if (dfa.stateCount !== undefined) {
    if (!Number.isInteger(dfa.stateCount) || dfa.stateCount <= maxState) {
      throw new TypeError("automaton stateCount does not cover all states");
    }
  }

  for (const [from, list] of byFrom) {
    list.sort((a, b) => a.start - b.start || a.end - b.end);
    for (let i = 1; i < list.length; i++) {
      if (list[i].start <= list[i - 1].end) {
        throw new TypeError(
          `automaton is not deterministic: overlapping edges from state ${from}`,
        );
      }
    }
  }
}

export function normalizeDfa(dfa: ScalarAutomaton): ScalarAutomaton {
  assertDfa(dfa);
  const byFrom = new Map<number, DfaEdge[]>();
  for (const edge of dfa.edges) {
    const list = byFrom.get(edge.from);
    if (list) list.push(edge);
    else byFrom.set(edge.from, [edge]);
  }
  const edges: DfaEdge[] = [];
  for (const list of byFrom.values()) {
    list.sort((a, b) => a.start - b.start || a.end - b.end);
    let current = list[0];
    for (let i = 1; i < list.length; i++) {
      const edge = list[i];
      if (current.to === edge.to && edge.start <= current.end + 1) {
        current = {
          from: current.from,
          to: current.to,
          start: current.start,
          end: Math.max(current.end, edge.end),
        };
      } else {
        edges.push(current);
        current = edge;
      }
    }
    edges.push(current);
  }
  edges.sort(
    (a, b) =>
      a.from - b.from || a.start - b.start || a.to - b.to || a.end - b.end,
  );
  return {
    initial: dfa.initial,
    accepting: [...new Set(dfa.accepting)].sort((a, b) => a - b),
    edges,
    ...(dfa.stateCount !== undefined ? { stateCount: dfa.stateCount } : {}),
  };
}

/** Build a small deterministic automaton from an explicit finite word set. */
export function dfaFromWords(words: Iterable<string>): ScalarAutomaton {
  const transitions = new Map<number, Map<number, number>>();
  const accepting = new Set<number>();
  let nextState = 1;

  for (const word of words) {
    let state = 0;
    for (const scalar of codePoints(word)) {
      let edges = transitions.get(state);
      if (!edges) {
        edges = new Map();
        transitions.set(state, edges);
      }
      let target = edges.get(scalar);
      if (target === undefined) {
        target = nextState++;
        edges.set(scalar, target);
      }
      state = target;
    }
    accepting.add(state);
  }

  const edges: DfaEdge[] = [];
  for (const [from, map] of transitions) {
    for (const [scalar, to] of map) {
      edges.push({ from, to, start: scalar, end: scalar });
    }
  }
  const dfa: ScalarAutomaton = {
    initial: 0,
    accepting: [...accepting],
    edges,
    stateCount: nextState,
  };
  return normalizeDfa(dfa);
}

function edgesByState(dfa: ScalarAutomaton): Map<number, readonly DfaEdge[]> {
  const map = new Map<number, DfaEdge[]>();
  for (const edge of dfa.edges) {
    const list = map.get(edge.from);
    if (list) list.push(edge);
    else map.set(edge.from, [edge]);
  }
  for (const list of map.values()) {
    list.sort((a, b) => a.start - b.start);
  }
  return map;
}

/**
 * Enumerate accepted words.  This is intentionally only used by the checker
 * and by tests; the decision path uses the symbolic product search below.
 */
export function enumerateAcceptedWords(
  dfa: ScalarAutomaton,
  maxScalars: number,
  limit = 10000,
): AcceptedWordsResult {
  assertDfa(dfa);
  const depthLimit = Number.isFinite(maxScalars)
    ? Math.max(0, Math.floor(maxScalars))
    : 64;
  const wordLimit = Number.isFinite(limit)
    ? Math.max(1, Math.floor(limit))
    : 10000;
  const byState = edgesByState(dfa);
  const accepting = new Set(dfa.accepting);
  const words: string[] = [];
  let truncated = false;
  const budget = Math.max(wordLimit * 16, 1000);
  let inspected = 0;

  const walk = (state: number, path: number[]): void => {
    if (truncated) return;
    if (accepting.has(state)) {
      if (words.length >= wordLimit) {
        truncated = true;
        return;
      }
      words.push(String.fromCodePoint(...path));
    }
    if (path.length >= depthLimit) return;
    for (const edge of byState.get(state) ?? []) {
      for (let scalar = edge.start; scalar <= edge.end; scalar++) {
        if (scalar >= 0xd800 && scalar <= 0xdfff) continue;
        if (inspected++ >= budget) {
          truncated = true;
          return;
        }
        path.push(scalar);
        walk(edge.to, path);
        path.pop();
        if (truncated) return;
      }
    }
  };

  walk(dfa.initial, []);
  return { words, truncated };
}

interface WordTrie {
  readonly children: Array<Map<number, number>>;
  readonly terminal: boolean[];
}

function buildWordTrie(domains: Iterable<string>): WordTrie {
  const children: Array<Map<number, number>> = [new Map()];
  const terminal: boolean[] = [false];
  for (const domain of domains) {
    let node = 0;
    for (const scalar of domainScalars(domain)) {
      const folded = foldAsciiCase(scalar);
      let next = children[node].get(folded);
      if (next === undefined) {
        next = children.length;
        children.push(new Map());
        terminal.push(false);
        children[node].set(folded, next);
      }
      node = next;
    }
    terminal[node] = true;
  }
  return { children, terminal };
}

function matchesPrefix(
  target: readonly number[],
  position: number,
  emitted: readonly number[],
): boolean {
  if (position + emitted.length > target.length) return false;
  for (let i = 0; i < emitted.length; i++) {
    if (target[position + i] !== emitted[i]) return false;
  }
  return true;
}

function interestingScalars(
  program: ConfusableProgram,
  target: readonly number[],
): number[] {
  const values = new Set<number>();
  for (const rule of program.table.rules) {
    for (const scalar of rule.consumes) values.add(scalar);
  }
  for (const symbol of target) values.add(symbol);
  return [...values].sort((a, b) => a - b);
}

function lowerBound(values: readonly number[], target: number): number {
  let low = 0;
  let high = values.length;
  while (low < high) {
    const middle = (low + high) >>> 1;
    if (values[middle] < target) low = middle + 1;
    else high = middle;
  }
  return low;
}

interface SearchNode {
  readonly fa: number;
  readonly pending: readonly Scalar[];
  readonly targetPos: number;
  readonly trie: number;
  readonly depth: number;
  readonly parent: SearchNode | null;
  readonly scalar: Scalar | null;
}

function reconstruct(node: SearchNode): Scalar[] {
  const scalars: Scalar[] = [];
  let current: SearchNode | null = node;
  while (current && current.parent) {
    scalars.push(current.scalar as Scalar);
    current = current.parent;
  }
  scalars.reverse();
  return scalars;
}

/**
 * Search the product of the candidate automaton, the streaming skeleton
 * matcher and a trie of proven alias domains.  It never enumerates the name
 * table: only algorithmically "interesting" scalars (rule inputs and target
 * skeleton symbols) can possibly stay on a matching path.
 */
export function searchCandidates(
  dfa: ScalarAutomaton,
  program: ConfusableProgram,
  targetSkeleton: readonly number[],
  options: CandidateSearchOptions = {},
): CandidateSearchResult {
  assertDfa(dfa);
  const requestedMaxScalars = options.maxScalars ?? 64;
  const requestedMaxStates = options.maxStates ?? 1_000_000;
  const maxScalars = Math.max(
    0,
    Math.min(
      64,
      Number.isFinite(requestedMaxScalars)
        ? Math.floor(requestedMaxScalars)
        : 64,
    ),
  );
  const maxStates = Math.max(
    1,
    Number.isFinite(requestedMaxStates)
      ? Math.floor(requestedMaxStates)
      : 1_000_000,
  );
  const proven = [...new Set(options.provenDomains ?? [])].sort();
  const trie = buildWordTrie(proven);
  const accepting = new Set(dfa.accepting);
  const byState = edgesByState(dfa);
  const interesting = interestingScalars(program, targetSkeleton);
  const target = targetSkeleton.slice();

  let acceptedSameSkeleton = 0;
  let provenSameSkeleton = 0;
  let exploredStates = 0;
  let truncated = false;
  let witness: CandidateWitness | null = null;

  const makeWitness = (node: SearchNode): CandidateWitness => {
    const scalars = reconstruct(node);
    return {
      domain: String.fromCodePoint(...scalars),
      scalars,
      skeleton: target,
      tableVersion: program.table.version,
    };
  };

  const evaluateAcceptance = (node: SearchNode): boolean => {
    if (!accepting.has(node.fa)) return false;
    const flushed = program.flush(node.pending);
    if (!matchesPrefix(target, node.targetPos, flushed)) return false;
    if (node.targetPos + flushed.length !== target.length) return false;
    if (options.isAllowedDomain) {
      const scalars = reconstruct(node);
      const domain = String.fromCodePoint(...scalars);
      if (!options.isAllowedDomain(domain, scalars)) return false;
    }
    acceptedSameSkeleton += 1;
    const isProven =
      node.trie >= 0 && trie.terminal[node.trie] === true;
    if (isProven) {
      provenSameSkeleton += 1;
      return false;
    }
    witness = makeWitness(node);
    return true;
  };

  const initial: SearchNode = {
    fa: dfa.initial,
    pending: [],
    targetPos: 0,
    trie: 0,
    depth: 0,
    parent: null,
    scalar: null,
  };

  if (evaluateAcceptance(initial)) {
    return {
      ok: false,
      truncated: false,
      targetSkeleton: target,
      provenDomains: proven,
      acceptedSameSkeleton,
      provenSameSkeleton,
      witness,
      exploredStates: 1,
      reason: "unproven-candidate",
    };
  }

  const queue: SearchNode[] = [initial];
  const seen = new Set<string>();
  seen.add(`0|${dfa.initial}|0|0|`);
  exploredStates = seen.size;

  for (let head = 0; head < queue.length; head++) {
    const node = queue[head];
    if (node.depth >= maxScalars) continue;

    for (const edge of byState.get(node.fa) ?? []) {
      let index = lowerBound(interesting, edge.start);
      while (
        index < interesting.length &&
        interesting[index] <= edge.end
      ) {
        const scalar = interesting[index++];
        const step = program.step(node.pending, scalar);
        if (!matchesPrefix(target, node.targetPos, step.emitted)) continue;

        const folded = foldAsciiCase(scalar);
        const trieNext =
          node.trie >= 0
            ? (trie.children[node.trie].get(folded) ?? -1)
            : -1;
        const next: SearchNode = {
          fa: edge.to,
          pending: step.pending,
          targetPos: node.targetPos + step.emitted.length,
          trie: trieNext,
          depth: node.depth + 1,
          parent: node,
          scalar,
        };
        const key = `${next.depth}|${next.fa}|${next.targetPos}|${next.trie}|${next.pending.join(",")}`;
        if (seen.has(key)) continue;
        seen.add(key);
        exploredStates = seen.size;
        if (exploredStates > maxStates) {
          truncated = true;
          break;
        }
        queue.push(next);
        if (evaluateAcceptance(next)) break;
      }
      if (witness || truncated) break;
    }
    if (witness || truncated) break;
  }

  const ok = witness === null && !truncated;
  return {
    ok,
    truncated,
    targetSkeleton: target,
    provenDomains: proven,
    acceptedSameSkeleton,
    provenSameSkeleton,
    witness,
    exploredStates,
    reason: truncated
      ? "state-limit"
      : witness
        ? "unproven-candidate"
        : acceptedSameSkeleton === 0
          ? "no-same-skeleton-candidates"
          : "all-candidates-proven",
  };
}
