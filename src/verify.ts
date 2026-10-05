import { classifyKeepMove } from "./diff.ts";
import { blocksCompatible } from "./select.ts";
import type { Block, DiffResult } from "./types.ts";

export interface Verification {
  ok: boolean;
  errors: string[];
}

/**
 * Independently check a diff result: blocks are exact matches and
 * non-overlapping, labels are consistent, deletions/insertions cover
 * exactly the uncovered tokens, and the new sequence is fully explained.
 * Note: `optimal` is a search guarantee and is not checked here.
 */
export function verifyDiff(
  oldTokens: string[],
  newTokens: string[],
  result: DiffResult,
): Verification {
  const errors: string[] = [];
  const blocks: Block[] = result.blocks;

  for (const b of blocks) {
    if (b.length < 1 || b.oldStart < 0 || b.newStart < 0) {
      errors.push(`block out of range: ${JSON.stringify(b)}`);
      continue;
    }
    for (let t = 0; t < b.length; t++) {
      if (oldTokens[b.oldStart + t] !== newTokens[b.newStart + t]) {
        errors.push(
          `block (${b.oldStart},${b.newStart},${b.length}) is not an exact match at offset ${t}`,
        );
        break;
      }
    }
  }
  for (let i = 0; i < blocks.length; i++) {
    for (let j = i + 1; j < blocks.length; j++) {
      if (!blocksCompatible(blocks[i], blocks[j])) {
        errors.push(`blocks ${i} and ${j} overlap`);
      }
    }
  }

  const expectedKinds = classifyKeepMove(blocks);
  for (let i = 0; i < blocks.length; i++) {
    if (blocks[i].kind !== expectedKinds[i]) {
      errors.push(`block ${i} kind ${blocks[i].kind} != expected ${expectedKinds[i]}`);
    }
  }

  const oldCovered = new Array<boolean>(oldTokens.length).fill(false);
  const newCovered = new Array<boolean>(newTokens.length).fill(false);
  for (const b of blocks) {
    for (let t = 0; t < b.length; t++) {
      oldCovered[b.oldStart + t] = true;
      newCovered[b.newStart + t] = true;
    }
  }

  for (let i = 0; i < oldTokens.length; i++) {
    if (result.oldLabels[i] !== `o#${i}`) {
      errors.push(`oldLabels[${i}] = ${result.oldLabels[i]}`);
    }
    const mapped = result.oldToNew[i];
    if (oldCovered[i]) {
      if (mapped === null || newTokens[mapped] !== oldTokens[i]) {
        errors.push(`oldToNew[${i}] inconsistent`);
      }
    } else if (mapped !== null) {
      errors.push(`oldToNew[${i}] set for deleted token`);
    }
  }
  for (let j = 0; j < newTokens.length; j++) {
    const source = result.newToOld[j];
    const expectedLabel = source === null ? `n#${j}` : `o#${source}`;
    if (result.newLabels[j] !== expectedLabel) {
      errors.push(`newLabels[${j}] = ${result.newLabels[j]}, want ${expectedLabel}`);
    }
    if (newCovered[j]) {
      if (source === null || oldTokens[source] !== newTokens[j]) {
        errors.push(`newToOld[${j}] inconsistent`);
      }
    } else if (source !== null) {
      errors.push(`newToOld[${j}] set for inserted token`);
    }
  }

  const expectRuns = (
    tokens: string[],
    covered: boolean[],
    actual: { start: number; length: number; tokens: string[] }[],
    name: string,
  ) => {
    const expected: { start: number; length: number }[] = [];
    let i = 0;
    while (i < tokens.length) {
      if (covered[i]) {
        i++;
        continue;
      }
      let end = i;
      while (end < tokens.length && !covered[end]) end++;
      expected.push({ start: i, length: end - i });
      i = end;
    }
    const actualSimple = actual.map((r) => ({ start: r.start, length: r.length }));
    if (JSON.stringify(actualSimple) !== JSON.stringify(expected)) {
      errors.push(`${name} mismatch: ${JSON.stringify(actualSimple)} != ${JSON.stringify(expected)}`);
    }
    for (const r of actual) {
      if (r.tokens.join("") !== tokens.slice(r.start, r.start + r.length).join("")) {
        errors.push(`${name} tokens mismatch at ${r.start}`);
      }
    }
  };
  expectRuns(oldTokens, oldCovered, result.deletions, "deletions");
  expectRuns(newTokens, newCovered, result.insertions, "insertions");

  return { ok: errors.length === 0, errors };
}
