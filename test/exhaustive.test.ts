/**
 * Exhaustive small-tree verification.
 *
 * For every generated (pattern, tree) pair we check:
 *   1. match() succeeds iff a brute-force oracle finds a variable assignment
 *      whose substitution into the pattern equals the tree.
 *   2. On success, substitute(pattern, bindings) is structurally equal to the
 *      tree, binding names are unique, and re-running match() is identical
 *      (stable bindings).
 *   3. On failure, the reported path is navigable in the pattern, no proper
 *      prefix of it is a context-free hard mismatch (shallowest), and the
 *      reported reason is consistent with the nodes at that path.
 */
import assert from "node:assert/strict";
import test from "node:test";
import {
  type Atom,
  equalTree,
  isTail,
  isVar,
  match,
  type MatchFailure,
  type PatternNode,
  patternVariables,
  substitute,
  t,
  type Tree,
  v,
} from "../src/match.ts";

const ATOMS: Atom[] = ["a", "b"];

function* product<T>(items: readonly T[], length: number): Generator<T[]> {
  if (length === 0) {
    yield [];
    return;
  }
  for (const rest of product(items, length - 1)) {
    for (const item of items) yield [...rest, item];
  }
}

function genTrees(depth: number, maxLen: number): Tree[] {
  if (depth === 0) return [...ATOMS];
  const smaller = genTrees(depth - 1, maxLen);
  const out: Tree[] = [...ATOMS];
  for (let len = 0; len <= maxLen; len++) {
    for (const combo of product(smaller, len)) out.push(combo);
  }
  return out;
}

function genPatterns(
  depth: number,
  maxLen: number,
  varNames: string[],
  tailNames: string[],
): PatternNode[] {
  const leaves: PatternNode[] = [...ATOMS, ...varNames.map(v)];
  if (depth === 0) return leaves;
  const smaller = genPatterns(depth - 1, maxLen, varNames, tailNames);
  const out: PatternNode[] = [...leaves];
  for (let len = 0; len <= maxLen; len++) {
    for (const combo of product(smaller, len)) {
      out.push(combo);
      for (const name of tailNames) out.push([...combo, t(name)]);
    }
  }
  return out;
}

function dedupeByJson<T>(items: T[]): T[] {
  const seen = new Set<string>();
  const out: T[] = [];
  for (const item of items) {
    const key = JSON.stringify(item);
    if (!seen.has(key)) {
      seen.add(key);
      out.push(item);
    }
  }
  return out;
}

function subtrees(tree: Tree): Tree[] {
  const out: Tree[] = [tree];
  if (Array.isArray(tree)) {
    for (const child of tree) out.push(...subtrees(child));
  }
  return dedupeByJson(out);
}

/** All suffixes (including empty) of every list occurring in the tree. */
function listTails(tree: Tree): Tree[][] {
  const out: Tree[][] = [];
  const walk = (node: Tree): void => {
    if (!Array.isArray(node)) return;
    for (let i = 0; i <= node.length; i++) out.push(node.slice(i));
    for (const child of node) walk(child);
  };
  walk(tree);
  return dedupeByJson(out);
}

/** Brute-force oracle: does any assignment make the pattern equal the tree? */
function oracleMatchable(pattern: PatternNode, tree: Tree): boolean {
  const { singles, tails } = patternVariables(pattern);
  const singleDomain = subtrees(tree);
  const tailDomain = listTails(tree);
  for (const singleAssign of product(singleDomain, singles.length)) {
    for (const tailAssign of product(tailDomain, tails.length)) {
      const bindings = new Map<string, Tree>();
      singles.forEach((name, i) => bindings.set(name, singleAssign[i]));
      tails.forEach((name, i) => bindings.set(name, tailAssign[i]));
      if (equalTree(substitute(pattern, bindings), tree)) return true;
    }
  }
  return false;
}

/** Context-free mismatch that does not depend on variable bindings. */
function hardMismatch(pattern: PatternNode, tree: Tree): boolean {
  if (isVar(pattern) || isTail(pattern)) return false;
  if (Array.isArray(pattern)) {
    if (!Array.isArray(tree)) return true;
    const last = pattern[pattern.length - 1];
    const hasTail = pattern.length > 0 && isTail(last);
    const fixed = hasTail ? pattern.length - 1 : pattern.length;
    return hasTail ? tree.length < fixed : tree.length !== pattern.length;
  }
  if (Array.isArray(tree)) return true;
  return !Object.is(pattern, tree);
}

