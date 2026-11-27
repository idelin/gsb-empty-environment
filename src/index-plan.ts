import type {
  BinaryExpr,
  Expr,
  JsonPathAst,
  JsonValue,
  PathExpr,
  Step,
  Subscript,
} from './ast.ts';

/**
 * An inverted-index fact of the kind stored by a GIN jsonb_path_ops index.
 *
 * - `key` facts: a member with this key is reachable at exactly this key path
 *   (arrays are transparent, so no array indices are recorded).
 * - `leaf` facts: the value reachable at exactly this key path is a scalar
 *   whose canonical form equals `value`.
 */
export type IndexFact =
  | { readonly kind: 'key'; readonly keyPath: readonly string[] }
  | {
      readonly kind: 'leaf';
      readonly keyPath: readonly string[];
      readonly value: string | number | boolean | null;
    };

/**
 * Conjunctive filter: every fact in `all` must be present.
 * A suffix constraint requires the fact's key path to end with `keyPath`.
 */
export interface Conjunction {
  readonly exact: readonly IndexFact[];
  readonly suffixKeyPaths: readonly (readonly string[])[];
  readonly suffixLeaves: readonly Extract<IndexFact, { kind: 'leaf' }>[];
  /**
   * Paths that must appear within the subtree rooted at `prefix`. Used once a
   * wildcard/recursive barrier has made positions relative: `prefix` anchors
   * the subtree, `suffix` is matched somewhere below it.
   */
  readonly anchoredSuffixes: readonly {
    readonly prefix: readonly string[];
    readonly suffix: readonly string[];
  }[];
  readonly anchoredLeaves: readonly {
    readonly prefix: readonly string[];
    readonly suffix: readonly string[];
    readonly value: string | number | boolean | null;
  }[];
}

export interface IndexPlan {
  /** `true` means the expression cannot constrain the index safely -> full scan. */
  readonly fullScan: boolean;
  /** Disjunction of conjunctions (DNF). A document matches if any group passes. */
  readonly groups: readonly Conjunction[];
}

export const FULL_SCAN: IndexPlan = { fullScan: true, groups: [] };

const EMPTY_GROUP: Conjunction = {
  exact: [],
  suffixKeyPaths: [],
  suffixLeaves: [],
  anchoredSuffixes: [],
  anchoredLeaves: [],
};

export function unconstrainedPlan(): IndexPlan {
  return { fullScan: false, groups: [EMPTY_GROUP] };
}

function factKey(fact: IndexFact): string {
  const path = fact.keyPath.join('.');
  return fact.kind === 'key' ? `k:${path}` : `v:${path}=${canonicalScalar(fact.value)}`;
}

export function canonicalScalar(value: string | number | boolean | null): string {
  if (value === null) return 'null';
  if (typeof value === 'number') return `n:${String(value)}`;
  if (typeof value === 'boolean') return `b:${value ? 't' : 'f'}`;
  return `s:${value}`;
}

function mergeConjunctions(left: Conjunction, right: Conjunction): Conjunction {
  const exactMap = new Map<string, IndexFact>();
  for (const fact of [...left.exact, ...right.exact]) {
    exactMap.set(factKey(fact), fact);
  }
  const suffixMap = new Map<string, readonly string[]>();
  for (const suffix of [...left.suffixKeyPaths, ...right.suffixKeyPaths]) {
    suffixMap.set(suffix.join('.'), suffix);
  }
  const leafMap = new Map<string, Extract<IndexFact, { kind: 'leaf' }>>();
  for (const leaf of [...left.suffixLeaves, ...right.suffixLeaves]) {
    leafMap.set(factKey(leaf), leaf);
  }
  const anchoredMap = new Map<string, { prefix: readonly string[]; suffix: readonly string[] }>();
  for (const anchored of [...left.anchoredSuffixes, ...right.anchoredSuffixes]) {
    anchoredMap.set(`${anchored.prefix.join('.')}|${anchored.suffix.join('.')}`, anchored);
  }
  const anchoredLeafMap = new Map<
    string,
    {
      prefix: readonly string[];
      suffix: readonly string[];
      value: string | number | boolean | null;
    }
  >();
  for (const leaf of [...left.anchoredLeaves, ...right.anchoredLeaves]) {
    anchoredLeafMap.set(
      `${leaf.prefix.join('.')}|${leaf.suffix.join('.')}=${canonicalScalar(leaf.value)}`,
      leaf,
    );
  }
  return {
    exact: [...exactMap.values()],
    suffixKeyPaths: [...suffixMap.values()],
    suffixLeaves: [...leafMap.values()],
    anchoredSuffixes: [...anchoredMap.values()],
    anchoredLeaves: [...anchoredLeafMap.values()],
  };
}

