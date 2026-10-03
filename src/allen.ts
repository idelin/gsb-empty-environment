/**
 * Allen's Interval Algebra — 13 basic relations, converse and composition.
 *
 * The composition table is generated at load time by exhaustive enumeration
 * of interval endpoint configurations over a small linear order, instead of
 * being hand-copied from the literature. This makes the table self-evidently
 * correct: compose(R, S) contains exactly those relations that are observable
 * between intervals A and C when A R B and B S C for some interval B.
 */

export type BasicRelation =
  | 'b' // before:        A.end < B.start
  | 'm' // meets:         A.end = B.start
  | 'o' // overlaps:      A.start < B.start < A.end < B.end
  | 's' // starts:        A.start = B.start, A.end < B.end
  | 'd' // during:        B.start < A.start, A.end < B.end
  | 'f' // finishes:      B.start < A.start, A.end = B.end
  | 'eq' // equals
  | 'bi' // after          (inverse of b)
  | 'mi' // met-by         (inverse of m)
  | 'oi' // overlapped-by  (inverse of o)
  | 'si' // started-by     (inverse of s)
  | 'di' // contains       (inverse of d)
  | 'fi'; // finished-by   (inverse of f)

export const BASIC_RELATIONS: readonly BasicRelation[] = [
  'b', 'm', 'o', 's', 'd', 'f', 'eq',
  'bi', 'mi', 'oi', 'si', 'di', 'fi',
];

const REL_INDEX = new Map<BasicRelation, number>(
  BASIC_RELATIONS.map((r, i) => [r, i]),
);

/** A set of basic relations, encoded as a 13-bit mask. */
export type RelationSet = number;

export const EMPTY: RelationSet = 0;
export const UNIVERSAL: RelationSet = (1 << BASIC_RELATIONS.length) - 1;

export function singleton(r: BasicRelation): RelationSet {
  return 1 << REL_INDEX.get(r)!;
}

export function relationSetOf(...rels: BasicRelation[]): RelationSet {
  let set = EMPTY;
  for (const r of rels) set |= singleton(r);
  return set;
}

export function toArray(set: RelationSet): BasicRelation[] {
  return BASIC_RELATIONS.filter((r) => (set & singleton(r)) !== 0);
}

export function intersect(a: RelationSet, b: RelationSet): RelationSet {
  return a & b;
}

export function isEmpty(set: RelationSet): boolean {
  return set === EMPTY;
}

export interface Interval {
  start: number;
  end: number;
}

/** The unique basic relation holding between intervals a and b. */
export function relationBetween(a: Interval, b: Interval): BasicRelation {
  if (a.end < b.start) return 'b';
  if (a.end === b.start) return 'm';
  if (b.end < a.start) return 'bi';
  if (b.end === a.start) return 'mi';
  // Interiors intersect.
  if (a.start === b.start) {
    if (a.end < b.end) return 's';
    if (a.end > b.end) return 'si';
    return 'eq';
  }
  if (a.start < b.start) {
    if (a.end < b.end) return 'o';
    if (a.end > b.end) return 'di';
    return 'fi';
  }
  if (a.end < b.end) return 'd';
  if (a.end > b.end) return 'oi';
  return 'f';
}

/** Human-readable names, e.g. 'b' -> 'before'. */
export const RELATION_NAMES: Record<BasicRelation, string> = {
  b: 'before', m: 'meets', o: 'overlaps', s: 'starts', d: 'during',
  f: 'finishes', eq: 'equals', bi: 'after', mi: 'met-by',
  oi: 'overlapped-by', si: 'started-by', di: 'contains', fi: 'finished-by',
};

export function formatRelationSet(set: RelationSet): string {
  const rels = toArray(set);
  if (rels.length === BASIC_RELATIONS.length) return '<any>';
  return '{' + rels.map((r) => RELATION_NAMES[r]).join(', ') + '}';
}

// ---------------------------------------------------------------------------
// Table generation by enumeration.
// ---------------------------------------------------------------------------

// Composing two relations involves three intervals, i.e. up to six distinct
// endpoint positions (e.g. before ∘ before needs a0<a1<b0<b1<c0<c1), so the
// enumeration order must contain at least six points; seven give slack.
const POINTS = [0, 1, 2, 3, 4, 5, 6];
const ALL_INTERVALS: Interval[] = [];
for (const s of POINTS) {
  for (const e of POINTS) {
    if (s < e) ALL_INTERVALS.push({ start: s, end: e });
  }
}

// beforeOf[b][r] = intervals a with relationBetween(a, b) === r
// afterOf[b][r]  = intervals c with relationBetween(b, c) === r
function groupByRelation(
  fixed: Interval,
  side: 'before' | 'after',
): Map<BasicRelation, Interval[]> {
  const groups = new Map<BasicRelation, Interval[]>();
  for (const other of ALL_INTERVALS) {
    const rel =
      side === 'before'
        ? relationBetween(other, fixed)
        : relationBetween(fixed, other);
    const list = groups.get(rel);
    if (list) list.push(other);
    else groups.set(rel, [other]);
  }
  return groups;
}

function buildConverse(): Map<BasicRelation, BasicRelation> {
  const map = new Map<BasicRelation, BasicRelation>();
  for (const a of ALL_INTERVALS) {
    for (const b of ALL_INTERVALS) {
      map.set(relationBetween(a, b), relationBetween(b, a));
    }
  }
  return map;
}

const CONVERSE = buildConverse();

export function converse(r: BasicRelation): BasicRelation {
  return CONVERSE.get(r)!;
}

export function converseSet(set: RelationSet): RelationSet {
  let out = EMPTY;
  for (const r of toArray(set)) out |= singleton(converse(r));
  return out;
}

function buildCompositionTable(): RelationSet[][] {
  const table: RelationSet[][] = BASIC_RELATIONS.map(() =>
    BASIC_RELATIONS.map(() => EMPTY),
  );
  for (const b of ALL_INTERVALS) {
    const preds = groupByRelation(b, 'before');
    const succs = groupByRelation(b, 'after');
    for (const r of BASIC_RELATIONS) {
      const as = preds.get(r);
      if (!as) continue;
      for (const s of BASIC_RELATIONS) {
        const cs = succs.get(s);
        if (!cs) continue;
        let cell = table[REL_INDEX.get(r)!][REL_INDEX.get(s)!];
        for (const a of as) {
          for (const c of cs) {
            cell |= singleton(relationBetween(a, c));
          }
        }
        table[REL_INDEX.get(r)!][REL_INDEX.get(s)!] = cell;
      }
    }
  }
  return table;
}

const COMPOSE = buildCompositionTable();

/** Composition of two basic relations: which relations may hold between A and C. */
export function compose(r: BasicRelation, s: BasicRelation): RelationSet {
  return COMPOSE[REL_INDEX.get(r)!][REL_INDEX.get(s)!];
}

/** Composition of two relation sets (union of pairwise compositions). */
export function composeSets(x: RelationSet, y: RelationSet): RelationSet {
  let out = EMPTY;
  for (const r of toArray(x)) {
    for (const s of toArray(y)) {
      out |= compose(r, s);
    }
  }
  return out;
}
