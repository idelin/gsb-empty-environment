/**
 * Atomic JSON Patch application (add/remove/replace/move/test) with precise
 * inverse generation. The input document and operations are never mutated:
 * work happens on a deep clone, so a failure leaves the original untouched.
 */

import {
  deepClone,
  deepEqual,
  parsePointer,
  formatPointer,
  arrayIndex,
  PointerError,
  type Json,
} from './json.ts';

export type Op =
  | { op: 'add'; path: string; value: Json }
  | { op: 'remove'; path: string }
  | { op: 'replace'; path: string; value: Json }
  | { op: 'move'; path: string; from: string }
  | { op: 'test'; path: string; value: Json };

export class PatchError extends Error {
  readonly opIndex: number;
  readonly path: string;
  readonly reason: string;
  constructor(opIndex: number, path: string, reason: string) {
    super(`op ${opIndex} at ${path === '' ? '<root>' : path}: ${reason}`);
    this.name = 'PatchError';
    this.opIndex = opIndex;
    this.path = path;
    this.reason = reason;
  }
}

export interface ApplyResult {
  doc: Json;
  /** Inverse ops in replay order: apply(result.doc, inverse) restores the input. */
  inverse: Op[];
}

type Container = Json[] | { [key: string]: Json };

function isContainer(v: Json): v is Container {
  return v !== null && typeof v === 'object';
}

