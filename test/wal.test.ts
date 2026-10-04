import test from "node:test";
import * as assert from "node:assert/strict";
import * as fs from "fs";
import * as os from "os";
import * as path from "path";
import { Wal, FRAME_HEADER_SIZE } from "../src/wal";
import { RecordType, UpdateRecord } from "../src/logrecord";

function tmpdir(): string {
  return fs.mkdtempSync(path.join(os.tmpdir(), "aries-wal-test-"));
}

function updateRec(txnId: number, pageId: number, tag: number) {
  return {
    type: RecordType.Update as const, lsn: 0, txnId, prevLSN: 0,
    pageId, offset: 8, before: Buffer.from([0]), after: Buffer.from([tag & 0xff]),
  };
}

test("append/scan roundtrip with LSN == file offset", () => {
  const dir = tmpdir();
  const wal = Wal.open(path.join(dir, "wal.log"));
  const lsn1 = wal.appendSync({ type: RecordType.Begin, lsn: 0, txnId: 1, prevLSN: 0 });
  const lsn2 = wal.appendSync(updateRec(1, 5, 7));
  assert.equal(lsn1, 0);
  assert.ok(lsn2 > lsn1);
  assert.equal(wal.nextLSN, fs.fstatSync(fs.openSync(wal.path, "r")).size);

  const scanned = [...wal.scan()];
  assert.equal(scanned.length, 2);
  assert.equal(scanned[0]!.lsn, lsn1);
  assert.equal(scanned[1]!.lsn, lsn2);
  assert.equal((scanned[1]!.record as UpdateRecord).pageId, 5);

  // random access by LSN
  const rec = wal.readRecord(lsn2) as UpdateRecord;
  assert.equal(rec.type, RecordType.Update);
  assert.ok(rec.after.equals(Buffer.from([7])));
  wal.close();
  assert.ok(wal.isClosed);
  assert.throws(() => wal.appendSync(updateRec(1, 1, 1)), /already closed/);
  fs.rmSync(dir, { recursive: true, force: true });
});

test("500 concurrent async appends: unique ordered LSNs, no frame interleave", async () => {
  const dir = tmpdir();
  const wal = Wal.open(path.join(dir, "wal.log"));
  const N = 500;
  const lsns = await Promise.all(
    Array.from({ length: N }, (_, i) => wal.append(updateRec(i + 1, i % 8, i))),
  );
  // LSNs are unique and exactly tile the file.
  const sorted = [...lsns].sort((a, b) => a - b);
  for (let i = 1; i < N; i++) assert.ok(sorted[i]! > sorted[i - 1]!);
  assert.equal(sorted[0], 0);
  assert.equal(wal.nextLSN, fs.readFileSync(wal.path).length);

  // Every frame parses; each txnId appears exactly once.
  const seen = new Set<number>();
  let count = 0;
  for (const frame of wal.scan()) {
    seen.add(frame.record.txnId);
    count++;
  }
  assert.equal(count, N);
  assert.equal(seen.size, N);
  wal.close();
  fs.rmSync(dir, { recursive: true, force: true });
});

test("sync append is rejected while async appends are in flight", async () => {
  const dir = tmpdir();
  const wal = Wal.open(path.join(dir, "wal.log"));
  const p = wal.append(updateRec(1, 1, 1));
  assert.throws(() => wal.appendSync(updateRec(1, 1, 1)), /async appends are pending/);
  await p;
  const lsn = wal.appendSync(updateRec(1, 1, 1)); // queue drained: allowed
  assert.ok(lsn > 0);
  wal.close();
  fs.rmSync(dir, { recursive: true, force: true });
});

