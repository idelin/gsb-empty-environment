import assert from 'node:assert/strict';
import test from 'node:test';
import { PolicyEngine } from '../src/engine.ts';
import { and, inSet, lit, or } from '../src/expr.ts';
import { AttrStore } from '../src/store.ts';

test('两主体缓存隔离：结论与失效互不影响', async () => {
  const perSubject: Record<string, number> = { alice: 1, bob: 2 };
  let fetches = 0;
  const store = new AttrStore(async (subject, _attr) => {
    fetches++;
    return perSubject[subject];
  });
  const engine = new PolicyEngine(store, inSet('role', [1]));

  const a1 = await engine.decide('alice');
  const b1 = await engine.decide('bob');
  assert.equal(a1.status, 'decided');
  assert.equal(a1.value, 'true');
  assert.equal(b1.status, 'decided');
  assert.equal(b1.value, 'false'); // 未受 alice 的允许缓存污染

  // 热更新 alice 的属性：只有 alice 的缓存失效
  store.update('alice', 'role', 5);
  const before = fetches;
  const b2 = await engine.decide('bob');
  assert.equal(b2.status, 'decided');
  assert.equal(b2.fromCache, true); // bob 缓存仍有效
  assert.equal(fetches, before); // 没有重新取数
  const a2 = await engine.decide('alice');
  assert.equal(a2.fromCache, false);
  assert.equal(a2.value, 'false');
});

test('属性热更新只失效读取了该属性的缓存', async () => {
  const values: Record<string, number | undefined> = { a: 1, b: 2, c: 3 };
  const store = new AttrStore(async (_s, attr) => values[attr]);
  const engine = new PolicyEngine(
    store,
    and(inSet('a', [1]), inSet('b', [2])),
  );
  const d1 = await engine.decide('s');
  assert.equal(d1.value, 'true');

  store.update('s', 'c', 99); // 策略未读取 c
  const d2 = await engine.decide('s');
  assert.equal(d2.fromCache, true); // 缓存不受影响

  store.update('s', 'a', 7); // 策略读取了 a
  const d3 = await engine.decide('s');
  assert.equal(d3.fromCache, false); // 精确失效
  assert.equal(d3.value, 'false');
});

test('规则热更新使全部缓存失效', async () => {
  const values: Record<string, number | undefined> = { a: 1 };
  const store = new AttrStore(async (_s, attr) => values[attr]);
  const engine = new PolicyEngine(store, inSet('a', [1]));
  const d1 = await engine.decide('s');
  assert.equal(d1.value, 'true');

  engine.setPolicy(inSet('a', [2]));
  const d2 = await engine.decide('s');
  assert.equal(d2.fromCache, false);
  assert.equal(d2.value, 'false');
});

test('取消不留下可复用的缓存', async () => {
  const values: Record<string, number | undefined> = { a: 1, b: 2 };
  const store = new AttrStore(async (_s, attr) => values[attr]);
  const engine = new PolicyEngine(store, and(inSet('a', [1]), inSet('b', [2])));

  const controller = new AbortController();
  const realRead = store.read.bind(store);
  store.read = async (subject, attr) => {
    const entry = await realRead(subject, attr);
    if (attr === 'a') controller.abort(); // 读完 a 后取消，b 不应被读取
    return entry;
  };
  const aborted = await engine.decide('s', { signal: controller.signal });
  assert.equal(aborted.status, 'aborted');
  assert.equal(engine.cacheSize, 0); // 无残留缓存

  // 恢复正常读取后可重新得出一致结论
  store.read = realRead;
  const d = await engine.decide('s');
  assert.equal(d.status, 'decided');
  assert.equal(d.value, 'true');
});

test('取值失败不留下可复用的错误允许缓存', async () => {
  let fail = true;
  const store = new AttrStore(async (_s, _attr) => {
    if (fail) throw new Error('attribute service unavailable');
    return 1;
  });
  const engine = new PolicyEngine(store, or(inSet('a', [1]), lit(false)));

  const err = await engine.decide('s');
  assert.equal(err.status, 'error');
  assert.equal(engine.cacheSize, 0); // 失败结果不入缓存

  fail = false;
  const ok = await engine.decide('s');
  assert.equal(ok.status, 'decided');
  assert.equal(ok.fromCache, false); // 重新计算，而非复用错误结果
  assert.equal(ok.value, 'true');
});
