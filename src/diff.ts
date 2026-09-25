import { buildIndex, ROOT_KEY } from './index-tree.ts';
import type { TreeIndex } from './index-tree.ts';
import { canonicalize, isPlainObject, jsonEqual } from './json.ts';
import type {
  JsonObject,
  JsonValue,
  Operation,
  PathSegment,
} from './types.ts';

/**
 * Ids of `target` (restricted to those also in `oldOrder`) that can stay put:
 * a longest subsequence of `target` whose relative order matches `oldOrder`.
 * Deterministic: ties resolve toward the earliest elements.
 */
function computeStayIds(target: string[], oldOrder: string[]): Set<string> {
  const oldPos = new Map(oldOrder.map((id, i) => [id, i]));
  const seq: { id: string; pos: number }[] = [];
  for (const id of target) {
    const pos = oldPos.get(id);
    if (pos !== undefined) seq.push({ id, pos });
  }
  const n = seq.length;
  const length = new Array<number>(n).fill(1);
  const prev = new Array<number>(n).fill(-1);
  let best = 0;
  for (let i = 0; i < n; i++) {
    for (let j = 0; j < i; j++) {
      if (seq[j].pos < seq[i].pos && length[j] + 1 > length[i]) {
        length[i] = length[j] + 1;
        prev[i] = j;
      }
    }
    if (length[i] > length[best]) best = i;
  }
  const stays = new Set<string>();
  for (let i = n === 0 ? -1 : best; i !== -1; i = prev[i]) {
    stays.add(seq[i].id);
  }
  return stays;
}

function diffAttrs(
  oldAttrs: JsonObject,
  newAttrs: JsonObject,
  id: string,
  base: PathSegment[],
  out: Operation[],
): void {
  const keys = new Set([...Object.keys(oldAttrs), ...Object.keys(newAttrs)]);
  for (const key of [...keys].sort()) {
    const path = [...base, key];
    const inOld = key in oldAttrs;
    const inNew = key in newAttrs;
    if (inOld && !inNew) {
      out.push({ op: 'update', id, path, unset: true });
      continue;
    }
    if (!inOld && inNew) {
      out.push({ op: 'update', id, path, value: canonicalize(newAttrs[key]) });
      continue;
    }
    const oldValue = oldAttrs[key];
    const newValue = newAttrs[key];
    if (isPlainObject(oldValue) && isPlainObject(newValue)) {
      diffAttrs(oldValue, newValue, id, path, out);
    } else if (!jsonEqual(oldValue, newValue)) {
      out.push({ op: 'update', id, path, value: canonicalize(newValue) });
    }
  }
}

/**
 * Computes a minimal deterministic patch transforming `oldTree` into
 * `newTree`. Nodes are matched by stable `id`:
 *
 * - ids only in the new tree -> `insert` (parents always before children);
 * - ids in both, at a new parent/key/index -> `move` (identity preserved);
 * - ids only in the old tree -> `remove` (topmost removed ancestors only, so
 *   children moved elsewhere are never removed twice);
 * - ids in both, with changed attributes -> `update` (leaf-level set/unset).
 *
 * Op order: updates, then inserts/moves in new-tree pre-order, then removes.
 * Object key order in the inputs never affects the result.
 */
