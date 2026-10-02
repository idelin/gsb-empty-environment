import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  signal,
  computed,
  effect,
  batch,
  TimeTravelController,
} from '../src/index.ts';

test('验收：3次修改 → Undo×2 精准恢复 → Redo×1 无漂移', () => {
  const count = signal(0);
  const step = signal(1);
  const double = computed(() => count.get() * 2);
  const total = computed(() => count.get() + step.get());

  // 模拟 UI：依赖变化时自动重渲染
  const renders: string[] = [];
  const stop = effect(() => {
    renders.push(`count=${count.get()} double=${double.get()}`);
  });

  const tt = new TimeTravelController();
  try {
    // 连续 3 次用户状态修改
    count.set(1);
    count.set(2);
    step.set(5);
    assert.equal(count.get(), 2);
    assert.equal(step.get(), 5);
    assert.equal(double.get(), 4);
    assert.equal(total.get(), 7);
    assert.equal(tt.current.id, 3); // initial + 3 次提交

    // Undo 回退 2 步 → 回到第 1 次修改后的状态
    tt.undo();
    tt.undo();
    assert.equal(tt.current.id, 1);
    assert.equal(count.get(), 1, 'count 应恢复到第一步');
    assert.equal(step.get(), 1, 'step 应恢复到第一步');
    assert.equal(double.get(), 2, 'computed double 应精准恢复');
    assert.equal(total.get(), 2, 'computed total 应精准恢复');
    // UI 自动同步：最后一次渲染就是恢复后的状态
    assert.equal(renders.at(-1), 'count=1 double=2');

    // Redo 前进 1 步 → 第 2 次修改后的状态，无漂移
    tt.redo();
    assert.equal(tt.current.id, 2);
    assert.equal(count.get(), 2);
    assert.equal(step.get(), 1);
    assert.equal(double.get(), 4);
    assert.equal(total.get(), 3);
    assert.equal(renders.at(-1), 'count=2 double=4');

    // 恢复是单事务：Undo/Redo 每次只触发一次重渲染，无中间态
    const before = renders.length;
    tt.undo();
    assert.equal(renders.length, before + 1, '一次 Undo 只触发一次重渲染');
  } finally {
    stop();
    tt.destroy();
  }
});

test('jumpTo 跳转至任意历史快照', () => {
  const value = signal('a');
  const tt = new TimeTravelController();
  try {
    value.set('b'); // #1
    value.set('c'); // #2
    value.set('d'); // #3

    const snap = tt.jumpTo(1);
    assert.equal(snap.id, 1);
    assert.equal(value.get(), 'b');

    tt.jumpTo(0); // 回到初始
    assert.equal(value.get(), 'a');
    assert.equal(tt.canUndo, false);
    assert.equal(tt.canRedo, true);

    assert.throws(() => tt.jumpTo(999), RangeError);
  } finally {
    tt.destroy();
  }
});

test('Undo 后产生新修改会创建分支，Redo 仍沿原路', () => {
  const n = signal(0);
  const tt = new TimeTravelController();
  try {
    n.set(1); // #1
    n.set(2); // #2
    tt.undo(); // 回到 #1
    n.set(10); // 从 #1 分出 #3

    assert.equal(tt.current.id, 3);
    assert.deepEqual(tt.snapshots().find((s) => s.id === 1)!.childIds, [2, 3]);

    tt.undo(); // #1
    const snap = tt.redo(); // 沿 Undo 来路 → #3
    assert.equal(snap.id, 3);
    assert.equal(n.get(), 10);
  } finally {
    tt.destroy();
  }
});

test('快照不可变：后续对对象值的篡改不影响历史', () => {
  const user = signal({ name: 'ann', tags: ['x'] });
  const tt = new TimeTravelController();
  try {
    user.set({ name: 'bob', tags: ['y'] }); // #1
    // 原地篡改当前值（不经过 set）
    user.peek().tags.push('hacked');

    tt.undo(); // 回 initial
    assert.deepEqual(user.get(), { name: 'ann', tags: ['x'] });

    tt.redo(); // 回 #1：不应看到 hacked
    assert.deepEqual(user.get(), { name: 'bob', tags: ['y'] });

    // 恢复出来的值与快照存储互不影响
    user.get().tags.push('local');
    tt.undo();
    tt.redo();
    assert.deepEqual(user.get(), { name: 'bob', tags: ['y'] });
  } finally {
    tt.destroy();
  }
});

test('batch 合并多次修改为单个快照；等值 set 不产生快照', () => {
  const a = signal(1);
  const b = signal(2);
  const tt = new TimeTravelController();
  try {
    batch(() => {
      a.set(10);
      b.set(20);
    });
    assert.equal(tt.current.id, 1, 'batch 只记录一次快照');

    a.set(10); // 等值，无变化
    assert.equal(tt.current.id, 1, '等值 set 不产生新快照');

    tt.undo();
    assert.equal(a.get(), 1);
    assert.equal(b.get(), 2);
  } finally {
    tt.destroy();
  }
});

test('computed 惰性重算且缓存：Undo 后读取触发一次重算', () => {
  const n = signal(1);
  let computeCount = 0;
  const triple = computed(() => {
    computeCount++;
    return n.get() * 3;
  });
  const tt = new TimeTravelController();
  try {
    assert.equal(triple.get(), 3);
    assert.equal(computeCount, 1);
    triple.get(); // 缓存命中
    assert.equal(computeCount, 1);

    n.set(5);
    assert.equal(triple.get(), 15);
    assert.equal(computeCount, 2);

    tt.undo();
    assert.equal(triple.get(), 3, 'Undo 后 computed 恢复到旧值');
    tt.redo();
    assert.equal(triple.get(), 15, 'Redo 后 computed 无漂移');
  } finally {
    tt.destroy();
  }
});
