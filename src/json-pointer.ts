import type { JsonObject, JsonPatchOperation, JsonValue } from './types.js';

/** Thrown when an RFC 6901 pointer or RFC 6902 operation is invalid. */
export class JsonPatchError extends Error {
  public readonly operationIndex: number;

  constructor(message: string, operationIndex: number) {
    super(message);
    this.name = 'JsonPatchError';
    this.operationIndex = operationIndex;
  }
}

/** Parse an RFC 6901 JSON Pointer into reference tokens ('' => root). */
export function parsePointer(pointer: string): string[] {
  if (pointer === '') {
    return [];
  }
  if (!pointer.startsWith('/')) {
    throw new Error(`invalid JSON Pointer (must start with "/" or be empty): ${pointer}`);
  }
  const tokens: string[] = [];
  for (const raw of pointer.slice(1).split('/')) {
    let token = '';
    for (let i = 0; i < raw.length; i += 1) {
      const char = raw[i];
      if (char === '~') {
        const next = raw[i + 1];
        if (next === '0') {
          token += '~';
          i += 1;
        } else if (next === '1') {
          token += '/';
          i += 1;
        } else {
          throw new Error(`invalid JSON Pointer escape in "${pointer}"`);
        }
      } else {
        token += char;
      }
    }
    tokens.push(token);
  }
  return tokens;
}

