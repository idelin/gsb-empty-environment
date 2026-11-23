import type { Grammar, GrammarAlt, TypeGraph, TypeNode, TypeRef } from '../src/types.ts';
import { assemblyMatches } from '../src/version.ts';

/** A finite, acyclic tree obtained by cutting a cyclic descriptor graph. */
export interface FiniteTree {
  node: TypeNode;
  args: FiniteTree[];
  element?: FiniteTree;
  cut?: boolean;
}

/**
 * Depth-limited full unfolding of a (possibly cyclic) type graph. Every
 * back-reference encountered after `maxDepth` constructor levels is cut and
 * marked, so the result is always a finite tree and stays well defined even
 * when the input depth exceeds the JS call stack (iterative construction).
 */
export function unfold(graph: TypeGraph, maxDepth: number): FiniteTree {
  interface Frame {
    tree: FiniteTree;
    ref: TypeRef;
    depth: number;
    childIndex: number;
    kind: 'arg' | 'element';
  }
  const rootNode = resolve(graph, graph.root);
  const rootTree: FiniteTree = { node: rootNode, args: [] };
  const stack: Frame[] = [{ tree: rootTree, ref: graph.root, depth: 0, childIndex: 0, kind: 'arg' }];

  while (stack.length > 0) {
    const frame = stack[stack.length - 1];
    const node = frame.tree.node;
    const isArray = (node.arrayRank ?? 0) > 0;
    const childRefs: TypeRef[] = isArray
      ? node.element !== undefined
        ? [node.element]
        : []
      : (node.args ?? []);

    if (frame.childIndex >= childRefs.length) {
      stack.pop();
      continue;
    }
    const ref = childRefs[frame.childIndex];
    frame.childIndex += 1;

    if (frame.depth + 1 > maxDepth) {
      const cutNode = resolve(graph, ref);
      const cutTree: FiniteTree = { node: cutNode, args: [], cut: true };
      attach(frame.tree, cutTree, isArray);
      continue;
    }
    const childNode = resolve(graph, ref);
    const childTree: FiniteTree = { node: childNode, args: [] };
    attach(frame.tree, childTree, isArray);
    stack.push({ tree: childTree, ref, depth: frame.depth + 1, childIndex: 0, kind: isArray ? 'element' : 'arg' });
  }
  return rootTree;
}

function attach(parent: FiniteTree, child: FiniteTree, parentIsArray: boolean): void {
  if (parentIsArray) parent.element = child;
  else parent.args.push(child);
}

function resolve(graph: TypeGraph, ref: TypeRef): TypeNode {
  if (typeof ref !== 'string') return ref;
  const node = graph.nodes[ref];
  if (node === undefined) throw new Error(`dangling ref ${ref}`);
  return node;
}

/**
 * Reference membership oracle for finite trees: a plain recursive matcher
 * used only in tests to cross-check the iterative production gate. Cut
 * leaves are treated as "unknown" and skipped by callers comparing verdicts.
 */
export function finiteTreeMember(tree: FiniteTree, nt: string, grammar: Grammar): boolean | undefined {
  if (tree.cut) return true;
  const prod = grammar.productions.find((p) => p.nt === nt);
  if (prod === undefined) return false;
  const alt = prod.alts.find((candidate) => matches(tree.node, candidate));
  if (alt === undefined) return false;
  if ((alt.arrayRank ?? 0) > 0) {
    if (tree.element === undefined || alt.element === undefined) return false;
    return finiteTreeMember(tree.element, alt.element, grammar);
  }
  const specs = alt.args ?? [];
  if (specs.length !== tree.args.length) return false;
  for (const [index, spec] of specs.entries()) {
    const result = finiteTreeMember(tree.args[index], spec.nt, grammar);
    if (result !== true) return result;
  }
  return true;
}

function matches(node: TypeNode, alt: GrammarAlt): boolean {
  if (node.typeName !== alt.typeName) return false;
  if ((node.arrayRank ?? 0) !== (alt.arrayRank ?? 0)) return false;
  if ((node.args ?? []).length !== (alt.args ?? []).length) return false;
  return assemblyMatches(alt.assembly, node.assembly);
}

/** Count constructor nodes in a finite unfolding. */
export function treeSize(tree: FiniteTree): number {
  let total = 1;
  for (const child of tree.args) total += treeSize(child);
  if (tree.element !== undefined) total += treeSize(tree.element);
  return total;
}
