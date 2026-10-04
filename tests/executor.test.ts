import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  DependencyExecutor,
  compareNodeIds,
  type RunReport,
} from '../src/executor.ts';

interface Deferred<T> {
  promise: Promise<T>;
  resolve: (value: T) => void;
  reject: (error: unknown) => void;
}

function deferred<T>(): Deferred<T> {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

const ok = (value: unknown) => () => value;
const boom = (message: string) => () => {
  throw new Error(message);
};

test('基本 DAG：按拓扑顺序执行并收集结果', async () => {
  const order: string[] = [];
  const ex = new DependencyExecutor();
  ex.addNode('a', () => (order.push('a'), 'A'));
  ex.addNode('b', () => (order.push('b'), 'B'), ['a']);
  ex.addNode('c', () => (order.push('c'), 'C'), ['a']);
  ex.addNode('d', () => (order.push('d'), 'D'), ['b', 'c']);

  const report = await ex.run();

  assert.deepEqual(report.succeeded, ['a', 'b', 'c', 'd']);
  assert.equal(report.results.get('d')!.value, 'D');
  assert.ok(order.indexOf('a') < order.indexOf('b'));
  assert.ok(order.indexOf('a') < order.indexOf('c'));
  assert.ok(order.indexOf('b') < order.indexOf('d'));
  assert.ok(order.indexOf('c') < order.indexOf('d'));
});

test('重复边：deps 去重，后继只注册一次，撤销原因不重复', async () => {
  const ex = new DependencyExecutor();
  ex.addNode('a', boom('a failed'));
  ex.addNode('b', ok('B'), ['a', 'a', 'a']);

  assert.deepEqual(ex.getDependencies('b'), ['a']);

  const report = await ex.run();
  const b = report.results.get('b')!;
  assert.equal(b.status, 'revoked');
  assert.deepEqual(b.revocation!.causes, ['a']);
  assert.equal(b.revocation!.primaryCause, 'a');
  assert.deepEqual(b.revocation!.chain, ['a', 'b']);
});

test('重复节点 id / 未知依赖 / 环：直接报错', async () => {
  const dup = new DependencyExecutor();
  dup.addNode('a', ok(1));
  assert.throws(() => dup.addNode('a', ok(2)), /duplicate node id/);

  const missing = new DependencyExecutor();
  missing.addNode('a', ok(1), ['ghost']);
  await assert.rejects(() => missing.run(), /unknown node/);

  const cyclic = new DependencyExecutor();
  cyclic.addNode('a', ok(1), ['c']);
  cyclic.addNode('b', ok(1), ['a']);
  cyclic.addNode('c', ok(1), ['b']);
  await assert.rejects(() => cyclic.run(), /cycle/);

  const selfLoop = new DependencyExecutor();
  selfLoop.addNode('a', ok(1), ['a']);
  await assert.rejects(() => selfLoop.run(), /cycle/);
});

test('菱形依赖：全部成功时汇合节点正常执行', async () => {
  const ex = new DependencyExecutor();
  ex.addNode('n1', ok(1));
  ex.addNode('n2', ok(2), ['n1']);
  ex.addNode('n3', ok(3), ['n1']);
  ex.addNode('n4', ok(4), ['n2', 'n3']);

  const report = await ex.run();
  assert.deepEqual(report.succeeded, ['n1', 'n2', 'n3', 'n4']);
  assert.equal(report.results.get('n4')!.value, 4);
});

test('菱形依赖：根失败时撤销沿两条边传播并汇合原因链', async () => {
  const ex = new DependencyExecutor();
  ex.addNode('n1', boom('root failed'));
  ex.addNode('n2', ok(2), ['n1']);
  ex.addNode('n3', ok(3), ['n1']);
  ex.addNode('n4', ok(4), ['n2', 'n3']);

  const report = await ex.run();

  assert.equal(report.results.get('n1')!.status, 'failed');
  assert.deepEqual(report.revoked.sort(), ['n2', 'n3', 'n4']);

  const n2 = report.results.get('n2')!.revocation!;
  assert.equal(n2.kind, 'propagated');
  assert.deepEqual(n2.chain, ['n1', 'n2']);

  // 汇合点 n4 的两个前驱都被撤销：主因取编号最小者 n2，链沿主因回溯
  const n4 = report.results.get('n4')!.revocation!;
  assert.deepEqual(n4.causes, ['n2', 'n3']);
  assert.equal(n4.primaryCause, 'n2');
  assert.deepEqual(n4.chain, ['n1', 'n2', 'n4']);
});

test('多依赖同时失败：全部记入 causes，按节点编号（数值序）选主因', async () => {
  const ex = new DependencyExecutor();
  // 数字 id：若按字典序 '10' < '2'，主因会错选 10
  ex.addNode(10, boom('ten failed'));
  ex.addNode(2, boom('two failed'));
  ex.addNode(99, ok('never'), [10, 2]);

  const report = await ex.run();

  const revocation = report.results.get(99)!.revocation!;
  assert.deepEqual(new Set(revocation.causes), new Set([10, 2]));
  assert.equal(revocation.primaryCause, 2);
  assert.deepEqual(revocation.chain, [2, 99]);
});

test('compareNodeIds：数字按数值，数字先于字符串，字符串按字典序', () => {
  assert.ok(compareNodeIds(2, 10) < 0);
  assert.ok(compareNodeIds('2', '10') < 0);
  assert.ok(compareNodeIds(1, 'a') < 0);
  assert.ok(compareNodeIds('b', 'a') > 0);
  assert.equal(compareNodeIds('x', 'x'), 0);
});

test('运行中撤销：未启动后继随之撤销并记录原因链，被撤销节点保留结果', async () => {
  const gate = deferred<string>();
  const started = deferred<void>();
  const ex = new DependencyExecutor();
  ex.addNode('a', async () => {
    started.resolve();
    return gate.promise;
  });
  ex.addNode('b', ok('B'), ['a']);
  ex.addNode('c', ok('C'), ['b']);

  const runPromise = ex.run();
  await started.promise;

  // a 运行中被撤销：b、c 尚未启动，随之撤销
  assert.equal(ex.revoke('a', 'no longer needed'), true);
  assert.equal(ex.getStatus('b'), 'revoked');
  assert.equal(ex.getStatus('c'), 'revoked');

  // a 已启动，继续跑完并保留自身结果
  gate.resolve('A-result');
  const report = await runPromise;

  const a = report.results.get('a')!;
  assert.equal(a.status, 'succeeded');
  assert.equal(a.value, 'A-result');
  assert.equal(a.revokeRequested, true);

  const b = report.results.get('b')!.revocation!;
  assert.deepEqual(b.chain, ['a', 'b']);
  const c = report.results.get('c')!.revocation!;
  assert.deepEqual(c.chain, ['a', 'b', 'c']);
});

test('已启动后继保留自身结果：撤销运行中节点不影响其兄弟分支', async () => {
  const gateB = deferred<string>();
  const bStarted = deferred<void>();
  const ex = new DependencyExecutor();
  ex.addNode('a', ok('A'));
  ex.addNode('b', async () => {
    bStarted.resolve();
    return gateB.promise;
  }, ['a']);
  ex.addNode('c', ok('C'), ['a']);
  ex.addNode('d', ok('D'), ['b']);

  const runPromise = ex.run();
  await bStarted.promise;
  await sleep(0); // 让 c 完成

  // b 运行中被撤销：d（未启动）随之撤销；b、c 的结果保留
  assert.equal(ex.revoke('b'), true);
  gateB.resolve('B-result');
  const report = await runPromise;

  assert.equal(report.results.get('b')!.status, 'succeeded');
  assert.equal(report.results.get('b')!.value, 'B-result');
  assert.equal(report.results.get('c')!.status, 'succeeded');
  assert.equal(report.results.get('c')!.value, 'C');

  const d = report.results.get('d')!;
  assert.equal(d.status, 'revoked');
  assert.deepEqual(d.revocation!.chain, ['b', 'd']);
});

test('完成与撤销竞争（同 tick）：撤销先生效，节点结果仍保留', async () => {
  const gate = deferred<string>();
  const started = deferred<void>();
  const ex = new DependencyExecutor();
  ex.addNode('a', async () => {
    started.resolve();
    return gate.promise;
  });
  ex.addNode('b', ok('B'), ['a']);

  const runPromise = ex.run();
  await started.promise;

  // 同一 tick 内先 resolve 再 revoke：任务完成的回调排在微任务队列，
  // 同步的 revoke 先执行，此时 a 仍是 running，撤销对后继生效。
  gate.resolve('A-result');
  assert.equal(ex.revoke('a'), true);

  const report = await runPromise;
  const a = report.results.get('a')!;
  assert.equal(a.status, 'succeeded');
  assert.equal(a.value, 'A-result');
  assert.equal(a.revokeRequested, true);
  assert.equal(report.results.get('b')!.status, 'revoked');
});

test('完成与撤销竞争（完成后）：撤销为无操作，后继正常执行', async () => {
  const gate = deferred<string>();
  const started = deferred<void>();
  const ex = new DependencyExecutor();
  ex.addNode('a', async () => {
    started.resolve();
    return gate.promise;
  });
  ex.addNode('b', ok('B'), ['a']);

  const runPromise = ex.run();
  await started.promise;

  gate.resolve('A-result');
  await sleep(0); // 等待完成被调度器观察，a 进入 succeeded

  assert.equal(ex.revoke('a'), false);
  const report = await runPromise;
  assert.equal(report.results.get('a')!.status, 'succeeded');
  assert.equal(report.results.get('b')!.status, 'succeeded');
  assert.equal(report.results.get('b')!.value, 'B');
});

test('失败传播：未启动后继撤销，错误保留在失败节点上', async () => {
  const ex = new DependencyExecutor();
  const error = new Error('disk full');
  ex.addNode('a', () => {
    throw error;
  });
  ex.addNode('b', ok('B'), ['a']);
  ex.addNode('c', ok('C'), ['b']);

  const report = await ex.run();
  assert.equal(report.results.get('a')!.status, 'failed');
  assert.equal(report.results.get('a')!.error, error);
  assert.equal(report.results.get('b')!.revocation!.kind, 'propagated');
  assert.deepEqual(report.results.get('c')!.revocation!.chain, ['a', 'b', 'c']);
});

test('手动撤销 pending 节点：kind 为 manual，后继链从它开始', async () => {
  const ex = new DependencyExecutor();
  ex.addNode('a', ok('A'));
  ex.addNode('b', ok('B'), ['a']);

  assert.equal(ex.revoke('a', 'user cancelled'), true);
  const report = await ex.run();

  const a = report.results.get('a')!.revocation!;
  assert.equal(a.kind, 'manual');
  assert.equal(a.message, 'user cancelled');
  assert.deepEqual(a.chain, ['a']);
  assert.deepEqual(report.results.get('b')!.revocation!.chain, ['a', 'b']);
});

test('撤销幂等：终态节点的撤销请求返回 false', async () => {
  const ex = new DependencyExecutor();
  ex.addNode('a', ok('A'));
  const report = await ex.run();
  assert.equal(report.results.get('a')!.status, 'succeeded');
  assert.equal(ex.revoke('a'), false);
  assert.equal(ex.getStatus('a'), 'succeeded');
});

test('运行中被撤销时任务收到 AbortSignal', async () => {
  const gate = deferred<string>();
  const started = deferred<void>();
  let aborted = false;
  const ex = new DependencyExecutor();
  ex.addNode('a', async ({ signal }) => {
    signal.addEventListener('abort', () => {
      aborted = true;
    });
    started.resolve();
    return gate.promise;
  });

  const runPromise = ex.run();
  await started.promise;
  ex.revoke('a');
  assert.equal(aborted, true);
  gate.resolve('done');
  await runPromise;
});

test('并发限制：同时运行的任务数不超过 concurrency', async () => {
  const ex = new DependencyExecutor({ concurrency: 2 });
  let current = 0;
  let peak = 0;
  const task = async () => {
    current += 1;
    peak = Math.max(peak, current);
    await sleep(10);
    current -= 1;
  };
  for (const id of [1, 2, 3, 4, 5]) ex.addNode(id, task);

  const report: RunReport = await ex.run();
  assert.equal(report.succeeded.length, 5);
  assert.ok(peak <= 2, `peak concurrency ${peak} exceeds 2`);
});
