import { generateCandidates } from "./candidates.ts";
import { selectBlocks } from "./select.ts";
import type { Block, BlockKind, ClassifiedBlock, DiffOptions, DiffResult, TokenRange } from "./types.ts";

/**
 * Split selected blocks into `keep` (relative order preserved) and `move`.
 * The keep set is a maximum-weight (by token count) subsequence of blocks
 * whose new positions are strictly increasing; everything else moved.
 */
export function classifyKeepMove(blocks: Block[]): BlockKind[] {
  const k = blocks.length;
  const weight = new Array<number>(k).fill(0);
  const parent = new Array<number>(k).fill(-1);
  for (let i = 0; i < k; i++) {
    weight[i] = blocks[i].length;
    for (let j = 0; j < i; j++) {
      if (blocks[j].newStart < blocks[i].newStart) {
        const candidate = weight[j] + blocks[i].length;
        if (candidate > weight[i]) {
          weight[i] = candidate;
          parent[i] = j;
        }
      }
    }
  }
  let end = -1;
  for (let i = 0; i < k; i++) {
    if (end === -1 || weight[i] > weight[end]) end = i;
  }
  const kinds: BlockKind[] = new Array<BlockKind>(k).fill("move");
  for (let i = end; i !== -1; i = parent[i]) kinds[i] = "keep";
  return kinds;
}

function uncoveredRuns(
  tokens: string[],
  covered: boolean[],
): TokenRange[] {
  const ranges: TokenRange[] = [];
  let i = 0;
  while (i < tokens.length) {
    if (covered[i]) {
      i++;
      continue;
    }
    let end = i;
    while (end < tokens.length && !covered[end]) end++;
    ranges.push({ start: i, length: end - i, tokens: tokens.slice(i, end) });
    i = end;
  }
  return ranges;
}

/**
 * Diff two token sequences, distinguishing moved blocks from
 * deletions and (re)insertions.
 */
export function diffTokens(
  oldTokens: string[],
  newTokens: string[],
  options: DiffOptions = {},
): DiffResult {
  const candidates = generateCandidates(oldTokens, newTokens, {
    minLength: options.minBlockLength,
    maxCandidates: options.maxCandidates,
  });
  const selection = selectBlocks(candidates, {
    maxStates: options.maxStates,
    oldLength: oldTokens.length,
    newLength: newTokens.length,
  });

  const blocks = [...selection.blocks].sort(
    (a, b) => a.oldStart - b.oldStart || a.newStart - b.newStart,
  );
  const kinds = classifyKeepMove(blocks);

  const oldToNew: (number | null)[] = new Array(oldTokens.length).fill(null);
  const newToOld: (number | null)[] = new Array(newTokens.length).fill(null);
  const oldCovered = new Array<boolean>(oldTokens.length).fill(false);
  const newCovered = new Array<boolean>(newTokens.length).fill(false);
  for (const b of blocks) {
    for (let t = 0; t < b.length; t++) {
      oldToNew[b.oldStart + t] = b.newStart + t;
      newToOld[b.newStart + t] = b.oldStart + t;
      oldCovered[b.oldStart + t] = true;
      newCovered[b.newStart + t] = true;
    }
  }

  const classified: ClassifiedBlock[] = blocks.map((b, i) => ({
    ...b,
    kind: kinds[i],
    tokens: oldTokens.slice(b.oldStart, b.oldStart + b.length),
  }));

  return {
    oldLabels: oldTokens.map((_, i) => `o#${i}`),
    newLabels: newTokens.map((_, j) =>
      newToOld[j] === null ? `n#${j}` : `o#${newToOld[j]}`,
    ),
    oldToNew,
    newToOld,
    blocks: classified,
    deletions: uncoveredRuns(oldTokens, oldCovered),
    insertions: uncoveredRuns(newTokens, newCovered),
    coveredTokens: selection.covered,
    optimal: selection.optimal,
    statesExplored: selection.statesExplored,
  };
}
