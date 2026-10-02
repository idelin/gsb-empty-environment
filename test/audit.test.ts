import test from "node:test";
import assert from "node:assert/strict";
import { deriveKeys, generateMasterKey } from "../src/crypto.ts";
import { TrajectoryIndex } from "../src/store.ts";
import { AuditAuthority, AuditLog, Auditor } from "../src/audit.ts";

const HOUR = 60 * 60 * 1000;
const T0 = Date.UTC(2026, 0, 1, 0, 0, 0);

function setup() {
  const index = new TrajectoryIndex(deriveKeys(generateMasterKey()), {
    bucketSizeMs: HOUR,
    kAnonymity: 1,
  });
  index.ingest({ entityId: "target", lat: 31.23, lon: 121.47, timestamp: T0 + 5 * HOUR });
  index.ingest({ entityId: "target", lat: 31.24, lon: 121.48, timestamp: T0 + 20 * HOUR });
  const authority = AuditAuthority.generate("judicial-authority");
  const log = new AuditLog();
  const auditor = new Auditor(authority, index, log);
  return { index, authority, log, auditor };
}

test("授权凭证：签发与验证通过", () => {
  const { authority } = setup();
  const cred = authority.issue("CASE-1", "target", T0, T0 + 10 * HOUR, HOUR, T0);
  assert.doesNotThrow(() => authority.verify(cred, T0 + 30 * 60 * 1000));
});

test("授权凭证：过期被拒绝", () => {
  const { authority } = setup();
  const cred = authority.issue("CASE-1", "target", T0, T0 + 10 * HOUR, HOUR, T0);
  assert.throws(() => authority.verify(cred, T0 + 2 * HOUR), /expired/);
});

test("授权凭证：篡改目标或时间窗导致签名失效", () => {
  const { authority } = setup();
  const cred = authority.issue("CASE-1", "target", T0, T0 + 10 * HOUR, HOUR, T0);
  const tamperedTarget = { ...cred, entityId: "someone-else" };
  const tamperedRange = { ...cred, to: T0 + 99 * HOUR };
  assert.throws(() => authority.verify(tamperedTarget, T0), /signature/);
  assert.throws(() => authority.verify(tamperedRange, T0), /signature/);
});

test("授权凭证：陌生签发方被拒绝", () => {
  const { authority } = setup();
  const rogue = AuditAuthority.generate("rogue-authority");
  const cred = rogue.issue("CASE-1", "target", T0, T0 + HOUR, HOUR, T0);
  assert.throws(() => authority.verify(cred, T0), /issuer/);
});

test("定向还原：合法凭证仅还原授权窗口内的目标轨迹并写审计日志", () => {
  const { auditor, log, authority } = setup();
  const cred = authority.issue("CASE-7", "target", T0, T0 + 10 * HOUR, HOUR, T0);
  const points = auditor.reveal(cred, T0 + 1000);
  assert.equal(points.length, 1);
  assert.equal(points[0].lat, 31.23);
  const entries = log.list();
  assert.equal(entries.length, 1);
  assert.equal(entries[0].action, "TARGETED_REVEAL");
  assert.equal(entries[0].caseId, "CASE-7");
  assert.equal(entries[0].entityId, "target");
  assert.equal(entries[0].resultCount, 1);
  assert.ok(log.verify());
});

test("定向还原：伪造凭证被拒绝且留下拒绝审计记录", () => {
  const { auditor, log, authority } = setup();
  const cred = authority.issue("CASE-8", "target", T0, T0 + 10 * HOUR, HOUR, T0);
  const forged = { ...cred, entityId: "victim" };
  assert.throws(() => auditor.reveal(forged, T0), /signature/);
  const entries = log.list();
  assert.equal(entries.length, 1);
  assert.equal(entries[0].action, "REVEAL_DENIED");
  assert.equal(entries[0].entityId, "victim");
  assert.ok(log.verify());
});

test("审计日志：哈希链完整，篡改任一字段即检测失败", () => {
  const { auditor, log, authority } = setup();
  for (let i = 0; i < 3; i++) {
    const cred = authority.issue(`CASE-${i}`, "target", T0, T0 + HOUR, HOUR, T0);
    auditor.reveal(cred, T0 + i * 1000);
  }
  assert.equal(log.list().length, 3);
  assert.ok(log.verify());
  const entries = [...log.list()];
  entries[1].resultCount = 999;
  assert.ok(!log.verify());
});
