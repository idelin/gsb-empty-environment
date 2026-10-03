/** WAL 管理器：序列化往返、LSN 全序、并发追加安全性、检查点定位。 */
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { WalWriter, WalIndex, readWal } from '../src/wal.ts';
import { LogType } from '../src/log-record.ts';

function withTempDir(fn: (dir: string) => void | Promise<void>): () => Promise<void> {
  return async () => {
    const dir = mkdtempSync(join(tmpdir(), 'aries-wal-'));
    try {
      await fn(dir);
    } finally {
      rmSync(dir, { recursive: true, force: true });
    }
  };
}

test('追加-读取往返：记录完整保真，LSN 从 1 连续递增', withTempDir((dir) => {
  const path = join(dir, 'wal.log');
  const wal = new WalWriter(path);
  const r1 = wal.append({ type: LogType.Update, txnId: 1, prevLSN: null, pageId: 'P1', before: 0, after: 5 });
  const r2 = wal.append({ type: LogType.Commit, txnId: 1, prevLSN: r1.lsn }, { fsync: true });
  const r3 = wal.append({ type: LogType.End, txnId: 1, prevLSN: r2.lsn });
  wal.close();

  assert.deepEqual([r1.lsn, r2.lsn, r3.lsn], [1, 2, 3]);
  const records = readWal(path);
  assert.equal(records.length, 3);
  assert.deepEqual(records[0], r1);
  assert.deepEqual(records[2], r3);
  assert.equal(wal.isClosed, true);
}));

test('关闭后追加抛错（句柄泄漏防护）', withTempDir((dir) => {
  const wal = new WalWriter(join(dir, 'wal.log'));
  wal.close();
  assert.throws(() => wal.append({ type: LogType.End, txnId: 1, prevLSN: null }), /after close/);
}));

test('重启后时钟从最大 LSN 恢复，不重发已用 LSN', withTempDir((dir) => {
  const path = join(dir, 'wal.log');
  const w1 = new WalWriter(path);
  w1.append({ type: LogType.End, txnId: 1, prevLSN: null });
  w1.append({ type: LogType.End, txnId: 2, prevLSN: null });
  w1.close();
  const w2 = new WalWriter(path);
  const r = w2.append({ type: LogType.End, txnId: 3, prevLSN: null });
  w2.close();
  assert.equal(r.lsn, 3);
}));

test('并发异步追加构成全序：无丢失、无交错、LSN 严格递增', withTempDir(async (dir) => {
  const path = join(dir, 'wal.log');
  const wal = new WalWriter(path);
  const N = 200;
  const promises = [];
  for (let i = 0; i < N; i += 1) {
    promises.push(wal.appendAsync({ type: LogType.End, txnId: i + 1, prevLSN: null }));
  }
  const records = await Promise.all(promises);
  wal.close();

  const lsns = records.map((r) => r.lsn).sort((a, b) => a - b);
  assert.deepEqual(lsns, Array.from({ length: N }, (_, i) => i + 1));
  const onDisk = readWal(path);
  assert.equal(onDisk.length, N);
  for (let i = 1; i < N; i += 1) {
    assert.ok(onDisk[i].lsn > onDisk[i - 1].lsn, 'WAL 内 LSN 必须严格递增');
  }
}));

test('非单调 LSN 的损坏 WAL 被拒绝（拓扑约束校验）', withTempDir((dir) => {
  const path = join(dir, 'wal.log');
  writeFileSync(path, '{"lsn":2,"type":"END","txnId":1,"prevLSN":null}\n{"lsn":2,"type":"END","txnId":1,"prevLSN":null}\n');
  assert.throws(() => readWal(path), /non-monotonic/);
}));

test('WalIndex：定位最后一个 END_CHECKPOINT 与二分查找', withTempDir((dir) => {
  const path = join(dir, 'wal.log');
  const wal = new WalWriter(path);
  wal.append({ type: LogType.BeginCheckpoint, txnId: null, prevLSN: null });
  wal.append({ type: LogType.EndCheckpoint, txnId: null, prevLSN: null, txnTable: [], dirtyPageTable: [] });
  wal.append({ type: LogType.End, txnId: 1, prevLSN: null });
  wal.append({ type: LogType.BeginCheckpoint, txnId: null, prevLSN: null });
  wal.append({ type: LogType.EndCheckpoint, txnId: null, prevLSN: null, txnTable: [], dirtyPageTable: [['P1', 1]] });
  wal.close();

  const index = new WalIndex(readWal(path));
  assert.equal(index.lastEndCheckpoint()?.lsn, 5);
  assert.equal(index.firstIndexAtOrAfter(3), 2);
  assert.equal(index.firstIndexAtOrAfter(99), 5);
  assert.equal(index.get(4).type, LogType.BeginCheckpoint);
  assert.throws(() => index.get(42), /not found/);
}));
