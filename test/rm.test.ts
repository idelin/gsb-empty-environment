import assert from 'node:assert/strict';
import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { test } from 'node:test';
import {
  InvalidStateError,
  LockConflictError,
  ResourceManager,
  UnknownTransactionError,
} from '../src/index.ts';

function freshDir(): string {
  return mkdtempSync(join(tmpdir(), 'xa-rm-'));
}

test('prepare -> commit applies staged writes and releases locks', () => {
  const dir = freshDir();
  const rm = ResourceManager.open(dir);
  rm.begin('tx-1');
  rm.write('tx-1', 'a', 1);
  assert.equal(rm.prepare('tx-1'), 'YES');
  assert.equal(rm.isLocked('a'), true);
  rm.commit('tx-1');
  assert.equal(rm.status('tx-1'), 'COMMITTED');
  assert.equal(rm.isLocked('a'), false);
  assert.equal(rm.committedValue('a'), 1);
  rm.close();
  rmSync(dir, { recursive: true, force: true });
});

test('rollback discards staged writes and releases locks', () => {
  const dir = freshDir();
  const rm = ResourceManager.open(dir);
  rm.begin('tx-1');
  rm.write('tx-1', 'a', 1);
  rm.prepare('tx-1');
  rm.rollback('tx-1');
  assert.equal(rm.status('tx-1'), 'ROLLEDBACK');
  assert.equal(rm.isLocked('a'), false);
  assert.equal(rm.committedValue('a'), undefined);
  rm.close();
  rmSync(dir, { recursive: true, force: true });
});

test('prepared record is fsynced to the WAL before prepare() returns', () => {
  const dir = freshDir();
  const rm = ResourceManager.open(dir);
  rm.begin('tx-1');
  rm.write('tx-1', 'a', { v: 42 });
  rm.prepare('tx-1');
  const wal = readFileSync(join(dir, 'wal.log'), 'utf8');
  assert.match(wal, /"op":"prepared"/);
  assert.match(wal, /"writes":\{"a":\{"v":42\}\}/);
  rm.close();
  rmSync(dir, { recursive: true, force: true });
});

test('restart recovers in-doubt tx: locks held, staged writes visible, commit applies', () => {
  const dir = freshDir();
  const before = ResourceManager.open(dir);
  before.begin('tx-1');
  before.write('tx-1', 'account:A', { balance: 50 });
  before.prepare('tx-1');
  before.close(); // simulates restart; crash case is covered by crash.test.ts

  const rm = ResourceManager.open(dir);
  assert.equal(rm.status('tx-1'), 'PREPARED');
  assert.equal(rm.isLocked('account:A'), true);
  assert.equal(rm.lockHolder('account:A'), 'tx-1');
  assert.deepEqual(rm.read('tx-1', 'account:A'), { balance: 50 });
  assert.throws(() => {
    rm.begin('tx-2');
    rm.write('tx-2', 'account:A', 'dirty');
  }, LockConflictError);

  rm.commit('tx-1');
  assert.equal(rm.isLocked('account:A'), false);
  assert.deepEqual(rm.committedValue('account:A'), { balance: 50 });
  rm.close();

  const again = ResourceManager.open(dir);
  assert.equal(again.status('tx-1'), 'COMMITTED');
  assert.deepEqual(again.committedValue('account:A'), { balance: 50 });
  again.close();
  rmSync(dir, { recursive: true, force: true });
});

test('rollback after restart releases locks without applying writes', () => {
  const dir = freshDir();
  const before = ResourceManager.open(dir);
  before.begin('tx-1');
  before.write('tx-1', 'k', 'v');
  before.prepare('tx-1');
  before.close();

  const rm = ResourceManager.open(dir);
  rm.rollback('tx-1');
  assert.equal(rm.isLocked('k'), false);
  assert.equal(rm.committedValue('k'), undefined);
  rm.close();
  rmSync(dir, { recursive: true, force: true });
});

test('protocol violations are rejected with XA-style errors', () => {
  const dir = freshDir();
  const rm = ResourceManager.open(dir);
  assert.throws(() => rm.commit('nope'), UnknownTransactionError);
  rm.begin('tx-1');
  assert.throws(() => rm.commit('tx-1'), InvalidStateError); // not prepared yet
  assert.throws(() => rm.begin('tx-1'), InvalidStateError); // duplicate begin
  rm.write('tx-1', 'a', 1);
  rm.prepare('tx-1');
  assert.throws(() => rm.write('tx-1', 'b', 2), InvalidStateError); // frozen after prepare
  rm.commit('tx-1');
  assert.throws(() => rm.commit('tx-1'), UnknownTransactionError); // no double commit
  rm.close();
  rmSync(dir, { recursive: true, force: true });
});

test('read-your-writes inside a transaction; committed data readable by others', () => {
  const dir = freshDir();
  const rm = ResourceManager.open(dir);
  rm.begin('tx-1');
  rm.write('tx-1', 'a', 1);
  rm.prepare('tx-1');
  rm.commit('tx-1');
  rm.begin('tx-2');
  assert.equal(rm.read('tx-2', 'a'), 1);
  rm.write('tx-2', 'a', 2);
  assert.equal(rm.read('tx-2', 'a'), 2);
  rm.rollback('tx-2');
  assert.equal(rm.committedValue('a'), 1);
  rm.close();
  rmSync(dir, { recursive: true, force: true });
});
