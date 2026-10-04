// Versioned schema registry. Compiled schemas and their cached
// compatibility results are bound to an immutable version. Sessions pin
// the version current at begin() time: during a hot swap, in-flight
// messages keep converting with the version they started with.

import type { RecordType } from './types.ts';
import { parseSchema } from './schema.ts';
import { checkParsed } from './check.ts';
import type { CheckResult } from './check.ts';
import { convertMessage } from './convert.ts';
import type { ConvertResult } from './convert.ts';

export interface CompiledPair {
  version: number;
  oldSchema: RecordType;
  newSchema: RecordType;
  compat: CheckResult;
}

export class SchemaRegistry {
  private pairs: Map<number, CompiledPair> = new Map();
  private current: number | null = null;

  register(
    version: number,
    oldJson: unknown,
    newJson: unknown,
  ): { ok: true; compat: CheckResult } | { ok: false; reason: string } {
    if (!Number.isSafeInteger(version) || version < 0) {
      return { ok: false, reason: 'version must be a non-negative safe integer' };
    }
    if (this.pairs.has(version)) {
      return { ok: false, reason: `version ${version} is already registered; versions are immutable` };
    }
    const o = parseSchema(oldJson);
    if (!o.ok) return { ok: false, reason: `old schema: ${o.reason}` };
    const n = parseSchema(newJson);
    if (!n.ok) return { ok: false, reason: `new schema: ${n.reason}` };
    const compat = checkParsed(o.schema, n.schema);
    this.pairs.set(version, { version, oldSchema: o.schema, newSchema: n.schema, compat });
    if (this.current === null) this.current = version;
    return { ok: true, compat };
  }

  setCurrent(version: number): void {
    if (!this.pairs.has(version)) throw new Error(`unknown schema version ${version}`);
    this.current = version;
  }

  get currentVersion(): number | null {
    return this.current;
  }

  // Begin a session pinned to the current version. Later register() /
  // setCurrent() calls do not affect this session.
  begin(): Session {
    if (this.current === null) throw new Error('no schema version registered');
    return new Session(this.pairs.get(this.current)!);
  }
}

export class Session {
  private pair: CompiledPair;
  constructor(pair: CompiledPair) {
    this.pair = pair;
  }
  get version(): number {
    return this.pair.version;
  }
  get compat(): CheckResult {
    return this.pair.compat;
  }
  convert(message: unknown): ConvertResult {
    return convertMessage(this.pair.oldSchema, this.pair.newSchema, message);
  }
}
