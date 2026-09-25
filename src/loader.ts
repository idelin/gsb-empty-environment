import { Arena } from './arena.ts';
import { DuplicateIdError, GraphError } from './errors.ts';
import type { JsonValue, NodeKind, Slot } from './model.ts';

// Builds arena slots from a JSON-compatible tree.
// `declared` collects $id names within this single load operation so that
// duplicates introduced by one value are reported immediately, with a path.
export function buildSlot(
  arena: Arena,
  json: JsonValue,
  path: string,
  declared: Set<string>,
): Slot {
  if (json === null || typeof json !== 'object') return { t: 'prim', v: json };

  if (Array.isArray(json)) {
    const node = arena.create('array');
    json.forEach((item, index) => {
      const slot = buildSlot(arena, item, `${path}/${index}`, declared);
      node.slots.set(index, slot);
      if (slot.t === 'child') arena.linkChild(node.id, index, slot.node);
    });
    return { t: 'child', node: node.id };
  }

  const ref = json['$ref'];
  if (typeof ref === 'string') {
    const expectRaw = json['$expect'];
    if (expectRaw !== undefined && expectRaw !== 'object' && expectRaw !== 'array') {
      throw new GraphError(`invalid $expect ${JSON.stringify(expectRaw)}`, path);
    }
    const slot: Slot = { t: 'ref', refId: ref };
    if (expectRaw !== undefined) slot.expect = expectRaw as NodeKind;
    return slot;
  }

  let idName: string | undefined;
  const idRaw = json['$id'];
  if (idRaw !== undefined) {
    if (typeof idRaw !== 'string') throw new GraphError('$id must be a string', path);
    if (declared.has(idRaw)) throw new DuplicateIdError(idRaw, path);
    declared.add(idRaw);
    idName = idRaw;
  }

  const node = arena.create('object', idName);
  for (const key of Object.keys(json)) {
    if (key === '$id' || key === '$expect') continue;
    const slot = buildSlot(arena, json[key], `${path}/${key}`, declared);
    node.slots.set(key, slot);
    if (slot.t === 'child') arena.linkChild(node.id, key, slot.node);
  }
  return { t: 'child', node: node.id };
}
