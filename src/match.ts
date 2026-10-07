/**
 * Rule-tree pattern matching.
 *
 * A pattern is a tree built from:
 *   - literal atoms (string | number | boolean | null)
 *   - single-node variables  v("x")   — match exactly one node
 *   - tail variables         t("rest") — match the (possibly empty) rest of a
 *     list, and may only appear as the last element of a list
 *
 * Matching is deterministic (no backtracking is needed because tail variables
 * only appear at the end of a list). A variable with the same name must bind
 * to a structurally equal value at every occurrence.
 *
 * On success, `match` returns bindings as an ordered list of [name, tree]
 * pairs in first-occurrence pre-order — stable across runs. On failure it
 * returns the shallowest failure path (fewest indices from the root; ties
 * broken leftmost).
 */

export type Atom = string | number | boolean | null;
export type Tree = Atom | Tree[];

export interface Var {
  readonly kind: "var";
  readonly name: string;
}
export interface Tail {
  readonly kind: "tail";
  readonly name: string;
}
export type PatternNode = Atom | Var | Tail | PatternNode[];

/** Single-node variable pattern. */
export function v(name: string): Var {
  return { kind: "var", name };
}

/** Tail variable pattern; only legal as the last element of a list. */
export function t(name: string): Tail {
  return { kind: "tail", name };
}

export function isVar(x: unknown): x is Var {
  return (
    typeof x === "object" && x !== null && (x as Var).kind === "var" &&
    typeof (x as Var).name === "string"
  );
}

export function isTail(x: unknown): x is Tail {
  return (
    typeof x === "object" && x !== null && (x as Tail).kind === "tail" &&
    typeof (x as Tail).name === "string"
  );
}

export function isAtom(x: unknown): x is Atom {
  return (
    x === null || typeof x === "string" || typeof x === "number" ||
    typeof x === "boolean"
  );
}

/** Structural equality on trees. */
export function equalTree(a: Tree, b: Tree): boolean {
  if (Array.isArray(a) || Array.isArray(b)) {
    if (!Array.isArray(a) || !Array.isArray(b)) return false;
    if (a.length !== b.length) return false;
    for (let i = 0; i < a.length; i++) {
      if (!equalTree(a[i], b[i])) return false;
    }
    return true;
  }
  return Object.is(a, b);
}

export class PatternError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "PatternError";
  }
}

function checkName(name: string): void {
  if (typeof name !== "string" || name.length === 0) {
    throw new PatternError("variable name must be a non-empty string");
  }
}

function validateNode(node: PatternNode): void {
  if (isVar(node)) {
    checkName(node.name);
    return;
  }
  if (isTail(node)) {
    throw new PatternError(
      "tail variable may only appear as the last element of a list",
    );
  }
  if (Array.isArray(node)) {
    for (let i = 0; i < node.length; i++) {
      const child = node[i];
      if (isTail(child)) {
        if (i !== node.length - 1) {
          throw new PatternError(
            "tail variable may only appear as the last element of a list",
          );
        }
        checkName(child.name);
      } else {
        validateNode(child);
      }
    }
    return;
  }
  if (!isAtom(node)) {
    throw new PatternError(`invalid pattern node: ${String(node)}`);
  }
}

/** Throws PatternError if the pattern is malformed. */
export function validatePattern(pattern: PatternNode): void {
  validateNode(pattern);
}

function validateTree(tree: Tree): void {
  if (Array.isArray(tree)) {
    for (const child of tree) validateTree(child);
    return;
  }
  if (!isAtom(tree)) {
    throw new PatternError(`invalid tree node: ${String(tree)}`);
  }
}

export type Binding = readonly [name: string, value: Tree];

export type FailureReason =
  | "atom-mismatch"
  | "kind-mismatch"
  | "length-mismatch"
  | "variable-conflict";

export interface MatchFailure {
  /** Indices from the root to the failing position. */
  readonly path: readonly number[];
  readonly reason: FailureReason;
  readonly detail: string;
}

export type MatchResult =
  | { readonly ok: true; readonly bindings: Binding[] }
  | { readonly ok: false; readonly failure: MatchFailure };

type Step =
  | { ok: true; added: Binding[] }
  | { ok: false; failure: MatchFailure };

function fail(
  path: readonly number[],
  reason: FailureReason,
  detail: string,
): Step {
  return { ok: false, failure: { path, reason, detail } };
}

function show(node: unknown): string {
  return JSON.stringify(node) ?? String(node);
}

