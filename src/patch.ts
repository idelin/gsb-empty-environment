import { DigestMismatchError, JsonDagError, PatchApplyError, deepEqual, formatPath, isObject } from './json.ts';
import type { Json, Path } from './json.ts';
import { digestOfTree } from './digest.ts';
import type { DigestOptions } from './digest.ts';
import { diff } from './diff.ts';
import type { Change } from './diff.ts';
import { getAt } from './json.ts';

export interface PatchOp {
  op: 'set' | 'remove' | 'move';
  path: Path;
  value?: Json;
  from?: Path;
  /** Optional precondition: current value must deep-equal this before applying. */
  expectOld?: Json;
}

/** An atomic patch: validated against baseDigest before any mutation and against
 *  targetDigest after; any failing step rolls back every applied op. */
export interface Patch {
  ops: PatchOp[];
  baseDigest: string;
  targetDigest: string;
}

export function patchFromChanges(base: Json, target: Json, changes: Change[], opts: DigestOptions = {}): Patch {
  const removes: PatchOp[] = [];
  const moves: PatchOp[] = [];
  const sets: PatchOp[] = [];
  // Group same-array moves: a reorder with >1 move becomes one whole-array set so
  // index-based application stays unambiguous.
  const moveGroups = new Map<string, Change[]>();
  for (const c of changes) {
    if (c.kind === 'remove') removes.push({ op: 'remove', path: c.path });
    else if (c.kind === 'set') sets.push({ op: 'set', path: c.path, value: c.after as Json });
    else {
      const parentKey = formatPath(c.path.slice(0, -1));
      const group = moveGroups.get(parentKey) ?? [];
      group.push(c);
      moveGroups.set(parentKey, group);
    }
  }
  for (const [parentKey, group] of moveGroups) {
    if (group.length === 1) {
      moves.push({ op: 'move', from: group[0].from!, path: group[0].path });
    } else {
      const parentPath = group[0].path.slice(0, -1);
      sets.push({ op: 'set', path: parentPath, value: getAt(target, parentPath) });
    }
  }
  removes.sort((a, b) => b.path.length - a.path.length);
  sets.sort((a, b) => a.path.length - b.path.length);
  return {
    ops: [...removes, ...moves, ...sets],
    baseDigest: digestOfTree(base, opts),
    targetDigest: digestOfTree(target, opts),
  };
}

export function buildPatch(base: Json, target: Json, opts: DigestOptions = {}): Patch {
  return patchFromChanges(base, target, diff(base, target, opts).changes, opts);
}

class Holder {
  value: Json;
  constructor(value: Json) {
    this.value = value;
  }
}

function locate(holder: Holder, path: Path): { parent: unknown; key: string | number } {
  if (path.length === 0) return { parent: holder, key: 'value' };
  let cur: unknown = holder.value;
  for (let i = 0; i < path.length - 1; i++) {
    if (cur === null || typeof cur !== 'object') throw new JsonDagError('path not found', path.slice(0, i + 1));
    const seg = path[i];
    const next = (cur as Record<string | number, Json>)[seg as string];
    if (next === undefined) throw new JsonDagError('path not found', path.slice(0, i + 1));
    cur = next;
  }
  return { parent: cur, key: path[path.length - 1] };
}

function childExists(parent: unknown, key: string | number): boolean {
  if (parent instanceof Holder) return true;
  if (Array.isArray(parent)) return typeof key === 'number' && key >= 0 && key < parent.length;
  return isObject(parent) && Object.prototype.hasOwnProperty.call(parent, key);
}

function readChild(parent: unknown, key: string | number): Json {
  if (parent instanceof Holder) return parent.value;
  return (parent as Record<string | number, Json>)[key as string];
}

function writeChild(parent: unknown, key: string | number, value: Json): void {
  if (parent instanceof Holder) {
    parent.value = value;
    return;
  }
  if (Array.isArray(parent)) {
    if (typeof key !== 'number' || key < 0 || key > parent.length) {
      throw new JsonDagError(`array index ${String(key)} out of bounds`, []);
    }
    if (key === parent.length) parent.push(value);
    else parent[key] = value;
    return;
  }
  (parent as Record<string, Json>)[key as string] = value;
}

function removeChild(parent: unknown, key: string | number): void {
  if (parent instanceof Holder) throw new JsonDagError('cannot remove the document root', []);
  if (Array.isArray(parent)) parent.splice(key as number, 1);
  else delete (parent as Record<string, Json>)[key as string];
}

