// Containment decision: does the scope of `container` include the scope of
// `containee` over an explicit finite domain? Answers with a minimal
// counterexample when not contained, or `undecided` when the budget runs out
// or a referenced attribute has no declared domain.

import {
  boundaryConstants,
  collectVars,
  FALSE,
  type Cond,
  type Point,
} from './conditions.ts';
import {
  cellKey,
  cellPoint,
  domainOrder,
  isSingletonCell,
  rootCell,
  splitCell,
  type Cell,
  type Domain,
} from './domain.ts';
import { BudgetExceeded, isEmptyCell, outcomes, type Budget, type OutcomeCache } from './eval.ts';

export type ContainmentResult =
  | { readonly status: 'contained' }
  | { readonly status: 'not-contained'; readonly counterexample: Point }
  | { readonly status: 'undecided'; readonly reason: 'budget' | 'unknown-domain'; readonly detail: string };

export interface ContainOptions {
  /** Maximum number of abstract evaluation steps before giving up. */
  readonly budget?: number;
}

const DEFAULT_BUDGET = 1_000_000;

function validate(container: Cond, containee: Cond, domain: Domain): ContainmentResult | null {
  const referenced = new Set([...collectVars(container), ...collectVars(containee)]);
  for (const v of referenced) {
    if (!(v in domain)) {
      // Unknown attribute domain: containment cannot be proven.
      return { status: 'undecided', reason: 'unknown-domain', detail: `no domain declared for attribute "${v}"` };
    }
  }
  const checkKinds = (cond: Cond): void => {
    switch (cond.kind) {
      case 'cmp':
        if (domain[cond.var].kind !== 'int') throw new TypeError(`cmp on non-integer attribute "${cond.var}"`);
        break;
      case 'diff':
        if (domain[cond.left].kind !== 'int' || domain[cond.right].kind !== 'int') {
          throw new TypeError('diff constraints require integer attributes');
        }
        break;
      case 'not': checkKinds(cond.a); break;
      case 'and': case 'or': for (const a of cond.args) checkKinds(a); break;
      default: break;
    }
  };
  checkKinds(container);
  checkKinds(containee);
  return null;
}

interface Search {
  readonly container: Cond;
  readonly containee: Cond;
  readonly order: readonly string[];
  readonly boundaries: ReadonlyMap<string, readonly number[]>;
  readonly cache: OutcomeCache;
  readonly budget: Budget;
}

/**
 * Lexicographically smallest point (domain order, ⊥ smallest) where the
 * containee is definitely true and the container is not definitely true.
 * Cells are refined by interval splitting; abstractly clean cells are pruned
 * without enumerating their values.
 */
function findMinCounterexample(search: Search, cell: Cell): Point | null {
  if (isEmptyCell(cell, search.order)) return null;
  const key = cellKey(cell, search.order);
  const containeeOut = outcomes(search.containee, cell, key, search.cache, search.budget);
  if (!containeeOut.has('T')) return null;
  const containerOut = outcomes(search.container, cell, key, search.cache, search.budget);
  if (!containerOut.has('F') && !containerOut.has('U')) return null;

  if (isSingletonCell(cell, search.order)) return cellPoint(cell, search.order);

  const halves = splitCell(cell, search.order, search.boundaries);
  if (halves === null) return cellPoint(cell, search.order);
  const [lower, upper] = halves;
  return findMinCounterexample(search, lower) ?? findMinCounterexample(search, upper);
}

/** Decide whether `container`'s scope includes `containee`'s scope. */
export function contains(container: Cond, containee: Cond, domain: Domain, options: ContainOptions = {}): ContainmentResult {
  const invalid = validate(container, containee, domain);
  if (invalid !== null) return invalid;

  const order = domainOrder(domain);
  const search: Search = {
    container,
    containee,
    order,
    boundaries: boundaryConstants([container, containee]),
    cache: new Map(),
    budget: { remaining: options.budget ?? DEFAULT_BUDGET },
  };
  try {
    const witness = findMinCounterexample(search, rootCell(domain));
    return witness === null ? { status: 'contained' } : { status: 'not-contained', counterexample: witness };
  } catch (err) {
    if (err instanceof BudgetExceeded) {
      return { status: 'undecided', reason: 'budget', detail: 'evaluation budget exceeded' };
    }
    throw err;
  }
}

/** True when no point of the domain makes `cond` definitely true. */
export function isEmpty(cond: Cond, domain: Domain, options: ContainOptions = {}): ContainmentResult {
  return contains(FALSE, cond, domain, options);
}
