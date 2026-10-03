import { test } from 'node:test';
import assert from 'node:assert/strict';
import { AuditAuthority, verifyCredential, type AuditCredential } from '../src/authority.ts';
import { TrajectoryVault } from '../src/vault.ts';
import { gridCell, gridHash } from '../src/grid.ts';
import { hkdf, randomKey } from '../src/crypto.ts';

const HOUR = 60 * 60 * 1000;
const DAY = 24 * HOUR;
const T0 = Date.UTC(2026, 0, 1);

function setup() {
  const authority = AuditAuthority.generate();
  const vault = new TrajectoryVault({ authorityPublicKeyPem: authority.publicKeyPem });
  return { authority, vault };
}

function issue(authority: AuditAuthority, overrides: Partial<Parameters<AuditAuthority['issueCredential']>[0]> = {}) {
  return authority.issueCredential({
    issuer: 'court-order-1',
    targetRealId: 'alice',
    from: T0,
    to: T0 + DAY,
    purpose: 'investigation',
    ttlMs: 60_000,
    ...overrides,
  });
}

test('时变假名：同一窗口内稳定，跨窗口不可链接', () => {
  const { vault } = setup();
  const p1 = vault.pseudonymFor('alice', T0 + HOUR);
  const p2 = vault.pseudonymFor('alice', T0 + 2 * HOUR);
  const p3 = vault.pseudonymFor('alice', T0 + DAY + HOUR);
  assert.equal(p1, p2, '同一 epoch 内假名应稳定');
  assert.notEqual(p1, p3, '跨 epoch 假名必须变化');
});

test('假名依赖密钥：不同主密钥产生不同假名', () => {
  const authority = AuditAuthority.generate();
  const v1 = new TrajectoryVault({ authorityPublicKeyPem: authority.publicKeyPem, masterSecret: randomKey() });
  const v2 = new TrajectoryVault({ authorityPublicKeyPem: authority.publicKeyPem, masterSecret: randomKey() });
  assert.notEqual(v1.pseudonymFor('alice', T0), v2.pseudonymFor('alice', T0));
});

test('存储记录不包含明文真实 ID 或精确坐标', () => {
  const { vault } = setup();
  vault.ingest('alice-secret-identity', 31.2304, 121.4737, T0);
  const serialized = JSON.stringify((vault as unknown as { records: unknown }).records);
  assert.ok(!serialized.includes('alice-secret-identity'), '序列化记录泄露真实 ID');
  assert.ok(!serialized.includes('31.2304'), '序列化记录泄露精确纬度');
  assert.ok(!serialized.includes('121.4737'), '序列化记录泄露精确经度');
});

test('网格哈希加盐：相同坐标不同盐结果不同，且可稳定索引', () => {
  const cell = gridCell(31.2304, 121.4737, 0.01);
  const h1 = gridHash(randomKey(), cell);
  const h2 = gridHash(randomKey(), cell);
  assert.notEqual(h1, h2);
  const salt = randomKey();
  assert.equal(gridHash(salt, gridCell(31.2301, 121.4739, 0.01)), gridHash(salt, gridCell(31.2309, 121.4731, 0.01)));
  assert.throws(() => gridCell(91, 0, 0.01));
});

test('日常查询：热力图只返回脱敏聚合计数', () => {
  const { vault } = setup();
  for (let i = 0; i < 5; i++) vault.ingest(`user-${i}`, 31.2301, 121.4701, T0 + HOUR);
  vault.ingest('user-9', 32.5, 122.5, T0 + HOUR);
  const heat = vault.heatmap(T0, T0 + DAY);
  assert.equal(heat.length, 2);
  assert.equal(heat[0].count, 5);
  assert.equal(heat[1].count, 1);
  for (const cell of heat) {
    assert.deepEqual(Object.keys(cell).sort(), ['count', 'gridHash']);
  }
});

