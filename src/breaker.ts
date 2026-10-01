import { breakPositions, measureLine } from './nodes.ts';
import { lineCost, type LineCost } from './cost.ts';
import type { BreakOptions, Node } from './types.ts';

/**
 * DP state. The history constraints live IN the state key:
 *  - hyphens:        hyphenated lines used so far (cross-line budget)
 *  - prevHyphenated: previous line ended in a hyphen (no-consecutive rule)
 *  - prevWidth:      previous line natural width (adjacent width-diff penalty)
 */
export interface DPState {
  cost: number;
  hyphens: number;
  prevHyphenated: boolean;
  prevWidth: number | null;
  /** break positions taken so far, ending at this state's position */
  path: number[];
}

export type StateMap = Map<string, DPState>;

export function stateKey(s: {
  hyphens: number;
  prevHyphenated: boolean;
  prevWidth: number | null;
}): string {
  return `${s.hyphens}|${s.prevHyphenated ? 1 : 0}|${s.prevWidth ?? 'start'}`;
}

/** Lexicographic comparison of break-position paths (earlier breaks win). */
export function comparePaths(a: number[], b: number[]): number {
  const n = Math.min(a.length, b.length);
  for (let i = 0; i < n; i++) {
    if (a[i] !== b[i]) return a[i] - b[i];
  }
  return a.length - b.length;
}

export interface FrontierResult {
  /** frontier[k] holds the states whose path ends at positions[k] */
  frontier: StateMap[];
  positions: number[];
  relaxations: number;
  budgetExhausted: boolean;
}

/**
 * Forward DP over break positions. Reuses `prior` frontier entries for
 * positions[0 .. fromIndex-1] (incremental editing); computes the rest.
 *
 * When `reexpandFromIndex` is set, positions[reexpandFromIndex .. fromIndex-1]
 * are ALSO expanded, but only toward breaks beyond positions[fromIndex]:
 * lines starting before an edit may cross into edited content, while lines
 * ending at or before it were already relaxed correctly in the prior run.
 */
export function computeFrontier(
  nodes: Node[],
  options: BreakOptions,
  positions: number[],
  fromIndex: number,
  initial: StateMap,
  prior: StateMap[] = [],
  reexpandFromIndex?: number,
): FrontierResult {
  const frontier: StateMap[] = positions.map((_, k) =>
    k < fromIndex ? prior[k] : new Map<string, DPState>(),
  );
  frontier[fromIndex] = initial;
  const startIndex = reexpandFromIndex ?? fromIndex;
  const reuseLimit = positions[fromIndex];
  let relaxations = 0;
  let budgetExhausted = false;

  outer: for (let k = startIndex; k < positions.length; k++) {
    const a = positions[k];
    const states = frontier[k];
    if (!states || states.size === 0) continue;
    for (let m = k + 1; m < positions.length; m++) {
      const b = positions[m];
      if (k < fromIndex && b <= reuseLimit) continue; // already relaxed in prior run
      const line = measureLine(nodes, a, b);
      if (!line.hasContent) continue;
      const isLast = b === nodes.length;
      for (const s of states.values()) {
        if (++relaxations > options.budget) {
          budgetExhausted = true;
          break outer;
        }
        const hyphens = s.hyphens + (line.hyphenated ? 1 : 0);
        if (hyphens > options.maxHyphens) continue;
        if (
          options.forbidConsecutiveHyphens &&
          s.prevHyphenated &&
          line.hyphenated
        ) {
          continue;
        }
        const c = lineCost(line, hyphenPenaltyOf(nodes, b), s.prevWidth, isLast, options);
        const next: DPState = {
          cost: s.cost + c.total,
          hyphens,
          prevHyphenated: line.hyphenated,
          prevWidth: line.naturalWidth,
          path: [...s.path, b],
        };
        const key = stateKey(next);
        const existing = frontier[m].get(key);
        if (
          !existing ||
          next.cost < existing.cost ||
          (next.cost === existing.cost && comparePaths(next.path, existing.path) < 0)
        ) {
          frontier[m].set(key, next);
        }
      }
    }
  }
  return { frontier, positions, relaxations, budgetExhausted };
}

function hyphenPenaltyOf(nodes: Node[], breakPos: number): number {
  const n = nodes[breakPos - 1];
  return n && n.kind === 'penalty' ? n.penalty : 0;
}

export interface LineInfo extends LineCost {
  index: number;
  startBreak: number;
  endBreak: number;
  text: string;
  naturalWidth: number;
  remaining: number;
  hyphenated: boolean;
  atomic: boolean;
  cumulativeCost: number;
}

export interface CandidateEvidence {
  endBreak: number;
  text: string;
  incrementalCost: number;
  cumulativeIfChosen: number;
  chosen: boolean;
  rejectedBy?: 'hyphen-budget' | 'consecutive-hyphen';
}

export interface StepEvidence {
  lineIndex: number;
  fromBreak: number;
  stateBefore: { cost: number; hyphens: number; prevHyphenated: boolean; prevWidth: number | null };
  candidates: CandidateEvidence[];
}

export interface BreakResult {
  status: 'ok' | 'budget-exhausted';
  optimalCost: number | null;
  breaks: number[];
  lines: LineInfo[];
  evidence: StepEvidence[];
  warnings: string[];
  certificate: string[];
  stats: { relaxations: number; states: number };
}

