// Versioned schema registry with converter caching and hot-swap support.
//
// - Schemas are registered under their explicit `version`; converters are
//   cached per (fromVersion, toVersion) pair and bind immutable schema
//   snapshots.
// - Re-registering a version swaps the schema and invalidates affected
//   cache entries, but conversions that already started keep executing
//   against the version captured at their start (in-flight safety).

import { convertMessage } from './convert.ts';
import type { ConvertResult } from './convert.ts';
import type { Schema } from './types.ts';
import { parseSchema } from './validate.ts';
import type { SchemaIssue } from './validate.ts';

export class SchemaError extends Error {
  issues: SchemaIssue[];
  constructor(issues: SchemaIssue[]) {
    super(issues.map((i) => `[${i.level}] ${i.path}: ${i.message}`).join('; '));
    this.issues = issues;
  }
}

export interface Converter {
  fromVersion: number;
  toVersion: number;
  convert(msg: unknown): ConvertResult;
}

// A conversion in progress: bound to the converter captured at begin time.
export interface Conversion {
  converter: Converter;
  convert(msg: unknown): ConvertResult;
}

export class SchemaRegistry {
  private schemas = new Map<number, Schema>();
  private converters = new Map<string, Converter>();

  register(raw: unknown): number {
    const { schema, issues } = parseSchema(raw);
    if (!schema) throw new SchemaError(issues.filter((i) => i.level !== 'warning'));
    const version = schema.version;
    this.schemas.set(version, schema);
    // Invalidate cache entries involving the swapped version.
    for (const key of [...this.converters.keys()]) {
      const [a, b] = key.split('->').map(Number);
      if (a === version || b === version) this.converters.delete(key);
    }
    return version;
  }

  getSchema(version: number): Schema | undefined {
    return this.schemas.get(version);
  }

  getConverter(fromVersion: number, toVersion: number): Converter {
    const key = `${fromVersion}->${toVersion}`;
    let converter = this.converters.get(key);
    if (!converter) {
      const from = this.schemas.get(fromVersion);
      const to = this.schemas.get(toVersion);
      if (!from) throw new Error(`unknown schema version ${fromVersion}`);
      if (!to) throw new Error(`unknown schema version ${toVersion}`);
      // Bind immutable snapshots of both schemas.
      converter = {
        fromVersion,
        toVersion,
        convert: (msg: unknown) => convertMessage(msg, from, to),
      };
      this.converters.set(key, converter);
    }
    return converter;
  }

  // Start a conversion now; the returned handle keeps using the schema
  // versions in effect at this moment, even if the registry is updated
  // (hot-swapped) before `convert` is called.
  beginConversion(fromVersion: number, toVersion: number): Conversion {
    const converter = this.getConverter(fromVersion, toVersion);
    return { converter, convert: (msg) => converter.convert(msg) };
  }
}
