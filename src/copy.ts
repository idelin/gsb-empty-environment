import { util } from './node-compat.ts';
import {
  AccessorRejectedError,
  BudgetExceededError,
  ProxyRejectedError,
  UnsupportedValueError,
} from './errors.ts';
import {
  assertNotCapability,
  type Capability,
  type CapabilityRegistry,
} from './capability-registry.ts';

export type Primitive = string | number | bigint | boolean | null | undefined;

export type CopyBudgets = {
  maxNodes: number;
  maxBytes: number;
};

export type CopyOptions = {
  registry?: CapabilityRegistry;
  budgets?: Partial<CopyBudgets>;
};

export const DEFAULT_MAX_NODES = 10_000;
export const DEFAULT_MAX_BYTES = 1_048_576;

const ARRAY_NODE_OVERHEAD = 16;
const RECORD_NODE_OVERHEAD = 32;

type Frame = {
  source: object;
  target: object;
  keys: string[];
  index: number;
};

type State = {
  registry: CapabilityRegistry | undefined;
  budgets: CopyBudgets;
  nodes: number;
  bytes: number;
  seen: Map<object, object>;
  stack: Frame[];
};

export function copyMessage<T>(root: T, options: CopyOptions = {}): T {
  const state: State = {
    registry: options.registry,
    budgets: {
      maxNodes: options.budgets?.maxNodes ?? DEFAULT_MAX_NODES,
      maxBytes: options.budgets?.maxBytes ?? DEFAULT_MAX_BYTES,
    },
    nodes: 0,
    bytes: 0,
    seen: new Map(),
    stack: [],
  };

  const copied = place(state, root, '<root>');
  drain(state);
  return copied as T;
}

function drain(state: State): void {
  while (state.stack.length > 0) {
    const frame = state.stack[state.stack.length - 1];
    if (frame === undefined) {
      state.stack.pop();
      continue;
    }
    if (frame.index >= frame.keys.length) {
      state.stack.pop();
      continue;
    }
    const key = frame.keys[frame.index++];
    const descriptor = Object.getOwnPropertyDescriptor(frame.source, key);
    if (descriptor === undefined) {
      continue;
    }
    if ('get' in descriptor || 'set' in descriptor) {
      throw new AccessorRejectedError(describeKey(frame, key));
    }
    const child = place(state, descriptor.value, describeKey(frame, key));
    Reflect.set(frame.target, key, child);
  }
}

function place(state: State, value: unknown, pathHint: string): unknown {
  if (isAllowedPrimitive(value)) {
    chargePrimitive(state, value);
    return value;
  }

  const valueType = typeof value;
  if (valueType !== 'object' && valueType !== 'function') {
    throw new UnsupportedValueError(valueType);
  }

  const objectValue = value as object;

  if (util.types.isProxy(objectValue)) {
    throw new ProxyRejectedError();
  }

  const existing = state.seen.get(objectValue);
  if (existing !== undefined) {
    return existing;
  }

  assertNotCapability(state.registry, objectValue);

  if (valueType === 'function') {
    throw new UnsupportedValueError('function');
  }

  if (Array.isArray(objectValue)) {
    return allocateArray(state, objectValue);
  }

  if (!isPlainRecord(objectValue)) {
    throw new UnsupportedValueError(typeLabel(objectValue));
  }

  return allocateRecord(state, objectValue, pathHint);
}

function allocateArray(state: State, source: object): unknown[] {
  chargeNode(state);
  chargeBytes(state, ARRAY_NODE_OVERHEAD);

  const target: unknown[] = [];
  state.seen.set(source, target);

  const keys: string[] = [];
  for (const key of Reflect.ownKeys(source)) {
    if (typeof key !== 'string') {
      throw new UnsupportedValueError('symbol-keyed array property');
    }
    if (key === 'length') {
      continue;
    }
    if (!isCanonicalArrayIndex(key, (source as unknown[]).length)) {
      throw new UnsupportedValueError('non-index array property');
    }
    const descriptor = Object.getOwnPropertyDescriptor(source, key);
    if (descriptor === undefined) {
      continue;
    }
    if ('get' in descriptor || 'set' in descriptor) {
      throw new AccessorRejectedError(`[${key}]`);
    }
    keys.push(key);
  }

  state.stack.push({ source, target, keys, index: 0 });
  return target;
}

function allocateRecord(
  state: State,
  source: object,
  pathHint: string,
): Record<string, unknown> {
  chargeNode(state);
  chargeBytes(state, RECORD_NODE_OVERHEAD);

  const target: Record<string, unknown> = {};
  state.seen.set(source, target);

  const keys: string[] = [];
  for (const key of Reflect.ownKeys(source)) {
    if (typeof key !== 'string') {
      throw new UnsupportedValueError('symbol-keyed property');
    }
    const descriptor = Object.getOwnPropertyDescriptor(source, key);
    if (descriptor === undefined) {
      continue;
    }
    if (!descriptor.enumerable) {
      throw new UnsupportedValueError(`non-enumerable property ${describePath(pathHint, key)}`);
    }
    if ('get' in descriptor || 'set' in descriptor) {
      throw new AccessorRejectedError(describePath(pathHint, key));
    }
    keys.push(key);
    chargeBytes(state, key.length * 2);
  }

  state.stack.push({ source, target, keys, index: 0 });
  return target;
}

function isPlainRecord(value: object): boolean {
  const prototype = Object.getPrototypeOf(value);
  return prototype === Object.prototype || prototype === null;
}

function isAllowedPrimitive(value: unknown): value is Primitive {
  if (value === null) {
    return true;
  }
  const kind = typeof value;
  return (
    kind === 'string' ||
    kind === 'number' ||
    kind === 'boolean' ||
    kind === 'bigint' ||
    kind === 'undefined'
  );
}

function chargePrimitive(state: State, value: Primitive): void {
  if (value === null || value === undefined) {
    chargeBytes(state, 1);
  } else if (typeof value === 'boolean') {
    chargeBytes(state, 1);
  } else if (typeof value === 'number') {
    chargeBytes(state, 8);
  } else if (typeof value === 'string') {
    chargeBytes(state, value.length * 2);
  } else {
    chargeBytes(state, Math.ceil(value.toString().length / 2) + 4);
  }
}

function chargeNode(state: State): void {
  state.nodes += 1;
  if (state.nodes > state.budgets.maxNodes) {
    throw new BudgetExceededError('node', state.nodes, state.budgets.maxNodes);
  }
}

function chargeBytes(state: State, amount: number): void {
  state.bytes += amount;
  if (state.bytes > state.budgets.maxBytes) {
    throw new BudgetExceededError('byte', state.bytes, state.budgets.maxBytes);
  }
}

function isCanonicalArrayIndex(key: string, length: number): boolean {
  if (!/^(0|[1-9][0-9]*)$/.test(key)) {
    return false;
  }
  const index = Number(key);
  return Number.isSafeInteger(index) && index >= 0 && index < length;
}

function typeLabel(value: object): string {
  const tag = Object.prototype.toString.call(value).slice(8, -1);
  return tag.length > 0 ? tag : 'object';
}

function describeKey(frame: Frame, key: string): string {
  return Array.isArray(frame.source) ? `[${key}]` : `.${key}`;
}

function describePath(pathHint: string, key: string): string {
  return `${pathHint}.${key}`;
}

export type { Capability };
