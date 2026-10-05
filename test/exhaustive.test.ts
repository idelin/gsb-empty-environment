/**
 * Differential testing: the engine is compared against the independent
 * brute-force oracle (test/oracle.ts) on
 *   1. a hand-picked pattern corpus heavy on nested empty branches, matched
 *      against every string over {a,b} of length <= 5;
 *   2. deterministically generated random patterns, matched against every
 *      string over {a,b} of length <= 4.
 *
 * The oracle enumerates all match paths from first principles; the host
 * RegExp engine is never consulted.
 */

import { parse } from '../src/ast';
import { compile, Match } from '../src/index';
import { enumerateAll } from '../src/matcher';
import { oracleEnumerateAll, oracleSearch, OracleMatch } from './oracle';
import { test, eq } from './harness';

const HAND_PATTERNS: string[] = [
  '', 'a', 'b', 'ab', 'a|b', 'a|ab', 'ab|a', 'a|b|ab',
  '(a)', '(a)(b)', '(a|b)', '(a|b)*', '((a))', '((a)|(b))*',
  'a*', '(a*)', '(a*)*', '((a*)*)*', 'a**', '(a*)b', '(a*)*b',
  '()*', '(()*)*', '((())*)*', '()|()', '(|)', '(|)*', '((|)*)*',
  '(|a)', '(a|)', '(|a)*', '(a|)*', '(|a|)*', '((|a)*)*', '(a*|)*',
  '((a*|b*)*)*', '(a*b*)*', '(a*)(b*)', '(a*)(a*)', '(a*)(a*)(a*)',
  '(a|ab)(c|bcd)', '(a)*', '(ab|a)*', '(a|ab)*', '(a(b)*)*',
  '(a*)(b*)*', '(a*)*(b*)*', 'a*b', 'b*(a*)b', '(a|b)*b', '(ba|a)*',
  '((a)*)*', '(a(a))*', '((a)|a)*', '(a)|(a)', '(a*)|b', '(a*)|()',
  '(a|b|c)*', '(|a)(a|)', '(a|)(|a)', '((a*b*)*|ab)*', 'b(a|)*b',
  '(a*(ba*|((a|a)*(ab*|aa*))a)*b)*',
];

function allStrings(alphabet: string[], maxLen: number): string[] {
  const out: string[] = [''];
  let level: string[] = [''];
  for (let len = 1; len <= maxLen; len++) {
    const next: string[] = [];
    for (const s of level) {
      for (const c of alphabet) next.push(s + c);
    }
    out.push(...next);
    level = next;
  }
  return out;
}

type Norm = { span: [number, number]; groups: Array<[number, number] | null> } | null;

function normLib(m: Match | null): Norm {
  if (m === null) return null;
  return {
    span: [m.span.start, m.span.end],
    groups: m.groups.slice(1).map((g) => (g === undefined ? null : [g.start, g.end] as [number, number])),
  };
}

function normOracle(m: OracleMatch | null): Norm {
  if (m === null) return null;
  return {
    span: [m.start, m.end],
    groups: m.groups.slice(1).map((g) => (g === null ? null : [g.start, g.end] as [number, number])),
  };
}

/** Canonical string form of one outcome: end + every group span. */
function outcomeKey(end: number, spans: ReadonlyArray<{ start: number; end: number } | null | undefined>): string {
  let key = end + '|';
  for (let i = 1; i < spans.length; i++) {
    const s = spans[i];
    key += s == null ? '-' : s.start + ',' + s.end;
    key += ';';
  }
  return key;
}

