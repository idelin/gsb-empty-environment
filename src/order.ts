import { evaluateNaive } from './reference';
import { EvalResult, Rule, Snapshot } from './types';
import { stableStringify } from './util';

/**
 * Canonical form of an evaluation result, insensitive to rule ordering.
 *
 * Notes on normalization:
 * - When several top-priority rules assign the SAME value, which one is
 *   labelled 'winner' vs 'agreed' depends on rule order, so both collapse to
 *   'topAgreed' and the winner's ruleId is dropped (its value and priority
 *   are order-independent).
 * - Everything else (matched flags, candidate sets, conflicts, output) is
 *   fully determined by the rule set and snapshot.
 */
export function canonicalize(result: EvalResult): string {
  const rules = result.rules
    .map((r) => ({ ruleId: r.ruleId, matched: r.matched }))
    .sort((a, b) => (a.ruleId < b.ruleId ? -1 : 1));
  const fields = result.fields
    .map((f) => ({
      field: f.field,
      status: f.status,
      winner: f.winner ? { priority: f.winner.priority, value: f.winner.value } : undefined,
      conflict: f.conflict
        ? { priority: f.conflict.priority, entries: f.conflict.entries }
        : undefined,
      candidates: f.candidates
        .map((c) => ({
          ruleId: c.ruleId,
          priority: c.priority,
          value: c.value,
          outcome: c.outcome === 'winner' || c.outcome === 'agreed' ? 'topAgreed' : c.outcome,
        }))
        .sort((a, b) => (a.ruleId < b.ruleId ? -1 : 1)),
    }))
    .sort((a, b) => (a.field < b.field ? -1 : 1));
  const conflicts = result.conflicts
    .slice()
    .sort((a, b) => (a.field < b.field ? -1 : 1));
  return stableStringify({ output: result.output, rules, fields, conflicts });
}

export interface OrderCheckReport {
  readonly independent: boolean;
  readonly permutationsChecked: number;
  readonly baseline: string;
  readonly divergentPermutation?: readonly string[];
  readonly divergentResult?: string;
}

function* allPermutations<T>(items: readonly T[]): Generator<readonly T[]> {
  if (items.length <= 1) {
    yield items;
    return;
  }
  for (let i = 0; i < items.length; i++) {
    const rest = [...items.slice(0, i), ...items.slice(i + 1)];
    for (const perm of allPermutations(rest)) {
      yield [items[i]!, ...perm];
    }
  }
}

/** Deterministic PRNG (mulberry32) for sampling permutations of large rule sets. */
function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function* sampledPermutations<T>(items: readonly T[], count: number, seed: number): Generator<readonly T[]> {
  const rand = mulberry32(seed);
  const seen = new Set<string>();
  // Key by identity within the original array: String(item) is not unique
  // for objects, which would collapse every permutation to one key.
  const key = (perm: readonly T[]) => perm.map((item) => items.indexOf(item)).join(',');
  const emit = function* (perm: readonly T[]) {
    const k = key(perm);
    if (!seen.has(k)) {
      seen.add(k);
      yield perm;
    }
  };
  yield* emit(items);
  yield* emit([...items].reverse());
  yield* emit([...items].sort());
  let produced = seen.size;
  while (produced < count) {
    const perm = [...items];
    for (let i = perm.length - 1; i > 0; i--) {
      const j = Math.floor(rand() * (i + 1));
      [perm[i], perm[j]] = [perm[j]!, perm[i]!];
    }
    const before = seen.size;
    yield* emit(perm);
    if (seen.size === before) continue;
    produced++;
  }
}

const EXHAUSTIVE_LIMIT = 8; // 8! = 40320 permutations
const SAMPLE_COUNT = 512;

/**
 * Verify that evaluation does not depend on the order rules are listed.
 * Runs the naive two-phase reference model over every permutation (for up to
 * 8 rules) or a deterministic sample of permutations (for larger sets), and
 * compares canonicalized results.
 */
export function checkOrderIndependence(
  rules: readonly Rule[],
  snapshot: Snapshot,
  options: { readonly maxPermutations?: number; readonly seed?: number } = {},
): OrderCheckReport {
  const perms =
    rules.length <= EXHAUSTIVE_LIMIT
      ? allPermutations(rules)
      : sampledPermutations(rules, options.maxPermutations ?? SAMPLE_COUNT, options.seed ?? 1);

  let baseline: string | undefined;
  let checked = 0;
  for (const perm of perms) {
    const canonical = canonicalize(evaluateNaive(perm, snapshot));
    if (baseline === undefined) {
      baseline = canonical;
    } else if (canonical !== baseline) {
      return {
        independent: false,
        permutationsChecked: checked + 1,
        baseline,
        divergentPermutation: perm.map((r) => r.id),
        divergentResult: canonical,
      };
    }
    checked++;
  }
  return { independent: true, permutationsChecked: checked, baseline: baseline ?? '' };
}
