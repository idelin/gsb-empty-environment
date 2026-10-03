import { AuditAuthority } from './authority.ts';
import { TrajectoryVault } from './vault.ts';

const DAY = 24 * 60 * 60 * 1000;
const T0 = Date.UTC(2026, 0, 1);

function walk(seedLat: number, seedLng: number, steps: number): Array<[number, number]> {
  const points: Array<[number, number]> = [];
  let lat = seedLat;
  let lng = seedLng;
  for (let i = 0; i < steps; i++) {
    lat += Math.sin(i * 1.7 + seedLat) * 0.004;
    lng += Math.cos(i * 2.3 + seedLng) * 0.004;
    points.push([lat, lng]);
  }
  return points;
}

const authority = AuditAuthority.generate();
const vault = new TrajectoryVault({ authorityPublicKeyPem: authority.publicKeyPem });

console.log('=== 1. 海量轨迹写入（假名化 + 网格哈希 + ECIES 密封） ===');
const users = ['user-1001', 'user-1002', 'user-1003'];
for (const [u, user] of users.entries()) {
  const route = walk(31.23 + u * 0.01, 121.47 + u * 0.01, 40);
  route.forEach(([lat, lng], i) => {
    vault.ingest(user, lat, lng, T0 + i * 3 * 60 * 60 * 1000);
  });
}
console.log(`已写入 ${vault.recordCount()} 条轨迹点（${users.length} 个实体 × 40 点，跨 5 个时态窗口）`);

const sample = vault.ingest('user-1001', 31.231, 121.471, T0 + 5 * DAY);
console.log('\n存储记录样例（无任何明文身份/坐标）:');
console.log({
  pseudonym: sample.pseudonym.slice(0, 16) + '…',
  epoch: sample.epoch,
  gridHash: sample.gridHash.slice(0, 16) + '…',
  payloadCiphertext: sample.payload.ciphertext.slice(0, 24) + '…',
});

console.log('\n=== 2. 日常只读查询：仅脱敏聚合热力分布 ===');
const heat = vault.heatmap(T0, T0 + 6 * DAY);
console.log(`返回 ${heat.length} 个网格的聚合计数，Top 3:`);
for (const cell of heat.slice(0, 3)) {
  console.log(`  grid=${cell.gridHash.slice(0, 16)}…  count=${cell.count}`);
}

console.log('\n=== 3. 无授权凭证的还原尝试被拒绝 ===');
const forged = {
  credentialId: 'forged-1',
  issuer: 'mall-cop',
  targetRealId: 'user-1001',
  from: T0,
  to: T0 + 2 * DAY,
  purpose: 'snooping',
  expiresAt: Date.now() + 60_000,
  nonce: '00',
  signature: 'invalid',
};
try {
  vault.reveal(forged, authority.revealKeyPem);
} catch (err) {
  console.log(`拒绝: ${(err as Error).message}`);
}

console.log('\n=== 4. 出示司法审计授权凭证 + 监管授权密钥，定向还原 ===');
const credential = authority.issueCredential({
  issuer: 'court-order-2026-0042',
  targetRealId: 'user-1001',
  from: T0,
  to: T0 + DAY,
  purpose: '刑事侦查（刑诉法授权）',
  ttlMs: 10 * 60 * 1000,
});
const trajectory = vault.reveal(credential, authority.revealKeyPem);
console.log(`凭证 ${credential.credentialId.slice(0, 8)}… 授权窗口 [t0, t0+24h)，还原 ${trajectory.length} 个轨迹点:`);
for (const p of trajectory.slice(0, 4)) {
  console.log(`  ${new Date(p.timestamp).toISOString()}  ${p.realId}  (${p.lat.toFixed(5)}, ${p.lng.toFixed(5)})`);
}
console.log('  …');

console.log('\n=== 5. 审计日志（哈希链，防篡改） ===');
for (const entry of vault.auditLog.list()) {
  console.log(`  #${entry.seq} ${entry.action} hash=${entry.hash.slice(0, 12)}… prev=${entry.prevHash.slice(0, 12)}…`);
}
console.log(`哈希链完整性校验: ${vault.auditLog.verify() ? '通过' : '失败'}`);
