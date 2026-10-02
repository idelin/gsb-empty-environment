import { test } from 'node:test';
import assert from 'node:assert/strict';
import { TemporalJoiner } from '../src/temporal-joiner.ts';
import type { JoinOutput } from '../src/temporal-joiner.ts';

function kinds(outputs: JoinOutput[]): string[] {
  return outputs.map((o) => o.kind);
}

test('验收1：L1@t=0 到达，R1@t=3 到达，立即输出配对结果', () => {
  const joiner = new TemporalJoiner();
  joiner.left({ key: '1', ts: 0 });
  assert.deepEqual(joiner.drain(), []); // 等待中，无输出

  joiner.right({ key: '1', ts: 3000 });
  const out = joiner.drain();
  assert.equal(out.length, 1);
  assert.equal(out[0].kind, 'matched');
  assert.equal(out[0].key, '1');
  assert.equal((out[0] as { late: boolean }).late, false);

  joiner.advanceTime(100_000); // 配对后不再有任何输出，状态零残留
  assert.deepEqual(joiner.drain(), []);
  assert.equal(joiner.stats().pendingLefts, 0);
});

test('验收2：L2@t=10，t=12 取消，t=15 超时不触发孤立输出', () => {
  const joiner = new TemporalJoiner();
  joiner.left({ key: '2', ts: 10_000 });
  joiner.cancel('2'); // t=12 的取消事件（处理顺序即事件顺序）

  joiner.advanceTime(15_000); // 越过原超时点 t=15
  joiner.advanceTime(100_000);
  assert.deepEqual(joiner.drain(), []); // 无任何输出

  const stats = joiner.stats();
  assert.equal(stats.cancelsApplied, 1);
  assert.equal(stats.leftOnly, 0);
  assert.equal(stats.pendingLefts, 0);
  assert.equal(stats.timedOutLefts, 0);
});

test('超时边界：窗口 5s 到期恰好输出 L-Only', () => {
  const joiner = new TemporalJoiner();
  joiner.left({ key: 'a', ts: 0 });

  joiner.advanceTime(4_999);
  assert.deepEqual(joiner.drain(), []); // 未到期

  joiner.advanceTime(5_000);
  assert.deepEqual(kinds(joiner.drain()), ['left-only']); // 恰好在 deadline 触发
});

test('窗口边界：R 恰好于 deadline（含等号）到达仍匹配', () => {
  const joiner = new TemporalJoiner();
  joiner.left({ key: 'b', ts: 0 });
  joiner.right({ key: 'b', ts: 5_000 });
  assert.deepEqual(kinds(joiner.drain()), ['matched']);
});

test('迟到 R 补配：先撤回 left-only，再输出 late matched', () => {
  const joiner = new TemporalJoiner();
  joiner.left({ key: 'c', ts: 0 });
  joiner.advanceTime(5_000); // 超时 → left-only
  assert.deepEqual(kinds(joiner.drain()), ['left-only']);

  joiner.right({ key: 'c', ts: 4_000 }); // 事件时间在窗口内，但处理上迟到
  const out = joiner.drain();
  assert.deepEqual(kinds(out), ['retract-left-only', 'matched']);
  assert.equal((out[1] as { late: boolean }).late, true);
});

test('乱序：R 先于 L 到达被缓冲，L 到达时立即匹配', () => {
  const joiner = new TemporalJoiner();
  joiner.right({ key: 'd', ts: 8_000 }); // R 先到，无 L 可配 → 缓冲
  assert.deepEqual(joiner.drain(), []);
  assert.equal(joiner.stats().bufferedRights, 1);

  joiner.left({ key: 'd', ts: 5_000 }); // 乱序的 L 到达，窗口 [5000,10000] 覆盖 R@8000
  const out = joiner.drain();
  assert.deepEqual(kinds(out), ['matched']);
  assert.equal((out[0] as { late: boolean }).late, true);
  assert.equal(joiner.stats().bufferedRights, 0);
});

test('乱序综合场景：验证状态零泄漏', () => {
  const joiner = new TemporalJoiner();

  joiner.cancel('ghost'); // 对未知 key 的取消 → 忽略，不留状态
  joiner.right({ key: 'x', ts: 1_000 }); // 缓冲的 R，永远等不到 L → 过期丢弃
  joiner.left({ key: 'y', ts: 2_000 });
  joiner.left({ key: 'y', ts: 2_000 }); // 重复左事件 → 忽略
  joiner.advanceTime(20_000); // y 超时输出 left-only；x 的缓冲 R 过期

  joiner.advanceTime(100_000);
  joiner.right({ key: 'z', ts: 500 }); // 远超允许迟到范围 → 直接丢弃

  const stats = joiner.stats();
  assert.deepEqual(kinds(joiner.drain()), ['left-only']); // 全程仅 y 的孤立输出
  assert.equal(stats.cancelsIgnored, 1);
  assert.equal(stats.duplicateLefts, 1);
  assert.equal(stats.expiredBufferedRights, 1);
  assert.equal(stats.droppedLateRights, 1);
  // 零泄漏：所有内部状态清零
  assert.equal(stats.pendingLefts, 0);
  assert.equal(stats.bufferedRights, 0);
  assert.equal(stats.timedOutLefts, 0);
});

test('取消的幂等性：匹配后取消为 no-op，不影响已发结果', () => {
  const joiner = new TemporalJoiner();
  joiner.left({ key: 'e', ts: 0 });
  joiner.right({ key: 'e', ts: 1_000 });
  joiner.cancel('e'); // 已配对，取消被忽略
  joiner.advanceTime(100_000);

  assert.deepEqual(kinds(joiner.drain()), ['matched']);
  assert.equal(joiner.stats().cancelsIgnored, 1);
});
