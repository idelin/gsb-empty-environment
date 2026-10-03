import { test } from 'node:test';
import assert from 'node:assert/strict';
import { InMemoryTable } from '../src/store.ts';
import { buildRollbackPackage } from '../src/rollback-package.ts';
import { replayStatements } from '../src/replay.ts';
import type { CdcEvent } from '../src/types.ts';

test('验收边界：误 UPDATE 清空 1000 条用户信息，引擎生成 1000 条反向 SQL 并毫秒级复原', () => {
  const users = new InMemoryTable('users', 'id');
  const cdcLog: CdcEvent[] = [];
  users.onCdc((e) => cdcLog.push(e));

  const seed = users.beginTx();
  for (let i = 1; i <= 1000; i++) {
    users.insert(
      { id: i, name: `user_${i}`, email: `user_${i}@example.com`, balance: 1000 + i },
      seed,
    );
  }
  const baseline = users.snapshot();

  // 误操作：单事务 UPDATE 清空 1000 行
  const mistakeTx = users.beginTx();
  const affected = users.update(() => true, { name: null, email: '', balance: 0 }, mistakeTx);
  assert.equal(affected, 1000);
  assert.ok(users.snapshot().every((r) => r.name === null && r.balance === 0));

  // 引擎读取 Before Image，生成 1000 条反向赋值 SQL
  const pkg = buildRollbackPackage(cdcLog, { txId: mistakeTx });
  assert.equal(pkg.manifest.statementCount, 1000);
  assert.ok(pkg.statements.every((s) => s.kind === 'UPDATE'));
  const sqlLines = pkg.sql.trim().split('\n').slice(1, -1);
  assert.equal(sqlLines.length, 1000);
  assert.match(
    sqlLines[0]!,
    /^UPDATE "users" SET "name" = 'user_1000', "email" = 'user_1000@example\.com', "balance" = 2000 WHERE "id" = 1000;$/,
  );

  // 回放冲正并校验毫秒级原貌（含逐行逐列深度一致）
  const { applied } = replayStatements(new Map([['users', users]]), pkg.statements);
  assert.equal(applied, 1000);
  assert.deepEqual(users.snapshot(), baseline);
});

test('混合误操作：INSERT/UPDATE/DELETE 组合可整体冲正', () => {
  const t = new InMemoryTable('orders', 'id');
  const log: CdcEvent[] = [];
  t.onCdc((e) => log.push(e));
  const seed = t.beginTx();
  t.insert({ id: 1, amount: 10 }, seed);
  t.insert({ id: 2, amount: 20 }, seed);
  const baseline = t.snapshot();

  const bad = t.beginTx();
  t.update((r) => r.id === 1, { amount: 0 }, bad);
  t.delete((r) => r.id === 2, bad);
  t.insert({ id: 3, amount: 999 }, bad);

  const pkg = buildRollbackPackage(log, { txId: bad });
  assert.deepEqual(
    pkg.statements.map((s) => s.kind),
    ['DELETE', 'INSERT', 'UPDATE'],
  );
  replayStatements(new Map([['orders', t]]), pkg.statements);
  assert.deepEqual(t.snapshot(), baseline);
});