function checkPatternAgainstStrings(pattern: string, inputs: string[]): void {
  const parsed = parse(pattern);
  const re = compile(pattern);
  for (const input of inputs) {
    const ctx = `pattern=${JSON.stringify(pattern)} input=${JSON.stringify(input)}`;

    // 1. Selected winner (leftmost-longest + group priority).
    const expected = normOracle(oracleSearch(parsed.root, input, parsed.groupCount));
    const actual = normLib(re.exec(input));
    eq(actual, expected, ctx);

    // 2. The complete sets of match outcomes at every start position.
    const libSets = enumerateAll(parsed.root, input, parsed.groupCount).map((list) =>
      list.map((o) => outcomeKey(o.end, o.captures)).sort(),
    );
    const oracleSets = oracleEnumerateAll(parsed.root, input, parsed.groupCount).map((list) =>
      list.map((o) => outcomeKey(o.end, o.groups)).sort(),
    );
    eq(libSets, oracleSets, `outcome sets ${ctx}`);
  }
}

test('exhaustive: hand-picked patterns x all {a,b} strings of length <= 5', () => {
  const inputs = allStrings(['a', 'b'], 5);
  for (const pattern of HAND_PATTERNS) {
    checkPatternAgainstStrings(pattern, inputs);
  }
});

// --- deterministic random pattern generation -----------------------------

function mulberry32(seed: number): () => number {
  let s = seed;
  return () => {
    s |= 0;
    s = (s + 0x6d2b79f5) | 0;
    let t = Math.imul(s ^ (s >>> 15), 1 | s);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

interface GenBudget {
  starDepth: number;
  groups: number;
}

const MAX_STAR_DEPTH = 2;
const MAX_GROUPS = 6;

function genAlt(rnd: () => number, depth: number, budget: GenBudget): string {
  const branches = 1 + Math.floor(rnd() * 2);
  const parts: string[] = [];
  for (let i = 0; i < branches; i++) parts.push(genConcat(rnd, depth, budget));
  return parts.join('|');
}

function genConcat(rnd: () => number, depth: number, budget: GenBudget): string {
  const parts = Math.floor(rnd() * 3); // 0..2 atoms (empty branch possible)
  let out = '';
  for (let i = 0; i < parts; i++) out += genRepeat(rnd, depth, budget);
  return out;
}

function genRepeat(rnd: () => number, depth: number, budget: GenBudget): string {
  // Star nesting is bounded: deeply nested stars over many groups make the
  // number of distinct capture vectors explode exponentially, which would
  // make this differential test intractable rather than more thorough.
  const wantStar = rnd() < 0.35 && budget.starDepth < MAX_STAR_DEPTH;
  if (wantStar) budget.starDepth++;
  const atom = genAtom(rnd, depth, budget);
  if (wantStar) budget.starDepth--;
  return wantStar ? atom + '*' : atom;
}

function genAtom(rnd: () => number, depth: number, budget: GenBudget): string {
  if (depth > 0 && budget.groups < MAX_GROUPS && rnd() < 0.4) {
    budget.groups++;
    return '(' + genAlt(rnd, depth - 1, budget) + ')';
  }
  return rnd() < 0.5 ? 'a' : 'b';
}

test('exhaustive: generated patterns x all {a,b} strings of length <= 4', () => {
  const rnd = mulberry32(20261005);
  const inputs = allStrings(['a', 'b'], 4);
  const seen = new Set<string>();
  let generated = 0;
  while (generated < 250) {
    const pattern = genAlt(rnd, 4, { starDepth: 0, groups: 0 });
    if (seen.has(pattern)) continue;
    seen.add(pattern);
    generated++;
    checkPatternAgainstStrings(pattern, inputs);
  }
});

test('stress: 12-group nested-star pattern x all {a,b} strings of length <= 3', () => {
  // This pattern has 12 groups under three levels of star nesting; the
  // outcome space is large but still tractable, and it exercises the
  // zero-width-iteration guard heavily.
  const pattern = '(a*(ba*|((a|a)*(ab*|aa*))a)*|((|ab*)*b|((|a*)*))*(ab|(a*|(|))*))*|';
  checkPatternAgainstStrings(pattern, allStrings(['a', 'b'], 3));
});
