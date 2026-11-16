export { parseNpmrc } from './src/parser.js';
export { validateNpmrc } from './src/validate.js';
export { mergeNpmrc } from './src/merge.js';
export {
  FORBIDDEN_EXECUTABLE_KEYS,
  buildRegisteredHosts,
  expandValue,
  extractEnvVars,
  hostFromUrl,
  isForbiddenExecutableKey,
  isRegistryKey,
  parseAuthKey,
} from './src/util.js';
export type {
  MergeOptions,
  MergeResult,
  NpmrcEntry,
  NpmrcIssue,
  IssueCode,
  ValidateOptions,
  ValidateResult,
} from './src/types.js';
