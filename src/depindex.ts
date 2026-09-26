import type { Segments } from "./paths.ts";

/**
 * Dependency invalidation modes:
 * - "node":    the check reads only the node at the dep path. It is
 *              invalidated when the changed path is the dep itself or one of
 *              its ancestors (the subtree containing the node was replaced).
 * - "subtree": the check reads the whole subtree under the dep path (e.g.
 *              array item checks). It is additionally invalidated when the
 *              changed path lies anywhere below the dep.
 */
export type DepMode = "node" | "subtree";

interface TrieNode {
  children: Map<string, TrieNode>;
  nodeMode: Set<number>;
  subtreeMode: Set<number>;
}

function newTrieNode(): TrieNode {
  return { children: new Map(), nodeMode: new Set(), subtreeMode: new Set() };
}

/** Path trie mapping instance-path dependencies to check ids. */
export class DepIndex {
  private root: TrieNode = newTrieNode();

  add(dep: Segments, mode: DepMode, checkId: number): void {
    let node = this.root;
    for (const segment of dep) {
      let child = node.children.get(segment);
      if (!child) {
        child = newTrieNode();
        node.children.set(segment, child);
      }
      node = child;
    }
    (mode === "node" ? node.nodeMode : node.subtreeMode).add(checkId);
  }

  /** Ids of checks whose dependencies are invalidated by a change at `changed`. */
  query(changed: Segments): Set<number> {
    const matched = new Set<number>();
    let node: TrieNode | undefined = this.root;
    // Ancestors of `changed` (strictly above): only subtree-mode deps reach below.
    for (const segment of changed) {
      for (const id of node.subtreeMode) matched.add(id);
      node = node.children.get(segment);
      if (!node) return matched;
    }
    // `changed` itself and everything below it: both modes are invalidated.
    collectSubtree(node, matched);
    return matched;
  }
}

function collectSubtree(node: TrieNode, out: Set<number>): void {
  for (const id of node.nodeMode) out.add(id);
  for (const id of node.subtreeMode) out.add(id);
  for (const child of node.children.values()) collectSubtree(child, out);
}
