import type { TreeNode, Op, Path } from './types.ts';
import { clone, treeEquals } from './tree.ts';

/**
 * Independent patch applier.
 *
 * Deliberately shares no state with the differ: it re-reads the source tree,
 * resolves every path against the evolving tree, applies ops strictly in
 * order, and can verify the final result against the expected target.
 *
 * Op conventions (mirroring script.ts):
 *  - update: set type/value/id of the node at `path` (`undefined` clears).
 *  - insert: insert a deep copy of `node` as the `index`-th child of the
 *    node at `parent`.
 *  - delete: remove the subtree at `path`.
 *  - move:   detach the node at `from`, then resolve `parent` in the
 *    resulting tree and insert the node at `index`.
 */

export class PatchError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'PatchError';
  }
}

function resolve(root: TreeNode, path: Path, opIndex: number): TreeNode {
  let node = root;
  for (const k of path) {
    if (!Number.isInteger(k) || k < 0 || k >= node.children.length) {
      throw new PatchError(`op ${opIndex}: invalid path [${path.join(', ')}]`);
    }
    node = node.children[k];
  }
  return node;
}

export function applyPatch(src: TreeNode, patch: Op[]): TreeNode {
  const root = clone(src);
  for (let i = 0; i < patch.length; i++) {
    const op = patch[i];
    switch (op.op) {
      case 'update': {
        const node = resolve(root, op.path, i);
        node.type = op.type;
        if (op.value !== undefined) node.value = op.value;
        else delete node.value;
        if (op.id !== undefined) node.id = op.id;
        else delete node.id;
        break;
      }
      case 'insert': {
        const parent = resolve(root, op.parent, i);
        if (!Number.isInteger(op.index) || op.index < 0 || op.index > parent.children.length) {
          throw new PatchError(`op ${i}: insert index ${op.index} out of range`);
        }
        parent.children.splice(op.index, 0, clone(op.node));
        break;
      }
      case 'delete': {
        if (op.path.length === 0) throw new PatchError(`op ${i}: cannot delete the root`);
        const parent = resolve(root, op.path.slice(0, -1), i);
        const k = op.path[op.path.length - 1];
        if (k < 0 || k >= parent.children.length) {
          throw new PatchError(`op ${i}: invalid delete path [${op.path.join(', ')}]`);
        }
        parent.children.splice(k, 1);
        break;
      }
      case 'move': {
        if (op.from.length === 0) throw new PatchError(`op ${i}: cannot move the root`);
        const fromParent = resolve(root, op.from.slice(0, -1), i);
        const k = op.from[op.from.length - 1];
        if (k < 0 || k >= fromParent.children.length) {
          throw new PatchError(`op ${i}: invalid move-from path [${op.from.join(', ')}]`);
        }
        const node = fromParent.children[k];
        fromParent.children.splice(k, 1);
        const parent = resolve(root, op.parent, i);
        if (!Number.isInteger(op.index) || op.index < 0 || op.index > parent.children.length) {
          throw new PatchError(`op ${i}: move index ${op.index} out of range`);
        }
        parent.children.splice(op.index, 0, node);
        break;
      }
      default: {
        const neverOp: never = op;
        throw new PatchError(`op ${i}: unknown op ${JSON.stringify(neverOp)}`);
      }
    }
  }
  return root;
}

/** Applies the patch and checks the result against the expected target. */
export function verifyPatch(src: TreeNode, patch: Op[], dst: TreeNode): boolean {
  let out: TreeNode;
  try {
    out = applyPatch(src, patch);
  } catch (err) {
    if (err instanceof PatchError) return false;
    throw err;
  }
  return treeEquals(out, dst);
}