function checkFailurePath(
  pattern: PatternNode,
  tree: Tree,
  failure: MatchFailure,
): void {
  const { path, reason } = failure;
  // Every proper prefix is navigable in both pattern and tree, and is not a
  // context-free hard mismatch (i.e. the reported failure is the shallowest).
  let pNode: PatternNode = pattern;
  let tNode: Tree = tree;
  for (const index of path) {
    assert.equal(
      hardMismatch(pNode, tNode),
      false,
      `proper prefix of ${JSON.stringify(path)} is already a hard mismatch`,
    );
    assert.ok(
      Array.isArray(pNode) && index < pNode.length,
      `path ${JSON.stringify(path)} not navigable in pattern`,
    );
    const child = pNode[index];
    if (isTail(child)) {
      // A tail slot may point one past the end of the tree list (empty tail).
      assert.ok(Array.isArray(tNode) && index <= tNode.length);
      pNode = child;
      tNode = index < tNode.length ? tNode[index] : [];
      continue;
    }
    assert.ok(
      Array.isArray(tNode) && index < tNode.length,
      `path ${JSON.stringify(path)} not navigable in tree`,
    );
    pNode = child;
    tNode = (tNode as Tree[])[index];
  }
  // The reason is consistent with the nodes at the failure position.
  switch (reason) {
    case "atom-mismatch":
      assert.ok(!Array.isArray(pNode) && !isVar(pNode) && !isTail(pNode));
      assert.ok(!Array.isArray(tNode));
      assert.ok(!Object.is(pNode, tNode));
      break;
    case "kind-mismatch":
      assert.ok(!isVar(pNode) && !isTail(pNode));
      assert.notEqual(Array.isArray(pNode), Array.isArray(tNode));
      break;
    case "length-mismatch":
      assert.ok(Array.isArray(pNode) && Array.isArray(tNode));
      assert.ok(hardMismatch(pNode, tNode));
      break;
    case "variable-conflict":
      assert.ok(isVar(pNode) || isTail(pNode));
      break;
  }
}

function verifyAll(
  label: string,
  patterns: PatternNode[],
  trees: Tree[],
  checkDeterminism: boolean,
): void {
  let succeeded = 0;
  let failed = 0;
  for (const pattern of patterns) {
    for (const tree of trees) {
      const result = match(pattern, tree);
      if (result.ok) {
        succeeded++;
        const names = result.bindings.map(([name]) => name);
        assert.equal(
          new Set(names).size,
          names.length,
          `${label}: duplicate binding name in ${JSON.stringify(pattern)}`,
        );
        assert.ok(
          equalTree(substitute(pattern, result.bindings), tree),
          `${label}: substitution of bindings does not reproduce tree`,
        );
        if (checkDeterminism) {
          assert.equal(
            JSON.stringify(match(pattern, tree)),
            JSON.stringify(result),
            `${label}: bindings not stable across runs`,
          );
        }
      } else {
        failed++;
        checkFailurePath(pattern, tree, result.failure);
        assert.equal(
          oracleMatchable(pattern, tree),
          false,
          `${label}: oracle found an assignment but match() failed for ` +
            `pattern=${JSON.stringify(pattern)} tree=${JSON.stringify(tree)}`,
        );
      }
    }
  }
  assert.ok(succeeded > 0, `${label}: no successful matches at all`);
  assert.ok(failed > 0, `${label}: no failed matches at all`);
}

const TREES = genTrees(2, 2);

test("exhaustive: literals + one single var + one tail var (nested tails)", () => {
  const patterns = genPatterns(2, 2, ["x"], ["s"]);
  verifyAll("single+tail", patterns, TREES, true);
});

test("exhaustive: two repeated single vars, no tails", () => {
  const patterns = genPatterns(2, 2, ["x", "y"], []);
  verifyAll("two-singles", patterns, TREES, false);
});

test("exhaustive: two tail vars in nested lists, no single vars", () => {
  const patterns = genPatterns(2, 2, [], ["s", "u"]);
  verifyAll("two-tails", patterns, TREES, false);
});
