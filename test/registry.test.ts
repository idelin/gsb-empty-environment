import { test } from 'node:test';
import assert from 'node:assert/strict';
import { FontRegistry } from '../src/fontRegistry.ts';
import { monoV1, serifV1, serifV2 } from '../src/fixtures.ts';

test('热更新原子切换代际：旧快照不变，新快照代际 +1', () => {
  const registry = new FontRegistry([serifV1, monoV1]);
  const before = registry.snapshot();
  const after = registry.hotReload(serifV2);
  assert.equal(before.generation + 1, after.generation);
  assert.equal(before.get('serif')!.version, 1); // 旧快照保持完整
  assert.equal(after.get('serif')!.version, 2);
  assert.equal(after.get('mono')!.version, 1); // 其他字体不受影响
  assert.equal(registry.snapshot(), after);
});

test('热更新要求版本递增', () => {
  const registry = new FontRegistry([serifV2]);
  assert.throws(() => registry.hotReload(serifV1), /版本必须递增/);
});

test('晚到的字体加载需校验版本：旧版本不得覆盖热更新', async () => {
  const registry = new FontRegistry([serifV1]);
  let resolveLoad!: (face: typeof serifV1) => void;
  const loadPromise = registry.loadFont(
    () => new Promise((resolve) => { resolveLoad = resolve; }),
  );
  // 加载在途期间发生热更新到 v2
  registry.hotReload(serifV2);
  // 晚到的 v1 加载完成：必须被拒绝
  resolveLoad(serifV1);
  const { applied } = await loadPromise;
  assert.equal(applied, false);
  assert.equal(registry.snapshot().get('serif')!.version, 2);
});

test('晚到加载版本更新时允许应用', async () => {
  const registry = new FontRegistry([serifV1]);
  const newer = { ...serifV2, version: 3 };
  const { applied } = await registry.loadFont(() => Promise.resolve(newer));
  assert.equal(applied, true);
  assert.equal(registry.snapshot().get('serif')!.version, 3);
});

test('变更监听器在原子切换后收到新快照', () => {
  const registry = new FontRegistry([serifV1]);
  const seen: number[] = [];
  registry.onChange((snapshot, changed) => {
    seen.push(snapshot.generation);
    assert.equal(changed.id, 'serif');
    assert.equal(registry.snapshot(), snapshot); // 通知时全局快照已切换
  });
  registry.hotReload(serifV2);
  assert.deepEqual(seen, [1]);
});
