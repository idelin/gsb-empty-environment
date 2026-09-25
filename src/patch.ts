import { buildDigest } from './digest.ts';
import type { DiffOp } from './diff.ts';
import { DigestMismatchError, PatchApplyError } from './errors.ts';
import { defaultHash, type HashFn } from './hash.ts';
import { deepClone, isObject, type JsonValue, type Path } from './types.ts';

/** 原子补丁：要么全部应用，要么完全回滚（含数组下标与根）。 */
export interface AtomicPatch {
  ops: DiffOp[];
}

export interface ApplyOptions {
  hash?: HashFn;
  /** 旧基线摘要：修改前校验，不一致则 DigestMismatchError，树不被改动。 */
  expectBaseDigest?: string;
  /** 目标摘要：应用后校验，不一致则回滚并抛 DigestMismatchError。 */
  expectTargetDigest?: string;
}

function getPath(root: JsonValue, path: Path, opIndex: number): JsonValue {
  let cur: JsonValue = root;
  for (let d = 0; d < path.length; d++) {
    const seg = path[d];
    if (Array.isArray(cur) && typeof seg === 'number' && seg >= 0 && seg < cur.length) {
      cur = cur[seg];
    } else if (isObject(cur) && typeof seg === 'string' && Object.prototype.hasOwnProperty.call(cur, seg)) {
      cur = cur[seg];
    } else {
      throw new PatchApplyError(`path does not exist (segment ${JSON.stringify(seg)})`, path, opIndex);
    }
  }
  return cur;
}

function parentOf(root: JsonValue, path: Path, opIndex: number): { parent: JsonValue; key: string | number } {
  if (path.length === 0) throw new PatchApplyError('internal: empty path has no parent', path, opIndex);
  const parent = getPath(root, path.slice(0, -1), opIndex);
  return { parent, key: path[path.length - 1] };
}

function applySet(root: JsonValue, path: Path, value: JsonValue, opIndex: number): JsonValue {
  if (path.length === 0) return deepClone(value);
  const { parent, key } = parentOf(root, path, opIndex);
  if (Array.isArray(parent)) {
    if (typeof key !== 'number' || key < 0 || key > parent.length) {
      throw new PatchApplyError(`array index ${JSON.stringify(key)} out of bounds for set`, path, opIndex);
    }
    if (key === parent.length) parent.push(deepClone(value));
    else parent[key] = deepClone(value);
    return root;
  }
  if (isObject(parent)) {
    if (typeof key !== 'string') {
      throw new PatchApplyError(`object key must be string, got ${JSON.stringify(key)}`, path, opIndex);
    }
    parent[key] = deepClone(value);
    return root;
  }
  throw new PatchApplyError('cannot set into a non-container', path, opIndex);
}

function applyInsert(root: JsonValue, path: Path, value: JsonValue, opIndex: number): JsonValue {
  const { parent, key } = parentOf(root, path, opIndex);
  if (!Array.isArray(parent) || typeof key !== 'number' || key < 0 || key > parent.length) {
    throw new PatchApplyError(`cannot insert at ${JSON.stringify(key)}`, path, opIndex);
  }
  parent.splice(key, 0, deepClone(value));
  return root;
}

function applyRemove(root: JsonValue, path: Path, opIndex: number): JsonValue {
  if (path.length === 0) throw new PatchApplyError('cannot remove the root', path, opIndex);
  const { parent, key } = parentOf(root, path, opIndex);
  if (Array.isArray(parent)) {
    if (typeof key !== 'number' || key < 0 || key >= parent.length) {
      throw new PatchApplyError(`array index ${JSON.stringify(key)} out of bounds for remove`, path, opIndex);
    }
    parent.splice(key, 1);
    return root;
  }
  if (isObject(parent)) {
    if (typeof key !== 'string' || !Object.prototype.hasOwnProperty.call(parent, key)) {
      throw new PatchApplyError(`object key ${JSON.stringify(key)} does not exist`, path, opIndex);
    }
    delete parent[key];
    return root;
  }
  throw new PatchApplyError('cannot remove from a non-container', path, opIndex);
}

