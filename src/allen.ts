/**
 * Allen's interval algebra: the 13 basic relations, converse, composition
 * and translation into endpoint (point-algebra) constraints.
 *
 * Relations are represented as bit positions inside a 13-bit mask, so any
 * disjunction of basic relations (a "label" on a constraint-network edge)
 * is a single integer.
 */

export const RELATIONS = [
  'b', // before:        A |---|     |---| B
  'm', // meets:         A |---||---| B
  'o', // overlaps:      A |---[==]---| B
  's', // starts:        A and B share their start, A ends first
  'd', // during:        A strictly inside B
  'f', // finishes:      A and B share their end, A starts later
  'eq', // equal
  'bi', // after        (converse of b)
  'mi', // met-by       (converse of m)
  'oi', // overlapped-by(converse of o)
  'si', // started-by   (converse of s)
  'di', // contains     (converse of d)
  'fi', // finished-by  (converse of f)
] as const;

export type RelationSymbol = (typeof RELATIONS)[number];

export const RELATION_NAMES: Record<RelationSymbol, string> = {
  b: 'before',
  m: 'meets',
  o: 'overlaps',
  s: 'starts',
  d: 'during',
  f: 'finishes',
  eq: 'equal',
  bi: 'after',
  mi: 'met-by',
  oi: 'overlapped-by',
  si: 'started-by',
  di: 'contains',
  fi: 'finished-by',
};

const INDEX: Record<RelationSymbol, number> = Object.fromEntries(
  RELATIONS.map((sym, i) => [sym, i]),
) as Record<RelationSymbol, number>;

export const EMPTY = 0;
export const UNIVERSAL = (1 << 13) - 1;
export const EQ_BIT = 1 << INDEX.eq;

export function bit(r: RelationSymbol): number {
  return 1 << INDEX[r];
}

export function maskOf(...rs: RelationSymbol[]): number {
  let mask = 0;
  for (const r of rs) mask |= bit(r);
  return mask;
}

export function relationsOf(mask: number): RelationSymbol[] {
  return RELATIONS.filter((_, i) => mask & (1 << i));
}

export function maskToString(mask: number): string {
  return `{${relationsOf(mask).join(',')}}`;
}

const NAME_TO_SYMBOL: Record<string, RelationSymbol> = Object.fromEntries(
  RELATIONS.map((sym) => [RELATION_NAMES[sym], sym]),
);

/** Accepts 'b', 'before', '{b,m}', ['b','meets'], or a raw mask number. */
export function parseRelations(input: string | string[] | number): number {
  if (typeof input === 'number') return input & UNIVERSAL;
  const parts = Array.isArray(input)
    ? input
    : input.replace(/[{}]/g, '').split(/[,\s]+/).filter(Boolean);
  let mask = 0;
  for (const part of parts) {
    const sym = (part in INDEX
      ? part
      : NAME_TO_SYMBOL[part.toLowerCase()]) as RelationSymbol | undefined;
    if (!sym || !(sym in INDEX)) throw new Error(`Unknown relation: ${part}`);
    mask |= bit(sym);
  }
  return mask;
}

// ---------------------------------------------------------------------------
// Endpoint order patterns.
//
// Each basic relation is uniquely determined by the weak ordering of the four
// endpoints (a.start, a.end, b.start, b.end). Ranks below encode that order;
// equal ranks mean the endpoints coincide.
// ---------------------------------------------------------------------------

const PATTERNS: Record<RelationSymbol, readonly [number, number, number, number]> = {
  b: [0, 1, 2, 3],
  m: [0, 1, 1, 2],
  o: [0, 2, 1, 3],
  s: [0, 1, 0, 2],
  d: [1, 2, 0, 3],
  f: [1, 2, 0, 2],
  eq: [0, 1, 0, 1],
  bi: [2, 3, 0, 1],
  mi: [1, 2, 0, 1],
  oi: [1, 3, 0, 2],
  si: [0, 2, 0, 1],
  di: [0, 3, 1, 2],
  fi: [0, 2, 1, 2],
};

const PATTERN_INDEX = new Map<string, number>(
  RELATIONS.map((sym, i) => [PATTERNS[sym].join(','), i]),
);

function relationFromRanks(a: number, b: number, c: number, d: number): number {
  // Compress the distinct rank values to 0..k so the lookup is canonical.
  const raw = [a, b, c, d];
  const distinct = [...new Set(raw)].sort((x, y) => x - y);
  const key = raw.map((r) => distinct.indexOf(r)).join(',');
  const idx = PATTERN_INDEX.get(key);
  if (idx === undefined) {
    throw new Error(`Endpoint ranks ${a},${b},${c},${d} do not form two intervals`);
  }
  return idx;
}

// ---------------------------------------------------------------------------
// Converse
// ---------------------------------------------------------------------------

const CONVERSE_INDEX = RELATIONS.map((_, i) =>
  i === INDEX.eq ? INDEX.eq : i < INDEX.eq ? i + 7 : i - 7,
);

