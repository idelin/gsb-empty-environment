import { PatchOp, TreeNode, TreeValue } from './types';

export class PatchApplyError extends Error {}

function cloneNode(n: TreeNode): TreeNode {
  const out: TreeNode = { type: n.type, children: n.children.map(cloneNode) };
  if (n.value !== undefined) out.value = n.value;
  if (n.id !== undefined) out.id = n.id;
  return out;
}

function isValidValue(v: unknown): boolean {
  const t = typeof v;
  return t === 'string' || t === 'number' || t === 'boolean' || v === null;
}

function validateSubtree(n: unknown, where: string): void {
  if (typeof n !== 'object' || n === null || Array.isArray(n)) {
    throw new PatchApplyError(where + ': node is not an object');
  }
  const node = n as TreeNode;
  if (typeof node.type !== 'string') throw new PatchApplyError(where + ': non-string type');
  if (node.id !== undefined && typeof node.id !== 'string') {
    throw new PatchApplyError(where + ': non-string id');
  }
  if (node.value !== undefined && !isValidValue(node.value)) {
    throw new PatchApplyError(where + ': non-scalar value');
  }
  if (!Array.isArray(node.children)) throw new PatchApplyError(where + ': non-array children');
  for (let i = 0; i < node.children.length; i++) {
    validateSubtree(node.children[i], where + '.children[' + i + ']');
  }
}

function fmtPath(path: number[]): string {
  return '[' + path.join(',') + ']';
}

function resolvePath(root: TreeNode, path: number[], opIndex: number): TreeNode {
  if (!Array.isArray(path)) {
    throw new PatchApplyError('op ' + opIndex + ': path is not an array');
  }
  let cur = root;
  for (const idx of path) {
    if (!Number.isInteger(idx) || idx < 0 || idx >= cur.children.length) {
      throw new PatchApplyError('op ' + opIndex + ': invalid path ' + fmtPath(path));
    }
    cur = cur.children[idx];
  }
  return cur;
}

function isPrefix(prefix: number[], path: number[]): boolean {
  if (prefix.length > path.length) return false;
  for (let i = 0; i < prefix.length; i++) {
    if (prefix[i] !== path[i]) return false;
  }
  return true;
}

function applyOne(root: TreeNode, op: PatchOp, opIndex: number): void {
  if (typeof op !== 'object' || op === null) {
    throw new PatchApplyError('op ' + opIndex + ': not an object');
  }
  switch (op.op) {
    case 'update': {
      const node = resolvePath(root, op.path, opIndex);
      if (typeof op.type !== 'string') {
        throw new PatchApplyError('op ' + opIndex + ': update has non-string type');
      }
      node.type = op.type;
      if (Object.prototype.hasOwnProperty.call(op, 'value')) {
        if (!isValidValue(op.value)) {
          throw new PatchApplyError('op ' + opIndex + ': update has non-scalar value');
        }
        node.value = op.value as TreeValue;
      } else {
        delete node.value;
      }
      if (Object.prototype.hasOwnProperty.call(op, 'id')) {
        if (op.id !== undefined && typeof op.id !== 'string') {
          throw new PatchApplyError('op ' + opIndex + ': update has non-string id');
        }
        if (op.id === undefined) delete node.id;
        else node.id = op.id;
      } else {
        delete node.id;
      }
      return;
    }
    case 'insert': {
      const parent = resolvePath(root, op.parentPath, opIndex);
      if (!Number.isInteger(op.index) || op.index < 0 || op.index > parent.children.length) {
        throw new PatchApplyError('op ' + opIndex + ': insert index out of range');
      }
      validateSubtree(op.node, 'op ' + opIndex + '.node');
      parent.children.splice(op.index, 0, cloneNode(op.node));
      return;
    }
    case 'delete': {
      if (!Array.isArray(op.path) || op.path.length === 0) {
        throw new PatchApplyError('op ' + opIndex + ': cannot delete the root');
      }
      const parent = resolvePath(root, op.path.slice(0, op.path.length - 1), opIndex);
      const idx = op.path[op.path.length - 1];
      if (!Number.isInteger(idx) || idx < 0 || idx >= parent.children.length) {
        throw new PatchApplyError('op ' + opIndex + ': invalid path ' + fmtPath(op.path));
      }
      parent.children.splice(idx, 1);
      return;
    }
    case 'move': {
      if (!Array.isArray(op.from) || op.from.length === 0) {
        throw new PatchApplyError('op ' + opIndex + ': cannot move the root');
      }
      if (isPrefix(op.from, op.toParentPath)) {
        throw new PatchApplyError(
          'op ' + opIndex + ': move target ' + fmtPath(op.toParentPath) +
            ' lies inside the moved subtree ' + fmtPath(op.from) + ' (ancestor cycle)'
        );
      }
      const node = resolvePath(root, op.from, opIndex);
      const fromParent = resolvePath(root, op.from.slice(0, op.from.length - 1), opIndex);
      const fromIndex = op.from[op.from.length - 1];
      const toParent = resolvePath(root, op.toParentPath, opIndex);
      const sameParent = fromParent === toParent;
      const maxIndex = sameParent ? toParent.children.length - 1 : toParent.children.length;
      if (!Number.isInteger(op.toIndex) || op.toIndex < 0 || op.toIndex > maxIndex) {
        throw new PatchApplyError('op ' + opIndex + ': move toIndex out of range');
      }
      fromParent.children.splice(fromIndex, 1);
      toParent.children.splice(op.toIndex, 0, node);
      return;
    }
    default:
      throw new PatchApplyError('op ' + opIndex + ': unknown op "' + String((op as PatchOp).op) + '"');
  }
}

/**
 * Applies a sequential patch to `tree` and returns the resulting new tree.
 * The input is never mutated. Every op is validated against the tree state
 * produced by the previous ops; any violation raises PatchApplyError.
 * After the final op the result is checked for duplicate stable ids.
 */
export function applyPatch(tree: TreeNode, ops: PatchOp[]): TreeNode {
  validateSubtree(tree, 'tree');
  if (!Array.isArray(ops)) throw new PatchApplyError('ops is not an array');
  const root = cloneNode(tree);
  for (let i = 0; i < ops.length; i++) {
    applyOne(root, ops[i], i);
  }
  const ids = new Set<string>();
  const stack: TreeNode[] = [root];
  while (stack.length > 0) {
    const n = stack.pop() as TreeNode;
    if (n.id !== undefined) {
      if (ids.has(n.id)) {
        throw new PatchApplyError('duplicate id "' + n.id + '" in the patched tree');
      }
      ids.add(n.id);
    }
    for (const c of n.children) stack.push(c);
  }
  return root;
}
