import assert from 'node:assert/strict';
import { test } from 'node:test';
import {
  BatchValidationError,
  Workspace,
  type BatchReceipt,
  type DocumentChange,
  type TextEdit,
  type WorkspaceChangeEvent,
  type WorkspaceSnapshot,
} from '../src/index.ts';

function setup(): Workspace {
  const ws = new Workspace();
  ws.openDocument('a.ts', 'const alpha = 1;\nconst beta = 2;\n');
  ws.openDocument('b.ts', 'export const gamma = 3;\n');
  return ws;
}

function initialSnapshot(): WorkspaceSnapshot {
  return {
    'a.ts': { id: 'a.ts', text: 'const alpha = 1;\nconst beta = 2;\n', version: 1 },
    'b.ts': { id: 'b.ts', text: 'export const gamma = 3;\n', version: 1 },
  };
}

test('commits a multi-document batch atomically and bumps versions', () => {
  const ws = setup();
  const receipt = ws.applyBatch([
    { documentId: 'a.ts', expectedVersion: 1, edits: [{ start: 6, end: 11, newText: 'ALPHA' }] },
    { documentId: 'b.ts', expectedVersion: 1, edits: [{ start: 0, end: 0, newText: '// hi\n' }] },
  ]);

  assert.deepEqual(ws.snapshot(), {
    'a.ts': { id: 'a.ts', text: 'const ALPHA = 1;\nconst beta = 2;\n', version: 2 },
    'b.ts': { id: 'b.ts', text: '// hi\nexport const gamma = 3;\n', version: 2 },
  });
  assert.deepEqual(
    receipt.entries.map((e) => [e.documentId, e.baseVersion, e.resultVersion]),
    [
      ['a.ts', 1, 2],
      ['b.ts', 1, 2],
    ],
  );
});

test('applies same-document edits in original coordinates regardless of order', () => {
  const ws = setup();
  // "const alpha = 1;" -> replace "alpha" and "1" using original offsets.
  ws.applyBatch([
    {
      documentId: 'a.ts',
      expectedVersion: 1,
      edits: [
        { start: 14, end: 15, newText: '42' },
        { start: 6, end: 11, newText: 'ALPHA' },
      ],
    },
  ]);

  assert.equal(ws.getDocument('a.ts')!.text, 'const ALPHA = 42;\nconst beta = 2;\n');
});

test('rejects the whole batch on version mismatch and modifies nothing', () => {
  const ws = setup();
  const before = ws.snapshot();

  assert.throws(
    () =>
      ws.applyBatch([
        { documentId: 'a.ts', expectedVersion: 1, edits: [{ start: 0, end: 0, newText: 'x' }] },
        { documentId: 'b.ts', expectedVersion: 99, edits: [{ start: 0, end: 0, newText: 'y' }] },
      ]),
    (err: unknown) => {
      assert.ok(err instanceof BatchValidationError);
      assert.deepEqual(err.errors.map((e) => e.code), ['VERSION_MISMATCH']);
      return true;
    },
  );
  assert.deepEqual(ws.snapshot(), before);
});

test('rejects out-of-range edits and modifies nothing', () => {
  const ws = setup();
  const before = ws.snapshot();
  const text = ws.getDocument('b.ts')!.text;

  assert.throws(
    () =>
      ws.applyBatch([
        { documentId: 'a.ts', expectedVersion: 1, edits: [{ start: 0, end: 1, newText: 'x' }] },
        {
          documentId: 'b.ts',
          expectedVersion: 1,
          edits: [{ start: text.length - 1, end: text.length + 5, newText: 'x' }],
        },
      ]),
    (err: unknown) => {
      assert.ok(err instanceof BatchValidationError);
      assert.deepEqual(err.errors.map((e) => e.code), ['INVALID_RANGE']);
      return true;
    },
  );
  assert.deepEqual(ws.snapshot(), before);
});

test('rejects overlapping edits in the same document', () => {
  const ws = setup();
  const before = ws.snapshot();

  assert.throws(
    () =>
      ws.applyBatch([
        {
          documentId: 'a.ts',
          expectedVersion: 1,
          edits: [
            { start: 6, end: 12, newText: 'x' },
            { start: 10, end: 14, newText: 'y' },
          ],
        },
      ]),
    (err: unknown) => {
      assert.ok(err instanceof BatchValidationError);
      assert.deepEqual(err.errors.map((e) => e.code), ['OVERLAPPING_EDITS']);
      return true;
    },
  );
  assert.deepEqual(ws.snapshot(), before);
});

