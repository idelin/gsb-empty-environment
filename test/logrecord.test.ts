import test from "node:test";
import * as assert from "node:assert/strict";
import { Writer, CodecError } from "../src/codec";
import {
  RecordType, LogRecord, UpdateRecord, ClrRecord, CheckpointRecord,
  serializeRecord, deserializeRecord,
} from "../src/logrecord";

function roundtrip(rec: LogRecord): LogRecord {
  return deserializeRecord(serializeRecord(rec));
}

test("roundtrip: all record types", () => {
  const begin = roundtrip({ type: RecordType.Begin, lsn: 8, txnId: 1, prevLSN: 0 });
  assert.deepEqual(begin, { type: RecordType.Begin, lsn: 8, txnId: 1, prevLSN: 0 });

  const update = roundtrip({
    type: RecordType.Update, lsn: 40, txnId: 2, prevLSN: 8,
    pageId: 7, offset: 128, before: Buffer.from([1, 2]), after: Buffer.from([3, 4]),
  }) as UpdateRecord;
  assert.equal(update.pageId, 7);
  assert.equal(update.offset, 128);
  assert.ok(update.before.equals(Buffer.from([1, 2])));
  assert.ok(update.after.equals(Buffer.from([3, 4])));

  const clr = roundtrip({
    type: RecordType.Clr, lsn: 99, txnId: 2, prevLSN: 40,
    pageId: 7, offset: 128, before: Buffer.from([3, 4]), after: Buffer.from([1, 2]),
    undoNextLSN: 8,
  }) as ClrRecord;
  assert.equal(clr.undoNextLSN, 8);
  assert.ok(clr.after.equals(Buffer.from([1, 2])));

  for (const type of [RecordType.Commit, RecordType.Abort, RecordType.End]) {
    const rec = roundtrip({ type, lsn: 200, txnId: 3, prevLSN: 150 } as LogRecord);
    assert.deepEqual(rec, { type, lsn: 200, txnId: 3, prevLSN: 150 });
  }

  const ckpt = roundtrip({
    type: RecordType.Checkpoint, lsn: 500, txnId: 0, prevLSN: 0,
    txns: [
      { txnId: 1, status: "committed", lastLSN: 100 },
      { txnId: 2, status: "running", lastLSN: 300 },
      { txnId: 3, status: "aborting", lastLSN: 400 },
    ],
    dirtyPages: [[5, 60], [9, 70]],
  }) as CheckpointRecord;
  assert.equal(ckpt.txns.length, 3);
  assert.deepEqual(ckpt.txns[0], { txnId: 1, status: "committed", lastLSN: 100 });
  assert.deepEqual(ckpt.dirtyPages, [[5, 60], [9, 70]]);
});

test("roundtrip: empty before/after images and max-size values", () => {
  const rec = roundtrip({
    type: RecordType.Update, lsn: Number.MAX_SAFE_INTEGER - 1, txnId: 2 ** 40,
    prevLSN: 2 ** 32, pageId: 2 ** 20, offset: 0,
    before: Buffer.alloc(0), after: Buffer.alloc(0),
  }) as UpdateRecord;
  assert.equal(rec.lsn, Number.MAX_SAFE_INTEGER - 1);
  assert.equal(rec.before.length, 0);
  assert.equal(rec.after.length, 0);
});

test("backward compat: v1 UPDATE without offset field parses with offset 0", () => {
  // Hand-craft a version-1 payload: no `offset` between pageId and beforeLen.
  const w = new Writer();
  w.writeU8(1); // version 1
  w.writeU8(RecordType.Update);
  w.writeVarint(64); // lsn
  w.writeVarint(9); // txnId
  w.writeVarint(32); // prevLSN
  w.writeVarint(3); // pageId
  w.writeVarint(2); // beforeLen
  w.writeBytes(Buffer.from([0xaa, 0xbb]));
  w.writeVarint(1); // afterLen
  w.writeBytes(Buffer.from([0xcc]));
  const rec = deserializeRecord(w.toBuffer()) as UpdateRecord;
  assert.equal(rec.type, RecordType.Update);
  assert.equal(rec.offset, 0);
  assert.equal(rec.pageId, 3);
  assert.ok(rec.before.equals(Buffer.from([0xaa, 0xbb])));
  assert.ok(rec.after.equals(Buffer.from([0xcc])));
});

test("forward compat: v3 payload with unknown trailing field is tolerated", () => {
  const v2 = serializeRecord({
    type: RecordType.Update, lsn: 10, txnId: 1, prevLSN: 0,
    pageId: 4, offset: 8, before: Buffer.from([1]), after: Buffer.from([2]),
  });
  // Re-stamp as v3 and append an unknown future field.
  const upgraded = Buffer.from(v2);
  upgraded.writeUInt8(3, 0);
  const w = new Writer();
  w.writeBytes(upgraded);
  w.writeVarint(42); // future field: ignored by this reader
  const rec = deserializeRecord(w.toBuffer()) as UpdateRecord;
  assert.equal(rec.pageId, 4);
  assert.equal(rec.offset, 8);
  assert.ok(rec.after.equals(Buffer.from([2])));
});

test("rejects unsupported versions, unknown types, truncated payloads", () => {
  const good = serializeRecord({ type: RecordType.Begin, lsn: 1, txnId: 1, prevLSN: 0 });

  const v99 = Buffer.from(good);
  v99.writeUInt8(99, 0);
  assert.throws(() => deserializeRecord(v99), /unsupported record format version 99/);

  const badType = Buffer.from(good);
  badType.writeUInt8(77, 1);
  assert.throws(() => deserializeRecord(badType), /unknown record type 77/);

  const truncated = good.subarray(0, good.length - 1);
  assert.throws(() => deserializeRecord(truncated), CodecError);

  // truncated inside an update's byte image
  const upd = serializeRecord({
    type: RecordType.Update, lsn: 1, txnId: 1, prevLSN: 0,
    pageId: 1, offset: 8, before: Buffer.from([1, 2, 3, 4]), after: Buffer.from([5]),
  });
  assert.throws(() => deserializeRecord(upd.subarray(0, upd.length - 3)), CodecError);
});

test("deserialized byte fields are zero-copy views into the payload", () => {
  const payload = serializeRecord({
    type: RecordType.Update, lsn: 1, txnId: 1, prevLSN: 0,
    pageId: 1, offset: 8, before: Buffer.from([9, 9]), after: Buffer.from([7, 7, 7]),
  });
  const rec = deserializeRecord(payload) as UpdateRecord;
  assert.equal(rec.before.buffer, payload.buffer);
  assert.equal(rec.after.buffer, payload.buffer);
  // views are slices of the single payload buffer; between `before` and
  // `after` sits exactly the 1-byte varint length prefix of `after`.
  assert.equal(rec.after.byteOffset, rec.before.byteOffset + rec.before.length + 1);
  // mutation through the view is visible in the payload buffer
  rec.after.writeUInt8(0xee, 0);
  assert.equal(payload.readUInt8(rec.after.byteOffset - payload.byteOffset), 0xee);
});
