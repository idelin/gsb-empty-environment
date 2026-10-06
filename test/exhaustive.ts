import {
  Atom,
  Item,
  Pattern,
  Tree,
  match,
  nodeAt,
  occurrencePaths,
  patternNames,
  substitute,
  treeEqual,
} from '../src';
import { check, section } from './harness';

const ATOMS: Atom[] = ['a', 'b'];
const VAR_NAMES = ['x', 'y'];
const TAIL_NAMES = ['xs', 'ys'];
const MAX_LIST_LEN = 3;
const TREE_BUDGET = 5;
const PATTERN_BUDGET = 4;

/** All trees whose node count (atoms + lists) is exactly `budget`. */
function* trees(budget: number): Generator<Tree> {
  if (budget === 1) {
    for (const atom of ATOMS) yield atom;
    return;
  }
  if (budget >= 2) {
    for (let len = 0; len <= Math.min(MAX_LIST_LEN, budget - 1); len++) {
      for (const children of treeTuples(len, budget - 1)) {
        yield children;
      }
    }
  }
}

/** All tuples of `len` trees whose node counts sum to `budget`. */
function* treeTuples(len: number, budget: number): Generator<Tree[]> {
  if (len === 0) {
    if (budget === 0) yield [];
    return;
  }
  for (let head = 1; head <= budget - (len - 1); head++) {
    for (const first of trees(head)) {
      for (const rest of treeTuples(len - 1, budget - head)) {
        yield [first, ...rest];
      }
    }
  }
}

/** All valid patterns whose node count is exactly `budget`. */
function* patterns(budget: number): Generator<Pattern> {
  if (budget === 1) {
    for (const atom of ATOMS) yield { kind: 'lit', value: atom };
    for (const name of VAR_NAMES) yield { kind: 'var', name };
    return;
  }
  if (budget >= 2) {
    for (const items of itemLists(budget - 1)) {
      yield { kind: 'list', items };
    }
  }
}

/** All item lists whose node counts sum to `budget`; tails only at the end. */
function* itemLists(budget: number): Generator<Item[]> {
  yield [];
  for (let len = 1; len <= Math.min(MAX_LIST_LEN, budget); len++) {
    for (const items of itemTuples(len, budget)) {
      yield items;
    }
  }
}

function* itemTuples(len: number, budget: number): Generator<Item[]> {
  if (len === 1) {
    for (let head = 1; head <= budget; head++) {
      if (head === 1) {
        for (const name of TAIL_NAMES) yield [{ kind: 'tail', name }];
      }
      for (const p of patterns(head)) yield [p];
    }
    return;
  }
  for (let head = 1; head <= budget - (len - 1); head++) {
    for (const p of patterns(head)) {
      for (const rest of itemTuples(len - 1, budget - head)) {
        yield [p, ...rest];
      }
    }
  }
}

function allTrees(): Tree[] {
  const out: Tree[] = [];
  for (let b = 1; b <= TREE_BUDGET; b++) for (const t of trees(b)) out.push(t);
  return out;
}

function allPatterns(): Pattern[] {
  const out: Pattern[] = [];
  for (let b = 1; b <= PATTERN_BUDGET; b++) for (const p of patterns(b)) out.push(p);
  return out;
}

function keyOf(tree: Tree): string {
  return JSON.stringify(tree);
}

/** All subtrees of a tree, structurally deduplicated (candidates for `var`). */
function subtrees(tree: Tree): Tree[] {
  const seen = new Set<string>();
  const out: Tree[] = [];
  const walk = (node: Tree): void => {
    const key = keyOf(node);
    if (!seen.has(key)) {
      seen.add(key);
      out.push(node);
    }
    if (Array.isArray(node)) node.forEach(walk);
  };
  walk(tree);
  return out;
}

/** All contiguous slices of every list in the tree (candidates for tails). */
function slices(tree: Tree): Tree[] {
  const seen = new Set<string>();
  const out: Tree[] = [];
  const walk = (node: Tree): void => {
    if (!Array.isArray(node)) return;
    for (let i = 0; i <= node.length; i++) {
      for (let j = i; j <= node.length; j++) {
        const slice = node.slice(i, j);
        const key = keyOf(slice);
        if (!seen.has(key)) {
          seen.add(key);
          out.push(slice);
        }
      }
    }
    node.forEach(walk);
  };
  walk(tree);
  return out;
}

/**
 * Independent oracle: tries every assignment of candidate values to the
 * pattern's names and reports whether any substitution equals the subject.
 */
function oracleMatches(pattern: Pattern, subject: Tree): boolean {
  const varNames: string[] = [];
  const tailNames: string[] = [];
  const walk = (p: Pattern): void => {
    switch (p.kind) {
      case 'lit':
        return;
      case 'var':
        if (!varNames.includes(p.name)) varNames.push(p.name);
        return;
      case 'list':
        for (const item of p.items) {
          if (item.kind === 'tail') {
            if (!tailNames.includes(item.name)) tailNames.push(item.name);
          } else {
            walk(item);
          }
        }
    }
  };
  walk(pattern);

  const names = [...varNames, ...tailNames];
  const pools: Tree[][] = [
    ...varNames.map(() => subtrees(subject)),
    ...tailNames.map(() => slices(subject)),
  ];
  const total = pools.reduce((acc, pool) => acc * pool.length, 1);
  const indices = new Array<number>(names.length).fill(0);
  for (let k = 0; k < total; k++) {
    const assignment = new Map<string, Tree>();
    names.forEach((name, i) => assignment.set(name, pools[i][indices[i]]));
    if (treeEqual(substitute(pattern, assignment), subject)) return true;
    for (let i = indices.length - 1; i >= 0; i--) {
      if (++indices[i] < pools[i].length) break;
      indices[i] = 0;
    }
  }
  return false;
}