test('rejects two insertions at the same offset', () => {
  const ws = setup();
  const before = ws.snapshot();

  assert.throws(
    () =>
      ws.applyBatch([
        {
          documentId: 'a.ts',
          expectedVersion: 1,
          edits: [
            { start: 5, end: 5, newText: 'x' },
            { start: 5, end: 5, newText: 'y' },
          ],
        },
      ]),
    BatchValidationError,
  );
  assert.deepEqual(ws.snapshot(), before);
});

test('rejects a document listed twice in one batch', () => {
  const ws = setup();
  const before = ws.snapshot();

  assert.throws(
    () =>
      ws.applyBatch([
        { documentId: 'a.ts', expectedVersion: 1, edits: [{ start: 0, end: 0, newText: 'x' }] },
        { documentId: 'a.ts', expectedVersion: 1, edits: [{ start: 1, end: 1, newText: 'y' }] },
      ]),
    (err: unknown) => {
      assert.ok(err instanceof BatchValidationError);
      assert.deepEqual(err.errors.map((e) => e.code), ['DUPLICATE_DOCUMENT']);
      return true;
    },
  );
  assert.deepEqual(ws.snapshot(), before);
});

test('collects errors from every change instead of stopping at the first', () => {
  const ws = setup();

  assert.throws(
    () =>
      ws.applyBatch([
        { documentId: 'a.ts', expectedVersion: 7, edits: [{ start: 0, end: 0, newText: 'x' }] },
        { documentId: 'b.ts', expectedVersion: 1, edits: [{ start: -1, end: 0, newText: 'y' }] },
        { documentId: 'missing.ts', expectedVersion: 1, edits: [{ start: 0, end: 0, newText: 'z' }] },
      ]),
    (err: unknown) => {
      assert.ok(err instanceof BatchValidationError);
      assert.deepEqual(
        err.errors.map((e) => e.code).sort(),
        ['DOCUMENT_NOT_FOUND', 'INVALID_RANGE', 'VERSION_MISMATCH'],
      );
      return true;
    },
  );
});

test('undo restores the exact pre-batch snapshot', () => {
  const ws = setup();
  const before = ws.snapshot();

  const receipt = ws.applyBatch([
    { documentId: 'a.ts', expectedVersion: 1, edits: [{ start: 6, end: 11, newText: 'ALPHA' }] },
    { documentId: 'b.ts', expectedVersion: 1, edits: [{ start: 0, end: 6, newText: '' }] },
  ]);
  assert.notDeepEqual(ws.snapshot(), before);

  ws.undo(receipt);
  assert.deepEqual(ws.snapshot(), before);
});

test('undo after a later edit fails atomically and modifies nothing', () => {
  const ws = setup();
  const receipt = ws.applyBatch([
    { documentId: 'a.ts', expectedVersion: 1, edits: [{ start: 6, end: 11, newText: 'ALPHA' }] },
    { documentId: 'b.ts', expectedVersion: 1, edits: [{ start: 0, end: 0, newText: '// x\n' }] },
  ]);

  // A later, independent edit lands on b.ts.
  ws.applyBatch([
    { documentId: 'b.ts', expectedVersion: 2, edits: [{ start: 0, end: 0, newText: '// later\n' }] },
  ]);
  const before = ws.snapshot();

  assert.throws(
    () => ws.undo(receipt),
    (err: unknown) => {
      assert.ok(err instanceof BatchValidationError);
      assert.deepEqual(err.errors.map((e) => e.code), ['UNDO_VERSION_MISMATCH']);
      assert.equal(err.errors[0].documentId, 'b.ts');
      return true;
    },
  );
  // a.ts must not have been reverted either: undo is atomic.
  assert.deepEqual(ws.snapshot(), before);
});

test('undoing the same receipt twice fails', () => {
  const ws = setup();
  const receipt = ws.applyBatch([
    { documentId: 'a.ts', expectedVersion: 1, edits: [{ start: 0, end: 0, newText: 'x' }] },
  ]);
  ws.undo(receipt);
  const before = ws.snapshot();

  assert.throws(() => ws.undo(receipt), BatchValidationError);
  assert.deepEqual(ws.snapshot(), before);
});