export function diffTrees(oldTree: JsonValue, newTree: JsonValue): Operation[] {
  const oldIndex: TreeIndex = buildIndex(oldTree);
  const newIndex: TreeIndex = buildIndex(newTree);

  const insertedIds = new Set(
    [...newIndex.nodes.keys()].filter((id) => !oldIndex.nodes.has(id)),
  );
  const deletedIds = new Set(
    [...oldIndex.nodes.keys()].filter((id) => !newIndex.nodes.has(id)),
  );
  const keptIds = [...newIndex.nodes.keys()]
    .filter((id) => oldIndex.nodes.has(id))
    .sort();

  // 1. Attribute updates for nodes present in both trees.
  const updateOps: Operation[] = [];
  for (const id of keptIds) {
    const oldRecord = oldIndex.nodes.get(id)!;
    const newRecord = newIndex.nodes.get(id)!;
    const oldAttrs: JsonObject = { ...oldRecord.attrs };
    const newAttrs: JsonObject = { ...newRecord.attrs };
    // A child slot that exists only in the old tree and whose key is entirely
    // gone in the new tree must be removed with an explicit unset. (If the
    // key survives as a plain attribute, the normal diff emits a set.)
    for (const key of oldRecord.slotKeys) {
      if (!newRecord.slotKeys.has(key) && !(key in newAttrs)) {
        oldAttrs[key] = [];
      }
    }
    diffAttrs(oldAttrs, newAttrs, id, [], updateOps);
  }

  // 2. Structural ops. Removes run last in the patch, so insert/move indices
  // must be computed against the physical slot contents at apply time. We
  // simulate that physical state here: deleted and moved-out children are
  // still present, and only "fixed" children (LIS stays + already placed
  // ones) anchor the insertion point of the next op.
  const structureOps: Operation[] = [];
  const physSlots = new Map<string | null, Map<string, string[]>>();
  const location = new Map<string, { parentId: string | null; key: string }>();
  const getSlot = (parentId: string | null, key: string): string[] => {
    let byKey = physSlots.get(parentId);
    if (!byKey) {
      byKey = new Map();
      physSlots.set(parentId, byKey);
    }
    let list = byKey.get(key);
    if (!list) {
      list = [];
      byKey.set(key, list);
    }
    return list;
  };
  for (const record of oldIndex.nodes.values()) {
    location.set(record.id, { parentId: record.parentId, key: record.key });
    for (const [key, childIds] of record.slots) {
      getSlot(record.id, key).push(...childIds);
    }
  }
  getSlot(null, ROOT_KEY).push(oldIndex.rootId);

  const processSlot = (
    parentId: string | null,
    key: string,
    target: string[],
  ): void => {
    const list = getSlot(parentId, key);
    const targetSet = new Set(target);
    const present = list.filter((id) => targetSet.has(id));
    const stays = computeStayIds(target, present);
    const fixed = new Set(stays);
    for (let i = 0; i < target.length; i++) {
      const id = target[i];
      if (stays.has(id)) continue; // already ordered correctly, never moved
      const isNew = insertedIds.has(id);
      if (!isNew) {
        const from = location.get(id)!;
        const fromList = getSlot(from.parentId, from.key);
        fromList.splice(fromList.indexOf(id), 1);
      }
      // Insert before the i-th fixed child (target[0..i-1] are all fixed),
      // or append when fewer than i fixed children are present.
      let index = list.length;
      let seen = 0;
      for (let j = 0; j < list.length; j++) {
        if (fixed.has(list[j])) {
          if (seen === i) {
            index = j;
            break;
          }
          seen++;
        }
      }
      if (isNew) {
        const record = newIndex.nodes.get(id)!;
        structureOps.push({
          op: 'insert',
          id,
          parentId,
          key,
          index,
          node: canonicalize({ ...record.attrs, id }),
        });
      } else {
        structureOps.push({ op: 'move', id, parentId, key, index });
        location.set(id, { parentId, key });
      }
      list.splice(index, 0, id);
      fixed.add(id);
    }
  };

  // Pre-order over the new tree: a node's slots are processed before its
  // children are visited, so parents are always created before children.
  const visit = (id: string): void => {
    const record = newIndex.nodes.get(id)!;
    const keys = [...record.slots.keys()].sort();
    for (const key of keys) processSlot(id, key, record.slots.get(key)!);
    for (const key of keys) {
      for (const childId of record.slots.get(key)!) visit(childId);
    }
  };
  processSlot(null, ROOT_KEY, [newIndex.rootId]);
  visit(newIndex.rootId);

  // 3. Removes, last: children that moved away are already relocated, and
  // only topmost deleted nodes need an op (their subtrees go with them).
  const removeOps: Operation[] = [];
  const topmostDeleted = [...deletedIds]
    .filter((id) => {
      let parent = oldIndex.nodes.get(id)!.parentId;
      while (parent !== null) {
        if (deletedIds.has(parent)) return false;
        parent = oldIndex.nodes.get(parent)!.parentId;
      }
      return true;
    })
    .sort();
  for (const id of topmostDeleted) {
    removeOps.push({ op: 'remove', id });
  }

  return [...updateOps, ...structureOps, ...removeOps];
}
