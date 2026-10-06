import { Marker, MarkerSet, Segment } from '../src/markers';
import { assert, assertDeepEqual, deepEq, test } from './harness';
import { anchorsAt, attrsAt } from './model';

function mk(
  id: string,
  source: string,
  start: number,
  end: number,
  priority: number,
  attrs: Record<string, unknown>,
): Marker {
  return { id, source, start, end, priority, attrs };
}

/** Expand segments to per-character attrs over [0, limit); gaps -> {}. */
function expand(segs: readonly Segment[], limit: number): Array<Record<string, unknown>> {
  const out: Array<Record<string, unknown>> = [];
  for (let pos = 0; pos < limit; pos++) {
    const seg = segs.find((s) => s.start <= pos && pos < s.end);
    out.push(seg ? seg.attrs : {});
  }
  return out;
}

/** Structural invariants: sorted, disjoint, non-empty, minimal (no mergeable neighbours). */
function assertWellFormed(segs: readonly Segment[]): void {
  for (let i = 0; i < segs.length; i++) {
    assert(segs[i].start < segs[i].end, `segment ${i} is non-empty`);
    if (i > 0) {
      assert(segs[i - 1].end <= segs[i].start, `segments ${i - 1}/${i} disjoint and sorted`);
      if (segs[i - 1].end === segs[i].start) {
        assert(
          !deepEq(segs[i - 1].attrs, segs[i].attrs),
          `adjacent segments ${i - 1}/${i} have different attrs (minimality)`,
        );
      }
    }
  }
}

test('higher priority wins per attribute key', () => {
  const set = new MarkerSet();
  set.upsert(mk('a', 'lex', 0, 10, 1, { color: 'red', weight: 'normal' }));
  set.upsert(mk('b', 'syn', 2, 5, 2, { color: 'blue' }));
  assertDeepEqual(
    set.segments(),
    [
      { start: 0, end: 2, attrs: { color: 'red', weight: 'normal' } },
      { start: 2, end: 5, attrs: { color: 'blue', weight: 'normal' } },
      { start: 5, end: 10, attrs: { color: 'red', weight: 'normal' } },
    ],
    'priority overlay splits into elementary segments',
  );
});

test('same priority is arbitrated by source id (greater wins)', () => {
  const set = new MarkerSet();
  set.upsert(mk('m1', 'alpha', 0, 4, 1, { color: 'red' }));
  set.upsert(mk('m2', 'beta', 0, 4, 1, { color: 'green' }));
  assertDeepEqual(set.segments(), [{ start: 0, end: 4, attrs: { color: 'green' } }], 'beta > alpha');
});

test('same priority and source falls back to marker id', () => {
  const set = new MarkerSet();
  set.upsert(mk('a1', 'lex', 0, 3, 0, { color: 'red' }));
  set.upsert(mk('a2', 'lex', 0, 3, 0, { color: 'blue' }));
  assertDeepEqual(set.segments(), [{ start: 0, end: 3, attrs: { color: 'blue' } }], 'a2 > a1');
});

test('attributes resolve independently per key', () => {
  const set = new MarkerSet();
  set.upsert(mk('a', 'lex', 0, 6, 1, { color: 'red' }));
  set.upsert(mk('b', 'syn', 0, 6, 2, { weight: 'bold' }));
  assertDeepEqual(
    set.segments(),
    [{ start: 0, end: 6, attrs: { color: 'red', weight: 'bold' } }],
    'each key keeps its own winner',
  );
});

test('empty ranges are anchors and occupy no characters', () => {
  const set = new MarkerSet();
  set.upsert(mk('cur', 'ide', 3, 3, 99, { cursor: true }));
  set.upsert(mk('a', 'lex', 0, 5, 1, { color: 'red' }));
  assertDeepEqual(
    set.segments(),
    [{ start: 0, end: 5, attrs: { color: 'red' } }],
    'anchor does not split or restyle the segment',
  );
  assertDeepEqual(set.resolveAt(3), { color: 'red' }, 'anchor contributes nothing at its offset');
  const anchors = set.anchors();
  assertDeepEqual(anchors.length, 1, 'one anchor group');
  assertDeepEqual(anchors[0].offset, 3, 'anchor offset');
  assertDeepEqual(anchors[0].markers.map((m) => m.id), ['cur'], 'anchor marker');
});

