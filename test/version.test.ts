import assert from 'node:assert/strict';
import test from 'node:test';
import { PolicyEngine } from '../src/engine.ts';
import { inSet, or } from '../src/expr.ts';
import { AttrStore } from '../src/store.ts';

// 策略：a==7 或 b==9 时允许。初始 a=1, b=1 => 应拒绝；
// 若在读取 b 期间 a 被热更新为 7，混版本会错误拒绝/放行，
// 引擎必须检测到版本变化并重试，最终按一致快照 a=7 放行。
test('请求过程中属性版本变化：重试并基于一致快照得出结论', async () => {
  const data: Record<string, number | undefined> = { a: 1, b: 1 };
  const store = new AttrStore(async (_s, attr) => data[attr]);
  const realRead = store.read.bind(store);
  let bumped = false;
  store.read = async (subject, attr) => {
    const entry = await realRead(subject, attr);
    if (attr === 'b' && !bumped) {
      bumped = true;
      store.update(subject, 'a', 7); // 模拟请求中途的热更新
    }
    return entry;
  };

  const engine = new PolicyEngine(store, or(inSet('a', [7]), inSet('b', [9])));
  const d = await engine.decide('s');
  assert.equal(d.status, 'decided');
  assert.equal(d.attempts, 2); // 第一次冲突，第二次成功
  assert.equal(d.value, 'true'); // 一致快照下 a=7 => 允许
  assert.deepEqual(d.explanation, { a: 7 });
});

test('持续版本冲突：返回 conflict 而非混版本放行', async () => {
  const data: Record<string, number | undefined> = { a: 1, b: 1 };
  const store = new AttrStore(async (_s, attr) => data[attr]);
  const realRead = store.read.bind(store);
  store.read = async (subject, attr) => {
    const entry = await realRead(subject, attr);
    if (attr === 'b') store.update(subject, 'a', 1); // 每次都让 a 的版本失效
    return entry;
  };

  const engine = new PolicyEngine(store, or(inSet('a', [7]), inSet('b', [9])));
  const d = await engine.decide('s', { maxRetries: 3 });
  assert.equal(d.status, 'conflict');
  assert.equal(d.attempts, 3);
  assert.equal(engine.cacheSize, 0); // 冲突结果绝不入缓存
});
