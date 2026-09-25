import { isPlainObject, toPointer } from './json.ts';
import { TreeDiffError } from './types.ts';
import type { JsonObject, JsonValue, PathSegment } from './types.ts';

/** Slot key used for the virtual parent of the root node. */
export const ROOT_KEY = '';

export interface NodeRecord {
  id: string;
  parentId: string | null;
  key: string;
  index: number;
  /** JSON Pointer of this node in the source document, for error reporting. */
  path: string;
  /** Own attributes: every property except `id` and child-slot arrays. */
  attrs: JsonObject;
  /** Slot key -> ordered child ids. */
  slots: Map<string, string[]>;
  slotKeys: Set<string>;
}

export interface TreeIndex {
  rootId: string;
  nodes: Map<string, NodeRecord>;
}

function isNodeLike(value: JsonValue): value is JsonObject {
  return isPlainObject(value) && 'id' in value;
}

function validateJsonValue(value: unknown, path: PathSegment[]): asserts value is JsonValue {
  if (value === null || typeof value === 'boolean' || typeof value === 'string') return;
  if (typeof value === 'number') {
    if (!Number.isFinite(value)) {
      throw new TreeDiffError('numbers must be finite JSON numbers', toPointer(path));
    }
    return;
  }
  if (Array.isArray(value)) {
    value.forEach((item, i) => validateJsonValue(item, [...path, i]));
    return;
  }
  if (isPlainObject(value)) {
    for (const key of Object.keys(value)) {
      validateJsonValue(value[key], [...path, key]);
    }
    return;
  }
  throw new TreeDiffError(`unsupported value of type ${typeof value}`, toPointer(path));
}

function readNodeId(obj: JsonObject, path: PathSegment[]): string {
  if (!('id' in obj)) {
    throw new TreeDiffError('node object is missing required string "id"', toPointer(path));
  }
  if (typeof obj.id !== 'string') {
    throw new TreeDiffError('node "id" must be a string', toPointer([...path, 'id']));
  }
  return obj.id;
}

/**
 * Validates a JSON-compatible tree and indexes every node by its stable id.
 *
 * A node is an object with a string `id`. An array-valued property whose
 * elements are all node-like objects is a child slot (ordered); everything
 * else is an attribute (object key order ignored, array order significant).
 */
export function buildIndex(root: unknown): TreeIndex {
  validateJsonValue(root, []);
  if (!isPlainObject(root)) {
    throw new TreeDiffError('root must be an object with a string "id"', '');
  }
  const nodes = new Map<string, NodeRecord>();

  const walk = (
    obj: JsonObject,
    parentId: string | null,
    key: string,
    index: number,
    path: PathSegment[],
  ): void => {
    const id = readNodeId(obj, path);
    const existing = nodes.get(id);
    if (existing) {
      throw new TreeDiffError(
        `duplicate node id ${JSON.stringify(id)} (first seen at ${existing.path === '' ? '/' : existing.path})`,
        toPointer(path),
      );
    }
    const record: NodeRecord = {
      id,
      parentId,
      key,
      index,
      path: toPointer(path),
      attrs: {},
      slots: new Map(),
      slotKeys: new Set(),
    };
    nodes.set(id, record);

    for (const childKey of Object.keys(obj)) {
      if (childKey === 'id') continue;
      const value = obj[childKey];
      if (Array.isArray(value) && value.length > 0 && value.every(isNodeLike)) {
        const childIds = value.map((child, i) =>
          readNodeId(child as JsonObject, [...path, childKey, i]),
        );
        record.slotKeys.add(childKey);
        record.slots.set(childKey, childIds);
        value.forEach((child, i) =>
          walk(child as JsonObject, id, childKey, i, [...path, childKey, i]),
        );
      } else {
        record.attrs[childKey] = value;
      }
    }
  };

  walk(root, null, ROOT_KEY, 0, []);
  return { rootId: root.id as string, nodes };
}
