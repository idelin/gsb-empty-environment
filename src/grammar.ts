export const EPSILON = "ε";
export const END = "$";

export interface Production {
  lhs: string;
  rhs: string[];
}

export interface Grammar {
  start: string;
  terminals: Set<string>;
  nonTerminals: Set<string>;
  productions: Production[];
}

export function makeGrammar(
  start: string,
  rules: Array<[string, ...string[]]>,
): Grammar {
  const productions: Production[] = rules.map(([lhs, ...rhs]) => ({
    lhs,
    rhs: rhs.length === 1 && rhs[0] === EPSILON ? [] : rhs,
  }));
  const nonTerminals = new Set(productions.map((p) => p.lhs));
  const terminals = new Set<string>();
  for (const p of productions) {
    for (const sym of p.rhs) {
      if (sym !== EPSILON && !nonTerminals.has(sym)) terminals.add(sym);
    }
  }
  return { start, terminals, nonTerminals, productions };
}

/** Fixed-point computation of FIRST sets for all non-terminals. */
export function computeFirst(g: Grammar): Map<string, Set<string>> {
  const first = new Map<string, Set<string>>();
  for (const nt of g.nonTerminals) first.set(nt, new Set());

  let changed = true;
  while (changed) {
    changed = false;
    for (const p of g.productions) {
      const target = first.get(p.lhs)!;
      const before = target.size;
      if (p.rhs.length === 0) {
        target.add(EPSILON);
      } else {
        for (const sym of p.rhs) {
          const symFirst = g.nonTerminals.has(sym)
            ? first.get(sym)!
            : new Set([sym]);
          for (const t of symFirst) {
            if (t !== EPSILON) target.add(t);
          }
          if (!symFirst.has(EPSILON)) break;
          if (sym === p.rhs[p.rhs.length - 1]) target.add(EPSILON);
        }
      }
      if (target.size !== before) changed = true;
    }
  }
  return first;
}

/** FIRST of a symbol sequence; `nullableSuffix` is appended when the whole
 *  sequence can derive the empty string (used for FIRST(βa) in closure). */
export function firstOfSequence(
  g: Grammar,
  first: Map<string, Set<string>>,
  symbols: string[],
  nullableSuffix: string[] = [],
): Set<string> {
  const result = new Set<string>();
  let nullable = true;
  for (const sym of symbols) {
    const symFirst = g.nonTerminals.has(sym)
      ? first.get(sym)!
      : new Set([sym]);
    for (const t of symFirst) {
      if (t !== EPSILON) result.add(t);
    }
    if (!symFirst.has(EPSILON)) {
      nullable = false;
      break;
    }
  }
  if (nullable) {
    for (const t of nullableSuffix) result.add(t);
  }
  return result;
}

/** Fixed-point computation of FOLLOW sets for all non-terminals. */
export function computeFollow(
  g: Grammar,
  first: Map<string, Set<string>>,
): Map<string, Set<string>> {
  const follow = new Map<string, Set<string>>();
  for (const nt of g.nonTerminals) follow.set(nt, new Set());
  follow.get(g.start)!.add(END);

  let changed = true;
  while (changed) {
    changed = false;
    for (const p of g.productions) {
      for (let i = 0; i < p.rhs.length; i++) {
        const sym = p.rhs[i];
        if (!g.nonTerminals.has(sym)) continue;
        const rest = p.rhs.slice(i + 1);
        const restFirst = firstOfSequence(g, first, rest, [EPSILON]);
        const target = follow.get(sym)!;
        const before = target.size;
        for (const t of restFirst) {
          if (t !== EPSILON) target.add(t);
        }
        if (restFirst.has(EPSILON) || rest.length === 0) {
          for (const t of follow.get(p.lhs)!) target.add(t);
        }
        if (target.size !== before) changed = true;
      }
    }
  }
  return follow;
}
