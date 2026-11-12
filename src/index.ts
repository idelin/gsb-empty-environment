export type {
  AnchorInfo,
  AnchorProof,
  AnchorVerificationContext,
  AnchorVerifierInput,
  AsyncAnchorVerifier,
  AnchorVerifier,
  BranchInfo,
  CidLike,
  Commit,
  ConflictReason,
  ConflictResult,
  ResolveOptions,
  ResolveOptionsInput,
  StreamState,
  StreamStateInput,
  TimestampValue,
} from './types.ts';

export {
  ConflictResolver,
  chooseStreamState,
  conflictResolution,
  getWinningState,
  pickTip,
  resolveConflict,
  resolveConflictAsync,
  resolveConflictDetailed,
  resolveConflictState,
  resolveStreamState,
  selectBranch,
  selectTip,
} from './conflict-resolution.ts';

export { chooseStreamState as resolve } from './conflict-resolution.ts';
