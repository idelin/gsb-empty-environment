export {
  SourceRegistry,
  effectiveReaders,
  normalizeSources,
  sameSources,
  type CharCell,
  type ReaderId,
  type SourceId,
  type SourceInfo,
} from './model.ts';
export {
  DEFAULT_REDACTION_CHAR,
  queryCells,
  type QueryResult,
  type Redaction,
} from './query.ts';
export { ReferenceDocument } from './reference.ts';
export { FragmentDocument } from './document.ts';
export {
  diffExpansions,
  diffQueries,
  verifyAgainstReference,
  type Expandable,
} from './verify.ts';
