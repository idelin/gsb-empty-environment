import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  Document,
  sortEditsForApplication,
  type Affinity,
  type Edit,
  type SelectionTransformOptions,
} from '../src/document.ts';
import { MarkerDocument } from '../src/marker-model.ts';

/** Deterministic PRNG so failures reproduce. */
function mulberry32(seed: number): () => number {
  let state = seed >>> 0;
  return () => {
    state = (state + 0x6d2b79f5) >>> 0;
    let t = state;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function randomInt(rand: () => number, lo: number, hi: number): number {
  return lo + Math.floor(rand() * (hi - lo + 1));
}

const ALPHABET = 'abc';

function randomText(rand: () => number, maxLen: number): string {
  const len = randomInt(rand, 0, maxLen);
  let out = '';
  for (let i = 0; i < len; i++) out += ALPHABET[randomInt(rand, 0, ALPHABET.length - 1)];
  return out;
}

/** Generate a non-overlapping batch in original coordinates. */
function randomBatch(rand: () => number, docLength: number): Edit[] {
  const count = randomInt(rand, 1, 4);
  const ranges: Array<{ from: number; to: number }> = [];
  for (let attempt = 0; attempt < count * 8 && ranges.length < count; attempt++) {
    const from = randomInt(rand, 0, docLength);
    const to = rand() < 0.4 ? from : randomInt(rand, from, docLength);
    if (ranges.every((r) => to <= r.from || from >= r.to)) {
      ranges.push({ from, to });
    }
  }
  return ranges.map(({ from, to }) => {
    const inserted = randomText(rand, 3);
    if (from === to) return { kind: 'insert', at: from, text: inserted };
    if (inserted.length === 0 || rand() < 0.5) return { kind: 'delete', from, to };
    return { kind: 'replace', from, to, text: inserted };
  });
}

function applyBatchToMarker(
  marker: MarkerDocument,
  edits: readonly Edit[],
  docLength: number,
  options: SelectionTransformOptions,
): void {
  for (const edit of sortEditsForApplication(edits, docLength)) {
    marker.applyEdit(edit, options);
  }
}

function assertModelsAgree(
  doc: Document,
  marker: MarkerDocument,
  anchorIds: readonly string[],
  context: string,
): void {
  assert.equal(doc.text, marker.text, `${context}: text diverged`);
  for (const id of anchorIds) {
    const anchor = doc.getAnchor(id);
    const markerAnchor = marker.anchors.find((a) => a.id === id);
    assert.ok(anchor && markerAnchor, `${context}: anchor ${id} missing`);
    assert.equal(anchor.offset, markerAnchor.gap, `${context}: anchor ${id} diverged`);
  }
  assert.deepEqual(doc.selection, marker.selection, `${context}: selection diverged`);
}

function makePair(
  rand: () => number,
): { doc: Document; marker: MarkerDocument; anchorIds: string[]; options: SelectionTransformOptions } {
  const text = randomText(rand, 24);
  const doc = new Document(text);
  const marker = new MarkerDocument(text);
  const anchorIds: string[] = [];
  const anchorCount = randomInt(rand, 0, 6);
  for (let i = 0; i < anchorCount; i++) {
    const id = `a${i}`;
    const offset = randomInt(rand, 0, text.length);
    const affinity: Affinity = rand() < 0.5 ? 'left' : 'right';
    doc.addAnchor(id, offset, affinity);
    marker.addAnchor(id, offset, affinity);
    anchorIds.push(id);
  }
  const options: SelectionTransformOptions = {
    startAffinity: rand() < 0.5 ? 'left' : 'right',
    endAffinity: rand() < 0.5 ? 'left' : 'right',
  };
  if (rand() < 0.8) {
    const a = randomInt(rand, 0, text.length);
    const b = randomInt(rand, 0, text.length);
    doc.setSelection(a, b);
    marker.setSelection(a, b);
  }
  return { doc, marker, anchorIds, options };
}

test('differential: random batches keep offset model and marker model in sync', () => {
  const rand = mulberry32(0xC0FFEE);
  for (let round = 0; round < 500; round++) {
    const { doc, marker, anchorIds, options } = makePair(rand);
    const edits = randomBatch(rand, doc.length);
    const context = `round ${round} (text=${JSON.stringify(doc.text)}, edits=${JSON.stringify(edits)})`;
    const originalLength = doc.length;
    doc.applyBatch(edits, options);
    applyBatchToMarker(marker, edits, originalLength, options);
    assertModelsAgree(doc, marker, anchorIds, context);
  }
});

test('differential: sequential single edits keep both models in sync', () => {
  const rand = mulberry32(0xBADC0DE);
  for (let round = 0; round < 200; round++) {
    const { doc, marker, anchorIds, options } = makePair(rand);
    const steps = randomInt(rand, 1, 5);
    for (let step = 0; step < steps; step++) {
      const edits = randomBatch(rand, doc.length);
      const context = `round ${round} step ${step} (edits=${JSON.stringify(edits)})`;
      const originalLength = doc.length;
      doc.applyBatch(edits, options);
      applyBatchToMarker(marker, edits, originalLength, options);
      assertModelsAgree(doc, marker, anchorIds, context);
    }
  }
});

test('undo restores text, anchors and selection after random batches', () => {
  const rand = mulberry32(0xFEED);
  for (let round = 0; round < 200; round++) {
    const { doc, anchorIds, options } = makePair(rand);
    const initialText = doc.text;
    const initialAnchors = anchorIds.map((id) => [id, doc.getAnchor(id)!.offset] as const);
    const initialSelection = doc.selection;
    const batches = randomInt(rand, 1, 4);
    for (let i = 0; i < batches; i++) {
      doc.applyBatch(randomBatch(rand, doc.length), options);
    }
    for (let i = 0; i < batches; i++) {
      assert.equal(doc.undo(), true, `round ${round}: undo ${i}`);
    }
    assert.equal(doc.text, initialText, `round ${round}: text not restored`);
    for (const [id, offset] of initialAnchors) {
      assert.equal(doc.getAnchor(id)!.offset, offset, `round ${round}: anchor ${id} not restored`);
    }
    assert.deepEqual(doc.selection, initialSelection, `round ${round}: selection not restored`);
    for (let i = 0; i < batches; i++) {
      assert.equal(doc.redo(), true, `round ${round}: redo ${i}`);
    }
    for (let i = 0; i < batches; i++) {
      assert.equal(doc.undo(), true, `round ${round}: undo after redo ${i}`);
    }
    assert.equal(doc.text, initialText, `round ${round}: text not restored after redo`);
  }
});

test('targeted: adjacent deletions equal one big deletion', () => {
  for (const affinity of ['left', 'right'] as const) {
    const doc = new Document('abcdef');
    const marker = new MarkerDocument('abcdef');
    for (const [i, offset] of [0, 2, 3, 4, 5, 6].entries()) {
      doc.addAnchor(`a${i}`, offset, affinity);
      marker.addAnchor(`a${i}`, offset, affinity);
    }
    const batch: Edit[] = [
      { kind: 'delete', from: 2, to: 4 },
      { kind: 'delete', from: 4, to: 6 },
    ];
    doc.applyBatch(batch);
    applyBatchToMarker(marker, batch, 6, {});
    assert.equal(doc.text, 'ab');
    assertModelsAgree(doc, marker, ['a0', 'a1', 'a2', 'a3', 'a4', 'a5'], 'adjacent deletions');
    assert.deepEqual(
      doc.anchors().map((a) => a.offset),
      [0, 2, 2, 2, 2, 2],
    );
  }
});

test('targeted: same-point inserts land in array order and split affinities', () => {
  const doc = new Document('ac');
  const marker = new MarkerDocument('ac');
  doc.addAnchor('L', 1, 'left');
  doc.addAnchor('R', 1, 'right');
  marker.addAnchor('L', 1, 'left');
  marker.addAnchor('R', 1, 'right');
  const batch: Edit[] = [
    { kind: 'insert', at: 1, text: 'X' },
    { kind: 'insert', at: 1, text: 'YZ' },
  ];
  doc.applyBatch(batch);
  applyBatchToMarker(marker, batch, 2, {});
  assert.equal(doc.text, 'aXYZc');
  assert.equal(doc.getAnchor('L')!.offset, 1);
  assert.equal(doc.getAnchor('R')!.offset, 4);
  assertModelsAgree(doc, marker, ['L', 'R'], 'same-point inserts');
});

test('targeted: empty selection transforms through insert, delete and replace', () => {
  // Insert at the caret: default affinities grow it into a range.
  const doc = new Document('abcd');
  const marker = new MarkerDocument('abcd');
  doc.setSelection(2, 2);
  marker.setSelection(2, 2);
  const insert: Edit = { kind: 'insert', at: 2, text: 'XY' };
  doc.applyEdit(insert);
  marker.applyEdit(insert);
  assert.deepEqual(doc.selection, { start: 2, end: 4 });
  assertModelsAgree(doc, marker, [], 'empty selection vs insert');

  // Caret-style affinities keep it empty.
  const caretDoc = new Document('abcd');
  const caretMarker = new MarkerDocument('abcd');
  caretDoc.setSelection(2, 2);
  caretMarker.setSelection(2, 2);
  const caretOptions: SelectionTransformOptions = { startAffinity: 'right', endAffinity: 'right' };
  caretDoc.applyEdit(insert, caretOptions);
  caretMarker.applyEdit(insert, caretOptions);
  assert.deepEqual(caretDoc.selection, { start: 4, end: 4 });
  assertModelsAgree(caretDoc, caretMarker, [], 'caret vs insert');

  // Deletion covering the caret snaps it to the deletion start.
  const delDoc = new Document('abcdef');
  const delMarker = new MarkerDocument('abcdef');
  delDoc.setSelection(4, 4);
  delMarker.setSelection(4, 4);
  const deletion: Edit = { kind: 'delete', from: 2, to: 5 };
  delDoc.applyEdit(deletion);
  delMarker.applyEdit(deletion);
  assert.deepEqual(delDoc.selection, { start: 2, end: 2 });
  assertModelsAgree(delDoc, delMarker, [], 'caret vs covering delete');
});

test('targeted: insert at deletion boundary in the same batch', () => {
  const doc = new Document('abcdef');
  const marker = new MarkerDocument('abcdef');
  doc.addAnchor('L', 2, 'left');
  doc.addAnchor('R', 2, 'right');
  marker.addAnchor('L', 2, 'left');
  marker.addAnchor('R', 2, 'right');
  const batch: Edit[] = [
    { kind: 'insert', at: 2, text: 'Q' },
    { kind: 'delete', from: 2, to: 4 },
  ];
  doc.applyBatch(batch);
  applyBatchToMarker(marker, batch, 6, {});
  assert.equal(doc.text, 'abQef');
  assertModelsAgree(doc, marker, ['L', 'R'], 'insert at deletion boundary');
});