test('update via upsert replaces range and attributes', () => {
  const set = new MarkerSet();
  set.upsert(mk('a', 'lex', 0, 4, 1, { color: 'red' }));
  set.upsert(mk('a', 'lex', 6, 8, 2, { color: 'blue' }));
  assertDeepEqual(
    set.segments(),
    [{ start: 6, end: 8, attrs: { color: 'blue' } }],
    'old range is fully released',
  );
  assertDeepEqual(set.resolveAt(1), {}, 'offset 1 uncovered after update');
});

test('remove deletes the marker and frees its range', () => {
  const set = new MarkerSet();
  set.upsert(mk('a', 'lex', 0, 4, 1, { color: 'red' }));
  set.upsert(mk('b', 'syn', 2, 6, 2, { weight: 'bold' }));
  assert(set.remove('b'), 'remove returns true for existing id');
  assert(!set.remove('b'), 'remove returns false for missing id');
  assertDeepEqual(
    set.segments(),
    [{ start: 0, end: 4, attrs: { color: 'red' } }],
    'deleted marker no longer contributes',
  );
});

test('adjacent segments with equal resolved attrs merge', () => {
  const set = new MarkerSet();
  set.upsert(mk('a', 'lex', 0, 3, 1, { color: 'red' }));
  set.upsert(mk('b', 'syn', 3, 7, 1, { color: 'red' }));
  assertDeepEqual(
    set.segments(),
    [{ start: 0, end: 7, attrs: { color: 'red' } }],
    'touching ranges with identical style merge into one segment',
  );
  set.upsert(mk('b', 'syn', 3, 7, 1, { color: 'blue' }));
  assertDeepEqual(
    set.segments(),
    [
      { start: 0, end: 3, attrs: { color: 'red' } },
      { start: 3, end: 7, attrs: { color: 'blue' } },
    ],
    'updating one side back to a different style splits again',
  );
});

test('shadowed attribute reappears when the stronger marker is removed', () => {
  const set = new MarkerSet();
  set.upsert(mk('a', 'lex', 0, 4, 1, { color: 'red' }));
  set.upsert(mk('b', 'syn', 0, 4, 2, { color: 'blue' }));
  set.remove('b');
  assertDeepEqual(
    set.segments(),
    [{ start: 0, end: 4, attrs: { color: 'red' } }],
    'lower-priority style wins again after deletion',
  );
});

test('segments are clipped to the requested window', () => {
  const set = new MarkerSet();
  set.upsert(mk('a', 'lex', 0, 10, 1, { color: 'red' }));
  set.upsert(mk('b', 'syn', 4, 6, 2, { weight: 'bold' }));
  assertDeepEqual(
    set.segments(2, 8),
    [
      { start: 2, end: 4, attrs: { color: 'red' } },
      { start: 4, end: 6, attrs: { color: 'red', weight: 'bold' } },
      { start: 6, end: 8, attrs: { color: 'red' } },
    ],
    'window [2, 8)',
  );
});

test('gaps produce no segments', () => {
  const set = new MarkerSet();
  set.upsert(mk('a', 'lex', 0, 2, 1, { color: 'red' }));
  set.upsert(mk('b', 'lex', 5, 7, 1, { color: 'blue' }));
  const segs = set.segments();
  assertDeepEqual(segs.map((s) => [s.start, s.end]), [[0, 2], [5, 7]], 'gap [2,5) uncovered');
  assertDeepEqual(set.resolveAt(3), {}, 'gap resolves to empty attrs');
});

