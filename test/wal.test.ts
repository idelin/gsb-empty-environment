import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { join } from 'node:path';
import { parseRecord } from '../src/log-record.ts';
import { Wal } from '../src/wal.ts';
import { cleanupDir, makeTempDir } from './helpers.ts';

test('append assigns sequential LSNs and chains prevLSN per transaction', async () => {
  const dir = await makeTempDir();
  try {
    const wal = await Wal.create(join(dir, 'wal.log'));
    const r1 = await wal.append({ kind: 'BEGIN', txnId: 'T1' });
    const r2 = await wal.append({ kind: 'BEGIN', txnId: 'T2' });
    const r3 = await wal.append({ kind: 'UPDATE', txnId: 'T1', pageId: 'P1', key: 'a', before: 0, after: 1 });
    const r4 = await wal.append({ kind: 'UPDATE', txnId: 'T2', pageId: 'P2', key: 'b', before: 0, after: 2 });
    const r5 = await wal.append({ kind: 'COMMIT', txnId: 'T1' });

    assert.deepEqual([r1.lsn, r2.lsn, r3.lsn, r4.lsn, r5.lsn], [1, 2, 3, 4, 5]);
    assert.equal(r1.prevLSN, null);
    assert.equal(r2.prevLSN, null);
    assert.equal(r3.prevLSN, 1);
    assert.equal(r4.prevLSN, 2);
    assert.equal(r5.prevLSN, 3);
    assert.equal(wal.size, 5);
    assert.equal(wal.pendingWrites, 0);
  } finally {
    await cleanupDir(dir);
  }
});

test('checkpoint records get no prevLSN and round-trip through disk', async () => {
  const dir = await makeTempDir();
  try {
    const path = join(dir, 'wal.log');
    const wal = await Wal.create(path);
    await wal.append({ kind: 'BEGIN', txnId: 'T1' });
    await wal.append({
      kind: 'CHECKPOINT',
      txnId: null,
      txnTable: [{ txnId: 'T1', status: 'running', lastLSN: 1 }],
      dirtyPageTable: [{ pageId: 'P9', recLSN: 1 }],
    });

    const reopened = await Wal.open(path);
    assert.equal(reopened.size, 2);
    const cp = reopened.scan()[1];
    assert.equal(cp.kind, 'CHECKPOINT');
    assert.equal(cp.prevLSN, null);
    if (cp.kind === 'CHECKPOINT') {
      assert.deepEqual(cp.txnTable, [{ txnId: 'T1', status: 'running', lastLSN: 1 }]);
      assert.deepEqual(cp.dirtyPageTable, [{ pageId: 'P9', recLSN: 1 }]);
    }

    // LSN sequence continues after reopen.
    const next = await reopened.append({ kind: 'COMMIT', txnId: 'T1' });
    assert.equal(next.lsn, 3);
    assert.equal(next.prevLSN, 1);
  } finally {
    await cleanupDir(dir);
  }
});

test('every appended record is persisted as one JSONL line (no leaks)', async () => {
  const dir = await makeTempDir();
  try {
    const path = join(dir, 'wal.log');
    const wal = await Wal.create(path);
    for (let i = 0; i < 10; i++) {
      await wal.append({ kind: 'UPDATE', txnId: 'T1', pageId: 'P1', key: `k${i}`, before: null, after: i });
    }
    const lines = (await readFile(path, 'utf8')).split('\n').filter((l) => l.trim() !== '');
    assert.equal(lines.length, 10);
    assert.equal(wal.pendingWrites, 0);
  } finally {
    await cleanupDir(dir);
  }
});

test('parseRecord rejects malformed input', () => {
  assert.throws(() => parseRecord('{"kind":"WAT","lsn":1,"prevLSN":null,"txnId":"T1"}'));
  assert.throws(() => parseRecord('{"kind":"BEGIN","lsn":0,"prevLSN":null,"txnId":"T1"}'));
  assert.throws(() => parseRecord('{"kind":"BEGIN","lsn":1,"prevLSN":null}'));
  assert.throws(() => parseRecord('"just a string"'));
  assert.throws(() => parseRecord('not json'));
});