/** Assemble lines, evidence, warnings and a step-by-step certificate. */
export function finalize(
  nodes: Node[],
  options: BreakOptions,
  fr: FrontierResult,
): BreakResult {
  const stats = {
    relaxations: fr.relaxations,
    states: fr.frontier.reduce((n, m) => n + (m ? m.size : 0), 0),
  };
  if (fr.budgetExhausted) {
    return {
      status: 'budget-exhausted',
      optimalCost: null,
      breaks: [],
      lines: [],
      evidence: [],
      warnings: [
        `budget exhausted after ${fr.relaxations} relaxations; result is NOT computed, no optimality claim`,
      ],
      certificate: [],
      stats,
    };
  }
  const finalStates = fr.frontier[fr.frontier.length - 1];
  let best: DPState | null = null;
  for (const s of finalStates.values()) {
    if (!best || s.cost < best.cost || (s.cost === best.cost && comparePaths(s.path, best.path) < 0)) {
      best = s;
    }
  }
  if (!best) throw new Error('no feasible layout (unreachable final state)');
  const breaks = [0, ...best.path];

  const lines: LineInfo[] = [];
  const warnings: string[] = [];
  const certificate: string[] = [];
  let cumulative = 0;
  let prevWidth: number | null = null;
  for (let k = 1; k < breaks.length; k++) {
    const a = breaks[k - 1];
    const b = breaks[k];
    const line = measureLine(nodes, a, b);
    const isLast = b === nodes.length;
    const c = lineCost(line, hyphenPenaltyOf(nodes, b), prevWidth, isLast, options);
    cumulative += c.total;
    const remaining = options.lineWidth - line.naturalWidth;
    lines.push({
      ...c,
      index: k - 1,
      startBreak: a,
      endBreak: b,
      text: line.text,
      naturalWidth: line.naturalWidth,
      remaining,
      hyphenated: line.hyphenated,
      atomic: line.atomic,
      cumulativeCost: cumulative,
    });
    if (c.overflow) {
      warnings.push(
        `line ${k - 1}: overflow, remaining width is negative (${remaining}); ` +
          (line.atomic
            ? 'unbreakable word wider than the line, allowed to overflow'
            : 'shrink insufficient, overfull line'),
      );
    } else if (remaining < 0) {
      warnings.push(`line ${k - 1}: negative remaining width (${remaining}), absorbed by shrink`);
    }
    certificate.push(
      `line ${k - 1}: break ${a} -> ${b} "${line.text}" | badness=${c.badness} ` +
        `hyphen=${c.hyphenCost} widthDiff=${c.widthDiffCost} overflow=${c.overflowCost} ` +
        `=> step=${c.total} cumulative=${cumulative}`,
    );
    prevWidth = line.naturalWidth;
  }
  certificate.push(`total = ${cumulative} (optimal over ${stats.states} reachable states)`);

  const evidence: StepEvidence[] = [];
  for (let k = 1; k < breaks.length; k++) {
    const a = breaks[k - 1];
    const posIndex = fr.positions.indexOf(a);
    const states = fr.frontier[posIndex];
    // The state the optimal path actually passed through at `a`.
    let passed: DPState | null = null;
    for (const s of states.values()) {
      if (s.path.length === k - 1 && s.path.every((p, i) => p === breaks[i + 1])) {
        passed = s;
        break;
      }
    }
    if (!passed) continue;
    const candidates: CandidateEvidence[] = [];
    for (let m = posIndex + 1; m < fr.positions.length; m++) {
      const b = fr.positions[m];
      const line = measureLine(nodes, a, b);
      if (!line.hasContent) continue;
      const hyphens = passed.hyphens + (line.hyphenated ? 1 : 0);
      const isLast = b === nodes.length;
      let rejectedBy: CandidateEvidence['rejectedBy'];
      if (hyphens > options.maxHyphens) rejectedBy = 'hyphen-budget';
      else if (options.forbidConsecutiveHyphens && passed.prevHyphenated && line.hyphenated)
        rejectedBy = 'consecutive-hyphen';
      const c = lineCost(line, hyphenPenaltyOf(nodes, b), passed.prevWidth, isLast, options);
      candidates.push({
        endBreak: b,
        text: line.text,
        incrementalCost: rejectedBy ? Infinity : c.total,
        cumulativeIfChosen: rejectedBy ? Infinity : passed.cost + c.total,
        chosen: b === breaks[k],
        rejectedBy,
      });
    }
    evidence.push({
      lineIndex: k - 1,
      fromBreak: a,
      stateBefore: {
        cost: passed.cost,
        hyphens: passed.hyphens,
        prevHyphenated: passed.prevHyphenated,
        prevWidth: passed.prevWidth,
      },
      candidates,
    });
  }

  return {
    status: 'ok',
    optimalCost: best.cost,
    breaks,
    lines,
    evidence,
    warnings,
    certificate,
    stats,
  };
}

/** One-shot optimal break. */
export function breakParagraph(nodes: Node[], options: BreakOptions): BreakResult {
  const positions = breakPositions(nodes);
  const initial: StateMap = new Map([
    [stateKey({ hyphens: 0, prevHyphenated: false, prevWidth: null }), {
      cost: 0,
      hyphens: 0,
      prevHyphenated: false,
      prevWidth: null,
      path: [],
    }],
  ]);
  const fr = computeFrontier(nodes, options, positions, 0, initial);
  return finalize(nodes, options, fr);
}