/** Intersection (AND): distribute groups over groups. */
export function intersectPlans(left: IndexPlan, right: IndexPlan): IndexPlan {
  if (left.fullScan || right.fullScan) return FULL_SCAN;
  const groups: Conjunction[] = [];
  for (const leftGroup of left.groups) {
    for (const rightGroup of right.groups) {
      groups.push(mergeConjunctions(leftGroup, rightGroup));
    }
  }
  return { fullScan: false, groups };
}

/** Union (OR). An unselective branch forces a safe full scan. */
export function unionPlans(left: IndexPlan, right: IndexPlan): IndexPlan {
  if (left.fullScan || right.fullScan) return FULL_SCAN;
  return { fullScan: false, groups: [...left.groups, ...right.groups] };
}

function singleFactPlan(fact: IndexFact): IndexPlan {
  return {
    fullScan: false,
    groups: [
      {
        exact: [fact],
        suffixKeyPaths: [],
        suffixLeaves: [],
        anchoredSuffixes: [],
        anchoredLeaves: [],
      },
    ],
  };
}

function suffixPlan(keyPath: readonly string[]): IndexPlan {
  return {
    fullScan: false,
    groups: [
      {
        exact: [],
        suffixKeyPaths: [keyPath],
        suffixLeaves: [],
        anchoredSuffixes: [],
        anchoredLeaves: [],
      },
    ],
  };
}

function anchoredSuffixPlan(
  prefix: readonly string[],
  suffix: readonly string[],
): IndexPlan {
  return {
    fullScan: false,
    groups: [
      {
        exact: [],
        suffixKeyPaths: [],
        suffixLeaves: [],
        anchoredSuffixes: [{ prefix, suffix }],
        anchoredLeaves: [],
      },
    ],
  };
}

function anchoredLeafPlan(
  prefix: readonly string[],
  suffix: readonly string[],
  value: string | number | boolean | null,
): IndexPlan {
  return {
    fullScan: false,
    groups: [
      {
        exact: [],
        suffixKeyPaths: [],
        suffixLeaves: [],
        anchoredSuffixes: [],
        anchoredLeaves: [{ prefix, suffix, value }],
      },
    ],
  };
}

/**
 * Extract a sound (no-false-negative) index plan.
 *
 * Soundness rule: whenever in doubt about what a path can touch, return
 * FULL_SCAN. Over-approximation (a weaker plan) only costs selectivity;
 * an over-strong plan would drop matching documents.
 */
export function extractIndexPlan(ast: JsonPathAst): IndexPlan {
  return extractPath(ast.expr, [], false);
}

function extractPath(
  path: PathExpr,
  contextPrefix: readonly string[],
  inheritedBarrier: boolean,
  barrierAnchor: readonly string[] = [],
): IndexPlan {
  if (path.base.type !== 'root' && path.base.type !== 'current') {
    // Named variables are runtime bindings; index facts are tied to the document.
    return FULL_SCAN;
  }
  // A `@`-relative path inside a filter continues the enclosing key path;
  // a `$` path restarts at the document root.
  const initialPrefix = path.base.type === 'current' ? [...contextPrefix] : [];
  // A `$` root reference inside a filter escapes any enclosing barrier.
  const barrier = path.base.type === 'current' ? inheritedBarrier : false;
  const anchor = path.base.type === 'current' ? barrierAnchor : [];
  return extractSteps(path.steps, initialPrefix, barrier, anchor);
}

