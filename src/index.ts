export { Bias, InverseEdit, PositionMap, Range, TextEdit } from './types';
export { buildPositionMap } from './positionMap';
export { expandReplacement } from './replacement';
export { applyEdits, findMatches, invertEdits, replaceAll, MatchInfo, ReplaceResult } from './replace';
export { AppliedChange, TextDocument } from './document';
export {
  beginTransaction,
  Transaction,
  TransactionConflict,
  TransactionFailure,
  TransactionOp,
  TransactionResult,
  TransactionSuccess,
} from './transaction';
