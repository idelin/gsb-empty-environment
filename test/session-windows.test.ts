import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  SessionWindowMerger,
  type WindowChange,
} from '../src/session-windows.ts';

function summarize(changes: WindowChange[]): string[] {
  return changes.map(
    (c) => `${c.kind}[${c.window.start}, ${c.window.end}]x${c.window.count}`,
  );
}

test('验收场景：gap=5，迟到事件合并双窗口，删除后重新分裂', () => {
  const merger = new SessionWindowMerger({ gap: 5, maxLateness: 10 });

  // t=0 到达 -> 窗口 [0, 0]
  assert.deepEqual(summarize(merger.addEvent('e0', 0)), ['upsert[0, 0]x1']);

  // t=9 到达，间隔 9 > gap=5 -> 第二个独立窗口 [9, 9]
  assert.deepEqual(summarize(merger.addEvent('e9', 9)), ['upsert[9, 9]x1']);
  assert.deepEqual(
    merger.getWindows().map((w) => [w.start, w.end]),
    [
      [0, 0],
      [9, 9],
    ],
  );

  // 迟到事件 t=4 到达，桥接两个窗口 -> 合并为 [0, 9]，先撤销旧窗口
  assert.deepEqual(summarize(merger.addEvent('e4', 4)), [
    'retract[0, 0]x1',
    'retract[9, 9]x1',
    'upsert[0, 9]x3',
  ]);
  assert.deepEqual(
    merger.getWindows().map((w) => [w.start, w.end]),
    [[0, 9]],
  );

  // 删除 t=4 -> 窗口重新分裂为 [0, 0] 与 [9, 9]
  assert.deepEqual(summarize(merger.removeEvent('e4')), [
    'retract[0, 9]x3',
    'upsert[0, 0]x1',
    'upsert[9, 9]x1',
  ]);
  assert.deepEqual(
    merger.getWindows().map((w) => [w.start, w.end]),
    [
      [0, 0],
      [9, 9],
    ],
  );
});

test('相邻事件间隔恰好等于 gap 时合并', () => {
  const merger = new SessionWindowMerger({ gap: 5, maxLateness: 0 });
  merger.addEvent('a', 0);
  assert.deepEqual(summarize(merger.addEvent('b', 5)), [
    'retract[0, 0]x1',
    'upsert[0, 5]x2',
  ]);
});

test('乱序事件插入窗口中间只扩展窗口', () => {
  const merger = new SessionWindowMerger({ gap: 5, maxLateness: 100 });
  merger.addEvent('a', 10);
  merger.addEvent('b', 14);
  // t=12 落在已有窗口 [10, 14] 内部：边界不变，仅 count 更新 -> 同键 upsert
  assert.deepEqual(summarize(merger.addEvent('c', 12)), [
    'upsert[10, 14]x3',
  ]);
});

test('水位线推进后丢弃超出最大延迟范围的状态', () => {
  const merger = new SessionWindowMerger({ gap: 5, maxLateness: 10 });
  merger.addEvent('e0', 0);
  merger.addEvent('e9', 9);
  merger.addEvent('e4', 4); // 合并为 [0, 9]
  assert.equal(merger.stateSize, 3);

  // 水位线 10：horizon = 10 - 10 - 5 = -5，窗口 [0, 9] 保留
  assert.deepEqual(merger.advanceWatermark(10), []);
  assert.equal(merger.stateSize, 3);

  // 水位线 30：horizon = 30 - 10 - 5 = 15，窗口 end=9 < 15 -> 状态被丢弃
  const discarded = merger.advanceWatermark(30);
  assert.deepEqual(
    discarded.map((w) => [w.start, w.end]),
    [[0, 9]],
  );
  assert.equal(merger.stateSize, 0);
  assert.deepEqual(merger.getWindows(), []);

  // 过于迟到的事件（t=5 < 30 - 10 = 20）被丢弃，不产生任何变更
  assert.deepEqual(merger.addEvent('late', 5), []);
  assert.equal(merger.stateSize, 0);

  // 水位线允许范围内的事件仍可正常开窗
  assert.deepEqual(summarize(merger.addEvent('fresh', 25)), [
    'upsert[25, 25]x1',
  ]);
});

test('水位线单调推进，回退无效', () => {
  const merger = new SessionWindowMerger({ gap: 5, maxLateness: 0 });
  merger.advanceWatermark(100);
  assert.deepEqual(merger.advanceWatermark(50), []);
  assert.equal(merger.currentWatermark, 100);
});

test('删除未知事件与重复添加同一 id', () => {
  const merger = new SessionWindowMerger({ gap: 5, maxLateness: 0 });
  assert.deepEqual(merger.removeEvent('ghost'), []);
  merger.addEvent('e', 0);
  // 同 id 更新事件时间：窗口从 [0, 0] 变为 [3, 3]
  assert.deepEqual(summarize(merger.addEvent('e', 3)), [
    'retract[0, 0]x1',
    'upsert[3, 3]x1',
  ]);
  assert.equal(merger.stateSize, 1);
});

test('变更流往返一致：合并后再删除桥接事件可完全还原', () => {
  const merger = new SessionWindowMerger({ gap: 5, maxLateness: 100 });
  const log: string[] = [];
  const apply = (changes: WindowChange[]) => {
    log.push(...summarize(changes));
  };
  apply(merger.addEvent('e0', 0));
  apply(merger.addEvent('e9', 9));
  apply(merger.addEvent('e4', 4));
  apply(merger.removeEvent('e4'));
  assert.deepEqual(log, [
    'upsert[0, 0]x1',
    'upsert[9, 9]x1',
    'retract[0, 0]x1',
    'retract[9, 9]x1',
    'upsert[0, 9]x3',
    'retract[0, 9]x3',
    'upsert[0, 0]x1',
    'upsert[9, 9]x1',
  ]);
});