function hasRepeatedName(pattern: Pattern): boolean {
  const occurrences = occurrencePaths(pattern).map((o) => o.name);
  return new Set(occurrences).size < occurrences.length;
}

function hasNestedTail(pattern: Pattern): boolean {
  const walk = (p: Pattern, depth: number): boolean => {
    if (p.kind !== 'list') return false;
    return p.items.some((item) =>
      item.kind === 'tail' ? depth >= 1 : walk(item, depth + 1),
    );
  };
  return walk(pattern, 0);
}

/** Every index of `path` must address a real node, except a final tail-start. */
function pathIsPlausible(subject: Tree, path: number[]): boolean {
  let node: Tree = subject;
  for (let i = 0; i < path.length; i++) {
    if (!Array.isArray(node)) return false;
    const last = i === path.length - 1;
    if (path[i] < 0 || path[i] > node.length) return false;
    if (!last && path[i] >= node.length) return false;
    if (path[i] < node.length) node = node[path[i]];
  }
  return true;
}

export function runExhaustive(): void {
  const trees = allTrees();
  const patterns = allPatterns();
  process.stdout.write(
    `-- exhaustive: ${patterns.length} patterns x ${trees.length} trees\n`,
  );

  let okCount = 0;
  let failCount = 0;
  let repeatedNamePatterns = 0;
  let repeatedNameOk = 0;
  let nestedTailPatterns = 0;
  let nestedTailOk = 0;
  let emptyTailMatches = 0;
  let broken = 0;
  const fail = (label: string): void => {
    broken++;
    if (broken <= 20) process.stdout.write(`FAIL: ${label}\n`);
  };

  for (const pattern of patterns) {
    const names = patternNames(pattern);
    const occurrences = occurrencePaths(pattern);
    const repeated = hasRepeatedName(pattern);
    const nestedTail = hasNestedTail(pattern);
    if (repeated) repeatedNamePatterns++;
    if (nestedTail) nestedTailPatterns++;

    for (const subject of trees) {
      const label = `pattern=${JSON.stringify(pattern)} subject=${keyOf(subject)}`;
      const result = match(pattern, subject);
      const again = match(pattern, subject);
      if (JSON.stringify(result) !== JSON.stringify(again)) {
        fail(`non-deterministic result: ${label}`);
        continue;
      }

      if (result.ok) {
        okCount++;
        if (repeated) repeatedNameOk++;
        if (nestedTail) nestedTailOk++;

        // Substitution with the produced bindings rebuilds the subject.
        let rebuilt: Tree;
        try {
          rebuilt = substitute(pattern, result.bindings);
        } catch (err) {
          fail(`substitute threw: ${label} ${String(err)}`);
          continue;
        }
        if (!treeEqual(rebuilt, subject)) {
          fail(`substitution mismatch: ${label} rebuilt=${keyOf(rebuilt)}`);
          continue;
        }

        // Bindings: one entry per name, stable first-occurrence order.
        if (result.bindings.map((b) => b.name).join('') !== names.join('')) {
          fail(`unstable binding order: ${label}`);
          continue;
        }

        // Every occurrence of a repeated name binds structurally equal values.
        let consistent = true;
        for (const name of names) {
          const spots = occurrences.filter((o) => o.name === name);
          const values = spots.map((spot) => {
            if (spot.kind === 'var') return nodeAt(subject, spot.path);
            const owner = nodeAt(subject, spot.path.slice(0, -1));
            return Array.isArray(owner) ? owner.slice(spot.path[spot.path.length - 1]) : undefined;
          });
          for (let i = 1; i < values.length; i++) {
            if (values[i] === undefined || !treeEqual(values[0] as Tree, values[i] as Tree)) {
              consistent = false;
            }
          }
          if (spots.some((s) => s.kind === 'tail')) {
            const bound = result.bindings.find((b) => b.name === name);
            if (bound && Array.isArray(bound.value) && bound.value.length === 0) {
              emptyTailMatches++;
            }
          }
        }
        if (!consistent) {
          fail(`repeated variable bound unequal values: ${label}`);
          continue;
        }

        if (!oracleMatches(pattern, subject)) {
          fail(`matcher succeeded but oracle found no assignment: ${label}`);
        }
      } else {
        failCount++;
        if (result.reason.length === 0) fail(`empty failure reason: ${label}`);
        if (!pathIsPlausible(subject, result.path)) {
          fail(`implausible failure path ${JSON.stringify(result.path)}: ${label}`);
        }
        if (oracleMatches(pattern, subject)) {
          fail(`matcher failed but oracle found an assignment: ${label}`);
        }
      }
    }
  }

  section('exhaustive summary');
  process.stdout.write(
    `ok=${okCount} fail=${failCount} repeatedNamePatterns=${repeatedNamePatterns} ` +
      `(matched ${repeatedNameOk}) nestedTailPatterns=${nestedTailPatterns} ` +
      `(matched ${nestedTailOk}) emptyTailMatches=${emptyTailMatches}\n`,
  );
  check(broken === 0, `${broken} exhaustive violations`);
  check(okCount > 0 && failCount > 0, 'both outcomes exercised');
  check(repeatedNamePatterns > 0 && repeatedNameOk > 0, 'repeated variables exercised');
  check(nestedTailPatterns > 0 && nestedTailOk > 0, 'nested tails exercised');
  check(emptyTailMatches > 0, 'empty tail bindings exercised');
}
