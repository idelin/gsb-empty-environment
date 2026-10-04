/** Source loaders: defaults, JSON file, environment variables, CLI arguments. */
import { readFileSync } from 'node:fs';
import type { ConfigSchema, FieldType, MergeIssue } from './types.ts';
import { parseJson } from './json.ts';
import type { Layer } from './merge.ts';

export function fromDefaults(value: unknown, name = 'defaults'): Layer {
  return { kind: 'defaults', name, value, issues: [] };
}

export function fromFile(path: string): Layer {
  const text = readFileSync(path, 'utf8');
  const parsed = parseJson(text, { kind: 'file', name: path });
  return {
    kind: 'file',
    name: path,
    value: parsed.value,
    locations: parsed.locations,
    issues: parsed.issues,
  };
}

/** Parse JSON text directly (useful for tests and non-file sources). */
export function fromJsonText(text: string, name = '<json>'): Layer {
  const parsed = parseJson(text, { kind: 'file', name });
  return {
    kind: 'file',
    name,
    value: parsed.value,
    locations: parsed.locations,
    issues: parsed.issues,
  };
}

interface CoerceOk {
  ok: true;
  value: unknown;
}
interface CoerceErr {
  ok: false;
  expected: string;
}
type CoerceResult = CoerceOk | CoerceErr;

function coerceOne(raw: string, type: 'string' | 'number' | 'boolean'): CoerceResult {
  switch (type) {
    case 'string':
      return { ok: true, value: raw };
    case 'number': {
      if (raw.trim() === '' || Number.isNaN(Number(raw))) {
        return { ok: false, expected: 'number' };
      }
      return { ok: true, value: Number(raw) };
    }
    case 'boolean': {
      const lowered = raw.trim().toLowerCase();
      if (['true', '1', 'yes', 'on'].includes(lowered)) return { ok: true, value: true };
      if (['false', '0', 'no', 'off'].includes(lowered)) return { ok: true, value: false };
      return { ok: false, expected: 'boolean (true/false/1/0/yes/no/on/off)' };
    }
  }
}

export function coerce(raw: string, type: FieldType): CoerceResult {
  switch (type) {
    case 'string':
    case 'number':
    case 'boolean':
      return coerceOne(raw, type);
    case 'string[]':
    case 'number[]': {
      const itemType = type === 'string[]' ? 'string' : 'number';
      const parts = raw === '' ? [] : raw.split(',').map((p) => p.trim());
      const out: unknown[] = [];
      for (const part of parts) {
        const r = coerceOne(part, itemType);
        if (!r.ok) return { ok: false, expected: type };
        out.push(r.value);
      }
      return { ok: true, value: out };
    }
    case 'object':
    case 'any': {
      try {
        return { ok: true, value: JSON.parse(raw) };
      } catch {
        return type === 'any' ? { ok: true, value: raw } : { ok: false, expected: 'JSON object' };
      }
    }
  }
}

/** Best-effort coercion when no schema type is declared. */
function autoCoerce(raw: string): unknown {
  const trimmed = raw.trim();
  const lowered = trimmed.toLowerCase();
  if (lowered === 'true') return true;
  if (lowered === 'false') return false;
  if (trimmed !== '' && !Number.isNaN(Number(trimmed))) return Number(trimmed);
  // Objects/arrays (e.g. list directives like {"$append":[...]}) pass through as JSON.
  if (trimmed.startsWith('{') || trimmed.startsWith('[')) {
    try {
      return JSON.parse(trimmed);
    } catch {
      // fall through: treat as plain string
    }
  }
  return raw;
}

function setPath(target: Record<string, unknown>, path: string, value: unknown): void {
  const segments = path.split('.');
  let node = target;
  for (const seg of segments.slice(0, -1)) {
    const next = node[seg];
    if (typeof next !== 'object' || next === null || Array.isArray(next)) {
      node[seg] = {};
    }
    node = node[seg] as Record<string, unknown>;
  }
  node[segments[segments.length - 1]!] = value;
}

function convertRaw(
  raw: string,
  path: string,
  schema: ConfigSchema,
  source: { kind: 'env' | 'cli'; name: string; location?: string },
  issues: MergeIssue[],
): unknown {
  const field = schema[path];
  if (field === undefined) return autoCoerce(raw);
  const result = coerce(raw, field.type);
  if (!result.ok) {
    issues.push({
      severity: 'error',
      code: 'type-conversion',
      path,
      message: `cannot convert '${raw}' to ${result.expected} for '${path}'`,
      source,
    });
    return undefined;
  }
  return result.value;
}

export interface EnvOptions {
  /** Only variables starting with this prefix are picked up; prefix is stripped. */
  prefix?: string;
  /** Separator between nesting segments, default "__". */
  separator?: string;
  schema?: ConfigSchema;
  env?: Record<string, string | undefined>;
}

/**
 * Maps APP_SERVER__PORT=8080 -> { server: { port: 8080 } }.
 * Segments are lowercased; values are coerced via the schema when declared.
 */
export function fromEnv(options: EnvOptions = {}): Layer {
  const {
    prefix = '',
    separator = '__',
    schema = {},
    env = process.env as Record<string, string | undefined>,
  } = options;
  const value: Record<string, unknown> = {};
  const issues: MergeIssue[] = [];

  for (const [key, raw] of Object.entries(env)) {
    if (raw === undefined) continue;
    if (prefix !== '' && !key.startsWith(prefix)) continue;
    const stripped = prefix === '' ? key : key.slice(prefix.length);
    if (stripped === '') continue;
    const path = stripped
      .split(separator)
      .map((s) => s.toLowerCase())
      .join('.');
    const converted = convertRaw(raw, path, schema, { kind: 'env', name: key }, issues);
    if (converted !== undefined) setPath(value, path, converted);
  }
  return { kind: 'env', name: prefix === '' ? 'env' : `env(${prefix}*)`, value, issues };
}

export interface CliOptions {
  schema?: ConfigSchema;
}

/**
 * Parses --key=value, --key value, --flag (true) and --no-flag (false).
 * Dotted keys nest: --server.port=8080 -> { server: { port: 8080 } }.
 */
export function fromCli(argv: string[], options: CliOptions = {}): Layer {
  const { schema = {} } = options;
  const value: Record<string, unknown> = {};
  const issues: MergeIssue[] = [];

  for (let i = 0; i < argv.length; i += 1) {
    const token = argv[i]!;
    if (!token.startsWith('--')) continue;
    let body = token.slice(2);
    let raw: string | undefined;
    const eq = body.indexOf('=');
    if (eq >= 0) {
      raw = body.slice(eq + 1);
      body = body.slice(0, eq);
    }
    let parsed: unknown;
    if (body.startsWith('no-') && raw === undefined) {
      body = body.slice(3);
      parsed = false;
    } else {
      if (raw === undefined) {
        const next = argv[i + 1];
        if (next !== undefined && !next.startsWith('--')) {
          raw = next;
          i += 1;
        }
      }
      if (raw === undefined) {
        parsed = true;
      } else {
        parsed = convertRaw(
          raw,
          body,
          schema,
          { kind: 'cli', name: `--${body}`, location: `argv[${i}]` },
          issues,
        );
      }
    }
    if (parsed !== undefined) setPath(value, body, parsed);
  }
  return { kind: 'cli', name: 'cli', value, issues };
}