function matchNode(
  pattern: PatternNode,
  tree: Tree,
  path: number[],
  env: ReadonlyMap<string, Tree>,
): Step {
  if (isVar(pattern)) {
    if (env.has(pattern.name)) {
      if (equalTree(env.get(pattern.name)!, tree)) {
        return { ok: true, added: [] };
      }
      return fail(
        path,
        "variable-conflict",
        `variable "${pattern.name}" is bound to ${
          show(env.get(pattern.name))
        } but matched against ${show(tree)}`,
      );
    }
    return { ok: true, added: [[pattern.name, tree]] };
  }

  if (Array.isArray(pattern)) {
    if (!Array.isArray(tree)) {
      return fail(
        path,
        "kind-mismatch",
        `list pattern matched against atom ${show(tree)}`,
      );
    }
    const last = pattern[pattern.length - 1];
    const hasTail = pattern.length > 0 && isTail(last);
    const fixed = hasTail ? pattern.slice(0, -1) : pattern;
    const arityOk = hasTail
      ? tree.length >= fixed.length
      : tree.length === fixed.length;
    if (!arityOk) {
      return fail(
        path,
        "length-mismatch",
        `pattern expects ${hasTail ? "at least " : ""}${fixed.length} element(s), found ${tree.length}`,
      );
    }

    const added: Binding[] = [];
    const localEnv = new Map(env);
    const failures: MatchFailure[] = [];

    for (let i = 0; i < fixed.length; i++) {
      const step = matchNode(fixed[i], tree[i], [...path, i], localEnv);
      if (step.ok) {
        for (const [name, value] of step.added) {
          localEnv.set(name, value);
          added.push([name, value]);
        }
      } else {
        failures.push(step.failure);
      }
    }

    if (hasTail) {
      const tailVar = last as Tail;
      const tailValue: Tree = tree.slice(fixed.length);
      const tailPath = [...path, fixed.length];
      if (localEnv.has(tailVar.name)) {
        if (!equalTree(localEnv.get(tailVar.name)!, tailValue)) {
          failures.push({
            path: tailPath,
            reason: "variable-conflict",
            detail: `tail variable "${tailVar.name}" is bound to ${
              show(localEnv.get(tailVar.name))
            } but matched against ${show(tailValue)}`,
          });
        }
      } else {
        localEnv.set(tailVar.name, tailValue);
        added.push([tailVar.name, tailValue]);
      }
    }

    if (failures.length > 0) {
      let best = failures[0];
      for (const f of failures) {
        if (f.path.length < best.path.length) best = f;
      }
      return { ok: false, failure: best };
    }
    return { ok: true, added };
  }

  // Literal atom pattern.
  if (Array.isArray(tree)) {
    return fail(
      path,
      "kind-mismatch",
      `atom pattern ${show(pattern)} matched against a list`,
    );
  }
  if (Object.is(pattern, tree)) {
    return { ok: true, added: [] };
  }
  return fail(
    path,
    "atom-mismatch",
    `atom ${show(pattern)} does not equal ${show(tree)}`,
  );
}

/**
 * Matches `pattern` against `tree`.
 *
 * On success returns stable bindings: each variable name appears once, in
 * first-occurrence pre-order of the pattern. On failure returns the
 * shallowest failure path (ties broken leftmost).
 */
export function match(pattern: PatternNode, tree: Tree): MatchResult {
  validatePattern(pattern);
  validateTree(tree);
  const step = matchNode(pattern, tree, [], new Map());
  if (!step.ok) return { ok: false, failure: step.failure };
  const bindings: Binding[] = [];
  const seen = new Set<string>();
  for (const [name, value] of step.added) {
    if (!seen.has(name)) {
      seen.add(name);
      bindings.push([name, value]);
    }
  }
  return { ok: true, bindings };
}

/**
 * Substitutes bindings into a pattern, producing a concrete tree.
 * Tail variables splice their (list) value into the surrounding list.
 * Throws if a variable is unbound or a tail variable is bound to a non-list.
 */
export function substitute(
  pattern: PatternNode,
  bindings: ReadonlyMap<string, Tree> | Iterable<Binding>,
): Tree {
  const map: ReadonlyMap<string, Tree> = bindings instanceof Map
    ? bindings
    : new Map(bindings);
  const go = (node: PatternNode): Tree => {
    if (isVar(node)) {
      if (!map.has(node.name)) {
        throw new PatternError(`unbound variable "${node.name}"`);
      }
      return map.get(node.name)!;
    }
    if (isTail(node)) {
      throw new PatternError("tail variable cannot stand alone");
    }
    if (Array.isArray(node)) {
      const out: Tree[] = [];
      for (const child of node) {
        if (isTail(child)) {
          if (!map.has(child.name)) {
            throw new PatternError(`unbound tail variable "${child.name}"`);
          }
          const value = map.get(child.name)!;
          if (!Array.isArray(value)) {
            throw new PatternError(
              `tail variable "${child.name}" is bound to a non-list`,
            );
          }
          out.push(...value);
        } else {
          out.push(go(child));
        }
      }
      return out;
    }
    return node;
  };
  return go(pattern);
}

/** Collects variable names used in a pattern, in first-occurrence order. */
export function patternVariables(pattern: PatternNode): {
  singles: string[];
  tails: string[];
} {
  const singles: string[] = [];
  const tails: string[] = [];
  const push = (list: string[], name: string) => {
    if (!list.includes(name)) list.push(name);
  };
  const walk = (node: PatternNode): void => {
    if (isVar(node)) {
      push(singles, node.name);
    } else if (isTail(node)) {
      push(tails, node.name);
    } else if (Array.isArray(node)) {
      for (const child of node) walk(child);
    }
  };
  walk(pattern);
  return { singles, tails };
}
