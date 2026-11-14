import { TemplateError } from './errors.ts';
import type { SourceLocation } from './location.ts';

export type SafeValue = object;

export const MISSING = Symbol('template.missing');
export const UNSAFE = Symbol('template.unsafe');

export type SafeRead = SafeValue | typeof MISSING | typeof UNSAFE;

type Primitive = string | number | boolean | null | undefined;

type SafeState =
  | { kind: 'primitive'; value: Primitive }
  | { kind: 'array'; value: readonly unknown[]; length: number }
  | { kind: 'object'; value: object }
  | { kind: 'unsupported' };

const states = new WeakMap<object, SafeState>();

const FORBIDDEN_KEYS = new Set([
  '__proto__',
  'prototype',
  'constructor',
  'caller',
  'callee',
  'arguments',
  'hasOwnProperty',
  'isPrototypeOf',
  'propertyIsEnumerable',
  'toLocaleString',
  'toString',
  'valueOf',
  '__defineGetter__',
  '__defineSetter__',
  '__lookupGetter__',
  '__lookupSetter__',
  'eval',
  'Function',
  'global',
  'globalThis',
  'module',
  'exports',
  'process',
  'require',
]);

export function isForbiddenKey(key: string): boolean {
  return FORBIDDEN_KEYS.has(key) || key.startsWith('__');
}

export function assertSafeKey(key: string, location: SourceLocation): void {
  if (isForbiddenKey(key)) {
    throw new TemplateError(location, 'FORBIDDEN_ACCESS');
  }
}

function emptyView(): SafeValue {
  return Object.freeze(Object.create(null));
}

/**
 * Wrap a value in a frozen, prototype-less view. The raw value is kept in a
 * private WeakMap and is never reachable from template expressions.
 */
export function box(value: unknown): SafeValue {
  if (typeof value === 'object' && value !== null && states.has(value)) {
    return value;
  }

  const view = emptyView();

  if (value === null || value === undefined) {
    states.set(view, { kind: 'primitive', value });
    return view;
  }

  const type = typeof value;
  if (type === 'string' || type === 'number' || type === 'boolean') {
    states.set(view, { kind: 'primitive', value: value as Primitive });
    return view;
  }

  if (type !== 'object') {
    states.set(view, { kind: 'unsupported' });
    return view;
  }

  if (Array.isArray(value)) {
    states.set(view, {
      kind: 'array',
      value: value as readonly unknown[],
      length: value.length,
    });
    return view;
  }

  states.set(view, { kind: 'object', value: value as object });
  return view;
}

function stateOf(value: SafeValue): SafeState | undefined {
  return states.get(value);
}

export function isPrimitiveView(value: SafeValue): boolean {
  return stateOf(value)?.kind === 'primitive';
}

export function isUnsupportedView(value: SafeValue): boolean {
  return stateOf(value)?.kind === 'unsupported';
}

export function primitiveValue(value: SafeValue): Primitive | undefined {
  const state = stateOf(value);
  return state?.kind === 'primitive' ? state.value : undefined;
}

export function arrayLength(value: SafeValue): number | undefined {
  const state = stateOf(value);
  return state?.kind === 'array' ? state.length : undefined;
}

export function isArrayView(value: SafeValue): boolean {
  return stateOf(value)?.kind === 'array';
}

export function isObjectView(value: SafeValue): boolean {
  return stateOf(value)?.kind === 'object';
}

function dataProperty(
  target: object,
  key: string,
): {
  present: boolean;
  value?: unknown;
  unsafe?: boolean;
  enumerable?: boolean;
} {
  try {
    const descriptor = Object.getOwnPropertyDescriptor(target, key);
    if (descriptor === undefined) {
      return { present: false };
    }
    if (!('value' in descriptor)) {
      return { present: true, unsafe: true };
    }
    return {
      present: true,
      value: descriptor.value,
      enumerable: descriptor.enumerable === true,
    };
  } catch {
    return { present: true, unsafe: true };
  }
}

function canonicalIndex(key: string): number | undefined {
  if (!/^(?:0|[1-9][0-9]*)$/.test(key)) {
    return undefined;
  }
  const index = Number(key);
  return Number.isSafeInteger(index) && index >= 0 ? index : undefined;
}

/**
 * Read one own data property. Prototype lookup, accessors and symbol keys are
 * never consulted.
 */
