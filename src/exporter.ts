import { Arena } from './arena.ts';
import { PendingRefError } from './errors.ts';
import { isJsonObject } from './model.ts';
import type { JsonValue, NodeId, Slot } from './model.ts';

export interface ExportResult {
  docs: Record<string, JsonValue>;
}

// Reversible-inline emission: a named node is fully inlined at its first
// encounter (definition site, carrying its $id); every later encounter —
// including cycle back-edges — becomes { "$ref": id }. Object keys are
// emitted in sorted order (object key order is not meaningful); array
// order is preserved.
export function emitNode(
  arena: Arena,
  idTable: ReadonlyMap<string, NodeId>,
  emitted: Set<NodeId>,
  id: NodeId,
): JsonValue {
  const node = arena.get(id);
  if (node.idName !== undefined) {
    if (emitted.has(id)) return { $ref: node.idName };
    emitted.add(id);
  }
  if (node.kind === 'array') {
    const arr: JsonValue[] = [];
    for (let i = 0; i < node.slots.size; i++) {
      arr.push(emitSlot(arena, idTable, emitted, node.slots.get(i)!));
    }
    return arr;
  }
  const obj: Record<string, JsonValue> = {};
  if (node.idName !== undefined) obj['$id'] = node.idName;
  const keys = ([...node.slots.keys()] as (string | number)[])
    .map(String)
    .sort();
  for (const key of keys) {
    obj[key] = emitSlot(arena, idTable, emitted, node.slots.get(key)!);
  }
  return obj;
}

function emitSlot(
  arena: Arena,
  idTable: ReadonlyMap<string, NodeId>,
  emitted: Set<NodeId>,
  slot: Slot,
): JsonValue {
  if (slot.t === 'prim') return slot.v;
  if (slot.t === 'child') return emitNode(arena, idTable, emitted, slot.node);
  const target = idTable.get(slot.refId);
  if (target === undefined) throw new PendingRefError(slot.refId, '<export>');
  const out = emitNode(arena, idTable, emitted, target);
  if (slot.expect !== undefined && isJsonObject(out) && typeof out['$ref'] === 'string') {
    return { ...out, $expect: slot.expect };
  }
  return out;
}