/** 按目标父容器类型放置值：数组→insert，对象→set。 */
function placeAt(root: JsonValue, path: Path, value: JsonValue, opIndex: number): JsonValue {
  const { parent } = parentOf(root, path, opIndex);
  if (Array.isArray(parent)) return applyInsert(root, path, value, opIndex);
  return applySet(root, path, value, opIndex);
}

function applyOp(root: JsonValue, op: DiffOp, opIndex: number): JsonValue {
  switch (op.op) {
    case 'set':
      return applySet(root, op.path, op.value, opIndex);
    case 'insert':
      return applyInsert(root, op.path, op.value, opIndex);
    case 'remove':
      return applyRemove(root, op.path, opIndex);
    case 'move': {
      const value = getPath(root, op.from, opIndex);
      const afterRemove = applyRemove(root, op.from, opIndex);
      return placeAt(afterRemove, op.to, value, opIndex);
    }
  }
}

/** 针对当前树状态求单步逆操作（用于日志回滚与逆补丁）。 */
function inverseOf(root: JsonValue, op: DiffOp, opIndex: number): DiffOp {
  switch (op.op) {
    case 'set': {
      if (op.path.length === 0) return { op: 'set', path: [], value: deepClone(root), oldValue: deepClone(op.value) };
      const { parent, key } = parentOf(root, op.path, opIndex);
      const existed =
        (Array.isArray(parent) && typeof key === 'number' && key < parent.length) ||
        (isObject(parent) && typeof key === 'string' && Object.prototype.hasOwnProperty.call(parent, key));
      if (!existed) return { op: 'remove', path: op.path, oldValue: deepClone(op.value) };
      const old = Array.isArray(parent) ? parent[key as number] : (parent as Record<string, JsonValue>)[key as string];
      return { op: 'set', path: op.path, value: deepClone(old), oldValue: deepClone(op.value) };
    }
    case 'insert':
      return { op: 'remove', path: op.path, oldValue: deepClone(op.value) };
    case 'remove': {
      const { parent } = parentOf(root, op.path, opIndex);
      const old = getPath(root, op.path, opIndex);
      if (Array.isArray(parent)) return { op: 'insert', path: op.path, value: deepClone(old) };
      return { op: 'set', path: op.path, value: deepClone(old) };
    }
    case 'move':
      return { op: 'move', from: op.to, to: op.from };
  }
}

/**
 * 原子应用补丁。
 * 1. 先校验旧基线摘要（先于任何修改）；
 * 2. 逐步应用并记录逆操作日志，任一步失败即逆序回滚，恢复所有数组下标与根；
 * 3. 可选校验目标摘要，不一致同样回滚。
 * 返回（可能被整体替换的）新根。
 */
export function applyPatch<T extends JsonValue>(root: T, patch: AtomicPatch, opts: ApplyOptions = {}): JsonValue {
  const hash = opts.hash ?? defaultHash;
  if (opts.expectBaseDigest !== undefined) {
    const actual = buildDigest(root, hash).digest;
    if (actual !== opts.expectBaseDigest) {
      throw new DigestMismatchError('base', opts.expectBaseDigest, actual, []);
    }
  }
  let current: JsonValue = root;
  const journal: DiffOp[] = [];
  const rollback = (): void => {
    for (let i = journal.length - 1; i >= 0; i--) current = applyOp(current, journal[i], i);
  };
  try {
    patch.ops.forEach((op, i) => {
      journal.push(inverseOf(current, op, i));
      current = applyOp(current, op, i);
    });
    if (opts.expectTargetDigest !== undefined) {
      const actual = buildDigest(current, hash).digest;
      if (actual !== opts.expectTargetDigest) {
        throw new DigestMismatchError('target', opts.expectTargetDigest, actual, []);
      }
    }
  } catch (err) {
    rollback();
    throw err;
  }
  return current;
}

/** 由基线树求整个补丁的逆补丁（应用后可还原基线）。 */
export function invertPatch(patch: AtomicPatch, base: JsonValue): AtomicPatch {
  let cur: JsonValue = deepClone(base);
  const inv: DiffOp[] = [];
  patch.ops.forEach((op, i) => {
    inv.push(inverseOf(cur, op, i));
    cur = applyOp(cur, op, i);
  });
  inv.reverse();
  return { ops: inv };
}