function extractSteps(
  steps: readonly Step[],
  prefix: readonly string[],
  initialBarrier = false,
  initialAnchor: readonly string[] = [],
): IndexPlan {
  let plan = unconstrainedPlan();
  let currentPrefix = [...prefix];
  let wildcardBarrier = initialBarrier;
  let prefixAtBarrier = initialBarrier ? [...initialAnchor] : null;

  for (const step of steps) {
    switch (step.type) {
      case 'member':
        currentPrefix = [...currentPrefix, step.key];
        if (wildcardBarrier) {
          // Positional certainty is lost: anchor the requirement to the
          // subtree that existed when the barrier fired, then match the
          // remaining tail somewhere inside that subtree.
          const anchor = prefixAtBarrier ?? [];
          const suffix = currentPrefix.slice(anchor.length);
          plan = intersectPlans(plan, anchoredSuffixPlan(anchor, suffix));
        } else {
          plan = intersectPlans(plan, singleFactPlan({ kind: 'key', keyPath: currentPrefix }));
        }
        break;

      case 'wildcardMember':
        // Any member name from here on: switch to suffix matching.
        if (!wildcardBarrier) {
          wildcardBarrier = true;
          prefixAtBarrier = [...currentPrefix];
        }
        break;

      case 'recursiveDescendant':
        // Reachability becomes suffix-only below a `.**`.
        if (!wildcardBarrier) {
          wildcardBarrier = true;
          prefixAtBarrier = [...currentPrefix];
        }
        break;

      case 'subscript': {
        const subPlan = extractSubscripts(step.subscripts, currentPrefix, wildcardBarrier);
        plan = intersectPlans(plan, subPlan);
        break;
      }

      case 'filter': {
        plan = intersectPlans(
          plan,
          extractPredicate(
            step.predicate,
            currentPrefix,
            wildcardBarrier,
            prefixAtBarrier ?? currentPrefix,
          ),
        );
        break;
      }

      case 'method': {
        // Methods consume the current sequence; key existence up to here is
        // already recorded. A `.type()/size()/double()` adds no value fact.
        break;
      }
    }
  }
  return plan;
}

function extractSubscripts(
  subscripts: readonly Subscript[],
  prefix: readonly string[],
  wildcardBarrier: boolean,
): IndexPlan {
  // A subscript applied at the document root (`$[...]`) carries no member
  // fact: arrays are transparent and root scalars are wrapped in lax mode.
  if (prefix.length === 0) return unconstrainedPlan();
  let plan = unconstrainedPlan();
  for (const subscript of subscripts) {
    if (subscript.kind === 'wildcardIndex') {
      // Arrays are transparent in jsonb_path_ops: no fact mentions indices.
      continue;
    }
    if (subscript.kind === 'slice') {
      // A non-empty slice requires the array at this prefix to exist.
      const requirement: IndexPlan = wildcardBarrier
        ? suffixPlan(prefix)
        : singleFactPlan({ kind: 'key', keyPath: prefix });
      plan = intersectPlans(plan, requirement);
      continue;
    }
    // An index expression could evaluate out of bounds; the only sound fact is
    // that the array itself exists at this prefix.
    const requirement: IndexPlan = wildcardBarrier
      ? suffixPlan(prefix)
      : singleFactPlan({ kind: 'key', keyPath: prefix });
    plan = intersectPlans(plan, requirement);
  }
  return plan;
}

function extractPredicate(
  expr: Expr,
  contextPrefix: readonly string[],
  wildcardBarrier: boolean,
  barrierAnchor: readonly string[],
): IndexPlan {
  switch (expr.type) {
    case 'literal':
      // A constant filter either always or never passes; no document fact needed.
      return unconstrainedPlan();
    case 'root':
    case 'current':
    case 'variable':
      return FULL_SCAN;
    case 'path':
      return extractPath(expr, contextPrefix, wildcardBarrier, barrierAnchor);
    case 'exists':
      return extractPath(expr.argument, contextPrefix, wildcardBarrier, barrierAnchor);
    case 'unary': {
      // Negation cannot be pushed down without risking false negatives.
      return FULL_SCAN;
    }
    case 'likeRegex':
      // Pattern matching is not representable as a GIN value fact.
      return FULL_SCAN;
    case 'binary':
      return extractBinaryPredicate(expr, contextPrefix, wildcardBarrier, barrierAnchor);
  }
}

