import assert from "node:assert/strict";
import test from "node:test";
import { BitemporalFileStore, comparePrecedence } from "../src/index.ts";
import type { Revision } from "../src/index.ts";

test("half-open interval: validFrom inclusive, validTo exclusive", () => {
  const store = new BitemporalFileStore();
  store.record({ fileId: "f", attributes: { v: 1 }, validFrom: 10, validTo: 20 });
  const at = store.currentVersion;
  assert.equal(store.query("f", 9, at), null);
  assert.equal(store.query("f", 10, at)?.attributes.v, 1);
  assert.equal(store.query("f", 19, at)?.attributes.v, 1);
  assert.equal(store.query("f", 20, at), null);
});

test("conflict resolved by knowledge version, then revisionId", () => {
  const store = new BitemporalFileStore();
  const older = store.record({ fileId: "f", attributes: { v: "old" }, validFrom: 0, validTo: 100 });
  const newer = store.record({ fileId: "f", attributes: { v: "new" }, validFrom: 0, validTo: 100 });
  const at = store.currentVersion;
  const result = store.query("f", 50, at);
  assert.equal(result?.attributes.v, "new");
  assert.equal(result?.source.revisionId, newer.revisionId);
  // Pinned to the earlier knowledge version, the older revision still wins.
  assert.equal(store.query("f", 50, older.recordedAt)?.source.revisionId, older.revisionId);

  // Tie-break rule itself: same recordedAt -> higher revisionId wins.
  const base: Revision = {
    revisionId: 1, fileId: "f", attributes: {},
    validFrom: 0, validTo: 1, recordedAt: 5, revokedAt: null,
  };
  assert.ok(comparePrecedence({ ...base, revisionId: 2 }, base) > 0);
  assert.ok(comparePrecedence(base, { ...base, recordedAt: 6 }) < 0);
});

test("revocation only affects later knowledge versions", () => {
  const store = new BitemporalFileStore();
  const first = store.record({ fileId: "f", attributes: { v: 1 }, validFrom: 0, validTo: 100 });
  const second = store.record({ fileId: "f", attributes: { v: 2 }, validFrom: 0, validTo: 100 });
  const beforeRevoke = store.currentVersion;
  store.revoke(second.revisionId);
  const afterRevoke = store.currentVersion;

  // Knowledge pinned before the revoke still sees revision 2.
  assert.equal(store.query("f", 50, beforeRevoke)?.source.revisionId, second.revisionId);
  // At/after the revoke version, visibility falls back to revision 1.
  assert.equal(store.query("f", 50, afterRevoke)?.source.revisionId, first.revisionId);
  // Revoking the last visible revision leaves nothing.
  store.revoke(first.revisionId);
  assert.equal(store.query("f", 50, store.currentVersion), null);
  assert.equal(store.query("f", 50, beforeRevoke)?.source.revisionId, second.revisionId);
});

test("late backfill overrides older knowledge for the same valid time", () => {
  const store = new BitemporalFileStore();
  store.record({ fileId: "f", attributes: { status: "pending" }, validFrom: 100, validTo: 200 });
  const beforeBackfill = store.currentVersion;
  // Recorded now, but effective for a past interval.
  const backfill = store.record({ fileId: "f", attributes: { status: "done" }, validFrom: 100, validTo: 200 });
  assert.equal(store.query("f", 150, beforeBackfill)?.attributes.status, "pending");
  assert.equal(store.query("f", 150, store.currentVersion)?.attributes.status, "done");
  assert.equal(store.query("f", 150, store.currentVersion)?.source.revisionId, backfill.revisionId);
});

test("same file across multiple disjoint intervals", () => {
  const store = new BitemporalFileStore();
  const a = store.record({ fileId: "f", attributes: { era: "a" }, validFrom: 0, validTo: 10 });
  const b = store.record({ fileId: "f", attributes: { era: "b" }, validFrom: 10, validTo: 20 });
  const c = store.record({ fileId: "f", attributes: { era: "c" }, validFrom: 30, validTo: 40 });
  const at = store.currentVersion;
  assert.equal(store.query("f", 5, at)?.source.revisionId, a.revisionId);
  assert.equal(store.query("f", 10, at)?.source.revisionId, b.revisionId);
  assert.equal(store.query("f", 25, at), null); // gap between intervals
  assert.equal(store.query("f", 39, at)?.source.revisionId, c.revisionId);
  assert.equal(store.query("f", 40, at), null);
});

test("edge cases: unknown file, invalid interval, double revoke", () => {
  const store = new BitemporalFileStore();
  assert.equal(store.query("nope", 0, 0), null);
  assert.throws(
    () => store.record({ fileId: "f", attributes: {}, validFrom: 10, validTo: 10 }),
    RangeError,
  );
  assert.throws(
    () => store.record({ fileId: "f", attributes: {}, validFrom: 20, validTo: 10 }),
    RangeError,
  );
  const rev = store.record({ fileId: "f", attributes: {}, validFrom: 0, validTo: 1 });
  store.revoke(rev.revisionId);
  assert.throws(() => store.revoke(rev.revisionId), /already revoked/);
  assert.throws(() => store.revoke(9999), /unknown revisionId/);
});
