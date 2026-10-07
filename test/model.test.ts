import assert from 'node:assert/strict';
import { test } from 'node:test';
import {
  BatchValidationError,
  DocumentStore,
  UndoConflictError,
} from '../src/index.ts';
import type {
  BatchReceipt,
  DocumentEdit,
  DocumentSnapshot,
  Edit,
} from '../src/index.ts';

/**
 * Snapshot-model differential test: an independent reference model tracks
 * full (text, version) snapshots of every document. Random batches and
 * undos are applied to both the store and the model, and the complete
 * snapshot of the store is compared against the model after every step.
 * On any expected failure the store's snapshot must be byte-identical to
 * the pre-operation snapshot (atomicity).
 */

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

interface ModelDoc {
  text: string;
  version: number;
}

/** Reference implementation: applies edits ascending with a cursor. */
function modelApplyEdits(text: string, edits: Edit[]): string {
  const ordered = [...edits].sort((a, b) => a.range.start - b.range.start);
  let out = '';
  let cursor = 0;
  for (const edit of ordered) {
    out += text.slice(cursor, edit.range.start) + edit.newText;
    cursor = edit.range.end;
  }
  return out + text.slice(cursor);
}

/** Reference validation: returns true when the batch must be rejected. */
function modelBatchInvalid(docs: Map<string, ModelDoc>, batch: DocumentEdit[]): boolean {
  const seen = new Set<string>();
  for (const documentEdit of batch) {
    const state = docs.get(documentEdit.docId);
    if (seen.has(documentEdit.docId) || state === undefined) return true;
    seen.add(documentEdit.docId);
    if (documentEdit.expectedVersion !== state.version) return true;
    const ordered = [...documentEdit.edits].sort((a, b) => a.range.start - b.range.start);
    for (let i = 0; i < ordered.length; i += 1) {
      const { start, end } = ordered[i].range;
      if (!Number.isInteger(start) || !Number.isInteger(end)) return true;
      if (start < 0 || end < start || end > state.text.length) return true;
      if (i > 0 && (start < ordered[i - 1].range.end || start === ordered[i - 1].range.start)) {
        return true;
      }
    }
  }
  return false;
}

function modelSnapshots(docs: Map<string, ModelDoc>): DocumentSnapshot[] {
  return [...docs.keys()].sort().map((docId) => {
    const state = docs.get(docId);
    assert(state !== undefined);
    return { docId, version: state.version, text: state.text };
  });
}

function randomText(rand: () => number, maxLength: number): string {
  const alphabet = 'abc ';
  const length = Math.floor(rand() * (maxLength + 1));
  let out = '';
  for (let i = 0; i < length; i += 1) {
    out += alphabet[Math.floor(rand() * alphabet.length)];
  }
  return out;
}

function randomEdit(rand: () => number, text: string): Edit {
  const start = Math.floor(rand() * (text.length + 1));
  const end = start + Math.floor(rand() * (text.length - start + 1));
  return { range: { start, end }, newText: randomText(rand, 4) };
}

function randomBatch(rand: () => number, docIds: string[], docs: Map<string, ModelDoc>): DocumentEdit[] {
  const shuffled = [...docIds].sort(() => rand() - 0.5);
  const count = 1 + Math.floor(rand() * docIds.length);
  const batch: DocumentEdit[] = [];

  for (const docId of shuffled.slice(0, count)) {
    const state = docs.get(docId);
    assert(state !== undefined);
    const edits: Edit[] = [];
    const editCount = Math.floor(rand() * 4);
    for (let i = 0; i < editCount; i += 1) {
      edits.push(randomEdit(rand, state.text));
    }

    // Occasionally corrupt the entry to exercise rejection paths.
    const fault = rand();
    if (fault < 0.12) {
      edits.push({ range: { start: 0, end: state.text.length + 2 }, newText: 'x' });
    } else if (fault < 0.24 && edits.length > 0) {
      const first = edits[0];
      edits.push({ range: { ...first.range }, newText: 'overlap' });
    } else if (fault < 0.30) {
      edits.push({ range: { start: 2, end: 1 }, newText: 'bad' });
    }

    const expectedVersion =
      rand() < 0.15 ? state.version + 1 + Math.floor(rand() * 3) : state.version;
    batch.push({ docId, expectedVersion, edits });
  }
  return batch;
}

for (const seed of [1, 7, 42, 2024]) {
  test(`store matches snapshot model over random batches and undos (seed ${seed})`, () => {
    const rand = mulberry32(seed);
    const store = new DocumentStore();
    const docs = new Map<string, ModelDoc>();
    const docIds = ['alpha', 'beta', 'gamma'];
    for (const docId of docIds) {
      const text = randomText(rand, 12);
      store.createDocument(docId, text);
      docs.set(docId, { text, version: 0 });
    }

    const receipts: BatchReceipt[] = [];

    for (let step = 0; step < 600; step += 1) {
      const before = modelSnapshots(docs);
      assert.deepEqual(store.listSnapshots(), before, `state diverged before step ${step}`);

      if (receipts.length > 0 && rand() < 0.4) {
        // Undo a random receipt.
        const receipt = receipts[Math.floor(rand() * receipts.length)];
        if (receipt.undone) {
          assert.throws(() => store.undo(receipt), /already been undone/);
        } else {
          const conflict = receipt.entries.some((entry) => {
            const state = docs.get(entry.docId);
            return state === undefined || state.version !== entry.afterVersion;
          });
          if (conflict) {
            assert.throws(() => store.undo(receipt), UndoConflictError);
          } else {
            store.undo(receipt);
            for (const entry of receipt.entries) {
              docs.set(entry.docId, { text: entry.beforeText, version: entry.beforeVersion });
            }
          }
        }
      } else {
        // Apply a random batch.
        const batch = randomBatch(rand, docIds, docs);
        if (modelBatchInvalid(docs, batch)) {
          assert.throws(() => store.applyBatch(batch), BatchValidationError);
        } else {
          const receipt = store.applyBatch(batch);
          receipts.push(receipt);
          for (const documentEdit of batch) {
            const state = docs.get(documentEdit.docId);
            assert(state !== undefined);
            docs.set(documentEdit.docId, {
              text: modelApplyEdits(state.text, documentEdit.edits),
              version: state.version + 1,
            });
          }
          // The receipt must faithfully record what the model computed.
          for (const entry of receipt.entries) {
            const state = docs.get(entry.docId);
            assert(state !== undefined);
            assert.equal(entry.afterText, state.text);
            assert.equal(entry.afterVersion, state.version);
          }
        }
      }

      // Atomicity: any failure above left the store identical to the model,
      // which only changes on the success paths.
      assert.deepEqual(store.listSnapshots(), modelSnapshots(docs), `state diverged after step ${step}`);
    }
  });
}