test('publishes one event per committed batch and per undo', () => {
  const ws = setup();
  const events: WorkspaceChangeEvent[] = [];
  ws.onDidChange((e) => events.push(e));

  const receipt = ws.applyBatch([
    { documentId: 'a.ts', expectedVersion: 1, edits: [{ start: 0, end: 0, newText: 'x' }] },
    { documentId: 'b.ts', expectedVersion: 1, edits: [{ start: 0, end: 0, newText: 'y' }] },
  ]);
  ws.undo(receipt);
  assert.throws(() =>
    ws.applyBatch([
      { documentId: 'a.ts', expectedVersion: 42, edits: [{ start: 0, end: 0, newText: 'z' }] },
    ]),
  );

  assert.deepEqual(
    events.map((e) => [e.reason, e.changes.length]),
    [
      ['apply', 2],
      ['undo', 2],
    ],
  );
  assert.equal(events[0].batchId, events[1].batchId);
});

test('snapshot model: randomized batches and undos track an independent model', () => {
  // Deterministic PRNG (mulberry32).
  let seed = 0x2f6e2b1;
  const random = () => {
    seed |= 0;
    seed = (seed + 0x6d2b79f5) | 0;
    let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
  const pick = <T>(items: T[]): T => items[Math.floor(random() * items.length)];

  const alphabet = 'abc \n';
  const randomText = (len: number) =>
    Array.from({ length: len }, () => alphabet[Math.floor(random() * alphabet.length)]).join('');

  const ws = new Workspace();
  const model = new Map<string, { text: string; version: number }>();
  const ids = ['m1', 'm2', 'm3'];
  for (const id of ids) {
    const text = randomText(24);
    ws.openDocument(id, text);
    model.set(id, { text, version: 1 });
  }

  const modelSnapshot = (): WorkspaceSnapshot =>
    Object.fromEntries(
      [...model.entries()].sort().map(([id, d]) => [id, { id, text: d.text, version: d.version }]),
    );

  const receipts: BatchReceipt[] = [];

  for (let step = 0; step < 300; step += 1) {
    const action = random();

    if (action < 0.55) {
      // Build a batch that is valid according to the model.
      const changes: DocumentChange[] = [];
      const chosen = ids.filter(() => random() < 0.6);
      for (const id of chosen) {
        const doc = model.get(id)!;
        const edits: TextEdit[] = [];
        let cursor = 0;
        const editCount = 1 + Math.floor(random() * 3);
        for (let k = 0; k < editCount; k += 1) {
          const start = cursor + Math.floor(random() * (doc.text.length - cursor + 1));
          const end = start + Math.floor(random() * (doc.text.length - start + 1));
          edits.push({ start, end, newText: randomText(Math.floor(random() * 4)) });
          cursor = end + 1;
          if (cursor > doc.text.length) break;
        }
        changes.push({ documentId: id, expectedVersion: doc.version, edits });
        // Apply to the model with original coordinates, right to left.
        let text = doc.text;
        for (const edit of [...edits].sort((x, y) => y.start - x.start)) {
          text = text.slice(0, edit.start) + edit.newText + text.slice(edit.end);
        }
        model.set(id, { text, version: doc.version + 1 });
      }
      if (changes.length === 0) continue;
      receipts.push(ws.applyBatch(changes));
    } else if (action < 0.75 && receipts.length > 0) {
      // Undo the most recent receipt in both workspace and model.
      const receipt = receipts.pop()!;
      ws.undo(receipt);
      for (const entry of receipt.entries) {
        model.set(entry.documentId, { text: entry.previousText, version: entry.baseVersion });
      }
    } else {
      // Inject an invalid batch: workspace and model must both stay put.
      const id = pick(ids);
      const doc = model.get(id)!;
      const before = ws.snapshot();
      assert.throws(() =>
        ws.applyBatch([
          {
            documentId: id,
            expectedVersion: doc.version + 1,
            edits: [{ start: 0, end: 0, newText: 'x' }],
          },
        ]),
      );
      assert.deepEqual(ws.snapshot(), before);
    }

    assert.deepEqual(ws.snapshot(), modelSnapshot(), `snapshot diverged at step ${step}`);
  }
});