export function apply(doc: Json, ops: readonly Op[]): ApplyResult {
  // Holder lets us replace the root itself.
  const holder: { v: Json } = { v: deepClone(doc) };
  const inverse: Op[] = [];

  const fail = (i: number, path: string, reason: string): never => {
    throw new PatchError(i, path, reason);
  };

  const parse = (i: number, path: string): string[] => {
    try {
      return parsePointer(path);
    } catch (e) {
      return fail(i, path, e instanceof PointerError ? e.message : String(e));
    }
  };

  // Resolve tokens to a value; distinguishes "missing" from null/0/false.
  const getAt = (tokens: string[]): { found: boolean; value?: Json } => {
    let cur: Json = holder.v;
    for (const t of tokens) {
      if (Array.isArray(cur)) {
        let idx: number | '-';
        try {
          idx = arrayIndex(t, false);
        } catch {
          return { found: false };
        }
        if (idx === '-' || idx >= cur.length) return { found: false };
        cur = cur[idx]!;
      } else if (isContainer(cur)) {
        if (!Object.prototype.hasOwnProperty.call(cur, t)) return { found: false };
        cur = (cur as { [key: string]: Json })[t]!;
      } else {
        return { found: false };
      }
    }
    return { found: true, value: cur };
  };

  // Resolve the parent container of tokens; returns the container and final token.
  const parentOf = (i: number, path: string, tokens: string[]): { parent: Container; key: string } => {
    const found = getAt(tokens.slice(0, -1));
    if (!found.found || !isContainer(found.value!)) {
      fail(i, path, 'parent path does not exist or is not a container');
    }
    return { parent: found.value as Container, key: tokens[tokens.length - 1]! };
  };

  // Insert value at tokens (add semantics). Returns the actual insertion path.
  const addAt = (i: number, path: string, tokens: string[], value: Json): string => {
    if (tokens.length === 0) {
      holder.v = value;
      return '';
    }
    const { parent, key } = parentOf(i, path, tokens);
    if (Array.isArray(parent)) {
      let idx: number | '-';
      try {
        idx = arrayIndex(key, true);
      } catch (e) {
        return fail(i, path, e instanceof PointerError ? e.message : String(e));
      }
      if (idx === '-') {
        parent.push(value);
        return formatPointer([...tokens.slice(0, -1), String(parent.length - 1)]);
      }
      if (idx > parent.length) fail(i, path, `array index ${idx} out of bounds (length ${parent.length})`);
      parent.splice(idx, 0, value);
      return path;
    }
    (parent as { [key: string]: Json })[key] = value;
    return path;
  };

  // Remove the value at tokens and return it.
  const removeAt = (i: number, path: string, tokens: string[]): Json => {
    const { parent, key } = parentOf(i, path, tokens);
    if (Array.isArray(parent)) {
      let idx: number | '-';
      try {
        idx = arrayIndex(key, false);
      } catch (e) {
        return fail(i, path, e instanceof PointerError ? e.message : String(e));
      }
      if ((idx as number) >= parent.length) {
        fail(i, path, `array index ${String(idx)} out of bounds (length ${parent.length})`);
      }
      return parent.splice(idx as number, 1)[0]!;
    }
    if (!Object.prototype.hasOwnProperty.call(parent, key)) fail(i, path, 'path does not exist');
    const old = (parent as { [key: string]: Json })[key]!;
    delete (parent as { [key: string]: Json })[key];
    return old;
  };

  for (let i = 0; i < ops.length; i++) {
    const op = ops[i]!;
    if (op === null || typeof op !== 'object' || typeof (op as Op).op !== 'string') {
      fail(i, '', 'malformed operation');
    }
    const path = (op as Op).path;
    if (typeof path !== 'string') fail(i, '', 'op is missing a string path');

    switch ((op as Op).op) {
      case 'add': {
        const o = op as Extract<Op, { op: 'add' }>;
        if (!('value' in o)) fail(i, path, 'add requires a value');
        const tokens = parse(i, path);
        if (tokens.length === 0) {
          inverse.push({ op: 'replace', path: '', value: holder.v });
          holder.v = deepClone(o.value);
          break;
        }
        // Object key overwrite must be undone with replace, not remove.
        const { parent, key } = parentOf(i, path, tokens);
        if (!Array.isArray(parent) && Object.prototype.hasOwnProperty.call(parent, key)) {
          const old = (parent as { [key: string]: Json })[key]!;
          (parent as { [key: string]: Json })[key] = deepClone(o.value);
          inverse.push({ op: 'replace', path, value: old });
        } else if (!Array.isArray(parent)) {
          (parent as { [key: string]: Json })[key] = deepClone(o.value);
          inverse.push({ op: 'remove', path });
        } else {
          const actual = addAt(i, path, tokens, deepClone(o.value));
          inverse.push({ op: 'remove', path: actual });
        }
        break;
      }
      case 'remove': {
        const tokens = parse(i, path);
        if (tokens.length === 0) fail(i, path, 'cannot remove the document root');
        const old = removeAt(i, path, tokens);
        inverse.push({ op: 'add', path, value: old });
        break;
      }
      case 'replace': {
        const o = op as Extract<Op, { op: 'replace' }>;
        if (!('value' in o)) fail(i, path, 'replace requires a value');
        const tokens = parse(i, path);
        if (tokens.length === 0) {
          inverse.push({ op: 'replace', path: '', value: holder.v });
          holder.v = deepClone(o.value);
          break;
        }
        const { parent, key } = parentOf(i, path, tokens);
        if (Array.isArray(parent)) {
          let idx: number | '-';
          try {
            idx = arrayIndex(key, false);
          } catch (e) {
            return failWithPtr(i, path, e);
          }
          if ((idx as number) >= parent.length) {
            fail(i, path, `array index ${String(idx)} out of bounds (length ${parent.length})`);
          }
          const old = parent[idx as number]!;
          parent[idx as number] = deepClone(o.value);
          inverse.push({ op: 'replace', path, value: old });
        } else {
          if (!Object.prototype.hasOwnProperty.call(parent, key)) fail(i, path, 'path does not exist');
          const old = (parent as { [key: string]: Json })[key]!;
          (parent as { [key: string]: Json })[key] = deepClone(o.value);
          inverse.push({ op: 'replace', path, value: old });
        }
        break;
      }
      case 'move': {
        const o = op as Extract<Op, { op: 'move' }>;
        if (typeof o.from !== 'string') fail(i, path, 'move requires a string "from"');
        const fromTokens = parse(i, o.from);
        const toTokens = parse(i, path);
        // The destination is interpreted after removal of the source, and must
        // not lie inside the moved node.
        if (
          toTokens.length > fromTokens.length &&
          fromTokens.every((t, k) => toTokens[k] === t)
        ) {
          fail(i, path, 'cannot move a node into its own descendant');
        }
        if (fromTokens.length === 0 && toTokens.length === 0) break; // no-op
        if (fromTokens.length === 0) fail(i, path, 'cannot move the document root');
        const value = removeAt(i, o.from, fromTokens);
        const actual = addAt(i, path, toTokens, value);
        inverse.push({ op: 'move', from: actual, path: formatPointer(fromTokens) });
        break;
      }
      case 'test': {
        const o = op as Extract<Op, { op: 'test' }>;
        if (!('value' in o)) fail(i, path, 'test requires a value');
        const tokens = parse(i, path);
        const found = getAt(tokens);
        if (!found.found) fail(i, path, 'test failed: path does not exist');
        if (!deepEqual(found.value!, o.value)) fail(i, path, 'test failed: values differ');
        break; // test never modifies; no inverse op
      }
      default:
        fail(i, path, `unknown op "${String((op as Op).op)}"`);
    }
  }

  inverse.reverse();
  return { doc: holder.v, inverse };

  function failWithPtr(i: number, path: string, e: unknown): never {
    return fail(i, path, e instanceof PointerError ? e.message : String(e));
  }
}
