import assert from 'node:assert/strict';
import { spawn, spawnSync } from 'node:child_process';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { test } from 'node:test';
import { fileURLToPath } from 'node:url';
import { ResourceManager } from '../src/index.ts';

const worker = join(dirname(fileURLToPath(import.meta.url)), '..', 'demo', 'rm-worker.ts');

/**
 * Acceptance scenario: the RM process is SIGKILLed right after voting YES on
 * Prepare. A restarted RM must recover the PREPARED state, keep the lock,
 * and only release it on an explicit Commit from the coordinator.
 */
test('prepare promise survives SIGKILL; lock held until explicit commit', async () => {
  const dir = mkdtempSync(join(tmpdir(), 'xa-crash-'));
  try {
    // Phase 1: prepare, then kill -9 the process mid-flight.
    const preparer = spawn(process.execPath, [worker, dir, 'prepare', 'tx-1', 'account:A', '{"balance":50}']);
    let out = '';
    await new Promise<void>((resolve) => {
      preparer.stdout.on('data', (chunk) => {
        out += chunk;
        if (out.includes('PREPARED')) {
          preparer.kill('SIGKILL');
          resolve();
        }
      });
    });
    const exit = await new Promise<{ signal: string | null }>((resolve) =>
      preparer.on('close', (_code, signal) => resolve({ signal })),
    );
    assert.equal(exit.signal, 'SIGKILL');

    // Phase 2: a fresh process recovers; the lock must still be enforced.
    const check = spawnSync(process.execPath, [worker, dir, 'check', 'tx-1', 'account:A', 'tx-intruder'], {
      encoding: 'utf8',
    });
    assert.equal(check.status, 0, check.stdout + check.stderr);
    assert.match(check.stdout, /status\(tx-1\) = PREPARED/);
    assert.match(check.stdout, /locked\(account:A\) = true holder=tx-1/);
    assert.match(check.stdout, /conflict-check: write by tx-intruder rejected/);

    // Phase 3: explicit commit releases the lock and persists the value.
    const commit = spawnSync(process.execPath, [worker, dir, 'commit', 'tx-1'], { encoding: 'utf8' });
    assert.equal(commit.status, 0, commit.stdout + commit.stderr);

    const rm = ResourceManager.open(dir);
    assert.equal(rm.status('tx-1'), 'COMMITTED');
    assert.equal(rm.isLocked('account:A'), false);
    assert.deepEqual(rm.committedValue('account:A'), { balance: 50 });
    rm.close();
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('rollback after SIGKILL discards the in-doubt transaction', async () => {
  const dir = mkdtempSync(join(tmpdir(), 'xa-crash-'));
  try {
    const preparer = spawn(process.execPath, [worker, dir, 'prepare', 'tx-9', 'k', '"v"']);
    let out = '';
    await new Promise<void>((resolve) => {
      preparer.stdout.on('data', (chunk) => {
        out += chunk;
        if (out.includes('PREPARED')) {
          preparer.kill('SIGKILL');
          resolve();
        }
      });
    });
    await new Promise((resolve) => preparer.on('close', resolve));

    const rollback = spawnSync(process.execPath, [worker, dir, 'rollback', 'tx-9'], { encoding: 'utf8' });
    assert.equal(rollback.status, 0, rollback.stdout + rollback.stderr);

    const rm = ResourceManager.open(dir);
    assert.equal(rm.status('tx-9'), 'ROLLEDBACK');
    assert.equal(rm.isLocked('k'), false);
    assert.equal(rm.committedValue('k'), undefined);
    rm.close();
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});
