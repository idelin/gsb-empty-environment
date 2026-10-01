import test from 'node:test';
import assert from 'node:assert/strict';
import { ErrorDigest } from '../src/digest.ts';
import { encode, decode } from '../src/serialize.ts';
import type { Segment, ValidationError } from '../src/types.ts';

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

const VERSIONS = ['v1', 'v2'];
const RULES: Array<[string, string, string]> = [
  ['type', '#/items/type', 'expected type integer'],
  ['minimum', '#/items/minimum', 'must be >= 1'],
  ['minLength', '#/items/name/minLength', 'must be at least 1 characters'],
];

function randomError(rnd: () => number): ValidationError {
  const [keyword, schemaPath, message] = RULES[Math.floor(rnd() * RULES.length)];
  const schemaVersion = VERSIONS[Math.floor(rnd() * VERSIONS.length)];
  const kind = rnd();
  let instancePath: Segment[];
  if (kind < 0.6) instancePath = [Math.floor(rnd() * 40)];
  else if (kind < 0.85) instancePath = [Math.floor(rnd() * 40), 'name'];
  else instancePath = ['meta', rnd() < 0.5 ? 'title' : 'tag'];
  return { schemaVersion, keyword, schemaPath, message, instancePath };
}

const asMultiset = (xs: ValidationError[]) =>
  xs
    .map((e) =>
      JSON.stringify({
        schemaVersion: e.schemaVersion,
        keyword: e.keyword,
        schemaPath: e.schemaPath,
        message: e.message,
        instancePath: e.instancePath,
      }),
    )
    .sort();

test('random add/retract operations stay consistent with an independent reference list', () => {
  const rnd = mulberry32(20261001);
  const digest = new ErrorDigest();
  const live = new Map<number, ValidationError>();
  const reference = (): ValidationError[] => [...live.entries()].sort((a, b) => a[0] - b[0]).map(([, e]) => e);

  for (let op = 0; op < 4000; op++) {
    if (live.size === 0 || rnd() < 0.6) {
      const e = randomError(rnd);
      live.set(digest.add(e), e);
    } else {
      const ids = [...live.keys()];
      const victim = ids[Math.floor(rnd() * ids.length)];
      assert.ok(digest.remove(victim));
      live.delete(victim);
    }

    if (op % 250 === 0) {
      assert.equal(digest.count, live.size, `count diverged at op ${op}`);
      assert.deepEqual(digest.expandAll('insertion'), reference(), `expansion diverged at op ${op}`);
      assert.deepEqual(
        asMultiset(decode(encode(digest))),
        asMultiset(reference()),
        `round trip diverged at op ${op}`,
      );
    }
  }

  // Final full check, including summary count conservation.
  const ref = reference();
  assert.equal(digest.count, ref.length);
  assert.equal(digest.summaries().reduce((a, s) => a + s.count, 0), ref.length);
  assert.deepEqual(digest.expandAll('insertion'), ref);

  // Random-access expansions match the reference filtered the same way.
  for (const [keyword] of RULES) {
    for (const version of VERSIONS) {
      assert.deepEqual(
        digest.expandByRule(keyword, version),
        ref.filter((e) => e.keyword === keyword && e.schemaVersion === version),
      );
    }
  }
  for (const prefix of [[0], [7], [20, 'name'], ['meta'], [39]] as Segment[][]) {
    assert.deepEqual(
      digest.expandByPrefix(prefix),
      ref.filter((e) => prefix.every((s, i) => e.instancePath[i] === s)),
      `prefix ${JSON.stringify(prefix)} diverged`,
    );
  }

  // Path-ordered expansion is the same multiset, sorted by path.
  const inPathOrder = digest.expandAll('path');
  assert.deepEqual(asMultiset(inPathOrder), asMultiset(ref));
});

test('decompressed output always equals the original error set under random states', () => {
  const rnd = mulberry32(7);
  for (let trial = 0; trial < 50; trial++) {
    const digest = new ErrorDigest();
    const raw: ValidationError[] = [];
    const n = Math.floor(rnd() * 200);
    for (let i = 0; i < n; i++) {
      const e = randomError(rnd);
      raw.push(e);
      digest.add(e);
    }
    assert.deepEqual(asMultiset(decode(encode(digest))), asMultiset(raw));
  }
});
