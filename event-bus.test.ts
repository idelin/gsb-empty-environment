import test from 'node:test';
import assert from 'node:assert/strict';
import { EventBus } from './event-bus.ts';
import type { DisposeHandle } from './event-bus.ts';

test('按注册顺序调用；emit 期间新注册的订阅从下一次 emit 生效', () => {
  const bus = new EventBus<string>();
  const calls: string[] = [];

  bus.subscribe((p) => {
    calls.push(`a:${p}`);
    if (p === 'first') {
      bus.subscribe((q) => calls.push(`late:${q}`));
    }
  });
  bus.subscribe((p) => calls.push(`b:${p}`));

  bus.emit('first');
  assert.deepEqual(calls, ['a:first', 'b:first']); // late 本轮不生效

  bus.emit('second');
  assert.deepEqual(calls, [
    'a:first', 'b:first',
    'a:second', 'b:second', 'late:second', // late 追加在注册顺序末尾
  ]);
});

test('嵌套 emit：同步重入作为新一轮重新快照', () => {
  const bus = new EventBus<string>();
  const calls: string[] = [];

  bus.subscribe((p) => {
    calls.push(`outer:${p}`);
    if (p === 'a') bus.emit('b'); // 同步重入
  });
  bus.subscribe((p) => calls.push(`inner:${p}`));

  const result = bus.emit('a');
  assert.deepEqual(calls, ['outer:a', 'outer:b', 'inner:b', 'inner:a']);
  assert.equal(result.invoked, 2); // 外层这一轮只调用了 2 个监听器
});

test('once 递归：调用前已移除，重入不会再次触发', () => {
  const bus = new EventBus<number>();
  let count = 0;

  bus.once((n) => {
    count++;
    if (n < 2) bus.emit(n + 1); // 递归触发同一事件
  });

  bus.emit(0);
  assert.equal(count, 1);
  assert.equal(bus.size, 0); // once 已自动移除
});

test('监听器中互相注销：本轮之前已 dispose 的订阅被跳过', () => {
  const bus = new EventBus<void>();
  const calls: string[] = [];
  let target: DisposeHandle;

  bus.subscribe(() => {
    calls.push('first');
    target.dispose(); // 注销排在自己后面的监听器
  });
  target = bus.subscribe(() => calls.push('target'));
  bus.subscribe(() => calls.push('last'));

  const r1 = bus.emit();
  assert.deepEqual(calls, ['first', 'last']); // target 被跳过
  assert.equal(r1.invoked, 2);
  assert.equal(r1.skipped, 1);

  bus.emit();
  assert.deepEqual(calls, ['first', 'last', 'first', 'last']); // target 已彻底移除
});

test('重复 dispose 幂等，计数只减一次', () => {
  const bus = new EventBus<void>();
  const handle = bus.subscribe(() => {});

  assert.equal(bus.size, 1);
  assert.equal(handle.disposed, false);

  handle.dispose();
  handle.dispose();
  handle.dispose();

  assert.equal(bus.size, 0);
  assert.equal(handle.disposed, true);
});

test('AbortSignal 已取消：不注册，直接返回已 dispose 的句柄', () => {
  const bus = new EventBus<void>();
  const ac = new AbortController();
  ac.abort();

  const handle = bus.subscribe(
    () => { throw new Error('must never be called'); },
    { signal: ac.signal },
  );

  assert.equal(handle.disposed, true);
  assert.equal(bus.size, 0);
  const result = bus.emit(); // 不应抛出
  assert.equal(result.invoked, 0);
  assert.equal(result.errors.length, 0);
});

test('AbortSignal 订阅后取消：自动 dispose', () => {
  const bus = new EventBus<void>();
  const ac = new AbortController();
  let called = 0;

  const handle = bus.subscribe(() => called++, { signal: ac.signal });
  assert.equal(bus.size, 1);

  bus.emit();
  assert.equal(called, 1);

  ac.abort();
  assert.equal(handle.disposed, true);
  assert.equal(bus.size, 0);

  bus.emit();
  assert.equal(called, 1); // 不再被调用
});

test('异常单独收集返回：不吞掉、不阻断其他监听器', () => {
  const bus = new EventBus<void>();
  const calls: string[] = [];
  const err1 = new Error('boom-1');
  const err2 = new Error('boom-2');

  bus.subscribe(() => { calls.push('a'); throw err1; });
  bus.subscribe(() => { calls.push('b'); });
  bus.subscribe(() => { calls.push('c'); throw err2; });

  const result = bus.emit(); // emit 本身不抛

  assert.deepEqual(calls, ['a', 'b', 'c']); // 全部都被调用
  assert.equal(result.invoked, 1); // 只有 b 成功返回
  assert.equal(result.errors.length, 2);
  assert.equal(result.errors[0].error, err1);
  assert.equal(result.errors[1].error, err2);
});

test('scope：关闭后批量注销并拒绝新订阅，close 幂等', () => {
  const bus = new EventBus<void>();
  const scope = bus.scope();
  let called = 0;

  scope.subscribe(() => called++);
  scope.once(() => called++);
  assert.equal(bus.size, 2);
  assert.equal(scope.size, 2);

  scope.close();
  assert.equal(scope.closed, true);
  assert.equal(bus.size, 0);
  assert.equal(scope.size, 0);

  assert.throws(() => scope.subscribe(() => {}), /scope is already closed/);
  assert.throws(() => scope.once(() => {}), /scope is already closed/);

  scope.close(); // 幂等，不抛
  bus.emit();
  assert.equal(called, 0);
});

test('循环创建销毁 scope 后计数归零（不依赖 GC）', () => {
  const bus = new EventBus<void>();

  for (let i = 0; i < 1000; i++) {
    const scope = bus.scope();
    const h1 = scope.subscribe(() => {});
    const h2 = scope.subscribe(() => {});
    scope.once(() => {});
    assert.equal(bus.size, 3);

    h1.dispose(); // 先单独 dispose 一个
    assert.equal(bus.size, 2);

    scope.close(); // 其余随 scope 释放
    assert.equal(h2.disposed, true);
    assert.equal(bus.size, 0);
    assert.equal(scope.size, 0);
  }

  assert.equal(bus.size, 0);
});

test('弱引用订阅：存活时可用，dispose 可靠释放，sweep 不误清存活引用', () => {
  const bus = new EventBus<void>();
  let called = 0;
  const listener = () => { called++; };

  const handle = bus.subscribe(listener, { weak: true });
  bus.emit();
  assert.equal(called, 1);

  assert.equal(bus.sweep(), 0); // 存活引用不被清扫
  assert.equal(bus.size, 1);

  handle.dispose(); // 可靠释放由 dispose 负责，不等 GC
  assert.equal(bus.size, 0);

  bus.emit();
  assert.equal(called, 1);
});

test('组合：once + signal + scope 在同一总线上互不干扰', () => {
  const bus = new EventBus<string>();
  const calls: string[] = [];
  const ac = new AbortController();
  const scope = bus.scope();

  bus.once((p) => calls.push(`once:${p}`));
  bus.subscribe((p) => calls.push(`signal:${p}`), { signal: ac.signal });
  scope.subscribe((p) => calls.push(`scope:${p}`));
  assert.equal(bus.size, 3);

  bus.emit('x');
  assert.deepEqual(calls, ['once:x', 'signal:x', 'scope:x']);
  assert.equal(bus.size, 2); // once 已移除

  ac.abort();
  scope.close();
  assert.equal(bus.size, 0);

  const result = bus.emit('y');
  assert.deepEqual(calls, ['once:x', 'signal:x', 'scope:x']);
  assert.equal(result.invoked, 0);
});
