import type { DiffResult, Equality, Token } from './types.ts';

export interface Verification {
  valid: boolean;
  errors: string[];
}

/**
 * Independently checks that a DiffResult is a well-formed explanation of the
 * two input sequences. This does not re-derive the optimal plan; it validates
 * structural soundness: stable labels, a consistent matching, an increasing
 * kept backbone, and non-overlapping move blocks.
 */
export function verifyDiff(
  oldTokens: Token[],
  newTokens: Token[],
  result: DiffResult,
  equals: Equality = (a, b) => a === b,
): Verification {
  const errors: string[] = [];
  const fail = (msg: string): void => {
    errors.push(msg);
  };

  const { oldTokens: oldOut, newTokens: newOut, blocks, stats } = result;

  // 1. Shapes and texts line up with the inputs.
  if (oldOut.length !== oldTokens.length) fail(`oldTokens length ${oldOut.length} != input ${oldTokens.length}`);
  if (newOut.length !== newTokens.length) fail(`newTokens length ${newOut.length} != input ${newTokens.length}`);
  oldOut.forEach((tok, i) => {
    if (i < oldTokens.length && tok.text !== oldTokens[i]) fail(`old token ${i} text mismatch`);
    if (tok.index !== i) fail(`old token ${i} has wrong index ${tok.index}`);
    if (tok.label !== `O${i}`) fail(`old token ${i} has unstable label ${tok.label}`);
  });
  newOut.forEach((tok, j) => {
    if (j < newTokens.length && tok.text !== newTokens[j]) fail(`new token ${j} text mismatch`);
    if (tok.index !== j) fail(`new token ${j} has wrong index ${tok.index}`);
  });

  // 2. Labels: old labels unique; new labels either reference an old token or a unique insertion id.
  const newLabelOwner = new Map<string, number>();
  const insertionIds = new Set<string>();
  newOut.forEach((tok, j) => {
    if (newLabelOwner.has(tok.label)) fail(`duplicate new label ${tok.label} at ${j}`);
    newLabelOwner.set(tok.label, j);
    if (tok.status === 'inserted') {
      if (!/^I\d+$/.test(tok.label)) fail(`inserted token ${j} lacks an insertion label`);
      if (insertionIds.has(tok.label)) fail(`duplicate insertion label ${tok.label}`);
      insertionIds.add(tok.label);
    } else if (!/^O\d+$/.test(tok.label)) {
      fail(`matched new token ${j} lacks an origin label`);
    }
  });

  // 3. Matching consistency: deleted iff absent from new; inserted iff not an old label;
  //    matched pairs carry equal text and agree on status/blockId.
  let kept = 0;
  let moved = 0;
  let deleted = 0;
  let inserted = 0;
  oldOut.forEach((tok, i) => {
    const j = newLabelOwner.get(tok.label);
    if (tok.status === 'deleted') {
      deleted++;
      if (j !== undefined) fail(`deleted old token ${i} appears in new sequence`);
      if (tok.blockId !== null) fail(`deleted old token ${i} has a block id`);
      return;
    }
    if (j === undefined) {
      fail(`${tok.status} old token ${i} is missing from the new sequence`);
      return;
    }
    const other = newOut[j];
    if (!equals(tok.text, other.text)) fail(`matched pair O${i} has unequal text`);
    if (other.status !== tok.status) fail(`status mismatch for O${i}: ${tok.status} vs ${other.status}`);
    if (other.blockId !== tok.blockId) fail(`block id mismatch for O${i}`);
    if (tok.status === 'kept') {
      kept++;
      if (tok.blockId !== null) fail(`kept old token ${i} has a block id`);
    } else {
      moved++;
      if (tok.blockId === null) fail(`moved old token ${i} has no block id`);
    }
  });
  newOut.forEach((tok) => {
    if (tok.status === 'inserted') {
      inserted++;
      if (tok.blockId !== null) fail('inserted token has a block id');
    }
  });

  // 4. The kept backbone must be strictly increasing in source order.
  const keptSrc: number[] = [];
  newOut.forEach((tok) => {
    if (tok.status === 'kept') keptSrc.push(Number(tok.label.slice(1)));
  });
  for (let k = 1; k < keptSrc.length; k++) {
    if (keptSrc[k] <= keptSrc[k - 1]) fail('kept backbone is not increasing in source order');
  }

  // 5. Move blocks: membership, contiguity, and non-overlap.
  const blockMembers = new Map<number, number[]>(); // blockId -> matched new positions
  const matchedNew: Array<{ newIndex: number; oldIndex: number; blockId: number | null }> = [];
  newOut.forEach((tok, j) => {
    if (tok.status === 'inserted') return;
    const oldIndex = Number(tok.label.slice(1));
    matchedNew.push({ newIndex: j, oldIndex, blockId: tok.blockId });
    if (tok.blockId !== null) {
      const list = blockMembers.get(tok.blockId) ?? [];
      list.push(matchedNew.length - 1);
      blockMembers.set(tok.blockId, list);
    }
  });

  const blockById = new Map<number, (typeof blocks)[number]>();
  blocks.forEach((block) => {
    if (blockById.has(block.id)) fail(`duplicate block id ${block.id}`);
    blockById.set(block.id, block);
  });
  for (const [blockId, memberPositions] of blockMembers) {
    if (!blockById.has(blockId)) fail(`tokens reference unknown block ${blockId}`);
  }

  const oldSpanOwner = new Map<number, number>(); // old index -> block id covering it
  for (const block of blocks) {
    const memberPositions = blockMembers.get(block.id);
    if (!memberPositions || memberPositions.length === 0) {
      fail(`block ${block.id} has no member tokens`);
      continue;
    }
    // Members must be consecutive among matched tokens in new order.
    for (let t = 1; t < memberPositions.length; t++) {
      if (memberPositions[t] !== memberPositions[t - 1] + 1) {
        fail(`block ${block.id} members are not contiguous in the new sequence`);
      }
    }
    const members = memberPositions.map((p) => matchedNew[p]);
    const oldIndices = members.map((m) => m.oldIndex);
    if (members.some((m) => m.blockId !== block.id)) fail(`block ${block.id} membership mismatch`);
    // Source indices must increase along the block.
    for (let t = 1; t < oldIndices.length; t++) {
      if (oldIndices[t] <= oldIndices[t - 1]) fail(`block ${block.id} source indices not increasing`);
    }
    const oldStart = Math.min(...oldIndices);
    const oldEnd = Math.max(...oldIndices);
    const newStart = matchedNew[memberPositions[0]].newIndex;
    const newEnd = matchedNew[memberPositions[memberPositions.length - 1]].newIndex;
    if (block.oldStart !== oldStart || block.oldEnd !== oldEnd) fail(`block ${block.id} old span mismatch`);
    if (block.newStart !== newStart || block.newEnd !== newEnd) fail(`block ${block.id} new span mismatch`);
    if (block.size !== members.length) fail(`block ${block.id} size mismatch`);
    // Every old index inside the span is either a member or a deleted token.
    const memberSet = new Set(oldIndices);
    for (let o = oldStart; o <= oldEnd; o++) {
      if (oldSpanOwner.has(o)) fail(`block ${block.id} source span overlaps block ${oldSpanOwner.get(o)}`);
      oldSpanOwner.set(o, block.id);
      if (memberSet.has(o)) continue;
      const oldTok = oldOut[o];
      if (!oldTok || oldTok.status !== 'deleted') {
        fail(`block ${block.id} source span covers non-deleted, non-member old token ${o}`);
      }
    }
  }

  // 6. Statistics agree with the labeling.
  const recomputed = {
    kept,
    moved,
    deleted,
    inserted,
    blockCount: blocks.length,
  };
  if (stats.kept !== recomputed.kept) fail('stats.kept mismatch');
  if (stats.moved !== recomputed.moved) fail('stats.moved mismatch');
  if (stats.deleted !== recomputed.deleted) fail('stats.deleted mismatch');
  if (stats.inserted !== recomputed.inserted) fail('stats.inserted mismatch');
  if (stats.blockCount !== recomputed.blockCount) fail('stats.blockCount mismatch');
  const movedSourceSum = oldOut
    .filter((t) => t.status === 'moved')
    .reduce((sum, t) => sum + t.index, 0);
  if (stats.movedSourceSum !== movedSourceSum) fail('stats.movedSourceSum mismatch');

  return { valid: errors.length === 0, errors };
}
