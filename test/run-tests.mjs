// Verification harness: a per-character source-tagged model is the ground
// truth. Every character carries labels { s0, s1 } = index in the original
// input / index after round 1 (null = inserted by that round). Mappings are
// derived from the labels, composed by the library, and every query result
// is checked against the labels.
import {
  anchorInSource,
  compose,
  isInsertion,
  queryOutputs,
  querySource,
  validateMapping,
} from '../dist/mapping.mjs';

let failures = 0;
function check(cond, msg) {
  if (!cond) {
    failures++;
    console.error('FAIL:', msg);
  }
}
function eq(actual, expected, msg) {
  const a = JSON.stringify(actual);
  const e = JSON.stringify(expected);
  check(a === e, `${msg}\n  actual:   ${a}\n  expected: ${e}`);
}

// ---- tagged-model helpers -------------------------------------------------

const nullTags = (n) => Array.from({ length: n }, () => ({ s0: null, s1: null }));

function opReplace(tags, start, end, count) {
  tags.splice(start, end - start, ...nullTags(count));
}
function opInsert(tags, pos, count) {
  tags.splice(pos, 0, ...nullTags(count));
}
function opCopy(tags, start, end, dest) {
  tags.splice(dest, 0, ...tags.slice(start, end).map((t) => ({ ...t })));
}

// Derive the segment mapping of one round from output labels (index of each
// output char in that round's input, or null when inserted).
function deriveMapping(labels) {
  const segs = [];
  const n = labels.length;
  const anchorAt = (idx) => {
    for (let j = idx - 1; j >= 0; j--) if (labels[j] !== null) return labels[j] + 1;
    for (let j = idx; j < n; j++) if (labels[j] !== null) return labels[j];
    return 0;
  };
  let i = 0;
  while (i < n) {
    if (labels[i] === null) {
      let j = i;
      while (j < n && labels[j] === null) j++;
      const anchor = anchorAt(i);
      segs.push({ outStart: i, outEnd: j, srcStart: anchor, srcEnd: anchor });
      i = j;
    } else {
      let j = i + 1;
      while (j < n && labels[j] === labels[j - 1] + 1) j++;
      segs.push({ outStart: i, outEnd: j, srcStart: labels[i], srcEnd: labels[j - 1] + 1 });
      i = j;
    }
  }
  return segs;
}

// Expected forward-query items from s0 labels: merge across null runs, never
// across source gaps (deletions) or backwards jumps (copies).
function expectedItems(labels) {
  const items = [];
  let lastSource = null;
  for (const lab of labels) {
    if (lab === null) {
      if (items.length === 0 || items[items.length - 1].kind !== 'insertion') {
        items.push({ kind: 'insertion' });
      }
      continue;
    }
    if (lastSource && lastSource.srcEnd === lab) {
      lastSource.srcEnd = lab + 1;
    } else {
      lastSource = { kind: 'source', srcStart: lab, srcEnd: lab + 1 };
      items.push(lastSource);
    }
  }
  return items;
}

// ---- deterministic cases --------------------------------------------------

const identity = (n) => [{ outStart: 0, outEnd: n, srcStart: 0, srcEnd: n }];

function testReplacement() {
  // S0 = "abcdef", round 1 replaces "cd" with 3 new chars -> "ab???ef".
  const tags = [0, 1, 2, 3, 4, 5].map((i) => ({ s0: i, s1: null }));
  opReplace(tags, 2, 4, 3);
  const A = deriveMapping(tags.map((t) => t.s0));
  validateMapping(A);
  const C = compose(A, identity(tags.length));
  const frags = querySource(C, 0, tags.length);
  eq(
    frags,
    [
      { kind: 'source', srcStart: 0, srcEnd: 2 },
      { kind: 'insertion', anchor: 2 },
      { kind: 'source', srcStart: 4, srcEnd: 6 },
    ],
    'replacement: deletion gap [2,4) must keep source fragments apart',
  );
  eq(queryOutputs(C, 0, 6), [{ outStart: 0, outEnd: 2 }, { outStart: 5, outEnd: 7 }], 'replacement: reverse');
}

