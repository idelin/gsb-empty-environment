import { Rat } from './rational.ts';
import { bracketContains, evaluate } from './ladder.ts';
import type { CompiledLadder } from './ladder.ts';
import { intervalContains, invert } from './inverse.ts';
import type { Interval, SearchWindow } from './inverse.ts';

export interface GridSpec {
  from: Rat;
  to: Rat;
  step: Rat; // small rational, e.g. Rat.parse('0.05')
}

export interface BreakpointCheck {
  at: Rat;
  rule: string;
  ok: boolean;
  detail: string;
}

export interface GridValidationReport {
  pointsChecked: number;
  inverseMismatches: { x: Rat; y: Rat }[];
  breakpointChecks: BreakpointCheck[];
  ok: boolean;
}

// Reference check on a small rational grid: for every grid point x, the
// forward value y = f(x) must satisfy x in invert(y), and every bracket
// threshold must be claimed by exactly one bracket (never double counted).
export function validateOnGrid(ladder: CompiledLadder, grid: GridSpec): GridValidationReport {
  if (grid.step.lte(Rat.zero())) throw new Error('validateOnGrid: step must be positive');
  if (!grid.from.lt(grid.to)) throw new Error('validateOnGrid: from must be < to');

  const win: SearchWindow = { from: grid.from, to: grid.to };
  const inverseMismatches: { x: Rat; y: Rat }[] = [];
  let pointsChecked = 0;

  for (let x = grid.from; x.lte(grid.to); x = x.add(grid.step)) {
    const y = evaluate(ladder, x).total;
    const intervals = invert(ladder, y, win);
    if (!intervals.some((iv) => intervalContains(iv, x))) {
      inverseMismatches.push({ x, y });
    }
    pointsChecked++;
  }

  const breakpointChecks: BreakpointCheck[] = [];
  for (const rule of ladder.rules) {
    const seen = new Set<string>();
    for (const b of rule.brackets) {
      for (const p of [b.lower, b.upper]) {
        if (p === null) continue;
        const key = p.toString();
        if (seen.has(key)) continue;
        seen.add(key);
        const claims = rule.brackets.filter((cand) => bracketContains(cand, p)).length;
        breakpointChecks.push({
          at: p,
          rule: rule.name,
          ok: claims <= 1,
          detail:
            claims === 0
              ? `threshold ${p} is a hole (claimed by no bracket)`
              : `threshold ${p} claimed by exactly one bracket`,
        });
      }
    }
  }

  return {
    pointsChecked,
    inverseMismatches,
    breakpointChecks,
    ok: inverseMismatches.length === 0 && breakpointChecks.every((c) => c.ok),
  };
}

// Width of an interval; zero for a singleton solution.
export function intervalWidth(iv: Interval): Rat {
  return iv.upper.sub(iv.lower);
}
