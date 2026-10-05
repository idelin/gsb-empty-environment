import { strict as assert } from 'assert';
import { evalPredicate, evalQuery, outputAttrs } from '../src/eval';
import { buildUniverse, enumerateDatabases, totalRows } from '../src/enumerate';
import { findDifference } from '../src/search';
import { Query, RelationSchema, Row } from '../src/types';

let passed = 0;
function test(name: string, fn: () => void): void {
  try {
    fn();
    passed++;
    console.log(`ok - ${name}`);
  } catch (err) {
    console.error(`FAIL - ${name}`);
    console.error(err);
    process.exit(1);
  }
}

const rel = (name: string): Query => ({ kind: 'relation', name });

const schemasAB: RelationSchema[] = [
  { name: 'R', attrs: ['a', 'b'], domains: [[0, 1], [0, 1]] },
  { name: 'S', attrs: ['a', 'b'], domains: [[0, 1], [0, 1]] },
];
const schemaMapAB = new Map(schemasAB.map((s) => [s.name, s]));

test('predicate evaluation: comparisons and boolean connectives', () => {
  const row: Row = { a: 1, b: 2 };
  assert.equal(evalPredicate({ op: 'eq', left: { attr: 'a' }, right: { const: 1 } }, row), true);
  assert.equal(evalPredicate({ op: 'lt', left: { attr: 'a' }, right: { attr: 'b' } }, row), true);
  assert.equal(
    evalPredicate(
      { op: 'and', args: [
        { op: 'ge', left: { attr: 'b' }, right: { const: 2 } },
        { op: 'not', arg: { op: 'ne', left: { attr: 'a' }, right: { const: 1 } } },
      ] },
      row,
    ),
    true,
  );
  assert.equal(
    evalPredicate(
      { op: 'or', args: [
        { op: 'eq', left: { attr: 'a' }, right: { const: 9 } },
        { op: 'eq', left: { attr: 'b' }, right: { const: 2 } },
      ] },
      row,
    ),
    true,
  );
});

test('universe is the lexicographic cartesian product', () => {
  const u = buildUniverse(schemasAB);
  assert.equal(u.length, 8);
  assert.deepEqual(u[0], { relation: 'R', row: { a: 0, b: 0 } });
  assert.deepEqual(u[3], { relation: 'R', row: { a: 1, b: 1 } });
  assert.deepEqual(u[4], { relation: 'S', row: { a: 0, b: 0 } });
});

test('enumeration is ordered by total rows then lexicographically', () => {
  const seen: string[] = [];
  for (const db of enumerateDatabases(schemasAB, 1)) {
    seen.push(JSON.stringify(db));
  }
  // 1 empty database + 8 singletons in universe order.
  assert.equal(seen.length, 9);
  const first = JSON.parse(seen[1]);
  assert.deepEqual(first.R, [{ a: 0, b: 0 }]);
  assert.deepEqual(first.S, []);
  const last = JSON.parse(seen[8]);
  assert.deepEqual(last.S, [{ a: 1, b: 1 }]);
});

test('set semantics: duplicate rows and projection collapse', () => {
  const db = { R: [{ a: 0, b: 0 }, { a: 0, b: 1 }, { a: 0, b: 0 }], S: [] };
  const q: Query = { kind: 'project', attrs: ['a'], input: rel('R') };
  const out = evalQuery(q, db, schemaMapAB);
  assert.equal(out.size, 1);
  const entry = [...out.values()][0];
  assert.deepEqual(entry.tuple, { a: 0 });
  // lineage dedupes the identical row but keeps both distinct source rows
  assert.equal(entry.lineage.size, 2);
});

test('equivalent: selection distributes over union', () => {
  const p = { op: 'eq' as const, left: { attr: 'a' }, right: { const: 0 } };
  const left: Query = { kind: 'select', where: p, input: { kind: 'union', left: rel('R'), right: rel('S') } };
  const right: Query = {
    kind: 'union',
    left: { kind: 'select', where: p, input: rel('R') },
    right: { kind: 'select', where: p, input: rel('S') },
  };
  const r = findDifference(schemasAB, left, right, 3);
  assert.equal(r.equivalentWithinBound, true);
});