function testPureInsertion() {
  // S0 = "abc", insert one char at 1 -> "a?bc"; source fragments merge
  // across the zero-width insertion.
  const tags = [0, 1, 2].map((i) => ({ s0: i, s1: null }));
  opInsert(tags, 1, 1);
  const A = deriveMapping(tags.map((t) => t.s0));
  const C = compose(A, identity(tags.length));
  const frags = querySource(C, 0, tags.length);
  eq(
    frags,
    [{ kind: 'source', srcStart: 0, srcEnd: 3 }, { kind: 'insertion', anchor: 1 }],
    'pure insertion: source fragments merge across the insertion',
  );
  eq(queryOutputs(C, 0, 3), [{ outStart: 0, outEnd: 1 }, { outStart: 2, outEnd: 4 }], 'pure insertion: reverse');
}

function testCopy() {
  // S0 = "abc", copy "ab" to the end -> "abcab"; reverse query is multi-valued.
  const tags = [0, 1, 2].map((i) => ({ s0: i, s1: null }));
  opCopy(tags, 0, 2, 3);
  const A = deriveMapping(tags.map((t) => t.s0));
  const C = compose(A, identity(tags.length));
  eq(queryOutputs(C, 0, 2), [{ outStart: 0, outEnd: 2 }, { outStart: 3, outEnd: 5 }], 'copy: reverse is multi-valued');
  eq(querySource(C, 3, 5), [{ kind: 'source', srcStart: 0, srcEnd: 2 }], 'copy: forward of the duplicate');
  const frags = querySource(C, 0, 5);
  eq(
    frags.filter((f) => f.kind === 'source'),
    [
      { kind: 'source', srcStart: 0, srcEnd: 3 },
      { kind: 'source', srcStart: 0, srcEnd: 2 },
    ],
    'copy: duplicated source is a separate fragment, not merged',
  );
}

function testTwoRoundAnchors() {
  // Round 1: insert X at 2 in "abcd" -> "abXcd" (anchor 2).
  // Round 2: insert Y at 3 -> "abXYcd"; Y's anchor in S1 is 3, which sits at
  // the start of the mapped segment covering "cd", i.e. S0 position 2.
  const t1 = [0, 1, 2, 3].map((i) => ({ s0: i, s1: null }));
  opInsert(t1, 2, 1);
  const A = deriveMapping(t1.map((t) => t.s0));
  t1.forEach((t, i) => { t.s1 = i; });
  const t2 = t1.map((t) => ({ ...t }));
  opInsert(t2, 3, 1);
  const B = deriveMapping(t2.map((t) => t.s1));
  const C = compose(A, B);
  validateMapping(C);
  check(anchorInSource(A, 3) === 2, 'anchorInSource translates S1 pos 3 to S0 pos 2');
  eq(querySource(C, 2, 3), [{ kind: 'insertion', anchor: 2 }], 'two-round: X anchored at 2');
  eq(querySource(C, 3, 4), [{ kind: 'insertion', anchor: 2 }], 'two-round: Y anchored at 2');
  eq(
    querySource(C, 0, 6).filter((f) => f.kind === 'source'),
    [{ kind: 'source', srcStart: 0, srcEnd: 4 }],
    'two-round: source merges across both insertions',
  );
}

// ---- randomized fuzz against the tagged model ------------------------------

