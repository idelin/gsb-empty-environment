import type { TreeNode } from './types.ts';

/** Error that can be located to a concrete JSON-pointer-style path. */
export class TreeError extends Error {
  /** JSON pointer (RFC 6901) of the offending value, e.g. `/children/0/id`. */
  readonly path: string;

  constructor(message: string, path: string) {
    const pointer = path === '' ? '/' : path;
    super(`${message} at ${pointer}`);
    this.name = 'TreeError';
    this.path = pointer;
  }
}

function escapePointer(segment: string): string {
  return segment.replace(/~/g, '~0').replace(/\//g, '~1');
}

function assertPlainObject(value: object, path: string): void {
  const proto: unknown = Object.getPrototypeOf(value);
  if (proto !== Object.prototype && proto !== null) {
    throw new TreeError('value must be a plain JSON object', path);
  }
}

/** Throws a TreeError (with path) unless `value` is JSON-compatible. */
export function assertJsonValue(value: unknown, path: string, seen: Set<object> = new Set()): void {
  if (value === null) return;
  switch (typeof value) {
    case 'string':
    case 'boolean':
      return;
    case 'number':
      if (!Number.isFinite(value)) {
        throw new TreeError('number is not JSON-compatible (NaN/Infinity)', path);
      }
      return;
    case 'object': {
      if (seen.has(value)) {
        throw new TreeError('cyclic value is not JSON-compatible', path);
      }
      seen.add(value);
      if (Array.isArray(value)) {
        value.forEach((item, i) => assertJsonValue(item, `${path}/${i}`, seen));
      } else {
        assertPlainObject(value, path);
        for (const [key, item] of Object.entries(value)) {
          assertJsonValue(item, `${path}/${escapePointer(key)}`, seen);
        }
      }
      seen.delete(value);
      return;
    }
    default:
      throw new TreeError(`value of type "${typeof value}" is not JSON-compatible`, path);
  }
}

const NODE_KEYS = new Set(['id', 'attrs', 'children']);

/**
 * Validates a whole tree and returns it typed as TreeNode.
 * Every problem is reported as a TreeError carrying the exact path.
 */
export function validateTree(input: unknown): TreeNode {
  const ids = new Set<string>();
  const visiting = new Set<object>();

  const visit = (value: unknown, path: string): void => {
    if (typeof value !== 'object' || value === null || Array.isArray(value)) {
      throw new TreeError('node must be a plain object', path);
    }
    if (visiting.has(value)) {
      throw new TreeError('cyclic children: input is not a tree', path);
    }
    visiting.add(value);
    assertPlainObject(value, path);
    const node = value as Record<string, unknown>;

    for (const key of Object.keys(node)) {
      if (!NODE_KEYS.has(key)) {
        throw new TreeError(`unknown node key "${key}"`, `${path}/${escapePointer(key)}`);
      }
    }

    if (typeof node.id !== 'string' || node.id.length === 0) {
      throw new TreeError('node "id" must be a non-empty string', `${path}/id`);
    }
    if (ids.has(node.id)) {
      throw new TreeError(`duplicate node id "${node.id}"`, `${path}/id`);
    }
    ids.add(node.id);

    if ('attrs' in node) {
      if (typeof node.attrs !== 'object' || node.attrs === null || Array.isArray(node.attrs)) {
        throw new TreeError('"attrs" must be a plain object', `${path}/attrs`);
      }
      assertPlainObject(node.attrs, `${path}/attrs`);
      for (const [key, item] of Object.entries(node.attrs)) {
        assertJsonValue(item, `${path}/attrs/${escapePointer(key)}`);
      }
    }

    if ('children' in node) {
      if (!Array.isArray(node.children)) {
        throw new TreeError('"children" must be an array', `${path}/children`);
      }
      node.children.forEach((child, i) => visit(child, `${path}/children/${i}`));
    }
    visiting.delete(value);
  };

  visit(input, '');
  return input as TreeNode;
}