test("torn tail (partial frame) is truncated on open", () => {
  const dir = tmpdir();
  const file = path.join(dir, "wal.log");
  const wal = Wal.open(file);
  wal.appendSync(updateRec(1, 1, 1));
  wal.appendSync(updateRec(2, 2, 2));
  const validEnd = wal.nextLSN;
  wal.close();

  // Simulate a crash mid-write: half of a frame lands on disk.
  fs.appendFileSync(file, Buffer.from([0x20, 0x00, 0x00, 0x00, 0xff]));

  const reopened = Wal.open(file);
  assert.equal(reopened.nextLSN, validEnd, "torn bytes must be truncated");
  assert.equal(fs.readFileSync(file).length, validEnd);
  assert.equal([...reopened.scan()].length, 2);
  reopened.close();
  fs.rmSync(dir, { recursive: true, force: true });
});

test("torn tail (CRC-corrupted payload) is truncated on open", () => {
  const dir = tmpdir();
  const file = path.join(dir, "wal.log");
  const wal = Wal.open(file);
  wal.appendSync(updateRec(1, 1, 1));
  const validEnd = wal.nextLSN;
  wal.appendSync(updateRec(2, 2, 2));
  wal.close();

  // Corrupt one byte inside the second frame's payload.
  const fd = fs.openSync(file, "r+");
  const pos = validEnd + FRAME_HEADER_SIZE;
  const byte = Buffer.allocUnsafe(1);
  fs.readSync(fd, byte, 0, 1, pos);
  byte.writeUInt8(byte.readUInt8(0) ^ 0xff, 0);
  fs.writeSync(fd, byte, 0, 1, pos);
  fs.closeSync(fd);

  const reopened = Wal.open(file);
  assert.equal(reopened.nextLSN, validEnd);
  assert.equal([...reopened.scan()].length, 1);
  reopened.close();
  fs.rmSync(dir, { recursive: true, force: true });
});

test("absurd frame length in tail is treated as torn", () => {
  const dir = tmpdir();
  const file = path.join(dir, "wal.log");
  const wal = Wal.open(file);
  wal.appendSync(updateRec(1, 1, 1));
  const validEnd = wal.nextLSN;
  wal.close();
  const bogus = Buffer.allocUnsafe(FRAME_HEADER_SIZE);
  bogus.writeUInt32LE(0x7fffffff, 0); // 2GB payload that does not exist
  bogus.writeUInt32LE(0, 4);
  fs.appendFileSync(file, bogus);
  const reopened = Wal.open(file);
  assert.equal(reopened.nextLSN, validEnd);
  reopened.close();
  fs.rmSync(dir, { recursive: true, force: true });
});

test("scan is zero-copy: all records in one scan share the file image", () => {
  const dir = tmpdir();
  const wal = Wal.open(path.join(dir, "wal.log"));
  wal.appendSync(updateRec(1, 1, 0x11));
  wal.appendSync(updateRec(2, 2, 0x22));
  wal.appendSync(updateRec(3, 3, 0x33));
  const records = [...wal.scan()].map((f) => f.record as UpdateRecord);
  const image = records[0]!.after.buffer;
  for (const rec of records) {
    assert.equal(rec.after.buffer, image, "every record views the same mapped buffer");
    assert.equal(rec.before.buffer, image);
  }
  // views point at increasing offsets inside the image (frame order)
  assert.ok(records[1]!.after.byteOffset > records[0]!.after.byteOffset);
  assert.ok(records[2]!.after.byteOffset > records[1]!.after.byteOffset);
  wal.close();
  fs.rmSync(dir, { recursive: true, force: true });
});

test("scan from a mid-log LSN seeks directly", () => {
  const dir = tmpdir();
  const wal = Wal.open(path.join(dir, "wal.log"));
  wal.appendSync(updateRec(1, 1, 1));
  const lsn2 = wal.appendSync(updateRec(2, 2, 2));
  wal.appendSync(updateRec(3, 3, 3));
  const rest = [...wal.scan(lsn2)];
  assert.equal(rest.length, 2);
  assert.equal(rest[0]!.lsn, lsn2);
  wal.close();
  fs.rmSync(dir, { recursive: true, force: true });
});
