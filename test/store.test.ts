import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, readFileSync, writeFileSync, existsSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { execFileSync } from 'node:child_process';
import { ReactiveStore, persist, CHECKPOINT_VERSION } from '../src/store.ts';

function tmpCheckpoint(): string {
  return join(mkdtempSync(join(tmpdir(), 'store-')), 'checkpoint.json');
}

test('检查点仅序列化带 @persist 注解的 Signal', () => {
  const file = tmpCheckpoint();
  const store = new ReactiveStore();
  const count = store.signal('count', 0, persist);
  const profile = store.signal('profile', { name: 'a', tags: ['x'] }, persist);
  const temp = store.signal('temp', 'secret');
  temp.set('changed');
  count.set(7);
  profile.set({ name: 'b', tags: ['y', 'z'] });

  store.checkpoint(file);

  const snapshot = JSON.parse(readFileSync(file, 'utf8'));
  assert.equal(snapshot.version, CHECKPOINT_VERSION);
  assert.deepEqual(Object.keys(snapshot.state).sort(), ['count', 'profile']);
  assert.deepEqual(snapshot.state.count, 7);
  assert.deepEqual(snapshot.state.profile, { name: 'b', tags: ['y', 'z'] });
  assert.ok(!('temp' in snapshot.state), '临时状态不得进入检查点');
});

test('hydrate 精确复原持久化状态并重置临时状态', () => {
  const file = tmpCheckpoint();

  // 进程 A：写入检查点
  const a = new ReactiveStore();
  const aCount = a.signal('count', 0, persist);
  const aTemp = a.signal('temp', 'init');
  aCount.set(99);
  aTemp.set('dirty');
  a.checkpoint(file);

  // 进程 B：全新存储，模拟重启
  const b = new ReactiveStore();
  const bCount = b.signal('count', 0, persist);
  const bTemp = b.signal('temp', 'init');
  bTemp.set('dirty-in-new-process');

  const report = b.hydrate(file);
  assert.equal(bCount.get(), 99, '持久化状态应完美复原');
  assert.equal(bTemp.get(), 'init', '临时状态应安全重置为初始值');
  assert.deepEqual(report.restored, ['count']);
  assert.deepEqual(report.reset, ['temp']);
  assert.deepEqual(report.skipped, []);
});

test('hydrate 触发派生 Effect 依赖链自动重新对齐', () => {
  const file = tmpCheckpoint();
  const a = new ReactiveStore();
  const aCount = a.signal('count', 0, persist);
  aCount.set(5);
  a.checkpoint(file);

  const b = new ReactiveStore();
  const count = b.signal('count', 0, persist);
  const doubled = b.computed(() => count.get() * 2);
  const seen: number[] = [];
  b.effect(() => seen.push(doubled.get()));

  assert.deepEqual(seen, [0], '初始执行一次');

  b.hydrate(file);
  assert.equal(count.get(), 5);
  assert.equal(doubled.get(), 10, '派生 computed 应重新求值');
  assert.ok(seen.includes(10), `Effect 应被 hydrate 重新触发，实际记录: ${seen}`);
});

test('hydrate 后依赖链继续正常响应后续变更', () => {
  const file = tmpCheckpoint();
  const a = new ReactiveStore();
  a.signal('n', 1, persist).set(3);
  a.checkpoint(file);

  const b = new ReactiveStore();
  const n = b.signal('n', 1, persist);
  const log: string[] = [];
  b.effect(() => log.push(`n=${n.get()}`));
  b.hydrate(file);
  n.set(100);
  assert.deepEqual(log.at(-1), 'n=100');
});

test('快照中未注册的键被安全跳过', () => {
  const file = tmpCheckpoint();
  writeFileSync(file, JSON.stringify({
    version: CHECKPOINT_VERSION,
    createdAt: new Date().toISOString(),
    state: { known: 1, ghost: 'x' },
  }));

  const store = new ReactiveStore();
  const known = store.signal('known', 0, persist);
  const report = store.hydrate(file);
  assert.equal(known.get(), 1);
  assert.deepEqual(report.skipped, ['ghost']);
});

test('版本不兼容的检查点抛出明确错误', () => {
  const file = tmpCheckpoint();
  writeFileSync(file, JSON.stringify({ version: 999, state: {} }));
  const store = new ReactiveStore();
  assert.throws(() => store.hydrate(file), /不兼容的检查点版本/);
});

test('跨进程：进程重启后从检查点恢复', () => {
  const file = tmpCheckpoint();
  const fixture = new URL('../test-fixture/process-lifecycle.ts', import.meta.url).pathname;

  // 进程 A：写入检查点
  execFileSync(process.execPath, [fixture, 'write', file], { stdio: 'inherit' });
  assert.ok(existsSync(file));

  // 进程 B：全新进程加载检查点并输出恢复后的状态
  const out = execFileSync(process.execPath, [fixture, 'read', file], { encoding: 'utf8' });
  const result = JSON.parse(out.trim().split('\n').at(-1)!);

  assert.deepEqual(result.persisted, { count: 42, theme: 'dark' });
  assert.equal(result.temp, 'init', '临时状态在新进程中保持初始值');
  assert.ok(result.effectRuns >= 2, 'Effect 至少执行：初始一次 + hydrate 重对齐一次');
  assert.equal(result.lastDerived, 'count=42/theme=dark');
});
