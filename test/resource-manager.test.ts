import { test } from 'node:test';
import assert from 'node:assert/strict';
import * as fs from 'node:fs';
import * as path from 'node:path';
import * as os from 'node:os';
import { spawnSync } from 'node:child_process';
import { ResourceManager, XaError } from '../src/resource-manager.ts';

function tmpDir(): string {
  return fs.mkdtempSync(path.join(os.tmpdir(), 'xa-rm-'));
}

test('prepare persists the promise and locks the resource', () => {
  const dir = tmpDir();
  const rm = new ResourceManager(dir);
  rm.prepare('tx-1', [{ type: 'put', key: 'account:A', value: 100 }]);

  assert.equal(rm.isLocked('account:A'), true);
  assert.equal(rm.lockHolder('account:A'), 'tx-1');
  assert.deepEqual(rm.recoverScan(), ['tx-1']);
  // prepared data is NOT yet visible in the committed store
  assert.equal(rm.read('account:A'), undefined);
  // WAL record exists on disk before any commit
  const wal = fs.readFileSync(path.join(dir, 'rm-wal.log'), 'utf8');
  assert.match(wal, /"state":"prepared"/);
  rm.close();
});

test('restart after prepare: promise survives, lock is re-acquired', () => {
  const dir = tmpDir();
  const rm1 = new ResourceManager(dir);
  rm1.prepare('tx-1', [{ type: 'put', key: 'account:A', value: 100 }]);
  rm1.close(); // simulate shutdown; in-memory state is gone

  const rm2 = new ResourceManager(dir); // "restarted" process
  assert.deepEqual(rm2.recoverScan(), ['tx-1'], 'in-doubt txn recovered');
  assert.equal(rm2.isLocked('account:A'), true, 'lock still held after restart');
  assert.equal(rm2.read('account:A'), undefined, 'still not committed');
  rm2.close();
});

test('explicit commit after restart releases the lock and applies data', () => {
  const dir = tmpDir();
  new ResourceManager(dir).prepare('tx-1', [
    { type: 'put', key: 'account:A', value: 100 },
  ]);

  const rm = new ResourceManager(dir); // restarted
  rm.commit('tx-1');
  assert.equal(rm.isLocked('account:A'), false);
  assert.equal(rm.read('account:A'), 100);
  assert.deepEqual(rm.recoverScan(), []);
  rm.close();

  const rm3 = new ResourceManager(dir); // committed data survives restarts
  assert.equal(rm3.read('account:A'), 100);
  assert.equal(rm3.isLocked('account:A'), false);
  rm3.close();
});

test('rollback after restart releases the lock without applying data', () => {
  const dir = tmpDir();
  new ResourceManager(dir).prepare('tx-1', [
    { type: 'put', key: 'account:A', value: 100 },
  ]);

  const rm = new ResourceManager(dir);
  rm.rollback('tx-1');
  assert.equal(rm.isLocked('account:A'), false);
  assert.equal(rm.read('account:A'), undefined);
  assert.deepEqual(rm.recoverScan(), []);
  rm.close();
});

test('locked key rejects local writes until commit/rollback', () => {
  const dir = tmpDir();
  const rm = new ResourceManager(dir);
  rm.prepare('tx-1', [{ type: 'put', key: 'k', value: 1 }]);
  assert.throws(() => rm.write('k', 2), (e: unknown) => e instanceof XaError && (e as XaError).code === 'XA_LOCKED');
  rm.commit('tx-1');
  rm.write('k', 2); // now allowed
  assert.equal(rm.read('k'), 2);
  rm.close();
});

test('XA error semantics: duplicate prepare, unknown commit/rollback', () => {
  const dir = tmpDir();
  const rm = new ResourceManager(dir);
  rm.prepare('tx-1', [{ type: 'put', key: 'k', value: 1 }]);
  assert.throws(() => rm.prepare('tx-1', []), (e: unknown) => (e as XaError).code === 'XAER_DUPID');
  assert.throws(() => rm.commit('nope'), (e: unknown) => (e as XaError).code === 'XAER_NOTA');
  assert.throws(() => rm.rollback('nope'), (e: unknown) => (e as XaError).code === 'XAER_NOTA');
  rm.close();
});

test('conflicting prepare on a locked key is rejected', () => {
  const dir = tmpDir();
  const rm = new ResourceManager(dir);
  rm.prepare('tx-1', [{ type: 'put', key: 'k', value: 1 }]);
  assert.throws(
    () => rm.prepare('tx-2', [{ type: 'put', key: 'k', value: 2 }]),
    (e: unknown) => (e as XaError).code === 'XA_LOCKED',
  );
  rm.close();
});

test('acceptance: SIGKILL after prepare, restart, lock held until explicit commit', () => {
  const dir = tmpDir();
  const childScript = path.resolve('demo/child.ts');
  const node = process.execPath;

  // 1. child process prepares, then is SIGKILLed (no cleanup possible)
  const prep = spawnSync(node, ['--experimental-strip-types', childScript, 'prepare', dir], {
    encoding: 'utf8',
    killSignal: 'SIGKILL',
    timeout: 3000, // child hangs after prepare; timeout delivers SIGKILL
  });
  assert.equal(prep.status, null, 'child was killed by a signal');
  assert.equal(prep.signal, 'SIGKILL');
  assert.match(prep.stdout, /PREPARED tx-kill/);

  // 2. new process recovers: lock must still be held, data not applied
  const check = spawnSync(node, ['--experimental-strip-types', childScript, 'check', dir], {
    encoding: 'utf8',
  });
  assert.equal(check.status, 0, check.stderr);
  assert.match(check.stdout, /LOCKED account:A by tx-kill/);
  assert.match(check.stdout, /NOT_COMMITTED account:A/);

  // 3. explicit commit from the coordinator releases the lock and persists
  const commit = spawnSync(node, ['--experimental-strip-types', childScript, 'commit', dir], {
    encoding: 'utf8',
  });
  assert.equal(commit.status, 0, commit.stderr);
  assert.match(commit.stdout, /COMMITTED tx-kill/);

  // 4. final state: value durable, lock gone
  const verify = spawnSync(node, ['--experimental-strip-types', childScript, 'verify', dir], {
    encoding: 'utf8',
  });
  assert.equal(verify.status, 0, verify.stderr);
  assert.match(verify.stdout, /VALUE account:A = 42/);
  assert.match(verify.stdout, /UNLOCKED account:A/);
});
