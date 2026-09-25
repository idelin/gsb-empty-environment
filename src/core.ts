/**
 * Subset-minimal unsatisfiable cores via deletion-based minimization:
 * an element stays in the core only if removing it would make the set
 * satisfiable again. Nothing is dropped silently - the returned core is
 * exactly the minimal conflicting evidence.
 */
export function minimalCore<T>(items: readonly T[], isInfeasible: (subset: readonly T[]) => boolean): T[] {
  if (!isInfeasible(items)) return [];
  let core = [...items];
  let changed = true;
  while (changed) {
    changed = false;
    for (const item of [...core]) {
      const trial = core.filter((x) => x !== item);
      if (isInfeasible(trial)) {
        core = trial;
        changed = true;
      }
    }
  }
  return core;
}

/** Verify subset-minimality: core infeasible, every proper subset feasible. */
export function isSubsetMinimalCore<T>(
  core: readonly T[],
  isInfeasible: (subset: readonly T[]) => boolean,
): boolean {
  if (core.length === 0 || !isInfeasible(core)) return false;
  return core.every((item) => !isInfeasible(core.filter((x) => x !== item)));
}