export function readProperty(value: SafeValue, key: string): SafeRead {
  if (isForbiddenKey(key)) {
    return UNSAFE;
  }

  const state = stateOf(value);
  if (state === undefined || state.kind === 'unsupported') {
    return UNSAFE;
  }

  if (state.kind === 'primitive') {
    if (typeof state.value === 'string' && key === 'length') {
      return box(state.value.length);
    }
    return MISSING;
  }

  if (state.kind === 'array') {
    if (key === 'length') {
      return box(state.length);
    }

    const index = canonicalIndex(key);
    if (index === undefined || index >= state.length) {
      return MISSING;
    }

    const property = dataProperty(state.value, key);
    if (property.unsafe) {
      return UNSAFE;
    }
    if (!property.present) {
      return MISSING;
    }
    if (!property.enumerable) {
      return MISSING;
    }
    return box(property.value);
  }

  const property = dataProperty(state.value, key);
  if (property.unsafe) {
    return UNSAFE;
  }
  if (!property.present) {
    return MISSING;
  }
  if (!property.enumerable) {
    return MISSING;
  }
  return box(property.value);
}

export interface EnumerableEntry {
  key: string;
  value: SafeValue;
}

export function enumerableEntries(
  value: SafeValue,
  limit = Number.MAX_SAFE_INTEGER,
): EnumerableEntry[] {
  const state = stateOf(value);
  if (state === undefined || state.kind !== 'object') {
    return [];
  }

  const entries: EnumerableEntry[] = [];
  for (const key in state.value) {
    if (entries.length >= limit) {
      break;
    }
    if (isForbiddenKey(key)) {
      continue;
    }
    const property = dataProperty(state.value, key);
    if (!property.present || property.unsafe) {
      continue;
    }

    const boxed = box(property.value);
    if (isUnsupportedView(boxed)) {
      continue;
    }
    entries.push({ key, value: boxed });
  }
  return entries;
}

export function isTruthy(value: SafeValue): boolean {
  const state = stateOf(value);
  if (state === undefined || state.kind === 'unsupported') {
    return false;
  }
  if (state.kind === 'primitive') {
    return Boolean(state.value);
  }
  if (state.kind === 'array') {
    return state.length > 0;
  }
  return true;
}

export function toPrimitive(value: SafeValue): Primitive | undefined {
  const state = stateOf(value);
  return state?.kind === 'primitive' ? state.value : undefined;
}

export interface StringifyBudget {
  maxLength: number;
  maxDepth: number;
  maxNodes: number;
  pathAllowed?: (path: readonly string[]) => boolean;
}

interface StringifyState {
  budget: StringifyBudget;
  location: SourceLocation;
  length: number;
  nodes: number;
  depth: number;
  seen: Set<object>;
}

function appendBudgeted(state: StringifyState, text: string): string {
  state.length += text.length;
  if (state.length > state.budget.maxLength) {
    throw new TemplateError(state.location, 'BUDGET_EXCEEDED');
  }
  return text;
}

function quoteJson(text: string, state: StringifyState): string {
  let result = '';
  for (let index = 0; index < text.length; index += 1) {
    const code = text.charCodeAt(index);
    const character = text[index];
    if (character === '"' || character === '\\') {
      result += `\\${character}`;
    } else if (character === '\n') {
      result += '\\n';
    } else if (character === '\r') {
      result += '\\r';
    } else if (character === '\t') {
      result += '\\t';
    } else if (code < 0x20) {
      result += `\\u${code.toString(16).padStart(4, '0')}`;
    } else {
      result += character;
    }
  }
  return appendBudgeted(state, result);
}

