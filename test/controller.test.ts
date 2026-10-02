import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ManualClock } from '../src/clock.ts';
import { NetworkHub } from '../src/network.ts';
import { Witness } from '../src/witness.ts';
import { FencedStorage } from '../src/storage.ts';
import { LeaseController } from '../src/controller.ts';
import { NotWritableError, StaleFenceError } from '../src/errors.ts';

const TTL = 10_000;

function makeCluster() {
  const clock = new ManualClock();
  const network = new NetworkHub();
  const witness = new Witness(clock);
  const make = (dcId: string) =>
    new LeaseController({ dcId, clock, network, witness, storage: new FencedStorage(dcId), ttlMs: TTL });
  return { clock, network, witness, dc1: make('dc1'), dc2: make('dc2') };
}

test('未持租约的备机不可写', () => {
  const { dc1 } = makeCluster();
  assert.equal(dc1.getRole(), 'standby');
  assert.equal(dc1.isWritable(), false);
  assert.throws(() => dc1.write('k', 'v'), NotWritableError);
});

test('持租约主节点可写，写入携带 fencing token 落盘', () => {
  const { dc1 } = makeCluster();
  assert.equal(dc1.tryAcquire(), true);
  dc1.write('k', 'v');
  assert.equal(dc1.read('k'), 'v');
});

test('租约本地到期后自动停写，即使没收到任何通知', () => {
  const { clock, dc1 } = makeCluster();
  dc1.tryAcquire();
  clock.advance(TTL);
  assert.equal(dc1.isWritable(), false);
  assert.throws(() => dc1.write('k', 'v'), NotWritableError);
});

test('失联期间续租失败不 panic，到期后停写；恢复后续租成功恢复可写', () => {
  const { clock, network, dc1 } = makeCluster();
  dc1.tryAcquire();
  network.sever('dc1', 'witness');
  clock.advance(5_000);
  dc1.maintain(); // 网络失败：保持主角色但租约会到期
  assert.equal(dc1.getRole(), 'primary');
  clock.advance(5_000); // 到期
  assert.equal(dc1.isWritable(), false);
  network.heal('dc1', 'witness');
  dc1.maintain(); // 恢复后续租成功
  assert.equal(dc1.isWritable(), true);
});

test('存储层 fencing：过期 epoch 的僵尸写被拒绝', () => {
  const s = new FencedStorage('dc-x');
  s.write('a', 1, 2); // epoch=2 写入成功
  assert.throws(() => s.write('a', 2, 1), StaleFenceError); // epoch=1 僵尸写被拒
  assert.equal(s.read('a'), 1);
  s.write('a', 3, 2); // 同 epoch 重复写允许（同一租约内多次写）
  assert.equal(s.read('a'), 3);
});