test('counterexample: projection does not distribute over difference', () => {
  // project_a(R - S)  vs  project_a(R) - project_a(S)
  const left: Query = {
    kind: 'project', attrs: ['a'],
    input: { kind: 'difference', left: rel('R'), right: rel('S') },
  };
  const right: Query = {
    kind: 'difference',
    left: { kind: 'project', attrs: ['a'], input: rel('R') },
    right: { kind: 'project', attrs: ['a'], input: rel('S') },
  };
  const r = findDifference(schemasAB, left, right, 4);
  assert.equal(r.equivalentWithinBound, false);
  const cx = r.counterexample!;
  // minimal distinguishing instance: R={(0,0)}, S={(0,1)}
  assert.equal(cx.totalRows, 2);
  assert.deepEqual(cx.db.R, [{ a: 0, b: 0 }]);
  assert.deepEqual(cx.db.S, [{ a: 0, b: 1 }]);
  assert.deepEqual(cx.leftResult, [{ a: 0 }]);
  assert.deepEqual(cx.rightResult, []);
  assert.equal(cx.differences.length, 1);
  const d = cx.differences[0];
  assert.equal(d.side, 'left');
  assert.deepEqual(d.tuple, { a: 0 });
  assert.deepEqual(d.sources, [{ relation: 'R', row: { a: 0, b: 0 } }]);
});

test('counterexample: minimality respects total-rows-then-lexicographic order', () => {
  // select_{a=0}(R union S)  vs  select_{a=0}(R) union S
  const p = { op: 'eq' as const, left: { attr: 'a' }, right: { const: 0 } };
  const left: Query = { kind: 'select', where: p, input: { kind: 'union', left: rel('R'), right: rel('S') } };
  const right: Query = {
    kind: 'union',
    left: { kind: 'select', where: p, input: rel('R') },
    right: rel('S'),
  };
  const r = findDifference(schemasAB, left, right, 2);
  assert.equal(r.equivalentWithinBound, false);
  const cx = r.counterexample!;
  // smallest witness is a single S row failing the predicate; the first such
  // row in universe order is S={(1,0)}.
  assert.equal(cx.totalRows, 1);
  assert.deepEqual(cx.db.R, []);
  assert.deepEqual(cx.db.S, [{ a: 1, b: 0 }]);
  assert.equal(cx.differences.length, 1);
  assert.equal(cx.differences[0].side, 'right');
  assert.deepEqual(cx.differences[0].sources, [{ relation: 'S', row: { a: 1, b: 0 } }]);
});

test('lineage merges across union and projection', () => {
  // project_a(R union S) on R={(0,0)}, S={(0,1)}: tuple (a=0) has both sources.
  const q: Query = { kind: 'project', attrs: ['a'], input: { kind: 'union', left: rel('R'), right: rel('S') } };
  const out = evalQuery(q, { R: [{ a: 0, b: 0 }], S: [{ a: 0, b: 1 }] }, schemaMapAB);
  const entry = out.get(JSON.stringify([0]))!;
  const ids = [...entry.lineage.keys()].sort();
  assert.deepEqual(ids, ['R#[0,0]', 'S#[0,1]']);
});

test('schema validation: queries with mismatched output schemas are rejected', () => {
  const schemas: RelationSchema[] = [
    { name: 'R', attrs: ['a'], domains: [[0]] },
    { name: 'S', attrs: ['a', 'b'], domains: [[0], [0]] },
  ];
  assert.throws(
    () => findDifference(schemas, rel('R'), rel('S'), 1),
    /same output schema/,
  );
});

test('output attribute order follows projection and union left side', () => {
  const q: Query = { kind: 'project', attrs: ['b', 'a'], input: rel('R') };
  assert.deepEqual(outputAttrs(q, schemaMapAB), ['b', 'a']);
  const u: Query = { kind: 'union', left: q, right: { kind: 'project', attrs: ['b', 'a'], input: rel('S') } };
  assert.deepEqual(outputAttrs(u, schemaMapAB), ['b', 'a']);
});

test('totalRows counts every relation', () => {
  assert.equal(totalRows({ R: [{ a: 0 }], S: [{ a: 1 }, { a: 2 }] }), 3);
});

console.log(`\n${passed} tests passed`);
