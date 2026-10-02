import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { CorruptionError, UnsupportedVersionError } from "../src/binary.ts";
import { FORMAT_VERSION, RecordType, type LogRecord } from "../src/logrecord.ts";
import {
  WAL_HEADER_SIZE,
  WalWriter,
  encodeWalHeader,
  mapWal,
  readRecordAt,
  scanWal,
  scanWalFrames,
} from "../src/wal.ts";
function tmpdirPath(): string {
  return mkdtempSync(join(tmpdir(), "aries-wal-test-"));
}
function updateRec(txnId: number, pageId: number, tag: string): LogRecord {
  return {
    type: RecordType.UPDATE, lsn: 0, txnId, prevLSN: 0,
    pageId, offset: 0, beforeImage: Buffer.from(""), afterImage: Buffer.from(tag),
  };
}

test("append assigns increasing LSNs equal to frame offsets", async () => {
  const dir = tmpdirPath();
  const wal = await WalWriter.create(join(dir, "w.log"));
  const lsns: number[] = [];
  for (let i = 0; i < 5; i++) lsns.push(await wal.append(updateRec(1, 1, `rec-${i}`)));
  await wal.flush();
  await wal.close();
  assert.ok(wal.closed);
  for (let i = 1; i < lsns.length; i++) assert.ok(lsns[i] > lsns[i - 1]);
  assert.equal(lsns[0], WAL_HEADER_SIZE);
  const mapped = mapWal(join(dir, "w.log"));
  const scanned = [...scanWal(mapped)];
  assert.deepEqual(scanned.map((s) => s.lsn), lsns);
  assert.equal(scanned[2].record.type, RecordType.UPDATE);
  if (scanned[2].record.type === RecordType.UPDATE) {
    assert.equal(Buffer.from(scanned[2].record.afterImage).toString(), "rec-2");
  }
});

test("readRecordAt performs random access by LSN", async () => {
  const dir = tmpdirPath();
  const path = join(dir, "w.log");
  const wal = await WalWriter.create(path);
  const a = await wal.append(updateRec(1, 1, "aaa"));
  const b = await wal.append(updateRec(2, 2, "bbb"));
  await wal.close();
  const mapped = mapWal(path);
  const ra = readRecordAt(mapped, a);
  const rb = readRecordAt(mapped, b);
  assert.equal(ra.txnId, 1);
  assert.equal(rb.txnId, 2);
  assert.throws(() => readRecordAt(mapped, 3), CorruptionError); // mid-header
  assert.throws(() => readRecordAt(mapped, mapped.length), CorruptionError);
});

test("zero-copy scan: decoded images alias the mapped file buffer", async () => {
  const dir = tmpdirPath();
  const path = join(dir, "w.log");
  const wal = await WalWriter.create(path);
  await wal.append(updateRec(1, 1, "zero-copy"));
  await wal.close();
  const mapped = mapWal(path);
  for (const { record } of scanWal(mapped)) {
    if (record.type === RecordType.UPDATE) {
      assert.equal(record.afterImage.buffer, mapped.buffer);
    }
  }
});

test("torn tail from a crash is the logical end of log", async () => {
  const dir = tmpdirPath();
  const path = join(dir, "w.log");
  const wal = await WalWriter.create(path);
  await wal.append(updateRec(1, 1, "good-1"));
  await wal.append(updateRec(1, 1, "good-2"));
  await wal.flush();
  await wal.close();
  // Simulate a torn write: half a frame appended by the crashed OS.
  const torn = Buffer.from([0x40, 0x00, 0x00, 0x00, 0xde, 0xad]);
  writeFileSync(path, Buffer.concat([mapWal(path), torn]));
  const records = [...scanWal(mapWal(path))];
  assert.equal(records.length, 2);
  // strict mode must flag the torn tail as corruption
  assert.throws(() => [...scanWal(mapWal(path), { strict: true })], CorruptionError);
});

test("CRC mismatch stops the scan (default) or throws (strict)", async () => {
  const dir = tmpdirPath();
  const path = join(dir, "w.log");
  const wal = await WalWriter.create(path);
  await wal.append(updateRec(1, 1, "first"));
  await wal.append(updateRec(1, 1, "second"));
  await wal.close();
  const mapped = mapWal(path);
  // Corrupt one payload byte inside the second frame.
  const second = [...scanWalFrames(mapped)][1];
  mapped[second.lsn + 8] ^= 0xff;
  writeFileSync(path, mapped);
  assert.equal([...scanWal(mapWal(path))].length, 1);
  assert.throws(() => [...scanWal(mapWal(path), { strict: true })], CorruptionError);
});

test("openForRecovery truncates the torn tail and continues appending", async () => {
  const dir = tmpdirPath();
  const path = join(dir, "w.log");
  const wal = await WalWriter.create(path);
  const l1 = await wal.append(updateRec(1, 1, "kept"));
  await wal.flush();
  await wal.close();
  writeFileSync(path, Buffer.concat([mapWal(path), Buffer.from([1, 2, 3])]));
  const wal2 = await WalWriter.openForRecovery(path);
  const l2 = await wal2.append(updateRec(2, 2, "after-recovery"));
  await wal2.close();
  const records = [...scanWal(mapWal(path))];
  assert.equal(records.length, 2);
  assert.deepEqual(records.map((r) => r.lsn), [l1, l2]);
  assert.ok(l2 > l1);
});

test("bad magic and unsupported future file version are rejected", () => {
  const dir = tmpdirPath();
  const badMagic = join(dir, "bad.log");
  writeFileSync(badMagic, encodeWalHeader(FORMAT_VERSION));
  const buf = mapWal(badMagic);
  buf[0] ^= 0xff;
  writeFileSync(badMagic, buf);
  assert.throws(() => [...scanWal(mapWal(badMagic))], CorruptionError);
  const future = join(dir, "future.log");
  writeFileSync(future, encodeWalHeader(FORMAT_VERSION + 1));
  assert.throws(() => [...scanWal(mapWal(future))], UnsupportedVersionError);
});

test("append after close is rejected", async () => {
  const dir = tmpdirPath();
  const wal = await WalWriter.create(join(dir, "w.log"));
  await wal.close();
  await assert.rejects(() => wal.append(updateRec(1, 1, "x")), /closed/);
});
