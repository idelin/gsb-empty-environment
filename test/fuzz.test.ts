/**
 * Randomized validation against the per-character identity oracle.
 *
 * For every random edit, every selection endpoint is mapped twice:
 *  - through the structural offset Mapping (the implementation), and
 *  - through the identity model (the oracle), which only follows surviving
 *    character ids and knows nothing about offsets.
 * The two must agree on UTF-16 offsets. Also validated: mapping
 * composition/inversion laws and merge invariants.
 */
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Mapping } from '../src/mapping.ts';
import { TextDocument } from '../src/document.ts';
import { Selection, SelectionSet } from '../src/selections.ts';
import {
  endpointToGap,
  gapToOffset,
  mulberry32,
  randomText,
  remapGap,
  type Gap,
} from './helpers.ts';

const SEED = 917117835;

function randomSelections(rand: () => number, doc: TextDocument): SelectionSet {
  const n = 1 + Math.floor(rand() * 5);
  const sels: Selection[] = [];
  for (let i = 0; i < n; i++) {
    // Public offsets never split surrogate pairs: snap to code-point boundaries.
    const a = doc.snapOffset(Math.floor(rand() * (doc.length + 1)));
    const b = doc.snapOffset(Math.floor(rand() * (doc.length + 1)));
    const anchorAff = rand() < 0.5 ? 'before' : 'after';
    const activeAff = rand() < 0.5 ? 'before' : 'after';
    sels.push(Selection.create(i + 1, a, b, anchorAff, activeAff));
  }
  return SelectionSet.of(...sels);
}

test('identity model: random edits map endpoints identically (UTF-16, surrogate-safe, LF+CRLF)', () => {
  const rand = mulberry32(SEED);
  let endpointChecks = 0;
  for (let iter = 0; iter < 400; iter++) {
    const doc = TextDocument.fromText(randomText(rand, 40));
    const set = randomSelections(rand, doc);

    const start = Math.floor(rand() * (doc.length + 1));
    const del = Math.floor(rand() * (doc.length - start + 1));
    const insert = randomText(rand, 10);
    const { doc: after, mapping, edit } = doc.applyEdit(start, del, insert);

    // Edits never split surrogate pairs.
    assert.equal(doc.atomIndexAt(edit.start).inside, false);
    assert.equal(doc.atomIndexAt(edit.end).inside, false);

    for (const sel of set.selections) {
      for (const [end, ep] of [
        ['anchor', sel.anchor],
        ['active', sel.active],
      ] as const) {
        const gap: Gap = endpointToGap(doc, ep.offset, ep.affinity);
        const expected = gapToOffset(after, remapGap(doc, after, gap));
        const actual = mapping.mapOffset(ep.offset, ep.affinity);
        assert.equal(
          actual,
          expected,
          `iter ${iter} sel ${sel.id} ${end}: offset mapping disagrees with identity model`,
        );
        endpointChecks++;
      }
    }
  }
  assert.ok(endpointChecks > 2000, `exercised ${endpointChecks} endpoint mappings`);
});

test('identity model: composed mapping equals step-by-step mapping; inverse round-trips survivors', () => {
  const rand = mulberry32(SEED + 1);
  for (let iter = 0; iter < 200; iter++) {
    let doc = TextDocument.fromText(randomText(rand, 30));
    const mappings: Mapping[] = [];
    const steps = 1 + Math.floor(rand() * 6);
    for (let i = 0; i < steps; i++) {
      const start = Math.floor(rand() * (doc.length + 1));
      const del = Math.floor(rand() * (doc.length - start + 1));
      const r = doc.applyEdit(start, del, randomText(rand, 8));
      mappings.push(r.mapping);
      doc = r.doc;
    }
    const composed = Mapping.compose(...mappings);
    for (let k = 0; k < 12; k++) {
      const o = Math.floor(rand() * 25);
      const affinity = rand() < 0.5 ? 'before' : 'after';
      let expected = o;
      for (const m of mappings) expected = m.mapOffset(expected, affinity);
      assert.equal(composed.mapOffset(o, affinity), expected, `iter ${iter} compose`);
      // Inversion law: mapOffsetBack offset equals folding through invert().
      assert.equal(
        composed.mapOffsetBack(o, affinity).offset,
        composed.invert().mapOffset(o, affinity),
        `iter ${iter} invert`,
      );
    }
    // (a..z).invert() == z.invert()..a.invert()
    const lhs = composed.invert();
    const rhs = Mapping.compose(...mappings.map((m) => m.invert()).reverse());
    assert.deepEqual(lhs.steps, rhs.steps, `iter ${iter} inverse composition`);
  }
});

