import { ConflictConstraint, ConflictReport } from './types';
import { isFeasible, MergedModel } from './solver';

/**
 * Deletion-based minimal unsatisfiable core: repeatedly drop any
 * constraint whose removal keeps the set infeasible. The result is
 * deletion-minimal — removing any remaining constraint makes it feasible.
 */
export function minimizeConflict(
  constraints: ConflictConstraint[],
  common: Set<string>,
): ConflictConstraint[] {
  let core = constraints.slice();
  let i = 0;
  while (i < core.length) {
    const candidate = core.slice(0, i).concat(core.slice(i + 1));
    if (!isFeasible(candidate, common)) {
      core = candidate; // constraint i is redundant, drop it
    } else {
      i++;
    }
  }
  return core;
}

function closureOfCore(
  core: ConflictConstraint[],
): Set<string> {
  const mandatory = new Set<string>();
  const requires = new Map<string, string[]>();
  for (const c of core) {
    if (c.kind === 'mandatory') mandatory.add(c.capability);
    if (c.kind === 'requires') {
      const list = requires.get(c.capability) || [];
      list.push(c.other!);
      requires.set(c.capability, list);
    }
  }
  const closure = new Set<string>(mandatory);
  const queue = Array.from(mandatory);
  while (queue.length > 0) {
    const node = queue.pop()!;
    for (const t of requires.get(node) || []) {
      if (!closure.has(t)) {
        closure.add(t);
        queue.push(t);
      }
    }
  }
  return closure;
}

function describe(c: ConflictConstraint): string {
  switch (c.kind) {
    case 'mandatory':
      return `"${c.capability}" is mandatory (declared by ${c.declaredBy.join(', ')})`;
    case 'requires':
      return `"${c.capability}" requires "${c.other}" (declared by ${c.declaredBy.join(', ')})`;
    case 'conflicts':
      return `"${c.capability}" conflicts with "${c.other}" (declared by ${c.declaredBy.join(', ')})`;
  }
}

/** Build the minimal conflict report for an infeasible model. */
export function analyzeConflict(model: MergedModel): ConflictReport {
  const common = new Set(model.common);
  const core = minimizeConflict(model.constraints, common);
  const closure = closureOfCore(core);
  const unsupported = Array.from(closure)
    .filter((n) => !common.has(n))
    .sort();

  const parts = core.map(describe);
  if (unsupported.length > 0) {
    parts.push(
      `not commonly supported: ${unsupported
        .map((n) => `"${n}"`)
        .join(', ')}`,
    );
  }
  return {
    constraints: core,
    unsupported,
    explanation: `No common configuration exists. Minimal conflict core (${core.length} constraint(s)): ${parts.join('; ')}.`,
  };
}
