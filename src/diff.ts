import { matchTokens, type MatchResult } from './match.ts';
import { computeMergeable, planMoves, type MovePlan } from './moves.ts';
import type {
  DiffOptions,
  DiffResult,
  LabeledNewToken,
  LabeledOldToken,
  MoveBlock,
  Token,
} from './types.ts';

export interface BlockLayout {
  /** blockOf[k] = move-block id of the k-th matched token, or -1 when kept. */
  blockOf: number[];
  blocks: MoveBlock[];
}

/**
 * Groups moved matched tokens into non-overlapping blocks, in new-position
 * order. Adjacent moved tokens merge exactly when `mergeable[k]` holds.
 */
export function computeBlocks(
  src: number[],
  newPos: number[],
  matchedOld: boolean[],
  kept: boolean[],
): BlockLayout {
  const mergeable = computeMergeable(src, matchedOld);
  const blockOf = new Array<number>(src.length).fill(-1);
  const blocks: MoveBlock[] = [];
  for (let k = 0; k < src.length; k++) {
    if (kept[k]) continue;
    if (k > 0 && !kept[k - 1] && mergeable[k]) {
      blockOf[k] = blockOf[k - 1];
      const block = blocks[blockOf[k]];
      block.oldEnd = src[k];
      block.newEnd = newPos[k];
      block.size++;
    } else {
      blockOf[k] = blocks.length;
      blocks.push({
        id: blocks.length,
        oldStart: src[k],
        oldEnd: src[k],
        newStart: newPos[k],
        newEnd: newPos[k],
        size: 1,
      });
    }
  }
  return { blockOf, blocks };
}

/** Assembles the labeled diff from a matching and a move plan. */
export function buildResult(
  oldTokens: Token[],
  newTokens: Token[],
  match: MatchResult,
  plan: MovePlan,
): DiffResult {
  const src: number[] = [];
  const newPos: number[] = [];
  for (let j = 0; j < newTokens.length; j++) {
    if (match.newToOld[j] >= 0) {
      src.push(match.newToOld[j]);
      newPos.push(j);
    }
  }
  const matchedOld = oldTokens.map((_, i) => match.oldToNew[i] >= 0);
  const { blockOf, blocks } = computeBlocks(src, newPos, matchedOld, plan.kept);

  const matchedOrderByOld = new Array<number>(oldTokens.length).fill(-1);
  src.forEach((oldIndex, k) => {
    matchedOrderByOld[oldIndex] = k;
  });

  const oldOut: LabeledOldToken[] = oldTokens.map((text, index) => {
    if (!matchedOld[index]) {
      return { text, index, label: `O${index}`, status: 'deleted', blockId: null };
    }
    const k = matchedOrderByOld[index];
    const moved = !plan.kept[k];
    return {
      text,
      index,
      label: `O${index}`,
      status: moved ? 'moved' : 'kept',
      blockId: moved ? blockOf[k] : null,
    };
  });

  let insertions = 0;
  const newOut: LabeledNewToken[] = newTokens.map((text, index) => {
    const oldIndex = match.newToOld[index];
    if (oldIndex < 0) {
      return { text, index, label: `I${insertions++}`, status: 'inserted', blockId: null };
    }
    const k = matchedOrderByOld[oldIndex];
    const moved = !plan.kept[k];
    return {
      text,
      index,
      label: `O${oldIndex}`,
      status: moved ? 'moved' : 'kept',
      blockId: moved ? blockOf[k] : null,
    };
  });

  return {
    oldTokens: oldOut,
    newTokens: newOut,
    blocks,
    stats: {
      kept: plan.keptCount,
      moved: plan.movedCount,
      deleted: oldTokens.length - plan.keptCount - plan.movedCount,
      inserted: insertions,
      blockCount: plan.blockCount,
      movedSourceSum: plan.movedSourceSum,
      optimal: plan.optimal,
      budgetExhausted: plan.budgetExhausted,
      nodesExplored: plan.nodesExplored,
    },
  };
}

/**
 * Diffs two token sequences, labeling every token with a stable origin and
 * classifying it as kept / moved / deleted (old side) or kept / moved /
 * inserted (new side). Moved tokens are grouped into non-overlapping blocks
 * optimizing: kept-token count, then block count, then source position.
 */
export function diffTokens(
  oldTokens: Token[],
  newTokens: Token[],
  options: DiffOptions = {},
): DiffResult {
  const equals = options.equals ?? ((a: Token, b: Token) => a === b);
  const budget = options.budget ?? Number.POSITIVE_INFINITY;
  const match = matchTokens(oldTokens, newTokens, equals);
  const src: number[] = [];
  for (let j = 0; j < newTokens.length; j++) {
    if (match.newToOld[j] >= 0) src.push(match.newToOld[j]);
  }
  const matchedOld = oldTokens.map((_, i) => match.oldToNew[i] >= 0);
  const plan = planMoves(src, matchedOld, budget);
  return buildResult(oldTokens, newTokens, match, plan);
}