function extractBinaryPredicate(
  expr: BinaryExpr,
  contextPrefix: readonly string[],
  wildcardBarrier: boolean,
  barrierAnchor: readonly string[],
): IndexPlan {
  const { operator } = expr;

  if (operator === '&&') {
    return intersectPlans(
      extractPredicate(expr.left, contextPrefix, wildcardBarrier, barrierAnchor),
      extractPredicate(expr.right, contextPrefix, wildcardBarrier, barrierAnchor),
    );
  }
  if (operator === '||') {
    return unionPlans(
      extractPredicate(expr.left, contextPrefix, wildcardBarrier, barrierAnchor),
      extractPredicate(expr.right, contextPrefix, wildcardBarrier, barrierAnchor),
    );
  }

  if (operator === '==') {
    return extractEquality(
      expr.left,
      expr.right,
      contextPrefix,
      wildcardBarrier,
      barrierAnchor,
    );
  }
  if (operator === '!=') {
    // Inequality of two document-touching sides is not a positive requirement.
    return FULL_SCAN;
  }

  // Ordering operators only imply existence of the path, never a value fact.
  if (operator === '<' || operator === '<=' || operator === '>' || operator === '>=') {
    // Each independently extractable side contributes an existence fact;
    // a literal or unextractable side simply adds no constraint.
    const plans: IndexPlan[] = [];
    for (const side of [expr.left, expr.right]) {
      const path = documentPath(side);
      if (path !== null) {
        plans.push(extractPath(path, contextPrefix, wildcardBarrier, barrierAnchor));
      }
    }
    if (plans.length === 0) return FULL_SCAN;
    return plans.reduce(intersectPlans);
  }

  // Arithmetic inside a filter yields no stable key fact.
  return FULL_SCAN;
}

function extractEquality(
  left: Expr,
  right: Expr,
  contextPrefix: readonly string[],
  wildcardBarrier: boolean,
  barrierAnchor: readonly string[],
): IndexPlan {
  const leftPath = documentPath(left);
  const rightPath = documentPath(right);
  const leftLiteral = scalarLiteral(left);
  const rightLiteral = scalarLiteral(right);

  if (leftPath !== null && rightLiteral !== undefined) {
    return equalityValuePlan(
      leftPath,
      rightLiteral,
      contextPrefix,
      wildcardBarrier,
      barrierAnchor,
    );
  }
  if (rightPath !== null && leftLiteral !== undefined) {
    return equalityValuePlan(
      rightPath,
      leftLiteral,
      contextPrefix,
      wildcardBarrier,
      barrierAnchor,
    );
  }
  // Both sides are paths: only their existence requirements are sound.
  const plans: IndexPlan[] = [];
  if (leftPath !== null) {
    plans.push(extractPath(leftPath, contextPrefix, wildcardBarrier, barrierAnchor));
  }
  if (rightPath !== null) {
    plans.push(extractPath(rightPath, contextPrefix, wildcardBarrier, barrierAnchor));
  }
  if (plans.length === 0) return FULL_SCAN;
  return plans.reduce(intersectPlans);
}

function equalityValuePlan(
  path: PathExpr,
  value: string | number | boolean | null,
  contextPrefix: readonly string[],
  wildcardBarrier: boolean,
  barrierAnchor: readonly string[],
): IndexPlan {
  // A value fact is only safe when the full key path is positional (no
  // wildcard or recursive descent steps), otherwise equality could hold at a
  // position the fact does not describe.
  if (!pathStepsArePositional(path.steps)) {
    return extractPath(path, contextPrefix, wildcardBarrier, barrierAnchor);
  }
  if (path.base.type !== 'root' && path.base.type !== 'current') return FULL_SCAN;
  const keyPath: string[] = path.base.type === 'current' ? [...contextPrefix] : [];
  for (const step of path.steps) {
    if (step.type === 'member') keyPath.push(step.key);
  }
  // Behind a `.*`/`.**` barrier only suffix matching is sound.
  if (!wildcardBarrier) {
    return singleFactPlan({ kind: 'leaf', keyPath, value });
  }
  const suffix = keyPath.slice(barrierAnchor.length);
  return anchoredLeafPlan(barrierAnchor, suffix, value);
}

function documentPath(expr: Expr): PathExpr | null {
  if (expr.type === 'path' && (expr.base.type === 'root' || expr.base.type === 'current')) {
    return expr;
  }
  return null;
}

function scalarLiteral(expr: Expr): string | number | boolean | null | undefined {
  if (expr.type === 'literal') {
    if (
      expr.value === null ||
      typeof expr.value === 'string' ||
      typeof expr.value === 'number' ||
      typeof expr.value === 'boolean'
    ) {
      return expr.value;
    }
    return undefined;
  }
  return undefined;
}

function pathStepsArePositional(steps: readonly Step[]): boolean {
  for (const step of steps) {
    if (step.type === 'wildcardMember' || step.type === 'recursiveDescendant') return false;
    if (step.type === 'subscript') {
      for (const subscript of step.subscripts) {
        if (subscript.kind !== 'wildcardIndex') return false;
      }
    }
    if (step.type === 'filter' || step.type === 'method') return false;
  }
  return true;
}

export type { JsonValue };
