export { findAllMatches } from './match.js';
export { expandReplacement } from './replacement.js';
export { replaceAll, applyForward, applyInverse } from './replace.js';
export { DocumentStore, commitTransaction } from './transaction.js';
export type {
  Assoc,
  MatchRecord,
  Edit,
  ReplaceResult,
  Replacement,
  ReplacementFn,
  DocSnapshot,
  ReplaceOp,
  VersionConflict,
  AppliedDocEdit,
  TxnSuccess,
  TxnFailure,
  TxnResult,
} from './types.js';
