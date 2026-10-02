import test from "node:test";
import assert from "node:assert/strict";
import {
  AnchorLedger,
  AnchorService,
  generateAnchorKeys,
  verifyAnchorSignature,
} from "../src/anchor.ts";
import { hashLeaf } from "../src/merkle.ts";

test("anchor record carries timestamp and valid signature", () => {
  const keys = generateAnchorKeys();
  const service = new AnchorService(keys, () => 1000);
  const record = service.anchor(hashLeaf("fact"));
  assert.equal(record.timestamp, 1000);
  assert.equal(record.seq, 0);
  assert.ok(verifyAnchorSignature(record, keys.publicKey));
});

test("signature binds root and timestamp: tampering fails", () => {
  const keys = generateAnchorKeys();
  const service = new AnchorService(keys, () => 1000);
  const record = service.anchor(hashLeaf("fact"));
  const tampered = { ...record, timestamp: 999 };
  assert.equal(verifyAnchorSignature(tampered, keys.publicKey), false);
  const tamperedRoot = { ...record, merkleRoot: "ab".repeat(32) };
  assert.equal(verifyAnchorSignature(tamperedRoot, keys.publicKey), false);
});

test("foreign public key cannot verify the anchor", () => {
  const keys = generateAnchorKeys();
  const other = generateAnchorKeys();
  const service = new AnchorService(keys, () => 1000);
  const record = service.anchor(hashLeaf("fact"));
  assert.equal(verifyAnchorSignature(record, other.publicKey), false);
});

test("ledger rejects replayed anchor (same anchorId)", () => {
  const keys = generateAnchorKeys();
  const service = new AnchorService(keys, () => 1000);
  const ledger = new AnchorLedger();
  const record = service.anchor(hashLeaf("fact"));
  assert.equal(ledger.register(record, keys.publicKey).ok, true);
  const replay = ledger.register(record, keys.publicKey);
  assert.equal(replay.ok, false);
  assert.match(replay.reason!, /replay/);
});

test("ledger rejects non-monotonic sequence and timestamp", () => {
  const keys = generateAnchorKeys();
  let now = 1000;
  const service = new AnchorService(keys, () => now);
  const ledger = new AnchorLedger();
  now = 1000;
  const first = service.anchor(hashLeaf("a"));
  now = 2000;
  const second = service.anchor(hashLeaf("b"));
  assert.equal(ledger.register(first, keys.publicKey).ok, true);
  assert.equal(ledger.register(second, keys.publicKey).ok, true);
  const outOfOrder = ledger.register(first, keys.publicKey);
  assert.equal(outOfOrder.ok, false);
  assert.match(outOfOrder.reason!, /replay/);
});

test("ledger rejects forged anchor signed by another key", () => {
  const keys = generateAnchorKeys();
  const attacker = generateAnchorKeys();
  const fakeService = new AnchorService(attacker, () => 1000);
  const forged = fakeService.anchor(hashLeaf("fact"));
  const ledger = new AnchorLedger();
  const res = ledger.register(forged, keys.publicKey);
  assert.equal(res.ok, false);
  assert.match(res.reason!, /signature/);
});

test("ledger enforces hash chain continuity", () => {
  const keys = generateAnchorKeys();
  const service = new AnchorService(keys, () => 1000);
  const ledger = new AnchorLedger();
  const first = service.anchor(hashLeaf("a"));
  const second = service.anchor(hashLeaf("b"));
  assert.equal(ledger.register(first, keys.publicKey).ok, true);
  const broken = { ...second, prevAnchorHash: "ff".repeat(32) };
  const res = ledger.register(broken, keys.publicKey);
  assert.equal(res.ok, false);
});
