import { buildIndex, ROOT_KEY } from './index-tree.ts';
import { deepCopy, isPlainObject } from './json.ts';
import { TreeDiffError } from './types.ts';
import type { JsonObject, JsonValue, Operation } from './types.ts';

interface Meta {
  node: JsonObject;
  parent: JsonObject | null;
  key: string;
}

function isNodeObject(value: JsonValue): value is JsonObject {
  return isPlainObject(value) && typeof value.id === 'string';
}

/**
 * Applies a patch produced by `diffTrees` to `oldTree`, returning the new
 * tree. The input is not mutated. Every reference is checked as it is
 * applied; failures raise `TreeDiffError` with path `/ops/<index>`.
 */
export function applyPatch(oldTree: JsonValue, ops: Operation[]): JsonObject {
  buildIndex(oldTree); // validate input before mutating anything
  const copied = deepCopy(oldTree);
  if (!isPlainObject(copied)) {
    throw new TreeDiffError('root must be an object with a string "id"', '');
  }

  const meta = new Map<string, Meta>();
  let rootId: string | undefined;

  const registerSubtree = (node: JsonObject, parent: JsonObject | null, key: string): void => {
    const id = node.id as string;
    meta.set(id, { node, parent, key });
    for (const childKey of Object.keys(node)) {
      const value = node[childKey];
      if (Array.isArray(value)) {
        for (const item of value) {
          if (isNodeObject(item)) registerSubtree(item, node, childKey);
        }
      }
    }
  };
  registerSubtree(copied, null, ROOT_KEY);
  rootId = copied.id as string;

  const detach = (id: string): void => {
    const entry = meta.get(id)!;
    if (entry.parent === null) {
      if (rootId === id) rootId = undefined;
    } else {
      const siblings = entry.parent[entry.key];
      if (Array.isArray(siblings)) {
        const at = siblings.findIndex((item) => isNodeObject(item) && item.id === id);
        if (at !== -1) siblings.splice(at, 1);
      }
    }
    entry.parent = null;
  };

  const attach = (
    id: string,
    node: JsonObject,
    parentId: string | null,
    key: string,
    index: number,
    opPath: string,
  ): void => {
    if (parentId === null) {
      rootId = id;
      meta.set(id, { node, parent: null, key: ROOT_KEY });
      return;
    }
    const parentMeta = meta.get(parentId);
    if (!parentMeta) {
      throw new TreeDiffError(
        `dangling reference: parent ${JSON.stringify(parentId)} does not exist`,
        opPath,
      );
    }
    let siblings = parentMeta.node[key];
    if (siblings === undefined) {
      siblings = [];
      parentMeta.node[key] = siblings;
    }
    if (!Array.isArray(siblings)) {
      throw new TreeDiffError(
        `slot ${JSON.stringify(key)} on node ${JSON.stringify(parentId)} is not an array`,
        opPath,
      );
    }
    if (!Number.isInteger(index) || index < 0 || index > siblings.length) {
      throw new TreeDiffError(
        `index ${index} is out of bounds for slot ${JSON.stringify(key)} on node ${JSON.stringify(parentId)}`,
        opPath,
      );
    }
    siblings.splice(index, 0, node);
    meta.set(id, { node, parent: parentMeta.node, key });
  };

  const dropSubtree = (node: JsonObject): void => {
    for (const key of Object.keys(node)) {
      const value = node[key];
      if (Array.isArray(value)) {
        for (const item of value) {
          if (isNodeObject(item)) dropSubtree(item);
        }
      }
    }
    meta.delete(node.id as string);
  };

  ops.forEach((op, i) => {
    const opPath = `/ops/${i}`;
    switch (op.op) {
      case 'insert': {
        if (meta.has(op.id)) {
          throw new TreeDiffError(`duplicate insert of id ${JSON.stringify(op.id)}`, opPath);
        }
        const node = deepCopy(op.node);
        node.id = op.id;
        attach(op.id, node, op.parentId, op.key, op.index, opPath);
        break;
      }
      case 'move': {
        const entry = meta.get(op.id);
        if (!entry) {
          throw new TreeDiffError(
            `dangling reference: moved node ${JSON.stringify(op.id)} does not exist`,
            opPath,
          );
        }
        detach(op.id);
        attach(op.id, entry.node, op.parentId, op.key, op.index, opPath);
        break;
      }
      case 'remove': {
        const entry = meta.get(op.id);
        if (!entry) {
          throw new TreeDiffError(
            `dangling reference: removed node ${JSON.stringify(op.id)} does not exist`,
            opPath,
          );
        }
        detach(op.id);
        dropSubtree(entry.node);
        break;
      }
      case 'update': {
        const entry = meta.get(op.id);
        if (!entry) {
          throw new TreeDiffError(
            `dangling reference: updated node ${JSON.stringify(op.id)} does not exist`,
            opPath,
          );
        }
        if (op.path.length === 0) {
          throw new TreeDiffError('update path must not be empty', opPath);
        }
        let target: JsonValue = entry.node;
        for (let s = 0; s < op.path.length - 1; s++) {
          if (!isPlainObject(target) && !Array.isArray(target)) {
            throw new TreeDiffError(
              `update path does not exist on node ${JSON.stringify(op.id)}`,
              opPath,
            );
          }
          target = (target as JsonObject)[op.path[s] as string];
        }
        if (!isPlainObject(target) && !Array.isArray(target)) {
          throw new TreeDiffError(
            `update path does not exist on node ${JSON.stringify(op.id)}`,
            opPath,
          );
        }
        const last = op.path[op.path.length - 1];
        if ('unset' in op) {
          if (Array.isArray(target) && typeof last === 'number') target.splice(last, 1);
          else delete (target as JsonObject)[last as string];
        } else {
          (target as JsonObject)[last as string] = deepCopy(op.value);
        }
        break;
      }
    }
  });

  if (rootId === undefined) {
    throw new TreeDiffError('patch removed the root without inserting a new one', '');
  }
  return meta.get(rootId)!.node;
}
