/**
 * Containment engine: decides whether the applicability of one policy
 * (condition) contains that of another over an explicit finite domain.
 *
 * Method: the negation `inner and not outer` is evaluated over boxes
 * (products of intervals / value lists) with interval arithmetic. Boxes that
 * are definitely false are pruned wholesale; mixed boxes are split
 * (largest-first), so wide integer ranges are handled in logarithmically
 * many steps instead of per-value enumeration. If a counterexample exists,
 * a second phase minimizes it attribute by attribute via binary search with
 * repeated existence queries, yielding the lexicographically minimal
 * counterexample. All conditions are hash-consed in a shared
 * ConditionFactory and box evaluations are memoized per canonical node, so
 * shared sub-conditions are never evaluated twice on the same box.
 *
 * A step budget bounds the search; exhausting it yields "undecided".
 * Domains where a referenced attribute may be unknown yield "unknown"
 * (containment cannot be proven under three-valued semantics).
 */

import { attrsOf, type Cond, type ConditionFactory } from "./conditions.ts";
import {
  boxIsSingleton,
  boxKey,
  domainIsEmpty,
  initialBox,
  minPoint,
  splitAttr,
  validateDomain,
  type Assignment,
  type AttrBox,
  type Box,
  type DomainSpec,
} from "./domain.ts";
import { evalBox, evalPoint, type Tri } from "./eval.ts";

export interface Policy {
  id: string;
  condition: Cond;
}

export type ContainsResult =
  | { status: "contained" }
  | { status: "not-contained"; counterexample: Assignment }
  | { status: "undecided"; reason: "budget"; stepsUsed: number }
  | { status: "unknown"; reason: "unknown-attributes"; attributes: string[] };

export interface ContainsOptions {
  /** Maximum number of box evaluations before giving up. Default 100_000. */
  budget?: number;
}

const DEFAULT_BUDGET = 100_000;

class BudgetExceeded extends Error {
  readonly stepsUsed: number;
  constructor(stepsUsed: number) {
    super("budget exceeded");
    this.stepsUsed = stepsUsed;
  }
}

function validateKinds(c: Cond, domain: DomainSpec): void {
  switch (c.tag) {
    case "cmp":
      if (domain[c.attr].kind !== "int") {
        throw new Error(`cmp() requires an int attribute, got "${c.attr}"`);
      }
      break;
    case "diff":
      if (domain[c.left].kind !== "int" || domain[c.right].kind !== "int") {
        throw new Error("diff() requires int attributes");
      }
      break;
    case "in":
      if (domain[c.attr].kind === "set") {
        throw new Error(`in() does not apply to set attribute "${c.attr}"`);
      }
      break;
    case "setHas":
    case "setSubset":
    case "setIntersects":
      if (domain[c.attr].kind !== "set") {
        throw new Error(`set predicate requires a set attribute, got "${c.attr}"`);
      }
      break;
    case "not":
      validateKinds(c.of, domain);
      break;
    case "and":
    case "or":
      for (const a of c.args) validateKinds(a, domain);
      break;
    default:
      break;
  }
}

