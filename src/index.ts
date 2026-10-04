export { diffTokens, buildResult, computeBlocks } from './diff.ts';
export { matchTokens } from './match.ts';
export { planMoves, computeMergeable } from './moves.ts';
export { verifyDiff, type Verification } from './verify.ts';
export { diffTokensExhaustive } from './exhaustive.ts';
export type {
  DiffOptions,
  DiffResult,
  DiffStats,
  Equality,
  LabeledNewToken,
  LabeledOldToken,
  MoveBlock,
  Token,
} from './types.ts';
