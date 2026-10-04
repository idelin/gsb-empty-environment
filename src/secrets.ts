/**
 * Secret handling: secret fields are masked for display, but the raw values
 * remain available in the merge result/provenance so changes stay comparable.
 */
import type { ConfigSchema } from './types.ts';
import { deepEqual, nodeAt, splitPath, type ProvNode } from './provenance.ts';

export const SECRET_MASK = '********';

/** True when `path` itself or any ancestor is declared secret. */
export function isSecretPath(schema: ConfigSchema, path: string): boolean {
  const segments = splitPath(path);
  for (let i = segments.length; i >= 1; i -= 1) {
    if (schema[segments.slice(0, i).join('.')]?.secret === true) return true;
  }
  return false;
}

/** Deep copy with secret leaves (or subtrees) replaced by the mask. */
export function redact(value: unknown, schema: ConfigSchema, path = ''): unknown {
  if (path !== '' && schema[path]?.secret === true) return SECRET_MASK;
  if (Array.isArray(value)) {
    return value.map((item, i) => redact(item, schema, `${path}[${i}]`));
  }
  if (typeof value === 'object' && value !== null) {
    const out: Record<string, unknown> = {};
    for (const [k, v] of Object.entries(value)) {
      const childPath = path === '' ? k : `${path}.${k}`;
      out[k] = redact(v, schema, childPath);
    }
    return out;
  }
  return value;
}

/**
 * Compare the raw values of two provenance trees at `path`.
 * Works on unmasked data, so secrets stay comparable without being exposed.
 */
export function valueChanged(a: ProvNode, b: ProvNode, path: string): boolean {
  const nodeA = nodeAt(a, path);
  const nodeB = nodeAt(b, path);
  const valueA = nodeA?.entries[0]?.value;
  const valueB = nodeB?.entries[0]?.value;
  if (nodeA === undefined && nodeB === undefined) return false;
  if (nodeA === undefined || nodeB === undefined) return true;
  return !deepEqual(valueA, valueB);
}

/** True when the winning value at `path` differs from any overridden value. */
export function wasOverridden(root: ProvNode, path: string): boolean {
  const node = nodeAt(root, path);
  return node !== undefined && node.entries.length > 1;
}

function displayValue(value: unknown, secret: boolean): string {
  if (secret) return SECRET_MASK;
  return typeof value === 'string' ? JSON.stringify(value) : JSON.stringify(value) ?? String(value);
}

/** Human-readable provenance chain for a path, masking secret values. */
export function explain(root: ProvNode, path: string, schema: ConfigSchema = {}): string {
  const node = nodeAt(root, path);
  if (node === undefined || node.entries.length === 0) {
    return `${path}: <no value>`;
  }
  const secret = isSecretPath(schema, path);
  const lines: string[] = [];
  node.entries.forEach((entry, i) => {
    const tag = i === 0 ? 'value' : 'overridden';
    const loc = entry.source.location ? ` at ${entry.source.location}` : '';
    lines.push(
      `${path}: ${tag}=${displayValue(entry.value, secret)} from ${entry.source.kind}:${entry.source.name}${loc}`,
    );
  });
  return lines.join('\n');
}
