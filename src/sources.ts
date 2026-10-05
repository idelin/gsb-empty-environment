import { readFileSync } from 'node:fs';
import { ConfigError } from './errors.ts';
import { parseJson } from './json.ts';
import type { ListOp, SourceEntry, SourceRef } from './origin.ts';

/**
 * List operation suffixes on keys (object sources and argv):
 *   "tags!" or "tags=" -> replace (also the default with no suffix)
 *   "tags+"            -> append values
 *   "tags-"            -> remove the given values
 */
const OP_SUFFIXES: Record<string, ListOp> = { '!': 'set', '=': 'set', '+': 'append', '-': 'remove' };

function splitOpKey(key: string): { key: string; op: ListOp } {
  const last = key.slice(-1);
  const op = OP_SUFFIXES[last];
  if (op && key.length > 1) return { key: key.slice(0, -1), op };
  return { key, op: 'set' };
}

/** Flatten a plain object into entries; objects recurse, arrays/scalars become entries. */
function flattenObject(
  obj: Record<string, unknown>,
  basePath: string[],
  source: (path: string[]) => SourceRef,
  out: SourceEntry[],
): void {
  for (const [rawKey, value] of Object.entries(obj)) {
    const { key, op } = splitOpKey(rawKey);
    const path = [...basePath, key];
    if (op === 'set' && isPlainObject(value)) {
      flattenObject(value as Record<string, unknown>, path, source, out);
    } else {
      out.push({ path, value, op, source: source(path) });
    }
  }
}

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

/** Lowest-priority source: a plain object of default values. */
export function fromDefaults(defaults: Record<string, unknown>): SourceEntry[] {
  const out: SourceEntry[] = [];
  flattenObject(defaults, [], () => ({ kind: 'defaults', name: 'defaults' }), out);
  return out;
}

/** JSON config file. Duplicate keys are rejected with line/column info. */
export function fromFile(path: string): SourceEntry[] {
  let text: string;
  try {
    text = readFileSync(path, 'utf8');
  } catch (err) {
    throw new ConfigError('PARSE_ERROR', `Cannot read config file: ${(err as Error).message}`, {
      source: { kind: 'file', name: path },
    });
  }
  const { value, locations } = parseJson(text, path);
  if (!isPlainObject(value)) {
    throw new ConfigError('TYPE_MISMATCH', 'Config file must contain a JSON object at the top level', {
      source: { kind: 'file', name: path },
    });
  }
  const out: SourceEntry[] = [];
  flattenObject(value, [], (p) => {
    const loc = locations.get(p.join('.'));
    return { kind: 'file', name: path, location: loc };
  }, out);
  return out;
}

export interface EnvOptions {
  /** Only variables with this prefix are considered; the prefix is stripped. */
  prefix?: string;
  /** Separator between path segments inside a variable name. Default '__'. */
  separator?: string;
}

/**
 * Environment variables. `APP_DB__HOST` with prefix `APP_` maps to `db.host`.
 * Values stay as strings; schema conversion happens during merge.
 */
export function fromEnv(env: Record<string, string | undefined>, options: EnvOptions = {}): SourceEntry[] {
  const { prefix = '', separator = '__' } = options;
  const out: SourceEntry[] = [];
  for (const [name, value] of Object.entries(env)) {
    if (value === undefined) continue;
    if (prefix && !name.startsWith(prefix)) continue;
    const stripped = prefix ? name.slice(prefix.length) : name;
    if (!stripped) continue;
    const path = stripped
      .split(separator)
      .filter(Boolean)
      .map((segment) => segment.toLowerCase());
    if (path.length === 0) continue;
    out.push({ path, value, op: 'set', source: { kind: 'env', name } });
  }
  return out;
}

/**
 * Command-line arguments. Supported forms:
 *   --port 8080        --port=8080
 *   --db.host=x        (dotted nesting)
 *   --tags+=a,b        (append)   --tags-=a   (remove)   --tags!=a,b (replace)
 *   --no-debug         (boolean false)
 * List values are comma-separated.
 */
export function fromArgv(argv: string[]): SourceEntry[] {
  const out: SourceEntry[] = [];
  for (let i = 0; i < argv.length; i++) {
    const token = argv[i];
    const source: SourceRef = { kind: 'argv', name: token };
    if (!token.startsWith('--')) {
      throw new ConfigError('PARSE_ERROR', `Unexpected argument ${JSON.stringify(token)} (expected --key[=value])`, { source });
    }
    let body = token.slice(2);
    if (body.startsWith('no-') && !body.includes('=')) {
      out.push({ path: body.slice(3).split('.'), value: 'false', op: 'set', source });
      continue;
    }
    let value: string;
    const eq = body.indexOf('=');
    if (eq >= 0) {
      value = body.slice(eq + 1);
      body = body.slice(0, eq);
    } else if (i + 1 < argv.length && !argv[i + 1].startsWith('--')) {
      value = argv[++i];
    } else {
      value = 'true';
    }
    // A trailing op suffix applies to the last path segment only.
    const segments = body.split('.').filter(Boolean);
    if (segments.length === 0) {
      throw new ConfigError('PARSE_ERROR', `Missing key in argument ${JSON.stringify(token)}`, { source });
    }
    const { key, op } = splitOpKey(segments[segments.length - 1]);
    segments[segments.length - 1] = key;
    const parsed: unknown = op === 'set' ? value : value.split(',');
    out.push({ path: segments, value: parsed, op, source });
  }
  return out;
}
