export {
  DEFAULT_MAX_DEPTH,
  JsonParseError,
  ParsedTree,
  parseJsonTree,
  type JsonArray,
  type JsonRecord,
  type JsonScalar,
  type JsonValue,
  type ParseOptions,
} from './src/json-parser.ts';

export {
  DANGEROUS_KEYS,
  ConfigStore,
  MergeError,
  encodePointerSegment,
  isParsedTree,
  mergeConfigs,
  type MergeOptions,
  type MergeResult,
  type MergeSource,
} from './src/merge-config.ts';
