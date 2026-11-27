import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import {
  collectFacts,
  encodeFact,
  extractIndexPlan,
  FULL_SCAN,
  GinPathIndex,
  matchJsonPath,
  parseJsonPath,
  queryJsonPath,
  type JsonValue,
} from '../src/index.ts';

describe('dangerous pushdown patterns never over-constrain', () => {
  const corpus: JsonValue[] = [
    { a: { b: 1 }, unrelated: true },
    { a: { b: 'string' } },
    { a: {} },
    { a: [{ b: 1 }, { b: 2 }] },
    { a: [[[{ b: 1 }]]] },
    { nested: { deep: { a: { b: 1 } } } },
    { b: 1 },
    null,
    42,
    [{ a: { b: 1 } }],
  ];
  const index = new GinPathIndex();
  corpus.forEach((doc, id) => index.addDocument(id, doc));

  const recall = (source: string): void => {
    const ast = parseJsonPath(source);
    const candidates = new Set(index.preFilter(ast));
    corpus.forEach((doc, id) => {
      let hit = false;
      try {
        hit = matchJsonPath(doc, ast);
      } catch {
        return;
      }
      if (hit) assert.ok(candidates.has(id), `false negative ${id} under ${source}`);
    });
  };

  it('forces full scan for outer negation', () => {
    assert.equal(extractIndexPlan(parseJsonPath('$.a ? (!(@.b == 1))')).fullScan, true);
    assert.equal(extractIndexPlan(parseJsonPath('$.a ? (!(exists(@.b)))')).fullScan, true);
  });

  it('degrades an OR containing a negated branch to full scan', () => {
    const plan = extractIndexPlan(parseJsonPath('$.a ? (@.b == 1 || !(@.c == 2))'));
    assert.equal(plan, FULL_SCAN);
  });

  it('keeps OR unions selective when both branches are safe', () => {
    const plan = extractIndexPlan(parseJsonPath('$.a ? (@.b == 1 || @.c == 2)'));
    assert.equal(plan.fullScan, false);
    assert.ok(plan.groups.length === 2);
  });

  it('preserves recall across lax auto-unwrapping and nested arrays', () => {
    recall('lax $.a.b');
    recall('lax $.a[*].b');
    recall('lax $.**.b');
    recall('strict $.**.b');
  });

  it('preserves recall for polymorphic comparisons', () => {
    recall('lax $.a ? (@.b == 1)');
    recall('lax $.a ? (@.b >= 0)');
    recall('lax $.a ? (@.b != 1)');
    recall('lax $.a ? (@.b == "string")');
  });

  it('handles root-level subscripts without inventing facts', () => {
    recall('lax $[0]');
    recall('lax $[0 to 1]');
    recall('lax $[*]');
  });

  it('emits only array-transparent facts', () => {
    const facts = collectFacts({ a: [{ b: 1 }, { b: 2 }] }).map(encodeFact);
    assert.ok(facts.includes('k:a'));
    assert.ok(facts.includes('k:a.b'));
    assert.ok(facts.includes('v:a.b=n:1'));
    // No fact may encode an array index segment.
    assert.ok(facts.every((fact) => !fact.split(/[:=]/)[1]?.split('.').some((segment) => /^\d+$/.test(segment))));
  });

  it('still returns structural errors in strict mode while lax returns empty', () => {
    // Lax auto-unwraps the array and reaches the nested member.
    assert.deepEqual(queryJsonPath({ a: [{ b: 1 }] }, 'lax $.a.b'), [1]);
    // A genuinely missing member still yields an empty lax sequence.
    assert.deepEqual(queryJsonPath({ a: [{ b: 1 }] }, 'lax $.a.absent'), []);
    assert.throws(() => queryJsonPath({ a: [{ b: 1 }] }, 'strict $.a.b'));
  });
});

describe('plan algebra', () => {
  it('intersects and unions plans without dropping constraints', () => {
    const left = extractIndexPlan(parseJsonPath('$.a ? (@.x == 1)'));
    const right = extractIndexPlan(parseJsonPath('$.a ? (@.y == 2)'));
    const merged = extractIndexPlan(parseJsonPath('$.a ? (@.x == 1 && @.y == 2)'));
    assert.equal(merged.fullScan, false);
    assert.ok(merged.groups[0].exact.length >= left.groups[0].exact.length);
    assert.ok(merged.groups[0].exact.length >= right.groups[0].exact.length);
  });
});