test('定向还原：有效凭证 + 监管密钥精准还原目标轨迹', () => {
  const { authority, vault } = setup();
  vault.ingest('alice', 31.2304, 121.4737, T0 + HOUR);
  vault.ingest('alice', 31.2315, 121.4748, T0 + 2 * HOUR);
  vault.ingest('alice', 31.5, 121.9, T0 + 3 * DAY); // 授权窗口之外
  vault.ingest('bob', 31.2304, 121.4737, T0 + HOUR); // 其他用户同位置

  const credential = issue(authority);
  const points = vault.reveal(credential, authority.revealKeyPem);

  assert.equal(points.length, 2, '只还原授权窗口内目标用户的点');
  assert.deepEqual(points[0], { timestamp: T0 + HOUR, realId: 'alice', lat: 31.2304, lng: 121.4737 });
  assert.equal(points[1].timestamp, T0 + 2 * HOUR);
  assert.ok(points.every((p) => p.realId === 'alice'));
});

test('还原跨时态窗口：假名随 epoch 轮换仍可被凭证串联', () => {
  const { authority, vault } = setup();
  for (let d = 0; d < 5; d++) vault.ingest('alice', 31.23 + d * 0.001, 121.47, T0 + d * DAY);
  const credential = issue(authority, { from: T0, to: T0 + 5 * DAY });
  const points = vault.reveal(credential, authority.revealKeyPem);
  assert.equal(points.length, 5);
  const pseudonyms = new Set([0, 1, 2, 3, 4].map((d) => vault.pseudonymFor('alice', T0 + d * DAY)));
  assert.equal(pseudonyms.size, 5, '5 个 epoch 应产生 5 个不同假名');
});

test('伪造凭证被拒绝并留下审计记录', () => {
  const { authority, vault } = setup();
  vault.ingest('alice', 31.23, 121.47, T0);
  const forged: AuditCredential = { ...issue(authority), targetRealId: 'mallory' };
  assert.throws(() => vault.reveal(forged, authority.revealKeyPem), /signature invalid/);
  const denied = vault.auditLog.list().filter((e) => e.action === 'AUDIT_DENIED');
  assert.equal(denied.length, 1);
});

test('过期凭证被拒绝', () => {
  const { authority, vault } = setup();
  const credential = issue(authority, { ttlMs: -1 });
  assert.throws(() => vault.reveal(credential, authority.revealKeyPem), /expired/);
  assert.throws(() => verifyCredential(credential, authority.publicKeyPem), /expired/);
});

test('其他机构签发的凭证不被接受', () => {
  const { vault } = setup();
  const stranger = AuditAuthority.generate();
  const credential = issue(stranger);
  assert.throws(() => vault.reveal(credential, stranger.revealKeyPem), /signature invalid/);
});

test('错误的监管密钥无法解密载荷', () => {
  const { authority, vault } = setup();
  vault.ingest('alice', 31.23, 121.47, T0);
  const wrongAuthority = AuditAuthority.generate();
  const credential = issue(authority);
  assert.throws(() => vault.reveal(credential, wrongAuthority.revealKeyPem));
});

test('审计日志：哈希链完整且篡改可检测', () => {
  const { authority, vault } = setup();
  vault.ingest('alice', 31.23, 121.47, T0);
  vault.reveal(issue(authority), authority.revealKeyPem);
  assert.ok(vault.auditLog.verify());

  const entries = vault.auditLog.list();
  const reveal = entries.find((e) => e.action === 'AUDIT_REVEAL');
  assert.ok(reveal, '还原操作必须留痕');
  assert.equal(reveal.details.revealedPoints, 1);
  assert.equal(reveal.details.issuer, 'court-order-1');

  (entries[0] as { action: string }).action = 'TAMPERED';
  assert.ok(!vault.auditLog.verify(), '篡改后哈希链校验必须失败');
});

test('密钥派生：HKDF 不同 info 产生不同密钥', () => {
  const master = randomKey();
  assert.notDeepEqual(hkdf(master, 'a'), hkdf(master, 'b'));
  assert.deepEqual(hkdf(master, 'a'), hkdf(master, 'a'));
});