/** Applies one op to the holder, pushing its inverse onto `record` when given. */
function applyOp(holder: Holder, op: PatchOp, record: PatchOp[] | null): void {
  switch (op.op) {
    case 'set': {
      const { parent, key } = locate(holder, op.path);
      const existed = childExists(parent, key);
      const old = existed ? readChild(parent, key) : undefined;
      if (op.expectOld !== undefined && !deepEqual(old, op.expectOld)) {
        throw new JsonDagError('precondition expectOld failed', op.path);
      }
      writeChild(parent, key, op.value!);
      record?.push(existed ? { op: 'set', path: op.path, value: old } : { op: 'remove', path: op.path });
      return;
    }
    case 'remove': {
      const { parent, key } = locate(holder, op.path);
      if (!childExists(parent, key)) throw new JsonDagError('remove target does not exist', op.path);
      const old = readChild(parent, key);
      removeChild(parent, key);
      record?.push({ op: 'set', path: op.path, value: old });
      return;
    }
    case 'move': {
      const src = locate(holder, op.from!);
      if (!childExists(src.parent, src.key)) throw new JsonDagError('move source does not exist', op.from!);
      const value = readChild(src.parent, src.key);
      removeChild(src.parent, src.key);
      const dst = locate(holder, op.path);
      writeChild(dst.parent, dst.key, value);
      record?.push({ op: 'move', from: op.path, path: op.from! });
      return;
    }
  }
}

/** Stable-id index of every id-mode array: formatPath(arrayPath) -> (elementId -> index). */
export function buildIdIndex(root: Json, opts: DigestOptions = {}): Map<string, Map<string, number>> {
  const idKey = opts.idKey ?? 'id';
  const index = new Map<string, Map<string, number>>();
  const walk = (node: Json, path: Path): void => {
    if (Array.isArray(node)) {
      if ((opts.arrayMode?.(path) ?? 'position') === 'id') {
        const entry = new Map<string, number>();
        node.forEach((el, i) => {
          if (isObject(el) && (typeof el[idKey] === 'string' || typeof el[idKey] === 'number')) {
            entry.set(String(el[idKey]), i);
          }
        });
        index.set(formatPath(path), entry);
      }
      node.forEach((el, i) => walk(el, [...path, i]));
    } else if (isObject(node)) {
      for (const k of Object.keys(node)) walk(node[k], [...path, k]);
    }
  };
  walk(root, []);
  return index;
}

export interface ApplyResult {
  root: Json;
  /** Patch that restores the exact pre-apply state. */
  inverse: Patch;
  idIndex: Map<string, Map<string, number>>;
}

/** Applies a patch atomically. The old baseline digest is validated before any
 *  modification; the target digest is validated after. If any step fails, every
 *  applied op is rolled back (restoring all indices and the root) and the error
 *  is rethrown with the failing op's path. The input tree is never mutated. */
export function applyPatch(root: Json, patch: Patch, opts: DigestOptions = {}): ApplyResult {
  const actualBase = digestOfTree(root, opts);
  if (actualBase !== patch.baseDigest) {
    throw new DigestMismatchError(
      `base digest mismatch: expected ${patch.baseDigest}, got ${actualBase}`,
      [],
    );
  }
  const holder = new Holder(structuredClone(root));
  const inverses: PatchOp[] = [];
  try {
    patch.ops.forEach((op, i) => {
      try {
        applyOp(holder, op, inverses);
      } catch (e) {
        throw new PatchApplyError(`patch op #${i} (${op.op}) failed: ${(e as Error).message}`, op.path, i);
      }
    });
    const actualTarget = digestOfTree(holder.value, opts);
    if (actualTarget !== patch.targetDigest) {
      throw new DigestMismatchError(
        `target digest mismatch after apply: expected ${patch.targetDigest}, got ${actualTarget}`,
        [],
      );
    }
  } catch (err) {
    for (let i = inverses.length - 1; i >= 0; i--) applyOp(holder, inverses[i], null);
    const restored = digestOfTree(holder.value, opts) === patch.baseDigest;
    if (err instanceof PatchApplyError) err.restored = restored;
    if (err instanceof DigestMismatchError && !restored) {
      throw new JsonDagError(`rollback failed after: ${err.message}`, err.path);
    }
    throw err;
  }
  const inverse: Patch = {
    ops: inverses.slice().reverse(),
    baseDigest: patch.targetDigest,
    targetDigest: patch.baseDigest,
  };
  return { root: holder.value, inverse, idIndex: buildIdIndex(holder.value, opts) };
}
