/** Atoms are the leaf values of a rule tree. */
export type Atom = string | number | boolean | null;

/** A rule tree: an atom, or a list of trees. */
export type Tree = Atom | Tree[];

/** A path addresses a node inside a tree by list indices, root first. */
export type Path = number[];

export function isAtom(value: unknown): value is Atom {
  return (
    value === null ||
    typeof value === 'string' ||
    typeof value === 'number' ||
    typeof value === 'boolean'
  );
}

export function isList(tree: Tree): tree is Tree[] {
  return Array.isArray(tree);
}

/** Structural equality over trees: atoms by ===, lists element-wise. */
export function treeEqual(a: Tree, b: Tree): boolean {
  if (Array.isArray(a) || Array.isArray(b)) {
    if (!Array.isArray(a) || !Array.isArray(b) || a.length !== b.length) return false;
    for (let i = 0; i < a.length; i++) {
      if (!treeEqual(a[i], b[i])) return false;
    }
    return true;
  }
  return a === b;
}

/** Fetch the node at `path`, or undefined when the path does not exist. */
export function nodeAt(tree: Tree, path: Path): Tree | undefined {
  let node: Tree = tree;
  for (const index of path) {
    if (!Array.isArray(node) || index >= node.length) return undefined;
    node = node[index];
  }
  return node;
}

export function showTree(tree: Tree): string {
  return Array.isArray(tree) ? `[${tree.map(showTree).join(' ')}]` : String(tree);
}