export function converseRelation(r: RelationSymbol): RelationSymbol {
  return RELATIONS[CONVERSE_INDEX[INDEX[r]]];
}

export function converseMask(mask: number): number {
  let out = 0;
  for (let i = 0; i < 13; i++) {
    if (mask & (1 << i)) out |= 1 << CONVERSE_INDEX[i];
  }
  return out;
}

// ---------------------------------------------------------------------------
// Composition table.
//
// Rather than hard-coding the 13x13 table, it is derived by enumerating every
// weak ordering of the six endpoints of three intervals (A, B, C) with
// start < end, and recording which (A,C) relations can co-occur with each
// (A,B), (B,C) pair. This is exhaustive (the relations depend only on the
// endpoint ordering), so the generated table is exact.
// ---------------------------------------------------------------------------

function buildCompositionTable(): Uint16Array {
  const table = new Uint16Array(13 * 13);
  // Endpoint slots: 0 a.s, 1 a.e, 2 b.s, 3 b.e, 4 c.s, 5 c.e
  const ranks = new Array<number>(6).fill(0);
  const visit = (slot: number): void => {
    if (slot === 6) {
      const ab = relationFromRanks(ranks[0], ranks[1], ranks[2], ranks[3]);
      const bc = relationFromRanks(ranks[2], ranks[3], ranks[4], ranks[5]);
      const ac = relationFromRanks(ranks[0], ranks[1], ranks[4], ranks[5]);
      table[ab * 13 + bc] |= 1 << ac;
      return;
    }
    for (let rank = 0; rank < 6; rank++) {
      ranks[slot] = rank;
      // Enforce start < end as soon as both endpoints of an interval are set.
      if (slot === 1 && !(ranks[0] < ranks[1])) continue;
      if (slot === 3 && !(ranks[2] < ranks[3])) continue;
      if (slot === 5 && !(ranks[4] < ranks[5])) continue;
      visit(slot + 1);
    }
  };
  visit(0);
  return table;
}

const COMPOSITION = buildCompositionTable();

/** Mask of all relations r with A r C possible when A r1 B and B r2 C. */
export function compose(r1: RelationSymbol, r2: RelationSymbol): number {
  return COMPOSITION[INDEX[r1] * 13 + INDEX[r2]];
}

export function composeMasks(m1: number, m2: number): number {
  let out = 0;
  for (let i = 0; i < 13; i++) {
    if (!(m1 & (1 << i))) continue;
    for (let j = 0; j < 13; j++) {
      if (m2 & (1 << j)) out |= COMPOSITION[i * 13 + j];
    }
  }
  return out;
}

// ---------------------------------------------------------------------------
// Translation to endpoint constraints (point algebra).
//
// A single basic relation fixes the order of every pair of endpoints, so it
// translates to exact difference constraints  time[to] - time[from] <= lag
// (lag measured in units of epsilon; -1 means a strict "<", 0 means "<=").
// For a disjunctive label we keep, per endpoint pair, the loosest bound that
// is valid for every disjunct — i.e. the convex hull of the disjunction,
// which is sound for deriving earliest/latest bounds.
// ---------------------------------------------------------------------------

export interface EndpointConstraint {
  /** Endpoint slot of the "from" point: 0 a.start, 1 a.end, 2 b.start, 3 b.end. */
  from: number;
  /** Endpoint slot of the "to" point. */
  to: number;
  /** time[to] - time[from] <= maxLagUnits * epsilon. */
  maxLagUnits: number;
}

const endpointConstraintCache = new Map<number, EndpointConstraint[]>();

export function endpointConstraints(mask: number): EndpointConstraint[] {
  const cached = endpointConstraintCache.get(mask);
  if (cached) return cached;

  // combined[from*4+to] = loosest lag (in epsilon units) valid for all disjuncts;
  // +Infinity marks "unconstrained in at least one disjunct".
  const combined = new Map<number, number>();
  for (let i = 0; i < 13; i++) {
    if (!(mask & (1 << i))) continue;
    const pat = PATTERNS[RELATIONS[i]];
    for (let p = 0; p < 4; p++) {
      for (let q = 0; q < 4; q++) {
        if (p === q) continue;
        // lag for  time[p] - time[q] <= lag
        let lag: number;
        if (pat[p] < pat[q]) lag = -1; // p strictly before q
        else if (pat[p] === pat[q]) lag = 0; // p coincides with q
        else lag = Infinity; // p after q: no upper bound on p - q
        const key = q * 4 + p; // constraint on (from=q, to=p)
        const prev = combined.get(key);
        combined.set(key, prev === undefined ? lag : Math.max(prev, lag));
      }
    }
  }

  const out: EndpointConstraint[] = [];
  for (const [key, lag] of combined) {
    if (!Number.isFinite(lag)) continue;
    out.push({ from: Math.floor(key / 4), to: key % 4, maxLagUnits: lag });
  }
  endpointConstraintCache.set(mask, out);
  return out;
}
