import {
  breakProblem,
  pageBreakCost,
  type Atom,
  type PageConfig,
} from './paginate.ts';

export interface ExhaustiveResult {
  layouts: number;
  minCost: number;
}

/** Enumerates every constraint-satisfying pagination of a small document and
 *  returns the minimum achievable cost. Used to prove DP optimality. */
export function exhaustiveMinCost(
  atoms: Atom[],
  newFootnotes: string[][],
  heights: Record<string, number>,
  config: PageConfig,
  maxAtoms = 26,
): ExhaustiveResult | null {
  if (atoms.length === 0 || atoms.length > maxAtoms) return null;
  let layouts = 0;
  let minCost = Infinity;

  const rec = (i: number, acc: number) => {
    if (acc >= minCost) return;
    if (i >= atoms.length) {
      layouts++;
      minCost = acc;
      return;
    }
    let used = 0;
    let footnoteSum = 0;
    let separator = 0;
    const fnSeen = new Set<string>();
    for (let j = i + 1; j <= atoms.length; j++) {
      const atom = atoms[j - 1];
      for (const id of newFootnotes[j - 1]) {
        if (!fnSeen.has(id)) {
          fnSeen.add(id);
          footnoteSum += heights[id] ?? 0;
        }
      }
      if (fnSeen.size > 0) separator = config.footnoteSeparatorHeight;
      const total = used + atom.height + footnoteSum + separator;
      if (total > config.pageCapacity) break;
      used += atom.height;
      const isLast = j === atoms.length;
      if (!isLast && breakProblem(atoms[j - 1], atoms[j], config) !== null) continue;
      const splitsParagraph =
        !isLast &&
        atoms[j - 1].kind === 'line' &&
        atoms[j].kind === 'line' &&
        atoms[j - 1].blockId === atoms[j].blockId;
      rec(j, acc + pageBreakCost(config.pageCapacity - total, isLast, splitsParagraph, config));
    }
  };
  rec(0, 0);
  return { layouts, minCost };
}