function jsonValue(
  value: SafeValue,
  state: StringifyState,
  path: readonly string[],
): string {
  state.nodes += 1;
  if (state.nodes > state.budget.maxNodes) {
    throw new TemplateError(state.location, 'BUDGET_EXCEEDED');
  }
  if (state.depth > state.budget.maxDepth) {
    throw new TemplateError(state.location, 'BUDGET_EXCEEDED');
  }

  const current = stateOf(value);
  if (current === undefined || current.kind === 'unsupported') {
    return 'null';
  }
  if (current.kind === 'primitive') {
    const primitive = current.value;
    if (primitive === null || primitive === undefined) {
      return 'null';
    }
    if (typeof primitive === 'string') {
      return `"${quoteJson(primitive, state)}"`;
    }
    if (typeof primitive === 'number') {
      return Number.isFinite(primitive) ? String(primitive) : 'null';
    }
    return primitive ? 'true' : 'false';
  }

  if (current.kind === 'array') {
    if (state.seen.has(current.value)) {
      throw new TemplateError(state.location, 'UNSAFE_VALUE');
    }
    state.seen.add(current.value);
    state.depth += 1;
    const items: string[] = [];
    for (let index = 0; index < current.length; index += 1) {
      const itemPath = [...path, String(index)];
      if (
        state.budget.pathAllowed !== undefined &&
        !state.budget.pathAllowed(itemPath)
      ) {
        items.push('null');
        continue;
      }
      const property = readProperty(value, String(index));
      items.push(
        property === MISSING || property === UNSAFE
          ? 'null'
          : jsonValue(property, state, itemPath),
      );
    }
    state.depth -= 1;
    state.seen.delete(current.value);
    return appendBudgeted(state, `[${items.join(',')}]`);
  }

  if (state.seen.has(current.value)) {
    throw new TemplateError(state.location, 'UNSAFE_VALUE');
  }
  state.seen.add(current.value);
  state.depth += 1;

  const remaining = state.budget.maxNodes - state.nodes;
  const entries = enumerableEntries(value, Math.max(0, remaining) + 1);
  if (entries.length > remaining) {
    throw new TemplateError(state.location, 'BUDGET_EXCEEDED');
  }
  const fields: string[] = [];
  for (const entry of entries) {
    const entryPath = [...path, entry.key];
    if (
      state.budget.pathAllowed !== undefined &&
      !state.budget.pathAllowed(entryPath)
    ) {
      continue;
    }
    fields.push(
      `"${quoteJson(entry.key, state)}":${jsonValue(entry.value, state, entryPath)}`,
    );
  }

  state.depth -= 1;
  state.seen.delete(current.value);
  return appendBudgeted(state, `{${fields.join(',')}}`);
}

export function toJson(
  value: SafeValue,
  budget: StringifyBudget,
  location: SourceLocation,
  path: readonly string[] = [],
): string {
  const state: StringifyState = {
    budget,
    location,
    length: 0,
    nodes: 0,
    depth: 0,
    seen: new Set(),
  };
  return jsonValue(value, state, path);
}

/**
 * The value supplied to a trusted, host-registered filter. It is a frozen,
 * prototype-less copy; filters cannot reach or mutate the original context.
 */
export function toFilterValue(
  value: SafeValue,
  budget: StringifyBudget,
  location: SourceLocation,
  pathPrefix: readonly string[] = [],
  pathAllowed?: (path: readonly string[]) => boolean,
): unknown {
  const seen = new Map<object, unknown>();
  let nodes = 0;

  const clone = (
    current: SafeValue,
    depth: number,
    path: readonly string[],
  ): unknown => {
    nodes += 1;
    if (nodes > budget.maxNodes) {
      throw new TemplateError(location, 'BUDGET_EXCEEDED');
    }
    if (depth > budget.maxDepth) {
      throw new TemplateError(location, 'BUDGET_EXCEEDED');
    }
    const state = stateOf(current);
    if (state === undefined || state.kind === 'unsupported') {
      return undefined;
    }
    if (state.kind === 'primitive') {
      return state.value;
    }
    if (state.kind === 'array') {
      if (seen.has(state.value)) {
        return undefined;
      }
      if (state.length > budget.maxNodes - nodes) {
        throw new TemplateError(location, 'BUDGET_EXCEEDED');
      }
      const output: unknown[] = [];
      seen.set(state.value, output);
      for (let index = 0; index < state.length; index += 1) {
        const itemPath = [...path, String(index)];
        if (pathAllowed !== undefined && !pathAllowed(itemPath)) {
          output.push(undefined);
          continue;
        }
        const property = readProperty(current, String(index));
        output.push(
          property === MISSING || property === UNSAFE
            ? undefined
            : clone(property, depth + 1, itemPath),
        );
      }
      Object.setPrototypeOf(output, null);
      return Object.freeze(output);
    }

    if (seen.has(state.value)) {
      return undefined;
    }
    const output: Record<string, unknown> = Object.create(null);
    seen.set(state.value, output);
    const remaining = budget.maxNodes - nodes;
    const entries = enumerableEntries(current, Math.max(0, remaining) + 1);
    if (entries.length > remaining) {
      throw new TemplateError(location, 'BUDGET_EXCEEDED');
    }
    for (const entry of entries) {
      const entryPath = [...path, entry.key];
      if (pathAllowed !== undefined && !pathAllowed(entryPath)) {
        continue;
      }
      output[entry.key] = clone(entry.value, depth + 1, entryPath);
    }
    return Object.freeze(output);
  };

  return clone(value, 0, pathPrefix);
}