export function contains(
  factory: ConditionFactory,
  outer: Cond,
  inner: Cond,
  domain: DomainSpec,
  opts: ContainsOptions = {},
): ContainsResult {
  validateDomain(domain);
  const attrs = [...attrsOf(outer, attrsOf(inner))].sort();
  for (const a of attrs) {
    if (!(a in domain)) {
      throw new Error(
        `attribute "${a}" is not declared in the domain; policies may only read explicit attributes`,
      );
    }
  }
  validateKinds(outer, domain);
  validateKinds(inner, domain);

  // Unknown-able attributes: containment cannot be proven.
  const unknownAttrs = attrs.filter((a) => domain[a].optional === true);
  if (unknownAttrs.length > 0) {
    return {
      status: "unknown",
      reason: "unknown-attributes",
      attributes: unknownAttrs,
    };
  }

  // An empty range is contained in any range (vacuous truth).
  if (domainIsEmpty(domain, attrs)) {
    return { status: "contained" };
  }

  const budget = opts.budget ?? DEFAULT_BUDGET;
  let steps = 0;
  const memo = new Map<string, Tri>();

  // Evaluate any canonical node over a box, memoized per (node, box) so that
  // sub-conditions shared between the two policies are evaluated at most
  // once per box. Each uncached node evaluation consumes one budget step.
  const evalNode = (c: Cond, box: Box): Tri => {
    const key = `${factory.id(c)}|${boxKey(box, attrs)}`;
    const hit = memo.get(key);
    if (hit !== undefined) return hit;
    if (steps >= budget) throw new BudgetExceeded(steps);
    steps += 1;
    let v: Tri;
    switch (c.tag) {
      case "not": {
        const inner = evalNode(c.of, box);
        v = inner === "T" ? "F" : inner === "F" ? "T" : "M";
        break;
      }
      case "and": {
        let mixed = false;
        v = "T";
        for (const sub of c.args) {
          const r = evalNode(sub, box);
          if (r === "F") {
            v = "F";
            break;
          }
          if (r === "M") mixed = true;
        }
        if (v !== "F") v = mixed ? "M" : "T";
        break;
      }
      case "or": {
        let mixed = false;
        v = "F";
        for (const sub of c.args) {
          const r = evalNode(sub, box);
          if (r === "T") {
            v = "T";
            break;
          }
          if (r === "M") mixed = true;
        }
        if (v !== "T") v = mixed ? "M" : "F";
        break;
      }
      default:
        v = evalBox(c, box);
        break;
    }
    memo.set(key, v);
    return v;
  };

  // Search for a point satisfying `inner and not outer`.
  const target = factory.and([inner, factory.not(outer)]);

  const attrSize = (b: AttrBox): number => {
    if (b.kind === "int") return b.hi - b.lo + 1;
    if (b.kind === "enum") return b.values.length;
    return b.sets.length;
  };

  // Existence search: returns some counterexample point in the box, or null
  // when the box provably contains none. Splits the largest attribute first.
  const search = (box: Box): Assignment | null => {
    const v = evalNode(target, box);
    if (v === "F") return null;
    if (v === "T") return minPoint(box, attrs);
    if (boxIsSingleton(box, attrs)) {
      // A singleton box cannot genuinely be mixed; fall back to exact
      // point evaluation (defensive, keeps the engine total).
      const point = minPoint(box, attrs);
      return evalPoint(target, point) === "true" ? point : null;
    }
    let attr: string | null = null;
    let best = 1;
    for (const a of attrs) {
      const size = attrSize(box[a]);
      if (size > best) {
        best = size;
        attr = a;
      }
    }
    if (attr === null) return null;
    const [lower, upper] = splitAttr(box[attr]);
    const found = search({ ...box, [attr]: lower });
    if (found !== null) return found;
    return search({ ...box, [attr]: upper });
  };

  // Minimization: shrink each attribute (in sorted order) to the smallest
  // value that still admits a counterexample, using existence queries over
  // prefixes. Feasibility is monotone in the prefix length, so binary search
  // applies. The result is the lexicographically minimal counterexample.
  const minimize = (start: Box): Assignment => {
    const box: Box = { ...start };
    for (const a of attrs) {
      const b = box[a];
      if (b.kind === "int") {
        let lo = b.lo;
        let hi = b.hi;
        while (lo < hi) {
          const mid = Math.floor((lo + hi) / 2);
          const probe = search({ ...box, [a]: { kind: "int", lo: b.lo, hi: mid } });
          if (probe !== null) hi = mid;
          else lo = mid + 1;
        }
        box[a] = { kind: "int", lo, hi: lo };
      } else if (b.kind === "enum") {
        let lo = 1;
        let hi = b.values.length;
        while (lo < hi) {
          const mid = Math.floor((lo + hi) / 2);
          const probe = search({ ...box, [a]: { kind: "enum", values: b.values.slice(0, mid) } });
          if (probe !== null) hi = mid;
          else lo = mid + 1;
        }
        box[a] = { kind: "enum", values: [b.values[lo - 1]] };
      } else {
        let lo = 1;
        let hi = b.sets.length;
        while (lo < hi) {
          const mid = Math.floor((lo + hi) / 2);
          const probe = search({ ...box, [a]: { kind: "set", sets: b.sets.slice(0, mid) } });
          if (probe !== null) hi = mid;
          else lo = mid + 1;
        }
        box[a] = { kind: "set", sets: [b.sets[lo - 1]] };
      }
    }
    return minPoint(box, attrs);
  };

  try {
    const start = initialBox(domain, attrs);
    const some = search(start);
    if (some !== null) {
      return { status: "not-contained", counterexample: minimize(start) };
    }
    return { status: "contained" };
  } catch (e) {
    if (e instanceof BudgetExceeded) {
      return { status: "undecided", reason: "budget", stepsUsed: e.stepsUsed };
    }
    throw e;
  }
}

/** Containment between the applicability ranges of two policies. */
export function policyContains(
  factory: ConditionFactory,
  outer: Policy,
  inner: Policy,
  domain: DomainSpec,
  opts: ContainsOptions = {},
): ContainsResult {
  return contains(factory, outer.condition, inner.condition, domain, opts);
}
