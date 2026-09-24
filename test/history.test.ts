import { test } from 'node:test';
import assert from 'node:assert/strict';
import { TextDocument } from '../src/document.ts';
import { Selection, SelectionSet } from '../src/selections.ts';
import { History } from '../src/history.ts';
import { mulberry32, randomText } from './helpers.ts';

test('compression composes mappings across reclaimed versions', () => {
  const h = new History();
  const v0 = h.init(
    TextDocument.fromText('abcdefghij'),
    SelectionSet.of(Selection.create(1, 1, 4)),
  );
  const v1 = h.commit(0, 0, '>>').version; // '>>abcdefghij'
  const v2 = h.commit(5, 2, '').version; // delete 2 chars
  const v3 = h.commit(0, 1, '').version; // delete 1 char

  const before = h.mappingBetween(v0, v3);
  const selBefore = h.selectionsAt(v3).selections[0];

  h.reclaim(v1);
  h.reclaim(v2);
  h.compress();

  assert.equal(h.size, 2, 'only live versions survive');
  const after = h.mappingBetween(v0, v3);
  assert.equal(after.steps.length, 3, 'steps are composed, not flattened to final offsets');
  for (let o = 0; o <= 12; o++) {
    for (const affinity of ['before', 'after'] as const) {
      assert.equal(after.mapOffset(o, affinity), before.mapOffset(o, affinity));
      assert.deepEqual(after.mapOffsetBack(o, affinity), before.mapOffsetBack(o, affinity));
    }
  }
  const selAfter = h.selectionsAt(v3).selections[0];
  assert.deepEqual(
    [selAfter.anchor, selAfter.active, selAfter.direction],
    [selBefore.anchor, selBefore.active, selBefore.direction],
    'live version selection query survives compression',
  );
});

test('reclaimed versions reject snapshot queries but stay routable', () => {
  const h = new History();
  const v0 = h.init(TextDocument.fromText('abc'), SelectionSet.of(Selection.cursor(1, 1)));
  const v1 = h.commit(0, 0, 'x').version;
  const v2 = h.commit(0, 0, 'y').version;

  h.reclaim(v1);
  assert.throws(() => h.selectionsAt(v1), /reclaimed/);
  h.compress();
  assert.throws(() => h.selectionsAt(v1), /unknown or reclaimed/);
  // Mapping between survivors still works.
  const m = h.mappingBetween(v0, v2);
  assert.equal(m.mapOffset(0, 'after'), 2);
});

test('branch undo then new edit truncates the redo tail', () => {
  const h = new History();
  const v0 = h.init(TextDocument.fromText('abc'), SelectionSet.of(Selection.cursor(1, 0)));
  h.commit(0, 0, 'x');
  h.commit(0, 0, 'y');
  assert.equal(h.size, 3);

  h.checkout(v0);
  const v3 = h.commit(3, 0, 'z').version;
  assert.equal(h.size, 2, 'redo tail dropped');
  assert.equal(h.docAt(v3).text(), 'abcz');
  assert.equal(h.currentVersion, v3);
});

test('random edits: compression never changes live-version queries', () => {
  const rand = mulberry32(20260924);
  for (let trial = 0; trial < 30; trial++) {
    const h = new History();
    const v0 = h.init(
      TextDocument.fromText(randomText(rand, 30)),
      SelectionSet.of(
        Selection.create(1, 2, 8),
        Selection.cursor(2, 5),
        Selection.create(3, 12, 4),
      ),
    );
    const versions = [v0];
    const edits = 5 + Math.floor(rand() * 10);
    for (let i = 0; i < edits; i++) {
      const len = h.docAt(h.currentVersion).length;
      const start = Math.floor(rand() * (len + 1));
      const del = Math.floor(rand() * (len - start + 1));
      versions.push(h.commit(start, del, randomText(rand, 6)).version);
    }

    // Snapshot pre-compression answers for every version.
    const pre = new Map<number, unknown>();
    for (const v of versions) {
      pre.set(v, {
        text: h.docAt(v).text(),
        sels: h.selectionsAt(v).selections.map((s) => [
          s.id,
          s.anchor,
          s.active,
          s.direction,
          s.sources,
        ]),
        mapFromBase: Array.from({ length: 8 }, (_, k) =>
          h.mappingBetween(v0, v).mapOffset(k * 2, k % 2 ? 'after' : 'before'),
        ),
      });
    }

    // Reclaim a random subset of non-current versions, keep the rest live.
    const live = new Set<number>([h.currentVersion]);
    for (const v of versions) {
      if (v !== h.currentVersion && rand() < 0.6) h.reclaim(v);
      else live.add(v);
    }
    h.compress();

    for (const v of live) {
      const want = pre.get(v) as {
        text: string;
        sels: unknown[];
        mapFromBase: number[];
      };
      assert.equal(h.docAt(v).text(), want.text, `trial ${trial} v${v} text`);
      assert.deepEqual(
        h.selectionsAt(v).selections.map((s) => [s.id, s.anchor, s.active, s.direction, s.sources]),
        want.sels,
        `trial ${trial} v${v} selections`,
      );
      if (live.has(v0)) {
        const got = Array.from({ length: 8 }, (_, k) =>
          h.mappingBetween(v0, v).mapOffset(k * 2, k % 2 ? 'after' : 'before'),
        );
        assert.deepEqual(got, want.mapFromBase, `trial ${trial} v${v} mapping`);
      }
    }
  }
});
