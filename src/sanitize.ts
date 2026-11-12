import { BridgeError } from './errors.ts';
import { BLOCKED_KEYS, type Schema, validate } from './schema.ts';

function scanValue(
  input: unknown,
  seen: WeakSet<object>,
  path: string,
): void {
  if (typeof input === 'function') {
    throw new BridgeError(
      'NON_CLONEABLE_ARGUMENT',
      `${path}: functions are not allowed`,
    );
  }
  if (input === null || typeof input !== 'object') {
    return;
  }
  if (seen.has(input)) {
    return;
  }
  seen.add(input);

  if (Array.isArray(input)) {
    input.forEach((item, index) => {
      scanValue(item, seen, `${path}[${index}]`);
    });
    return;
  }

  const descriptors = Object.getOwnPropertyDescriptors(input);

  for (const key of Reflect.ownKeys(input)) {
    if (typeof key === 'symbol') {
      throw new BridgeError(
        'SYMBOL_KEY',
        `${path}: symbol-keyed properties are not allowed`,
      );
    }
    if (BLOCKED_KEYS.has(key)) {
      throw new BridgeError(
        'FORBIDDEN_KEY',
        `${path}.${key}: prototype pollution keys are not allowed`,
      );
    }
    const descriptor = descriptors[key];
    if (descriptor && ('get' in descriptor || 'set' in descriptor)) {
      throw new BridgeError(
        'ACCESSOR_PROPERTY',
        `${path}.${key}: getter/setter properties are not allowed`,
      );
    }
  }

  for (const key of Object.keys(input)) {
    scanValue(
      (input as Record<string, unknown>)[key],
      seen,
      `${path}.${key}`,
    );
  }
}

export function safeClone<T>(input: unknown): T {
  scanValue(input, new WeakSet(), '$');
  try {
    return structuredClone(input) as T;
  } catch (error) {
    if (error instanceof BridgeError) {
      throw error;
    }
    if (error instanceof Error && error.name === 'DataCloneError') {
      throw new BridgeError(
        'NON_CLONEABLE_ARGUMENT',
        `$: value cannot be structured-cloned (${error.message})`,
      );
    }
    throw error;
  }
}

export function sanitizeArguments(
  args: unknown,
  params: readonly Schema[],
): unknown[] {
  if (!Array.isArray(args)) {
    throw new BridgeError('BAD_PAYLOAD', '$: args must be an array');
  }
  if (args.length !== params.length) {
    throw new BridgeError(
      'SCHEMA_MISMATCH',
      `$: expected ${params.length} argument(s), got ${args.length}`,
    );
  }

  const cloned = safeClone<unknown[]>(args);
  params.forEach((schema, index) => {
    validate(cloned[index], schema, `$[${index}]`);
  });
  return cloned;
}
