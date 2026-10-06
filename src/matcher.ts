import { Path, Tree, isList, showTree, treeEqual } from './tree';
import { Pattern, validatePattern } from './pattern';

/**
 * One binding of a successful match. A node variable binds the matched node;
 * a tail variable binds the matched slice as a list (possibly the empty list).
 */
export interface Binding {
  name: string;
  value: Tree;
}

export interface MatchOk {
  ok: true;
  /** Stable: one entry per name, in first-occurrence order in the pattern. */
  bindings: Binding[];
}

export interface MatchFail {
  ok: false;
  /**
   * Shallowest path at which the mismatch is decided: length mismatches are
   * reported at the list itself, node mismatches at the offending node, and
   * repeated-variable conflicts at the later occurrence.
   */
  path: Path;
  reason: string;
}

export type MatchResult = MatchOk | MatchFail;

interface Failure {
  path: Path;
  reason: string;
}

/**
 * Matches `pattern` against `subject`. Deterministic and left-to-right, so
 * repeated runs yield identical results. Every occurrence of a name must bind
 * a structurally equal value, otherwise the match fails at the later
 * occurrence.
 */
export function match(pattern: Pattern, subject: Tree): MatchResult {
  validatePattern(pattern);
  const bindings = new Map<string, Tree>();
  const failure = matchNode(pattern, subject, [], bindings);
  if (failure !== null) {
    return { ok: false, path: failure.path, reason: failure.reason };
  }
  return {
    ok: true,
    bindings: [...bindings.entries()].map(([name, value]) => ({ name, value })),
  };
}

function matchNode(
  pattern: Pattern,
  subject: Tree,
  path: Path,
  bindings: Map<string, Tree>,
): Failure | null {
  switch (pattern.kind) {
    case 'lit':
      return treeEqual(pattern.value, subject)
        ? null
        : {
            path,
            reason: `literal ${showTree(pattern.value)} does not match ${showTree(subject)}`,
          };
    case 'var': {
      const prev = bindings.get(pattern.name);
      if (prev === undefined) {
        bindings.set(pattern.name, subject);
        return null;
      }
      return treeEqual(prev, subject)
        ? null
        : {
            path,
            reason: `variable "${pattern.name}" re-bound to unequal node ${showTree(subject)}`,
          };
    }
    case 'list': {
      if (!isList(subject)) {
        return { path, reason: `expected a list, found atom ${showTree(subject)}` };
      }
      const items = pattern.items;
      const last = items[items.length - 1];
      const hasTail = last !== undefined && last.kind === 'tail';
      const fixed = hasTail ? items.length - 1 : items.length;
      if (!hasTail && subject.length !== items.length) {
        return {
          path,
          reason: `list length ${subject.length} does not match ${items.length} pattern items`,
        };
      }
      if (hasTail && subject.length < fixed) {
        return {
          path,
          reason: `list length ${subject.length} shorter than ${fixed} items before the tail`,
        };
      }
      for (let i = 0; i < fixed; i++) {
        const failure = matchNode(items[i] as Pattern, subject[i], [...path, i], bindings);
        if (failure !== null) return failure;
      }
      if (hasTail) {
        const name = (last as { kind: 'tail'; name: string }).name;
        const rest = subject.slice(fixed);
        const prev = bindings.get(name);
        if (prev === undefined) {
          bindings.set(name, rest);
        } else if (!treeEqual(prev, rest)) {
          return {
            path: [...path, fixed],
            reason: `tail variable "${name}" re-bound to unequal slice ${showTree(rest)}`,
          };
        }
      }
      return null;
    }
  }
}

/**
 * Substitutes bindings into a pattern, producing a tree. Tail variables
 * splice their bound list in place. Throws on unbound names. For any
 * successful `match(pattern, subject)`, the result equals `subject`.
 */
export function substitute(
  pattern: Pattern,
  bindings: Binding[] | ReadonlyMap<string, Tree>,
): Tree {
  validatePattern(pattern);
  const map: ReadonlyMap<string, Tree> = Array.isArray(bindings)
    ? new Map(bindings.map((b) => [b.name, b.value]))
    : bindings;
  const lookup = (name: string): Tree => {
    const value = map.get(name);
    if (value === undefined) throw new Error(`unbound variable "${name}"`);
    return value;
  };
  const walk = (p: Pattern): Tree => {
    switch (p.kind) {
      case 'lit':
        return p.value;
      case 'var':
        return lookup(p.name);
      case 'list': {
        const out: Tree[] = [];
        for (const item of p.items) {
          if (item.kind === 'tail') {
            const value = lookup(item.name);
            if (!isList(value)) {
              throw new Error(`tail variable "${item.name}" bound to a non-list`);
            }
            out.push(...value);
          } else {
            out.push(walk(item));
          }
        }
        return out;
      }
    }
  };
  return walk(pattern);
}
