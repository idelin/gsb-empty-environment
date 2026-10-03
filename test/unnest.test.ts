import { test } from 'node:test';
import assert from 'node:assert/strict';
import { parse } from '../src/parser.ts';
import { unnest } from '../src/unnest.ts';
import { formatSelect } from '../src/format.ts';
import { rewrite } from '../src/index.ts';

const ACCEPTANCE =
  'SELECT * FROM depts d WHERE EXISTS (SELECT 1 FROM emps e WHERE e.dept_id = d.id)';

test('验收：相关 EXISTS 重写为 SEMI JOIN，消除相关性', () => {
  const r = rewrite(ACCEPTANCE);
  // 不再有 EXISTS / 子查询
  assert.ok(!r.rewritten.includes('EXISTS'), '重写结果不应包含 EXISTS');
  assert.ok(!r.rewritten.includes('SELECT 1'), '子查询应被展开');
  // 转换为 SEMI JOIN，连接键保留关联条件
  assert.ok(r.rewritten.includes('SEMI JOIN'), '应生成 SEMI JOIN');
  assert.equal(
    r.rewritten,
    'SELECT d.* FROM depts d SEMI JOIN emps e ON e.dept_id = d.id',
  );
});

test('验收：物理计划为 HashSemiJoin，emps 为 build 侧', () => {
  const r = rewrite(ACCEPTANCE);
  assert.ok(r.planText.includes('HashSemiJoin'), '计划应包含 HashSemiJoin');
  assert.ok(!r.planText.includes('NestedLoop'), '不应出现嵌套循环逐行执行');

  const project = r.plan as any;
  const join = project.input;
  assert.equal(join.type, 'HashSemiJoin');
  assert.deepEqual(join.probeKeys, ['d.id']);
  assert.deepEqual(join.buildKeys, ['e.dept_id']);
  assert.equal(join.probe.type, 'SeqScan');
  assert.equal(join.probe.table, 'depts');
  assert.equal(join.build.type, 'SeqScan');
  assert.equal(join.build.table, 'emps');
});

test('SELECT * 在 Semi Join 下只展开为左表列', () => {
  const r = rewrite(ACCEPTANCE);
  assert.ok(r.rewritten.startsWith('SELECT d.* FROM'), '星号应展开为 d.*');
});

test('IN 子查询（非相关）重写为 SEMI JOIN', () => {
  const r = rewrite(
    'SELECT * FROM depts d WHERE d.id IN (SELECT e.dept_id FROM emps e)',
  );
  assert.equal(
    r.rewritten,
    'SELECT d.* FROM depts d SEMI JOIN emps e ON d.id = e.dept_id',
  );
  assert.ok(r.planText.includes('HashSemiJoin'));
});

test('IN 子查询的局部谓词并入 ON 条件', () => {
  const r = rewrite(
    'SELECT * FROM depts d WHERE d.id IN (SELECT e.dept_id FROM emps e WHERE e.salary > 5000)',
  );
  assert.equal(
    r.rewritten,
    'SELECT d.* FROM depts d SEMI JOIN emps e ON d.id = e.dept_id AND e.salary > 5000',
  );
});

test('相关 IN 子查询同样被解除', () => {
  const r = rewrite(
    'SELECT * FROM depts d WHERE d.id IN (SELECT e.dept_id FROM emps e WHERE e.region = d.region)',
  );
  assert.ok(!r.rewritten.includes(' IN ('));
  assert.ok(r.rewritten.includes('SEMI JOIN'));
  assert.ok(r.rewritten.includes('d.id = e.dept_id'));
  assert.ok(r.rewritten.includes('e.region = d.region'));
});

test('NOT EXISTS 重写为 ANTI JOIN / HashAntiJoin', () => {
  const r = rewrite(
    'SELECT d.id FROM depts d WHERE NOT EXISTS (SELECT 1 FROM emps e WHERE e.dept_id = d.id)',
  );
  assert.equal(
    r.rewritten,
    'SELECT d.id FROM depts d ANTI JOIN emps e ON e.dept_id = d.id',
  );
  assert.ok(r.planText.includes('HashAntiJoin'));
});

test('NOT IN 重写为 ANTI JOIN', () => {
  const r = rewrite(
    'SELECT * FROM depts d WHERE d.id NOT IN (SELECT e.dept_id FROM emps e)',
  );
  assert.ok(r.rewritten.includes('ANTI JOIN'));
  assert.ok(r.planText.includes('HashAntiJoin'));
});

test('外层残余谓词保留在 WHERE（Filter）中', () => {
  const r = rewrite(
    "SELECT * FROM depts d WHERE d.region = 'APAC' AND EXISTS (SELECT 1 FROM emps e WHERE e.dept_id = d.id)",
  );
  assert.ok(r.rewritten.includes("WHERE d.region = 'APAC'"), '外层谓词应保留');
  assert.ok(r.rewritten.includes('SEMI JOIN'));
  assert.ok(r.planText.includes('Filter'), '计划应包含 Filter 节点');
  assert.ok(r.planText.indexOf('Filter') < r.planText.indexOf('HashSemiJoin'));
});

test('多个 EXISTS 合取生成多个 Semi Join', () => {
  const r = rewrite(
    'SELECT * FROM depts d WHERE EXISTS (SELECT 1 FROM emps e WHERE e.dept_id = d.id) AND EXISTS (SELECT 1 FROM budgets b WHERE b.dept_id = d.id)',
  );
  const count = (r.rewritten.match(/SEMI JOIN/g) || []).length;
  assert.equal(count, 2);
  assert.ok(!r.rewritten.includes('EXISTS'));
});

test('非相关 EXISTS（无条件子查询）重写为 ON TRUE 的 Semi Join', () => {
  const r = rewrite('SELECT * FROM depts d WHERE EXISTS (SELECT 1 FROM emps e)');
  assert.equal(
    r.rewritten,
    'SELECT d.* FROM depts d SEMI JOIN emps e ON TRUE',
  );
});

test('无子查询的普通查询保持不变', () => {
  const sql = "SELECT d.id FROM depts d WHERE d.region = 'APAC'";
  const r = rewrite(sql);
  assert.equal(r.rewritten, "SELECT d.id FROM depts d WHERE d.region = 'APAC'");
  assert.ok(!r.planText.includes('Join'));
});

test('嵌套子查询被递归解除', () => {
  const r = rewrite(
    'SELECT * FROM depts d WHERE EXISTS (SELECT 1 FROM emps e WHERE e.dept_id = d.id AND EXISTS (SELECT 1 FROM payroll p WHERE p.emp_id = e.id))',
  );
  assert.ok(!r.rewritten.includes('EXISTS'), '嵌套 EXISTS 也应被消除');
  const count = (r.rewritten.match(/SEMI JOIN/g) || []).length;
  assert.equal(count, 2);
});

test('解析器往返：parse -> format 结构稳定', () => {
  const sql = 'SELECT a.id, a.name FROM t a WHERE a.x = 1 AND a.y < 2';
  const once = formatSelect(parse(sql));
  assert.equal(formatSelect(parse(once)), once);
});

test('unnest 幂等：重写结果再次重写不变', () => {
  const once = formatSelect(unnest(parse(ACCEPTANCE)));
  const twice = formatSelect(unnest(parse(once)));
  assert.equal(once, twice);
});