function mulberry32(seed) {
  let a = seed >>> 0;
  return () => {
    a |= 0; a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function applyRandomOps(rng, tags, rounds) {
  for (let k = 0; k < rounds; k++) {
    const n = tags.length;
    const pick = rng();
    if (n === 0 || pick < 0.34) {
      opInsert(tags, Math.floor(rng() * (n + 1)), 1 + Math.floor(rng() * 3));
    } else if (pick < 0.67) {
      const start = Math.floor(rng() * n);
      const end = start + Math.floor(rng() * (n - start));
      opReplace(tags, start, end, Math.floor(rng() * 3));
    } else {
      const start = Math.floor(rng() * n);
      const end = start + 1 + Math.floor(rng() * (n - start));
      opCopy(tags, start, end, Math.floor(rng() * (tags.length + 1)));
    }
  }
  return tags;
}

function fuzzOnce(seed) {
  const rng = mulberry32(seed);
  const n0 = 1 + Math.floor(rng() * 12);
  const t0 = Array.from({ length: n0 }, (_, i) => ({ s0: i, s1: null }));
  const t1 = applyRandomOps(rng, t0.map((t) => ({ ...t })), 1 + Math.floor(rng() * 3));
  t1.forEach((t, i) => { t.s1 = i; });
  const t2 = applyRandomOps(rng, t1.map((t) => ({ ...t })), 1 + Math.floor(rng() * 3));

  const A = deriveMapping(t1.map((t) => t.s0));
  const B = deriveMapping(t2.map((t) => t.s1));
  const C = compose(A, B);
  validateMapping(A);
  validateMapping(B);
  validateMapping(C);

  const labels = t2.map((t) => t.s0);
  const where = (msg) => `seed=${seed} ${msg} (labels=${JSON.stringify(labels)})`;

  // 1. per-character forward queries
  for (let i = 0; i < t2.length; i++) {
    const frags = querySource(C, i, i + 1);
    if (labels[i] === null) {
      check(frags.every((f) => f.kind === 'insertion'), where(`inserted char ${i} has no source`));
      for (const f of frags) check(f.anchor >= 0 && f.anchor <= n0, where(`anchor in range`));
    } else {
      const hit = frags.find((f) => f.kind === 'source');
      check(!!hit && hit.srcStart === labels[i] && hit.srcEnd === labels[i] + 1, where(`char ${i} maps to its label`));
    }
  }

  // 2. whole-range forward query: ordered fragments, no merge across gaps.
  // Adjacent insertion items are collapsed before comparing kinds: a run of
  // inserted chars may legitimately split into several anchors when its
  // characters were inserted by different rounds.
  const collapse = (items) => items.filter((f, i) => f.kind === 'source' || i === 0 || items[i - 1].kind !== 'insertion');
  const got = querySource(C, 0, t2.length);
  const want = expectedItems(labels);
  eq(collapse(got).map((f) => f.kind), collapse(want).map((f) => f.kind), where('fragment kinds'));
  eq(
    got.filter((f) => f.kind === 'source'),
    want.filter((f) => f.kind === 'source'),
    where('source fragments'),
  );

  // 3. reverse queries are multi-valued and exact
  for (let p = 0; p < n0; p++) {
    const wantRanges = [];
    labels.forEach((lab, i) => { if (lab === p) wantRanges.push({ outStart: i, outEnd: i + 1 }); });
    eq(queryOutputs(C, p, p + 1), wantRanges, where(`reverse of source pos ${p}`));
  }
  // range reverse query
  const s = Math.floor(rng() * n0);
  const e = s + Math.floor(rng() * (n0 - s));
  const wantRanges = [];
  for (let i = 0; i < labels.length; i++) {
    const lab = labels[i];
    if (lab === null || lab < s || lab >= e) continue;
    const last = wantRanges[wantRanges.length - 1];
    if (last && last.outEnd === i && labels[i - 1] === lab - 1) last.outEnd = i + 1;
    else wantRanges.push({ outStart: i, outEnd: i + 1 });
  }
  eq(queryOutputs(C, s, e), wantRanges, where(`reverse of range [${s},${e})`));
}

testReplacement();
testPureInsertion();
testCopy();
testTwoRoundAnchors();

const SEEDS = 2000;
for (let seed = 1; seed <= SEEDS; seed++) fuzzOnce(seed);

if (failures > 0) {
  console.error(`${failures} failure(s)`);
  process.exit(1);
}
console.log(`ok: 4 deterministic suites + ${SEEDS} fuzz rounds passed`);
