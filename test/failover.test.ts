/**
 * 验收边界端到端测试：
 * 机房1 主 / 机房2 备 → 光纤挖断 → 法官确认机房1 失联 → 机房2 获晋升租约
 * → 机房1 恢复后检测凭证失效 → 自动切换只读备机。全程无双写。
 */
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ManualClock } from '../src/clock.ts';
import { NetworkHub } from '../src/network.ts';
import { Witness } from '../src/witness.ts';
import { FencedStorage } from '../src/storage.ts';
import { LeaseController } from '../src/controller.ts';
import { NotWritableError, StaleFenceError } from '../src/errors.ts';

const TTL = 10_000;

test('跨机房故障转移：全程任一时刻至多一个可写主节点', () => {
  const clock = new ManualClock();
  const network = new NetworkHub();
  const witness = new Witness(clock);
  const storage1 = new FencedStorage('dc1');
  const storage2 = new FencedStorage('dc2');
  const dc1 = new LeaseController({ dcId: 'dc1', clock, network, witness, storage: storage1, ttlMs: TTL });
  const dc2 = new LeaseController({ dcId: 'dc2', clock, network, witness, storage: storage2, ttlMs: TTL });

  // 不变式检查器：任一时刻至多一个可写者
  const assertSingleWriter = () => {
    const writers = [dc1, dc2].filter((c) => c.isWritable());
    assert.ok(writers.length <= 1, `出现双写主节点: ${writers.map((c) => c.dcId)}`);
  };

  // 1) 机房1 为主，机房2 为备
  assert.equal(dc1.tryAcquire(), true);
  assert.equal(dc2.tryAcquire(), false);
  dc1.write('order/1', 'a');
  clock.advance(4_000);
  dc1.maintain(); // 续租 -> expiresAt=14000
  assertSingleWriter();

  // 2) 光纤挖断：机房1 与法官、机房2 失联
  network.sever('dc1', 'witness');
  network.sever('dc1', 'dc2');
  clock.advance(4_000); // t=8000
  dc1.maintain(); // 续租失败：法官不可达
  assertSingleWriter();

  // 备机房在租约到期前无法接管（法官确认旧租约仍活跃）
  assert.equal(dc2.tryAcquire(), false);
  assert.equal(dc2.getRole(), 'standby');
  assertSingleWriter();

  // 3) 租约真正过期：dc1 自动停写，法官向 dc2 颁发晋升租约
  clock.advance(6_000); // t=14000，dc1 租约到期
  assert.equal(dc1.isWritable(), false, '失联的旧主必须在租约到期后停写');
  assert.equal(dc2.tryAcquire(), true);
  assert.equal(dc2.getRole(), 'primary');
  assert.equal(dc2.currentLease()!.epoch, 2, '晋升租约 epoch 必须递增');
  dc2.write('order/2', 'b');
  assertSingleWriter();

  // 3b) 旧主僵尸写（携带过期 epoch=1）被存储层 fencing 拒绝
  assert.throws(() => storage2.write('order/x', 'zombie', 1), StaleFenceError);

  // 4) 机房1 恢复：检测到法官凭证失效，自动降级只读备机
  network.heal('dc1', 'witness');
  network.heal('dc1', 'dc2');
  assert.equal(witness.validate('dc1', 1), false, '法官侧旧凭证已失效');
  dc1.maintain(); // 续租被拒 -> 降级
  assert.equal(dc1.getRole(), 'standby');
  assert.equal(dc1.isWritable(), false);
  assert.throws(() => dc1.write('order/3', 'c'), NotWritableError);
  assertSingleWriter();

  // 终态：dc2 唯一可写主，dc1 只读备
  assert.equal(dc2.isWritable(), true);
  assert.equal(dc2.read('order/2'), 'b');
});