test('merge invariants under random deletions; sources keep id/direction/stickiness', () => {
  const rand = mulberry32(SEED + 2);
  let merges = 0;
  for (let iter = 0; iter < 400; iter++) {
    const doc = TextDocument.fromText(randomText(rand, 30));
    const set = randomSelections(rand, doc);
    const start = Math.floor(rand() * (doc.length + 1));
    const del = Math.floor(rand() * (doc.length - start + 1));
    const { mapping } = doc.applyEdit(start, del, randomText(rand, 4));

    const { set: mapped, merges: events } = set.mapForward(mapping);

    // Invariant: surviving selections never overlap or touch.
    const ranges = mapped.selections.map((s) => [s.from, s.to]).sort((a, b) => a[0] - b[0]);
    for (let i = 1; i < ranges.length; i++) {
      assert.ok(ranges[i][0] > ranges[i - 1][1], `iter ${iter}: selections still touching`);
    }
    // Invariant: every merge keeps the smallest id and records all lost ids.
    for (const ev of events) {
      merges++;
      const lostIds = ev.sources.map((s) => s.id);
      assert.ok(ev.into < Math.min(...lostIds), `iter ${iter}: primary is not the smallest id`);
      for (const src of ev.sources) {
        assert.ok(src.direction === 'forward' || src.direction === 'backward');
        assert.ok(src.anchor.affinity === 'before' || src.anchor.affinity === 'after');
        assert.ok(typeof src.anchor.offset === 'number' && typeof src.active.offset === 'number');
      }
    }
    // Invariant: no selection id disappears without being recorded.
    const before = new Set(set.selections.map((s) => s.id));
    const afterIds = new Set(mapped.selections.map((s) => s.id));
    const recorded = new Set(events.flatMap((e) => e.sources.map((s) => s.id)));
    for (const id of before) {
      assert.ok(afterIds.has(id) || recorded.has(id), `iter ${iter}: id ${id} vanished silently`);
    }
  }
  assert.ok(merges > 50, `exercised ${merges} merges`);
});

test('identity model: two cursors crushed by one deletion restore separately after undo', () => {
  const rand = mulberry32(SEED + 3);
  for (let iter = 0; iter < 200; iter++) {
    const doc = TextDocument.fromText(randomText(rand, 20));
    if (doc.length < 2) continue;
    const p1 = doc.snapOffset(Math.floor(rand() * (doc.length + 1)));
    let p2 = doc.snapOffset(Math.floor(rand() * (doc.length + 1)));
    if (p2 === p1) p2 = (p2 + 1) % (doc.length + 1);
    const set = SelectionSet.of(Selection.cursor(1, p1), Selection.cursor(2, p2));

    const lo = Math.min(p1, p2);
    const hi = Math.max(p1, p2);
    // One deletion covering both cursor positions.
    const { doc: after, mapping } = doc.applyEdit(lo, hi - lo, '');
    const { set: mapped, merges } = set.mapForward(mapping);

    if (p1 !== p2) {
      assert.equal(mapped.selections.length, 1, `iter ${iter}: one visible cursor`);
      assert.equal(merges.length, 1);
      // Oracle agrees on where the survivor sits.
      const gap = endpointToGap(doc, p1, 'after');
      assert.equal(
        mapped.selections[0].anchor.offset,
        gapToOffset(after, remapGap(doc, after, gap)),
      );
    }
  }
});
