import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Replica } from '../src/replica.ts';
import { VersionVector } from '../src/version-vector.ts';
import { mergeSiblings, syncBidirectional, syncOneWay } from '../src/synchronizer.ts';

test('支配关系 → 快速快照覆盖', () => {
  const r1 = new Replica('R1');
  const r2 = new Replica('R2');
  r1.put('k', 'v1', VersionVector.empty());
  r1.put('k', 'v2', r1.get('k').context); // (R1:2)

  // R2 只同步到旧版本 (R1:1)
  syncOneWay(r1, r2);
  r1.put('k', 'v3', r1.get('k').context); // (R1:3)

  const report = syncOneWay(r1, r2);
  assert.equal(report.keys[0].action, 'fast-overwrite');
  const got = r2.get('k');
  assert.equal(got.siblings.length, 1);
  assert.equal(got.siblings[0].value, 'v3');
  assert.equal(got.siblings[0].version.toString(), '{R1:3}');
});

test('验收场景：并发离散写入 → 同名分支保留待解决', () => {
  const r1 = new Replica('R1');
  const r2 = new Replica('R2');

  // 1. 客户端从 R1 读取（空上下文）并写入 → (R1:1)
  const ctx0 = r1.get('key').context;
  r1.put('key', 'v1', ctx0);
  assert.equal(r1.get('key').siblings[0].version.toString(), '{R1:1}');

  // 2. 同步到 R2，随后客户端在 R1 上并发覆盖 → (R1:2)
  syncOneWay(r1, r2);
  r1.put('key', 'v2', r1.get('key').context);
  assert.equal(r1.get('key').siblings[0].version.toString(), '{R1:2}');

  // 3. 离线客户端持有旧上下文 (R1:1)，在 R2 上写入 → (R1:1, R2:1)
  const offlineCtx = VersionVector.from({ R1: 1 });
  const offline = r2.put('key', 'offline', offlineCtx);
  assert.equal(offline.outcome, 'fast-overwrite'); // 在 R2 上覆盖了它见过的 (R1:1)
  assert.equal(offline.version.toString(), '{R1:1, R2:1}');

  // 4. 合并：(R1:2) 与 (R1:1,R2:1) 并发 → 两个同名分支都保留
  const report = syncOneWay(r2, r1);
  assert.equal(report.keys[0].action, 'sibling-fork');
  const merged = r1.get('key');
  assert.equal(merged.siblings.length, 2);
  assert.deepEqual(
    merged.siblings.map((s) => s.version.toString()).sort(),
    ['{R1:1, R2:1}', '{R1:2}'],
  );

  // 5. 上层业务解决冲突后回写，再次同步后双副本收敛
  r1.resolve('key', 'business-resolved');
  syncBidirectional(r1, r2);
  assert.equal(r2.get('key').siblings.length, 1);
  assert.equal(r2.get('key').siblings[0].value, 'business-resolved');
});

test('同步幂等且可交换', () => {
  const r1 = new Replica('R1');
  const r2 = new Replica('R2');
  r1.put('a', 1, VersionVector.empty());
  r2.put('a', 2, VersionVector.empty());
  r2.put('b', 'only-r2', VersionVector.empty());

  syncBidirectional(r1, r2);
  const snapshot1 = r1.get('a').siblings.map((s) => s.version.toString()).sort();
  const snapshot2 = r2.get('a').siblings.map((s) => s.version.toString()).sort();
  assert.deepEqual(snapshot1, snapshot2); // 收敛
  assert.equal(r1.get('b').siblings.length, 1); // 键传播

  const again = syncBidirectional(r1, r2); // 幂等：无变化
  assert.ok(again.every((r) => r.keys.every((k) => k.action !== 'sibling-fork' || k.siblingCount === 2)));
  assert.deepEqual(
    r1.get('a').siblings.map((s) => s.version.toString()).sort(),
    snapshot1,
  );
});

test('mergeSiblings 丢弃被支配分支、保留并发分支', () => {
  const older = { value: 'old', version: VersionVector.from({ R1: 1 }) };
  const newer = { value: 'new', version: VersionVector.from({ R1: 2 }) };
  const other = { value: 'other', version: VersionVector.from({ R2: 1 }) };

  assert.deepEqual(mergeSiblings([older], [newer]), {
    siblings: [newer],
    action: 'fast-overwrite',
  });
  const fork = mergeSiblings([newer], [other]);
  assert.equal(fork.action, 'sibling-fork');
  assert.equal(fork.siblings.length, 2);
  assert.equal(mergeSiblings([newer], [newer]).action, 'in-sync');
});
