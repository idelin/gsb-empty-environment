import { test } from 'node:test';
import assert from 'node:assert/strict';
import { WalManager } from '../src/wal.ts';
import { LogRecordType } from '../src/log-record.ts';

test('LSNs are assigned sequentially by the deterministic clock', () => {
  const wal = new WalManager();
  const r1 = wal.append({ type: LogRecordType.COMMIT, prevLSN: 0, txnId: 1 });
  const r2 = wal.append({ type: LogRecordType.COMMIT, prevLSN: 1, txnId: 2 });
  const r3 = wal.append({ type: LogRecordType.END, prevLSN: 2, txnId: 2 });
  assert.equal(r1.lsn, 1);
  assert.equal(r2.lsn, 2);
  assert.equal(r3.lsn, 3);
  assert.equal(wal.getLastLSN(), 3);
});

test('flush beyond end of log is rejected', () => {
  const wal = new WalManager();
  wal.append({ type: LogRecordType.COMMIT, prevLSN: 0, txnId: 1 });
  assert.throws(() => wal.flush(2), /cannot flush beyond/);
});

test('persistToFile contains only the durable (flushed) prefix', () => {
  const wal = new WalManager();
  wal.append({ type: LogRecordType.COMMIT, prevLSN: 0, txnId: 1 });
  wal.append({ type: LogRecordType.END, prevLSN: 1, txnId: 1 });
  wal.flush(1);
  const file = wal.persistToFile();
  const lines = file.split('\n');
  assert.equal(lines.length, 1);
  assert.equal(JSON.parse(lines[0]!).lsn, 1);
});

test('WAL file round-trips through loadFromFile', () => {
  const wal = new WalManager();
  wal.append({ type: LogRecordType.UPDATE, prevLSN: 0, txnId: 1, pageId: 'P', key: 'k', before: 1, after: 2 });
  wal.append({ type: LogRecordType.COMMIT, prevLSN: 1, txnId: 1 });
  wal.flush(2);
  const loaded = WalManager.loadFromFile(wal.persistToFile());
  assert.equal(loaded.getLastLSN(), 2);
  assert.equal(loaded.getFlushedLSN(), 2);
  assert.deepEqual(loaded.all(), wal.all());
});

test('loadFromFile rejects corrupt files with LSN gaps', () => {
  const wal = new WalManager();
  wal.append({ type: LogRecordType.COMMIT, prevLSN: 0, txnId: 1 });
  wal.append({ type: LogRecordType.END, prevLSN: 1, txnId: 1 });
  wal.flush(2);
  const lines = wal.persistToFile().split('\n');
  const corrupt = [lines[0], JSON.stringify({ ...JSON.parse(lines[1]!), lsn: 7 })].join('\n');
  assert.throws(() => WalManager.loadFromFile(corrupt), /LSN gap/);
  assert.throws(() => WalManager.loadFromFile('{"not":"a record"}'), /malformed/);
});

test('empty WAL file loads as an empty log', () => {
  const wal = WalManager.loadFromFile('');
  assert.equal(wal.getLastLSN(), 0);
  assert.equal(wal.all().length, 0);
});
