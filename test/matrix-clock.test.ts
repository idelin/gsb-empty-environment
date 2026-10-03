import { test } from 'node:test';
import assert from 'node:assert/strict';
import { MatrixClock } from '../src/matrix-clock.ts';

test('tick 只推进自己的时钟分量', () => {
  const clock = new MatrixClock(1, 3);
  const ts = clock.tick();
  assert.deepEqual(ts, [0, 1, 0]);
  assert.deepEqual(clock.ownVector(), [0, 1, 0]);
  clock.tick();
  assert.deepEqual(clock.ownVector(), [0, 2, 0]);
});

test('receive 按矩阵时钟规则合并: 自身行取 max、其余行传播、自身分量 +1', () => {
  const receiver = new MatrixClock(2, 3);
  receiver.tick(); // [0,0,1]

  // 发送方 0 的矩阵快照: 自己向量 [3,2,0], 且认知节点 1 的时钟为 4
  const senderMatrix = [
    [3, 2, 0],
    [0, 4, 0],
    [0, 0, 0],
  ];
  const ts = receiver.receive(0, senderMatrix);

  // 自身行 = max([0,0,1], [3,2,0]) 后自身分量 +1
  assert.deepEqual(ts, [3, 2, 2]);
  // 间接认知传播: 第 1 行学到「节点 1 的时钟为 4」
  assert.equal(receiver.get(1, 1), 4);
});

test('horizon 为逐列最小值, 表示全网已确认知悉的时间戳下界', () => {
  const clock = new MatrixClock(0, 3);
  // 手工构造矩阵: 行 i = 节点 i 的向量时钟认知
  clock.receive(1, [
    [5, 5, 5],
    [5, 5, 5],
    [5, 5, 5],
  ]);
  clock.receive(2, [
    [3, 7, 2],
    [3, 7, 2],
    [3, 7, 2],
  ]);
  const horizon = clock.horizon();
  // 合并后矩阵为 [[7,7,5],[5,7,5],[5,7,5]] (自身行两次 max 合并 + 两次自增)
  // 逐列取 min: col0 = min(7,5,5)=5, col1 = 7, col2 = 5
  assert.deepEqual(horizon, [5, 7, 5]);
});

test('coveredBy 逐分量支配判定', () => {
  const horizon = [3, 5, 2];
  assert.equal(MatrixClock.coveredBy([3, 5, 2], horizon), true);
  assert.equal(MatrixClock.coveredBy([2, 4, 1], horizon), true);
  assert.equal(MatrixClock.coveredBy([4, 5, 2], horizon), false);
  assert.equal(MatrixClock.coveredBy([3, 5, 3], horizon), false);
});

test('非法 nodeId 抛错', () => {
  assert.throws(() => new MatrixClock(3, 3), RangeError);
});
