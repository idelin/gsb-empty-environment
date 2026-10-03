'use strict';

const assert = require('node:assert');
const { test } = require('./helper');
const {
  parse,
  decorrelate,
  renderRewrittenSql,
  buildPlan,
  explain,
  unnest,
  executeBaseline,
  executePlan,
} = require('../lib');

const ACCEPTANCE_SQL =
  'SELECT * FROM depts d WHERE EXISTS (SELECT 1 FROM emps e WHERE e.dept_id = d.id)';

const db = {
  depts: [
    { id: 1, name: 'Engineering' },
    { id: 2, name: 'Sales' },
    { id: 3, name: 'HR' },
    { id: 4, name: 'Legal' },
  ],
  emps: [
    { id: 101, dept_id: 1, name: 'Alice', salary: 120000 },
    { id: 102, dept_id: 1, name: 'Bob', salary: 95000 },
    { id: 103, dept_id: 2, name: 'Carol', salary: 80000 },
    { id: 104, dept_id: 2, name: 'Dave', salary: 72000 },
    { id: 105, dept_id: 2, name: 'Erin', salary: 110000 },
  ],
};

test('parser: parses correlated EXISTS subquery into AST', () => {
  const stmt = parse(ACCEPTANCE_SQL);
  assert.strictEqual(stmt.from.length, 1);
  assert.deepStrictEqual(stmt.from[0], { table: 'depts', alias: 'd' });
  assert.strictEqual(stmt.where.kind, 'exists');
  assert.strictEqual(stmt.where.negated, false);
  const sub = stmt.where.subquery;
  assert.deepStrictEqual(sub.from[0], { table: 'emps', alias: 'e' });
  assert.strictEqual(sub.where.kind, 'binary');
  assert.strictEqual(sub.where.op, '=');
});

test('acceptance: EXISTS is decorrelated into a SEMI join', () => {
  const result = decorrelate(parse(ACCEPTANCE_SQL));
  assert.strictEqual(result.changed, true);
  assert.strictEqual(result.joins.length, 1);
  const join = result.joins[0];
  assert.strictEqual(join.type, 'SEMI');
  assert.strictEqual(join.table, 'emps');
  assert.deepStrictEqual(join.keys, [{ outer: 'd.id', inner: 'e.dept_id' }]);
  assert.strictEqual(result.residual.length, 0);
});

test('acceptance: rewritten SQL uses SEMI JOIN, no correlated subquery remains', () => {
  const { rewritten } = unnest(ACCEPTANCE_SQL);
  assert.strictEqual(
    rewritten,
    'SELECT d.* FROM depts d SEMI JOIN emps e ON d.id = e.dept_id',
  );
  assert.ok(!/EXISTS/.test(rewritten));
});

test('acceptance: physical plan is a Hash Semi Join', () => {
  const { plan, explain: text } = unnest(ACCEPTANCE_SQL);
  const joinNode = plan.children[0];
  assert.strictEqual(joinNode.op, 'HashSemiJoin');
  assert.deepStrictEqual(joinNode.keys, [{ outer: 'd.id', inner: 'e.dept_id' }]);
  assert.ok(text.includes('HashSemiJoin'));
});

test('NOT EXISTS is decorrelated into a Hash Anti Join', () => {
  const { plan, rewritten } = unnest(
    'SELECT * FROM depts d WHERE NOT EXISTS (SELECT 1 FROM emps e WHERE e.dept_id = d.id)',
  );
  assert.strictEqual(plan.children[0].op, 'HashAntiJoin');
  assert.ok(rewritten.includes('ANTI JOIN'));
});

test('correlated IN is decorrelated into a SEMI join', () => {
  const result = decorrelate(
    parse('SELECT * FROM emps e WHERE e.dept_id IN (SELECT d.id FROM depts d)'),
  );
  assert.strictEqual(result.changed, true);
  assert.strictEqual(result.joins[0].type, 'SEMI');
  assert.deepStrictEqual(result.joins[0].keys, [
    { outer: 'e.dept_id', inner: 'd.id' },
  ]);
});

test('NOT IN is decorrelated into an ANTI join', () => {
  const result = decorrelate(
    parse('SELECT * FROM emps e WHERE e.dept_id NOT IN (SELECT d.id FROM depts d)'),
  );
  assert.strictEqual(result.joins[0].type, 'ANTI');
});