test('invalid markers are rejected', () => {
  const set = new MarkerSet();
  const bad: Array<[string, Marker]> = [
    ['end < start', mk('x', 's', 5, 3, 0, {})],
    ['negative start', mk('x', 's', -1, 3, 0, {})],
    ['non-integer', mk('x', 's', 0.5, 3, 0, {})],
  ];
  for (const [why, m] of bad) {
    let threw = false;
    try {
      set.upsert(m);
    } catch {
      threw = true;
    }
    assert(threw, `rejects ${why}`);
  }
});

// --- randomized cross-check against the per-character model ---

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const SOURCES = ['lex', 'syn', 'sem'];
const KEYS: Array<[string, unknown[]]> = [
  ['color', ['red', 'green', 'blue']],
  ['weight', ['normal', 'bold']],
  ['italic', [true]],
];

function randomMarker(rng: () => number, seq: number): Marker {
  const start = Math.floor(rng() * 30);
  const empty = rng() < 0.15;
  const end = empty ? start : start + 1 + Math.floor(rng() * 8);
  const attrs: Record<string, unknown> = {};
  for (const [key, values] of KEYS) {
    if (rng() < 0.6) attrs[key] = values[Math.floor(rng() * values.length)];
  }
  return {
    id: `m${seq % 12}`, // small id pool forces updates
    source: SOURCES[Math.floor(rng() * SOURCES.length)],
    start,
    end,
    priority: Math.floor(rng() * 3),
    attrs,
  };
}

function verifyAgainstModel(set: MarkerSet, model: readonly Marker[], ctx: string): void {
  const LIMIT = 45;
  const segs = set.segments();
  assertWellFormed(segs);
  const perChar = expand(segs, LIMIT);
  for (let pos = 0; pos < LIMIT; pos++) {
    const expected = attrsAt(model, pos);
    assertDeepEqual(perChar[pos], expected, `${ctx}: segments at offset ${pos}`);
    assertDeepEqual(set.resolveAt(pos), expected, `${ctx}: resolveAt(${pos})`);
  }
  const expectedAnchors = anchorsAt(model);
  const actualAnchors = set.anchors().map((a) => ({ offset: a.offset, ids: a.markers.map((m) => m.id) }));
  assertDeepEqual(actualAnchors, expectedAnchors, `${ctx}: anchors`);
}

test('fuzz: incremental upsert/remove matches per-character model', () => {
  const rng = mulberry32(0xc0ffee);
  const set = new MarkerSet();
  const model = new Map<string, Marker>();
  const ids: string[] = [];
  for (let step = 0; step < 400; step++) {
    if (rng() < 0.65 || ids.length === 0) {
      const m = randomMarker(rng, step);
      set.upsert(m);
      model.set(m.id, m);
      if (!ids.includes(m.id)) ids.push(m.id);
    } else {
      const id = ids[Math.floor(rng() * ids.length)];
      set.remove(id);
      model.delete(id);
      ids.splice(ids.indexOf(id), 1);
    }
    verifyAgainstModel(set, [...model.values()], `step ${step}`);
  }
});

test('fuzz: windowed segments match model under clipping', () => {
  const rng = mulberry32(0xbeef);
  const set = new MarkerSet();
  const model = new Map<string, Marker>();
  for (let i = 0; i < 40; i++) {
    const m = randomMarker(rng, i + 100);
    set.upsert(m);
    model.set(m.id, m); // upsert semantics: same id replaces
  }
  const markers = [...model.values()];
  for (let from = 0; from < 40; from += 7) {
    const to = from + 1 + Math.floor(rng() * 10);
    const segs = set.segments(from, to);
    assertWellFormed(segs);
    for (const s of segs) {
      assert(s.start >= from && s.end <= to, `segment within [${from}, ${to})`);
      for (let pos = s.start; pos < s.end; pos++) {
        assertDeepEqual(s.attrs, attrsAt(markers, pos), `clipped attrs at ${pos}`);
      }
    }
  }
});