/** Render reference tokens back into an RFC 6901 pointer. */
export function formatPointer(tokens: readonly string[]): string {
  if (tokens.length === 0) {
    return '';
  }
  const escape = (token: string): string => token.replace(/~/g, '~0').replace(/\//g, '~1');
  return `/${tokens.map(escape).join('/')}`;
}

export function isArrayIndex(token: string): boolean {
  if (token === '0') {
    return true;
  }
  return /^[1-9][0-9]*$/.test(token);
}

function isContainer(value: JsonValue): value is JsonObject | JsonValue[] {
  return value !== null && typeof value === 'object';
}

/** Resolve the parent container plus final token for a pointer. */
function resolveParent(
  doc: JsonValue,
  tokens: readonly string[],
  operationIndex: number,
): { parent: JsonObject | JsonValue[] | null; token: string } {
  if (tokens.length === 0) {
    return { parent: null, token: '' };
  }
  let current: JsonValue = doc;
  for (let depth = 0; depth < tokens.length - 1; depth += 1) {
    const token = tokens[depth] as string;
    if (!isContainer(current)) {
      throw new JsonPatchError(`cannot traverse "${token}": not a container`, operationIndex);
    }
    if (Array.isArray(current)) {
      if (!isArrayIndex(token) || Number(token) >= current.length) {
        throw new JsonPatchError(`array index "${token}" out of bounds`, operationIndex);
      }
      current = current[Number(token)] as JsonValue;
    } else {
      if (!Object.prototype.hasOwnProperty.call(current, token)) {
        throw new JsonPatchError(`object member "${token}" does not exist`, operationIndex);
      }
      current = current[token] as JsonValue;
    }
  }
  if (!isContainer(current)) {
    throw new JsonPatchError('target parent is not a JSON object or array', operationIndex);
  }
  return { parent: current, token: tokens[tokens.length - 1] as string };
}

function addMember(
  parent: JsonObject | JsonValue[],
  token: string,
  value: JsonValue,
  operationIndex: number,
): void {
  if (Array.isArray(parent)) {
    if (token === '-') {
      parent.push(value);
      return;
    }
    if (!isArrayIndex(token)) {
      throw new JsonPatchError(`invalid array index "${token}" for add`, operationIndex);
    }
    const index = Number(token);
    if (index > parent.length) {
      throw new JsonPatchError(`add index ${index} exceeds length ${parent.length}`, operationIndex);
    }
    parent.splice(index, 0, value);
    return;
  }
  parent[token] = value;
}

function removeMember(
  parent: JsonObject | JsonValue[],
  token: string,
  operationIndex: number,
): JsonValue {
  if (Array.isArray(parent)) {
    if (!isArrayIndex(token) || Number(token) >= parent.length) {
      throw new JsonPatchError(`invalid array index "${token}" for remove`, operationIndex);
    }
    const [removed] = parent.splice(Number(token), 1);
    return removed as JsonValue;
  }
  if (!Object.prototype.hasOwnProperty.call(parent, token)) {
    throw new JsonPatchError(`cannot remove missing member "${token}"`, operationIndex);
  }
  const removed = parent[token] as JsonValue;
  delete parent[token];
  return removed;
}

function replaceMember(
  parent: JsonObject | JsonValue[],
  token: string,
  value: JsonValue,
  operationIndex: number,
): void {
  if (Array.isArray(parent)) {
    if (!isArrayIndex(token) || Number(token) >= parent.length) {
      throw new JsonPatchError(`invalid array index "${token}" for replace`, operationIndex);
    }
    parent[Number(token)] = value;
    return;
  }
  if (!Object.prototype.hasOwnProperty.call(parent, token)) {
    throw new JsonPatchError(`cannot replace missing member "${token}"`, operationIndex);
  }
  parent[token] = value;
}

function resolveValue(doc: JsonValue, pointer: string, operationIndex: number): JsonValue {
  const tokens = parsePointer(pointer);
  if (tokens.length === 0) {
    return doc;
  }
  let current: JsonValue = doc;
  for (const token of tokens) {
    if (!isContainer(current)) {
      throw new JsonPatchError(`cannot read "${pointer}": non-container encountered`, operationIndex);
    }
    if (Array.isArray(current)) {
      if (!isArrayIndex(token) || Number(token) >= current.length) {
        throw new JsonPatchError(`array index "${token}" out of bounds`, operationIndex);
      }
      current = current[Number(token)] as JsonValue;
    } else {
      if (!Object.prototype.hasOwnProperty.call(current, token)) {
        throw new JsonPatchError(`member "${token}" does not exist`, operationIndex);
      }
      current = current[token] as JsonValue;
    }
  }
  return current;
}

/** Deep structural equality for JSON values (key-order independent). */
export function jsonEqual(a: JsonValue, b: JsonValue): boolean {
  if (a === b) {
    return true;
  }
  if (typeof a !== typeof b || a === null || b === null) {
    return false;
  }
  if (Array.isArray(a) || Array.isArray(b)) {
    if (!Array.isArray(a) || !Array.isArray(b) || a.length !== b.length) {
      return false;
    }
    return a.every((item, index) => jsonEqual(item, b[index] as JsonValue));
  }
  if (typeof a === 'object' && typeof b === 'object') {
    const keysA = Object.keys(a).sort();
    const keysB = Object.keys(b).sort();
    if (keysA.length !== keysB.length) {
      return false;
    }
    return keysA.every(
      (key, index) =>
        key === keysB[index] &&
        jsonEqual((a as JsonObject)[key] as JsonValue, (b as JsonObject)[key] as JsonValue),
    );
  }
  return false;
}

/** Canonical serialization: object keys sorted recursively. */
export function canonicalJson(value: JsonValue): string {
  if (value === null || typeof value !== 'object') {
    return JSON.stringify(value);
  }
  if (Array.isArray(value)) {
    return `[${value.map(canonicalJson).join(',')}]`;
  }
  const entries = Object.keys(value)
    .sort()
    .map((key) => `${JSON.stringify(key)}:${canonicalJson((value as JsonObject)[key] as JsonValue)}`);
  return `{${entries.join(',')}}`;
}

export function deepClone<T extends JsonValue>(value: T): T {
  return structuredClone(value);
}

export interface ApplyPatchResult {
  doc: JsonValue;
  changed: boolean;
  /** Pointer paths touched by mutation ops that ran (deduplicated). */
  touchedPaths: string[];
}

function replaceRoot(next: JsonValue, operationIndex: number): JsonValue {
  if (next === null || typeof next !== 'object') {
    throw new JsonPatchError('document root must remain a JSON object or array', operationIndex);
  }
  return next;
}

/**
 * Apply an RFC 6902 patch atomically.
 *
 * The input document is never mutated: operations run against a defensive
 * clone. Any failing operation (including RFC 6902 `test`) discards the clone
 * and throws {@link JsonPatchError}, leaving the caller's document untouched,
 * with `operationIndex` identifying the failing operation.
 */
export function applyJsonPatch(
  doc: JsonValue,
  patch: readonly JsonPatchOperation[],
): ApplyPatchResult {
  let working: JsonValue = deepClone(doc);
  const before = canonicalJson(doc);
  const touched: string[] = [];
  const noteTouched = (pointer: string): void => {
    if (!touched.includes(pointer)) {
      touched.push(pointer);
    }
  };

  patch.forEach((operation, operationIndex) => {
    const { op, path } = operation;
    const tokens = parsePointer(path);

    switch (op) {
      case 'add': {
        if (tokens.length === 0) {
          throw new JsonPatchError('add cannot target the document root', operationIndex);
        }
        const { parent, token } = resolveParent(working, tokens, operationIndex);
        addMember(parent as JsonObject | JsonValue[], token, operation.value as JsonValue, operationIndex);
        noteTouched(path);
        break;
      }
      case 'remove': {
        if (tokens.length === 0) {
          throw new JsonPatchError('remove cannot target the document root', operationIndex);
        }
        const { parent, token } = resolveParent(working, tokens, operationIndex);
        removeMember(parent as JsonObject | JsonValue[], token, operationIndex);
        noteTouched(path);
        break;
      }
      case 'replace': {
        if (tokens.length === 0) {
          working = replaceRoot(operation.value as JsonValue, operationIndex);
        } else {
          const { parent, token } = resolveParent(working, tokens, operationIndex);
          replaceMember(parent as JsonObject | JsonValue[], token, operation.value as JsonValue, operationIndex);
        }
        noteTouched(path);
        break;
      }
      case 'move': {
        if (operation.from === undefined) {
          throw new JsonPatchError('move operation requires "from"', operationIndex);
        }
        if (operation.from === path || operation.from.startsWith(`${path}/`)) {
          throw new JsonPatchError('cannot move a path into itself or its descendant', operationIndex);
        }
        const fromTokens = parsePointer(operation.from);
        if (fromTokens.length === 0) {
          throw new JsonPatchError('move cannot take the document root as source', operationIndex);
        }
        const value = resolveValue(working, operation.from, operationIndex);
        const fromParent = resolveParent(working, fromTokens, operationIndex);
        removeMember(fromParent.parent as JsonObject | JsonValue[], fromParent.token, operationIndex);
        if (tokens.length === 0) {
          throw new JsonPatchError('move cannot target the document root', operationIndex);
        }
        const toParent = resolveParent(working, tokens, operationIndex);
        addMember(toParent.parent as JsonObject | JsonValue[], toParent.token, deepClone(value), operationIndex);
        noteTouched(operation.from);
        noteTouched(path);
        break;
      }
      case 'copy': {
        if (operation.from === undefined) {
          throw new JsonPatchError('copy operation requires "from"', operationIndex);
        }
        if (tokens.length === 0) {
          throw new JsonPatchError('copy cannot target the document root', operationIndex);
        }
        const value = resolveValue(working, operation.from, operationIndex);
        const { parent, token } = resolveParent(working, tokens, operationIndex);
        addMember(parent as JsonObject | JsonValue[], token, deepClone(value), operationIndex);
        noteTouched(path);
        break;
      }
      case 'test': {
        const actual = tokens.length === 0 ? working : resolveValue(working, path, operationIndex);
        if (!jsonEqual(actual, operation.value as JsonValue)) {
          throw new JsonPatchError(
            `test failed at "${path}": expected ${JSON.stringify(operation.value)}, got ${JSON.stringify(actual)}`,
            operationIndex,
          );
        }
        break;
      }
      default: {
        const exhaustive: never = op;
        throw new JsonPatchError(`unsupported operation "${String(exhaustive)}"`, operationIndex);
      }
    }
  });

  return {
    doc: working,
    changed: canonicalJson(working) !== before,
    touchedPaths: touched,
  };
}