test('inner-local predicates become filters on the build side', () => {
  const { result, plan } = unnest(
    'SELECT * FROM depts d WHERE d.id IN (SELECT e.dept_id FROM emps e WHERE e.salary > 100000)',
  );
  assert.strictEqual(result.joins[0].filters.length, 1);
  const filter = plan.children[0].children[1];
  assert.strictEqual(filter.op, 'Filter');
  assert.strictEqual(filter.children[0].op, 'SeqScan');
  assert.strictEqual(filter.children[0].table, 'emps');
});

test('extra correlated equality inside EXISTS adds a join key', () => {
  const result = decorrelate(
    parse(
      "SELECT * FROM depts d WHERE EXISTS (SELECT 1 FROM emps e WHERE e.dept_id = d.id AND e.name = d.name)",
    ),
  );
  assert.strictEqual(result.joins[0].keys.length, 2);
});

test('non-equality correlation is left as residual (safe bail-out)', () => {
  const result = decorrelate(
    parse(
      'SELECT * FROM depts d WHERE EXISTS (SELECT 1 FROM emps e WHERE e.salary > d.id)',
    ),
  );
  assert.strictEqual(result.changed, false);
  assert.strictEqual(result.joins.length, 0);
  assert.strictEqual(result.residual.length, 1);
  assert.ok(result.notes.some((n) => n.includes('kept as-is')));
});

test('OR-ed subquery is not unnested (only top-level conjuncts)', () => {
  const result = decorrelate(
    parse(
      "SELECT * FROM depts d WHERE d.id = 1 OR EXISTS (SELECT 1 FROM emps e WHERE e.dept_id = d.id)",
    ),
  );
  assert.strictEqual(result.changed, false);
});

test('outer query with multiple tables is not rewritten', () => {
  const result = decorrelate(
    parse(
      'SELECT * FROM depts d, regions r WHERE EXISTS (SELECT 1 FROM emps e WHERE e.dept_id = d.id)',
    ),
  );
  assert.strictEqual(result.changed, false);
});

test('multiple subqueries become a chain of semi joins', () => {
  const { result, rewritten } = unnest(
    'SELECT * FROM depts d WHERE EXISTS (SELECT 1 FROM emps e WHERE e.dept_id = d.id) ' +
      'AND d.id IN (SELECT m.dept_id FROM managers m)',
  );
  assert.strictEqual(result.joins.length, 2);
  assert.ok((rewritten.match(/SEMI JOIN/g) || []).length === 2);
});

test('engine: decorrelated result matches correlated baseline', () => {
  const cases = [
    ACCEPTANCE_SQL,
    'SELECT * FROM depts d WHERE NOT EXISTS (SELECT 1 FROM emps e WHERE e.dept_id = d.id)',
    'SELECT * FROM depts d WHERE d.id IN (SELECT e.dept_id FROM emps e WHERE e.salary > 100000)',
    'SELECT * FROM emps e WHERE e.dept_id IN (SELECT d.id FROM depts d)',
    'SELECT * FROM emps e WHERE e.dept_id NOT IN (SELECT d.id FROM depts d)',
  ];
  for (const sql of cases) {
    const { plan } = unnest(sql);
    const baseline = executeBaseline(parse(sql), db);
    const optimized = executePlan(plan, db);
    assert.deepStrictEqual(optimized.rows, baseline.rows, `mismatch for: ${sql}`);
  }
});

test('engine: decorrelation eliminates per-row subquery executions', () => {
  const { plan } = unnest(ACCEPTANCE_SQL);
  const baseline = executeBaseline(parse(ACCEPTANCE_SQL), db);
  const optimized = executePlan(plan, db);
  assert.strictEqual(baseline.stats.subqueryExecutions, db.depts.length);
  assert.strictEqual(optimized.stats.subqueryExecutions, 0);
  assert.strictEqual(optimized.stats.hashBuilds, 1);
  assert.ok(
    optimized.stats.rowsExamined < baseline.stats.rowsExamined,
    'hash semi join should examine fewer rows',
  );
});

test('explain output renders the operator tree', () => {
  const text = explain(buildPlan(decorrelate(parse(ACCEPTANCE_SQL))));
  assert.ok(text.includes('Project'));
  assert.ok(text.includes('HashSemiJoin  [d.id = e.dept_id]'));
  assert.ok(text.includes('SeqScan on depts d'));
  assert.ok(text.includes('SeqScan on emps e'));
});
