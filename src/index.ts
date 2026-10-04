/** Public API for the config-merge library. */
import type { ConfigSchema, MergeIssue } from './types.ts';
import { fromCli, fromDefaults, fromEnv, fromFile, type CliOptions, type EnvOptions } from './sources.ts';
import { mergeLayers, type Layer, type MergeResult } from './merge.ts';
import { redact } from './secrets.ts';
import { validate } from './validate.ts';

export type { SourceKind, SourceRef, MergeIssue, IssueCode, FieldSchema, FieldType, ConfigSchema } from './types.ts';
export { formatSource } from './types.ts';
export { parseJson, type ParsedJson, type JsonLocation } from './json.ts';
export { fromDefaults, fromFile, fromJsonText, fromEnv, fromCli, coerce, type EnvOptions, type CliOptions } from './sources.ts';
export { mergeLayers, type Layer, type MergeResult } from './merge.ts';
export {
  type ProvNode,
  type ProvEntry,
  type ProvenanceInfo,
  provenanceAt,
  nodeAt,
  deepEqual,
} from './provenance.ts';
export { SECRET_MASK, isSecretPath, redact, valueChanged, wasOverridden, explain } from './secrets.ts';
export { validate } from './validate.ts';

export interface LoadConfigOptions {
  defaults?: unknown;
  /** JSON file paths, applied in the given order. */
  files?: string[];
  env?: EnvOptions | false;
  cli?: { argv: string[] } & CliOptions;
  schema?: ConfigSchema;
}

export interface LoadedConfig {
  /** Final merged config with raw (unmasked) values. */
  value: unknown;
  /** Display copy with secret fields masked. */
  display: unknown;
  /** Provenance tree: source and overridden chain for every element. */
  provenance: MergeResult['provenance'];
  /** All issues: duplicate keys, null values, conversion failures, type mismatches. */
  issues: MergeIssue[];
}

/** One-shot helper: build layers in precedence order, merge, redact, validate. */
export function loadConfig(options: LoadConfigOptions): LoadedConfig {
  const schema = options.schema ?? {};
  const layers: Layer[] = [];
  if (options.defaults !== undefined) layers.push(fromDefaults(options.defaults));
  for (const file of options.files ?? []) layers.push(fromFile(file));
  if (options.env !== false && options.env !== undefined) layers.push(fromEnv(options.env));
  if (options.cli !== undefined) {
    const { argv, ...cliOptions } = options.cli;
    layers.push(fromCli(argv, cliOptions));
  }

  const merged = mergeLayers(layers);
  const issues = [...merged.issues, ...validate(merged, schema)];
  return {
    value: merged.value,
    display: redact(merged.value, schema),
    provenance: merged.provenance,
    issues,
  };
}
