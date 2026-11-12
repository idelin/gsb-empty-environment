import { ImportMapError } from './errors.ts';
import type { ImportMap, ImportMapEntries } from './types.ts';

interface Frame {
  object: boolean;
  keys: Set<string>;
}

export function scanJsonKeys(text: string): void {
  const stack: Frame[] = [];
  let index = 0;

  const skipWhitespace = (): void => {
    while (index < text.length && /\s/.test(text[index])) {
      index += 1;
    }
  };

  const readString = (): string => {
    const start = index;
    index += 1;
    while (index < text.length) {
      const char = text[index];
      if (char === '\\') {
        index += 2;
        continue;
      }
      if (char === '"') {
        index += 1;
        return text.slice(start, index);
      }
      index += 1;
    }
    throw new ImportMapError('INVALID_JSON', 'unterminated string while scanning import map');
  };

  const skipLiteralValue = (): void => {
    while (index < text.length && !/[\s,}\]]/.test(text[index])) {
      index += 1;
    }
  };

  while (index < text.length) {
    skipWhitespace();
    if (index >= text.length) {
      break;
    }

    const char = text[index];

    if (char === '{' || char === '[') {
      stack.push({ object: char === '{', keys: new Set<string>() });
      index += 1;
      continue;
    }

    if (char === '}' || char === ']') {
      stack.pop();
      index += 1;
      continue;
    }

    if (char === ',' || char === ':') {
      index += 1;
      continue;
    }

    if (char !== '"') {
      skipLiteralValue();
      continue;
    }

    const rawString = readString();
    skipWhitespace();
    const frame = stack[stack.length - 1];

    if (text[index] === ':') {
      if (frame === undefined || !frame.object) {
        throw new ImportMapError('INVALID_JSON', 'unexpected key while scanning import map');
      }
      if (rawString === '"__proto__"') {
        throw new ImportMapError('PROTO_KEY', 'import map must not contain __proto__ keys');
      }
      const decoded = JSON.parse(rawString) as string;
      if (frame.keys.has(decoded)) {
        throw new ImportMapError('DUPLICATE_KEY', `duplicate key ${rawString} in import map`);
      }
      frame.keys.add(decoded);
    }
  }
}

export function parseImportMapText(text: string): ImportMap {
  if (typeof text !== 'string') {
    throw new ImportMapError('INVALID_JSON', 'import map input must be a string or object');
  }

  scanJsonKeys(text);

  let value: unknown;
  try {
    value = JSON.parse(text);
  } catch (error) {
    throw new ImportMapError('INVALID_JSON', `import map is not valid JSON: ${(error as Error).message}`);
  }

  return validateImportMapShape(value);
}

export function validateImportMapShape(value: unknown): ImportMap {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) {
    throw new ImportMapError('INVALID_SHAPE', 'import map root must be an object');
  }

  const root = value as Record<string, unknown>;
  for (const key of Object.keys(root)) {
    if (key === '__proto__') {
      throw new ImportMapError('PROTO_KEY', 'import map must not contain __proto__ keys');
    }
    if (key !== 'imports' && key !== 'scopes') {
      throw new ImportMapError('INVALID_SHAPE', `unknown top-level import map key "${key}"`);
    }
  }

  const map: ImportMap = {};

  if (root.imports !== undefined) {
    map.imports = validateEntries(root.imports, 'imports');
  }

  if (root.scopes !== undefined) {
    if (root.scopes === null || typeof root.scopes !== 'object' || Array.isArray(root.scopes)) {
      throw new ImportMapError('INVALID_SHAPE', '"scopes" must be an object');
    }
    const scopes: Record<string, ImportMapEntries> = {};
    for (const [scopeKey, entries] of Object.entries(root.scopes as Record<string, unknown>)) {
      if (scopeKey === '__proto__') {
        throw new ImportMapError('PROTO_KEY', 'import map must not contain __proto__ keys');
      }
      if (scopeKey === '') {
        throw new ImportMapError('INVALID_SHAPE', 'scope keys must not be empty');
      }
      scopes[scopeKey] = validateEntries(entries, `scopes["${scopeKey}"]`);
    }
    map.scopes = scopes;
  }

  return map;
}

function validateEntries(value: unknown, path: string): ImportMapEntries {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) {
    throw new ImportMapError('INVALID_SHAPE', `"${path}" must be an object of string mappings`);
  }

  const entries: ImportMapEntries = {};
  for (const [key, target] of Object.entries(value as Record<string, unknown>)) {
    if (key === '__proto__') {
      throw new ImportMapError('PROTO_KEY', 'import map must not contain __proto__ keys');
    }
    if (key === '') {
      throw new ImportMapError('INVALID_SHAPE', `mapping keys in "${path}" must not be empty`);
    }
    if (typeof target !== 'string' || target === '') {
      throw new ImportMapError('INVALID_SHAPE', `target for "${key}" in "${path}" must be a non-empty string`);
    }
    entries[key] = target;
  }

  return entries;
}
