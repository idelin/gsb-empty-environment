import * as assert from 'assert';
import { Affinity, Edit, applyEdits, sortEdits, transformPositionThroughEdits, transformSelection } from '../src/index';
import { MarkerModel } from './model';
import { test } from './harness';

/** Deterministic PRNG (mulberry32). */
function rng(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const ALPHABET = 'abcde';

function randomText(rand: () => number, maxLen: number): string {
  const len = Math.floor(rand() * (maxLen + 1));
  let out = '';
  for (let i = 0; i < len; i++) out += ALPHABET[Math.floor(rand() * ALPHABET.length)];
  return out;
}

/**
 * Generate a random non-overlapping batch of edits in original coordinates.
 * Produces adjacent deletions, stacked same-point insertions and insertions
 * adjacent to deletions.
 */
function randomBatch(rand: () => number, len: number): Edit[] {
  const edits: Edit[] = [];
  let pos = 0;
  while (pos <= len) {
    const kind = rand();
    if (kind < 0.35) {
      // One or more insertions at the same point.
      const count = 1 + Math.floor(rand() * 2);
      for (let i = 0; i < count; i++) {
        edits.push({ start: pos, end: pos, text: randomText(rand, 3) });
      }
      if (rand() < 0.6) pos += 1;
      else if (pos >= len) break;
    } else if (pos < len) {
      const end = pos + 1 + Math.floor(rand() * Math.min(3, len - pos));
      const text = kind < 0.7 ? '' : randomText(rand, 2);
      edits.push({ start: pos, end, text });
      pos = end; // next edit may start exactly here: adjacent ranges
    } else {
      break;
    }
    if (rand() < 0.25) pos += 1; // sometimes leave a gap
  }
  return edits;
}

const ROUNDS = 500;

test('fuzz: batch edits vs per-character marker model', () => {
  for (let round = 0; round < ROUNDS; round++) {
    const rand = rng(round * 2654435761 + 7);
    const text = randomText(rand, 12);
    const edits = randomBatch(rand, text.length);

    // Random anchors on boundaries, with random affinities.
    const anchorCount = 1 + Math.floor(rand() * 4);
    const anchors: Array<{ pos: number; affinity: Affinity }> = [];
    for (let i = 0; i < anchorCount; i++) {
      anchors.push({
        pos: Math.floor(rand() * (text.length + 1)),
        affinity: rand() < 0.5 ? 'left' : 'right',
      });
    }

    // Library results.
    const newText = applyEdits(text, edits);
    const libPositions = anchors.map((a) => transformPositionThroughEdits(a.pos, edits, a.affinity));

    // Marker-model results: apply the same edits right-to-left, exactly like
    // the library does, but locate anchors via marker identities.
    const model = MarkerModel.from(text);
    const modelAnchors = anchors.map((a) => ({ anchor: model.capture(a.pos), affinity: a.affinity }));
    const descending = sortEdits(edits).reverse();
    for (const edit of descending) {
      model.applyEdit(edit, modelAnchors);
    }

    assert.strictEqual(
      model.text(),
      newText,
      `text mismatch in round ${round}: ${JSON.stringify({ text, edits })}`,
    );
    for (let i = 0; i < anchors.length; i++) {
      assert.strictEqual(
        modelAnchors[i].anchor.gap,
        libPositions[i],
        `anchor ${i} mismatch in round ${round}: ${JSON.stringify({ text, edits, anchor: anchors[i] })}`,
      );
    }
  }
});

test('fuzz: selections (incl. empty) vs marker model', () => {
  for (let round = 0; round < ROUNDS; round++) {
    const rand = rng(round * 1597334677 + 13);
    const text = randomText(rand, 10);
    const edits = randomBatch(rand, text.length);

    let start = Math.floor(rand() * (text.length + 1));
    let end = Math.floor(rand() * (text.length + 1));
    if (rand() < 0.3) end = start; // empty selection
    if (start > end) [start, end] = [end, start];

    const sel = transformSelection({ start, end }, edits);

    const model = MarkerModel.from(text);
    const descending = sortEdits(edits).reverse();
    if (start === end) {
      const ma = [{ anchor: model.capture(start), affinity: 'right' as Affinity }];
      for (const edit of descending) model.applyEdit(edit, ma);
      assert.strictEqual(sel.start, ma[0].anchor.gap, `empty sel start, round ${round}`);
      assert.strictEqual(sel.end, ma[0].anchor.gap, `empty sel end, round ${round}`);
    } else {
      const ma = [
        { anchor: model.capture(start), affinity: 'right' as Affinity },
        { anchor: model.capture(end), affinity: 'left' as Affinity },
      ];
      for (const edit of descending) model.applyEdit(edit, ma);
      assert.strictEqual(sel.start, ma[0].anchor.gap, `sel start, round ${round}`);
      assert.strictEqual(sel.end, ma[1].anchor.gap, `sel end, round ${round}`);
    }
  }
});
