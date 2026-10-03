import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, readFileSync, readdirSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createHash } from 'node:crypto';
import { buildRollbackPackage, writeRollbackPackage } from '../src/rollback-package.ts';
import { InMemoryTable } from '../src/store.ts';
import type { CdcEvent } from '../src/types.ts';

function makeLog(): { table: InMemoryTable; log: CdcEvent[]; mistakeTx: string } {
  const table = new InMemoryTable('users', 'id');
  const log: CdcEvent[] = [];
  table.onCdc((e) => log.push(e));
  const seed = table.beginTx();
  table.insert({ id: 1, name: 'alice' }, seed);
  table.insert({ id: 2, name: 'bob' }, seed);
  const mistakeTx = table.beginTx();
  table.update(() => true, { name: null }, mistakeTx);
  return { table, log, mistakeTx };
}

test('回滚包清单字段完整且校验和与 SQL 一致', () => {
  const { log, mistakeTx } = makeLog();
  const pkg = buildRollbackPackage(log, { txId: mistakeTx });
  assert.equal(pkg.manifest.eventCount, 2);
  assert.equal(pkg.manifest.statementCount, 2);
  assert.deepEqual(pkg.manifest.sequenceRange, { min: 3, max: 4 });
  assert.equal(pkg.manifest.sha256, createHash('sha256').update(pkg.sql).digest('hex'));
  assert.equal(pkg.statements.length, 2);
});

test('回滚包落盘：manifest.json / statements.json / rollback.sql', () => {
  const { log, mistakeTx } = makeLog();
  const pkg = buildRollbackPackage(log, { txId: mistakeTx });
  const dir = mkdtempSync(join(tmpdir(), 'rollback-'));
  writeRollbackPackage(pkg, dir);
  assert.deepEqual(readdirSync(dir).sort(), ['manifest.json', 'rollback.sql', 'statements.json']);
  assert.equal(readFileSync(join(dir, 'rollback.sql'), 'utf8'), pkg.sql);
  assert.deepEqual(JSON.parse(readFileSync(join(dir, 'manifest.json'), 'utf8')), pkg.manifest);
});

test('筛选条件无匹配事件时拒绝生成空包', () => {
  const { log } = makeLog();
  assert.throws(() => buildRollbackPackage(log, { txId: 'tx-404' }), /没有可冲正/);
});
