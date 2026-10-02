import { test } from "node:test";
import assert from "node:assert/strict";
import { BoundedWriter, CorruptionError } from "../src/binary.ts";
import {
  FORMAT_VERSION,
  RecordType,
  TxnStatus,
  decodeRecord,
  encodeRecord,
  type LogRecord,
} from "../src/logrecord.ts";

const SAMPLES: LogRecord[] = [
  {
    type: RecordType.UPDATE, lsn: 1234, txnId: 7, prevLSN: 1000,
    pageId: 42, offset: 96,
    beforeImage: Buffer.from("before"), afterImage: Buffer.from("after!"),
  },
  {
    type: RecordType.CLR, lsn: 2000, txnId: 7, prevLSN: 1234,
    pageId: 42, offset: 96,
    beforeImage: Buffer.from("after!"), afterImage: Buffer.from("before"),
    undoNextLSN: 0,
  },
  { type: RecordType.COMMIT, lsn: 3000, txnId: 7, prevLSN: 2000 },
  { type: RecordType.ABORT, lsn: 3100, txnId: 8, prevLSN: 0 },
  { type: RecordType.END, lsn: 3200, txnId: 7, prevLSN: 3000 },
  { type: RecordType.BEGIN_CHECKPOINT, lsn: 4000, txnId: 0, prevLSN: 0 },
  {
    type: RecordType.END_CHECKPOINT, lsn: 4100, txnId: 0, prevLSN: 0, beginLSN: 4000,
    txns: [
      { txnId: 7, status: TxnStatus.COMMITTED, lastLSN: 3000 },
      { txnId: 8, status: TxnStatus.RUNNING, lastLSN: 0 },
    ],
    pages: [
      { pageId: 1, recLSN: 1234 },
      { pageId: 2, recLSN: 5678 },
    ],
  },
];

test("every record type round-trips through encode/decode", () => {
  for (const rec of SAMPLES) {
    const decoded = decodeRecord(encodeRecord(rec));
    assert.deepEqual(decoded, rec);
  }
});

test("decode is zero-copy: images alias the source payload buffer", () => {
  const rec = SAMPLES[0];
  const payload = encodeRecord(rec);
  const decoded = decodeRecord(payload);
  assert.equal(decoded.type, RecordType.UPDATE);
  if (decoded.type === RecordType.UPDATE) {
    assert.equal(decoded.beforeImage.buffer, payload.buffer);
    assert.equal(decoded.afterImage.buffer, payload.buffer);
  }
});

test("empty and 64KiB images round-trip", () => {
  const big = Buffer.alloc(64 * 1024, 0xab);
  const rec: LogRecord = {
    type: RecordType.UPDATE, lsn: 1, txnId: 1, prevLSN: 0,
    pageId: 1, offset: 0, beforeImage: Buffer.alloc(0), afterImage: big,
  };
  const decoded = decodeRecord(encodeRecord(rec));
  assert.deepEqual(decoded, rec);
});

test("record version 0 is rejected as corruption", () => {
  const payload = encodeRecord(SAMPLES[2]);
  payload[0] = 0;
  assert.throws(() => decodeRecord(payload), CorruptionError);
});

test("forward compatibility: newer record version with trailing fields is tolerated", () => {
  // Hand-craft a "v2" COMMIT: v1 fields + unknown trailing bytes.
  const w = new BoundedWriter();
  w.writeU8(FORMAT_VERSION + 1);
  w.writeU8(RecordType.COMMIT);
  w.writeU64(555);
  w.writeU32(9);
  w.writeU64(444);
  w.writeU32(0xdeadbeef); // future field, must be ignored
  const decoded = decodeRecord(w.toBuffer());
  assert.deepEqual(decoded, { type: RecordType.COMMIT, lsn: 555, txnId: 9, prevLSN: 444 });
});

test("unknown record type decodes to UNKNOWN instead of failing", () => {
  const w = new BoundedWriter();
  w.writeU8(FORMAT_VERSION);
  w.writeU8(99);
  w.writeU64(1);
  w.writeU32(2);
  w.writeU64(0);
  w.writeBytes(Buffer.from("future-body"));
  const decoded = decodeRecord(w.toBuffer());
  assert.equal(decoded.type, RecordType.UNKNOWN);
  if (decoded.type === RecordType.UNKNOWN) assert.equal(decoded.rawType, 99);
});

test("truncated payloads raise CorruptionError with offset context", () => {
  const payload = encodeRecord(SAMPLES[0]);
  for (const cut of [0, 1, 5, payload.length - 1]) {
    assert.throws(() => decodeRecord(payload.subarray(0, cut)), CorruptionError);
  }
});

test("checkpoint snapshot with empty TT/DPT round-trips", () => {
  const rec: LogRecord = {
    type: RecordType.END_CHECKPOINT, lsn: 10, txnId: 0, prevLSN: 0,
    beginLSN: 9, txns: [], pages: [],
  };
  assert.deepEqual(decodeRecord(encodeRecord(rec)), rec);
});
