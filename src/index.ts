import { mergeConfig, MergedConfig, type MergeOptions } from './merge.ts';
import type { SourceEntry } from './origin.ts';
import { fromArgv, fromDefaults, fromEnv, fromFile, type EnvOptions } from './sources.ts';
import type { Schema } from './schema.ts';

export { ConfigError, type ConfigErrorCode } from './errors.ts';
export { SecretValue, isSecret, maskValue, deepEquals } from './secret.ts';
export { parseJson } from './json.ts';
export { mergeConfig, MergedConfig } from './merge.ts';
export type { Explanation, LeafExplanation, ArrayExplanation, DiffEntry, HistoryEntry } from './merge.ts';
export { fromDefaults, fromFile, fromEnv, fromArgv } from './sources.ts';
export type { SourceEntry, SourceRef, SourceKind, ListOp } from './origin.ts';
export type { Schema, SchemaNode } from './schema.ts';

export interface LoadOptions {
  schema?: Schema;
  allowUnknown?: boolean;
  defaults?: Record<string, unknown>;
  /** Config file paths, applied in the given order. */
  files?: string[];
  /** Environment map (defaults to process.env). */
  env?: Record<string, string | undefined>;
  envOptions?: EnvOptions;
  /** CLI args (defaults to process.argv.slice(2)). */
  argv?: string[];
}

/**
 * Load and merge configuration from all sources in priority order:
 * defaults < files (in order) < env < argv.
 */
export function loadConfig(options: LoadOptions = {}): MergedConfig {
  const entries: SourceEntry[] = [];
  if (options.defaults) entries.push(...fromDefaults(options.defaults));
  for (const file of options.files ?? []) entries.push(...fromFile(file));
  // env/argv are opt-in: without a prefix, process.env would flood the schema.
  if (options.env !== undefined || options.envOptions !== undefined) {
    entries.push(...fromEnv(options.env ?? process.env, options.envOptions));
  }
  if (options.argv !== undefined) entries.push(...fromArgv(options.argv));
  const mergeOptions: MergeOptions = { schema: options.schema, allowUnknown: options.allowUnknown };
  return mergeConfig(entries, mergeOptions);
}
