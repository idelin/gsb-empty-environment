import type { JsonValue } from './json.ts';
import { deepEqual, isPlainObject } from './json.ts';
import { MoveConflictError } from './errors.ts';
import { formatPath, isArrayIndex, isPrefixPath } from './path.ts';
import type { PlannedMove } from './plan.ts';

const REMOVED: unique symbol = Symbol('removed');
type MaybeRemoved = JsonValue | typeof REMOVED;

function pathKey(path: string[]): string {
  return JSON.stringify(path);
}

function comparePaths(a: string[], b: string[]): number {
  const n = Math.min(a.length, b.length);
  for (let i = 0; i < n; i += 1) {
    const x = a[i] as string;
    const y = b[i] as string;
    if (x < y) return -1;
    if (x > y) return 1;
  }
  return a.length - b.length;
}

function getPath(root: JsonValue, path: string[]): { found: boolean; value?: JsonValue } {
  let node: JsonValue = root;
  for (const seg of path) {
    if (Array.isArray(node)) {
      if (!isArrayIndex(seg)) return { found: false };
      const idx = Number(seg);
      if (idx >= node.length) return { found: false };
      node = node[idx] as JsonValue;
    } else if (isPlainObject(node)) {
      if (!Object.prototype.hasOwnProperty.call(node, seg)) return { found: false };
      node = node[seg] as JsonValue;
    } else {
      return { found: false };
    }
  }
  return { found: true, value: node };
}

/**
 * Rebuild the tree without the removed paths. Arrays are compacted, and
 * containers that became empty because their children were moved away are
 * pruned as well (containers that were already empty are kept).
 */
function prune(node: JsonValue, path: string[], removals: ReadonlySet<string>): MaybeRemoved {
  if (removals.has(pathKey(path))) return REMOVED;
  if (Array.isArray(node)) {
    const out: JsonValue[] = [];
    let removedAny = false;
    node.forEach((child, i) => {
      const kept = prune(child, [...path, String(i)], removals);
      if (kept === REMOVED) removedAny = true;
      else out.push(kept);
    });
    return removedAny && out.length === 0 ? REMOVED : out;
  }
  if (isPlainObject(node)) {
    const out: { [key: string]: JsonValue } = {};
    let removedAny = false;
    for (const key of Object.keys(node)) {
      const kept = prune(node[key] as JsonValue, [...path, key], removals);
      if (kept === REMOVED) removedAny = true;
      else out[key] = kept;
    }
    return removedAny && Object.keys(out).length === 0 ? REMOVED : out;
  }
  return node;
}

function setChild(container: JsonValue, seg: string, value: JsonValue, fullPath: string[]): void {
  if (Array.isArray(container)) {
    if (!isArrayIndex(seg)) {
      throw new MoveConflictError(formatPath(fullPath), [], `segment ${JSON.stringify(seg)} is not a valid array index`);
    }
    const idx = Number(seg);
    if (idx > container.length) {
      throw new MoveConflictError(formatPath(fullPath), [], `array index ${idx} is out of bounds (length ${container.length})`);
    }
    if (idx === container.length) container.push(value);
    else container[idx] = value;
    return;
  }
  if (isPlainObject(container)) {
    container[seg] = value;
    return;
  }
  throw new MoveConflictError(formatPath(fullPath), [], `cannot descend into a primitive value`);
}

function insert(root: JsonValue | undefined, path: string[], value: JsonValue): JsonValue {
  if (path.length === 0) return value;
  let current: JsonValue = root ?? (isArrayIndex(path[0] as string) ? [] : {});
  let node = current;
  for (let i = 0; i < path.length - 1; i += 1) {
    const seg = path[i] as string;
    const nextSeg = path[i + 1] as string;
    const here = getPath(node, [seg]);
    let child: JsonValue;
    if (here.found) {
      child = here.value as JsonValue;
    } else {
      child = isArrayIndex(nextSeg) ? [] : {};
      setChild(node, seg, child, path.slice(0, i + 1));
    }
    node = child;
  }
  setChild(node, path[path.length - 1] as string, value, path);
  return current;
}

/**
 * Check a plan for conflicts, then apply it to `tree` (which is not mutated).
 *
 * Conflicts (MoveConflictError, carrying the concrete target path and the
 * source paths involved):
 *  - two ops write different values to the same target;
 *  - one target is a strict prefix of another target;
 *  - the target location survives the removals and holds a different value.
 *
 * Identical values at the same target are merged silently (idempotent moves).
 */
export function applyMoves(tree: JsonValue, ops: PlannedMove[]): JsonValue {
  // 1. Merge / reject duplicate targets.
  const byTarget = new Map<string, PlannedMove[]>();
  for (const op of ops) {
    const key = pathKey(op.to);
    const group = byTarget.get(key);
    if (group) group.push(op);
    else byTarget.set(key, [op]);
  }
  const effective: PlannedMove[] = [];
  for (const group of byTarget.values()) {
    const first = group[0] as PlannedMove;
    const divergent = group.find((op) => !deepEqual(op.value, first.value));
    if (divergent) {
      throw new MoveConflictError(
        first.toPath,
        group.map((op) => op.fromPath),
        'multiple sources move different values to the same target',
      );
    }
    effective.push(first);
  }

  // 2. Reject overlapping targets (one target strictly inside another).
  const sorted = [...effective].sort((a, b) => comparePaths(a.to, b.to));
  let anchor: PlannedMove | null = null;
  for (const op of sorted) {
    if (anchor && isPrefixPath(anchor.to, op.to) && anchor.to.length < op.to.length) {
      throw new MoveConflictError(
        op.toPath,
        [anchor.fromPath, op.fromPath],
        `target is nested inside target ${JSON.stringify(anchor.toPath)}`,
      );
    }
    anchor = op;
  }

  // 3. Compute the tree with all sources removed (simultaneous-move semantics).
  // Every planned source is removed, including sources merged into an
  // identical duplicate target.
  const removals = new Set(ops.map((op) => pathKey(op.from)));
  const pruned = prune(tree, [], removals);
  const base: JsonValue | undefined = pruned === REMOVED ? undefined : pruned;

  // 4. Reject targets that survive the removals with a different value.
  for (const op of effective) {
    if (op.to.length === 0) {
      if (base !== undefined && !deepEqual(base, op.value)) {
        throw new MoveConflictError(op.toPath || '(root)', [op.fromPath], 'target root already exists with a different value');
      }
      continue;
    }
    if (base === undefined) continue;
    const existing = getPath(base, op.to);
    if (existing.found && !deepEqual(existing.value as JsonValue, op.value)) {
      throw new MoveConflictError(op.toPath, [op.fromPath], 'target already exists with a different value');
    }
  }

  // 5. Insert values at their targets (parents first for determinism).
  const inserts = [...effective].sort((a, b) => a.to.length - b.to.length || comparePaths(a.to, b.to));
  let result = base;
  for (const op of inserts) {
    result = insert(result, op.to, op.value);
  }
  return result as JsonValue;
}
