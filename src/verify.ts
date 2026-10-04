/**
 * 可控计算器验证：七个场景覆盖需求里的全部关键行为，
 * 每个场景打印断言结果与可核对的命中账本。
 */
import { ControlledComputer } from './controlled-computer';
import { LEDGER_KINDS, Ledger } from './ledger';
import { AbortError, ResultCache } from './result-cache';
import { VersionTracker } from './version-tracker';

let failures = 0;

function check(name: string, condition: boolean, detail = ''): void {
  if (!condition) failures += 1;
  console.log(`  [${condition ? 'PASS' : 'FAIL'}] ${name}${detail === '' ? '' : ` — ${detail}`}`);
}

function flush(): Promise<void> {
  return new Promise((resolve) => setImmediate(resolve));
}

function printLedger(ledger: Ledger): void {
  console.log('  命中账本:');
  for (const line of ledger.format().split('\n')) {
    console.log(`    ${line}`);
  }
  const counts = ledger.counts();
  console.log(`    合计 ${LEDGER_KINDS.map((kind) => `${kind}=${counts[kind]}`).join(' ')}`);
}

type Params = { scale: number; offset?: number };

function scaleCopy(input: Float64Array, scale: number): Float64Array {
  const out = new Float64Array(input.length);
  for (let index = 0; index < input.length; index += 1) {
    out[index] = input[index] * scale;
  }
  return out;
}

function newComputer(): ControlledComputer<Float64Array, Params, Float64Array> {
  return new ControlledComputer<Float64Array, Params, Float64Array>((input, params) =>
    scaleCopy(input, params.scale),
  );
}

function newCache(
  computer: ControlledComputer<Float64Array, Params, Float64Array>,
  tracker: VersionTracker,
  capacity = 8,
): ResultCache<Float64Array, Params, Float64Array> {
  return new ResultCache<Float64Array, Params, Float64Array>({
    capacity,
    compute: computer.compute,
    tracker,
  });
}

/** run + 立即兑现所有受控调用，返回结果。 */
function settle<R>(promise: Promise<R>, computer: { completeAll(): void }): Promise<R> {
  computer.completeAll();
  return promise;
}

async function scenario1(): Promise<void> {
  console.log('\n场景 1: 并发同键请求合并（只算一次，各拿独立副本）');
  const tracker = new VersionTracker();
  const computer = newComputer();
  const cache = newCache(computer, tracker);
  const data = new Float64Array([1, 2, 3]);

  const first = cache.run(data, { scale: 2 });
  const second = cache.run(data, { scale: 2 });
  check('同键并发只触发一次计算', computer.invocations === 1, `invocations=${computer.invocations}`);
  computer.completeAll();
  const [a, b] = await Promise.all([first, second]);
  check(
    '两个调用方都拿到正确结果',
    Array.from(a).join(',') === '2,4,6' && Array.from(b).join(',') === '2,4,6',
  );
  check('两个调用方拿到独立副本', a !== b);
  printLedger(cache.ledger);
}

async function scenario2(): Promise<void> {
  console.log('\n场景 2: 参数变化不能误命中（规范化参数参与键）');
  const tracker = new VersionTracker();
  const computer = newComputer();
  const cache = newCache(computer, tracker);
  const data = new Float64Array([1, 2, 3]);

  await settle(cache.run(data, { scale: 2 }), computer);
  await settle(cache.run(data, { scale: 3 }), computer);
  const again = await settle(cache.run(data, { scale: 2 }), computer);
  check('参数不同各自计算、参数相同命中', computer.invocations === 2, `invocations=${computer.invocations}`);
  check('命中返回正确结果', Array.from(again).join(',') === '2,4,6');

  await settle(cache.run(data, { scale: 4, offset: 1 }), computer);
  const before = computer.invocations;
  await settle(cache.run(data, { offset: 1, scale: 4 }), computer);
  check('参数键顺序不影响命中', computer.invocations === before);
  await settle(cache.run(data, { scale: 4, offset: 2 }), computer);
  check('参数值变化触发重算', computer.invocations === before + 1);
  printLedger(cache.ledger);
}

async function scenario3(): Promise<void> {
  console.log('\n场景 3: 共享数组视图追踪真实底层版本');
  const tracker = new VersionTracker();
  const computer = newComputer();
  const cache = newCache(computer, tracker);
  const base = new Float64Array([1, 2, 3, 4]);
  const view = base.subarray(1, 3);

  check('视图与底层共享同一身份', tracker.identityOf(view) === tracker.identityOf(base));
  tracker.touch(base);
  check(
    '视图读到的是底层版本',
    tracker.versionOf(view) === tracker.versionOf(base) && tracker.versionOf(view) === 1,
  );

  await settle(cache.run(view, { scale: 1 }), computer);
  await settle(cache.run(view, { scale: 1 }), computer);
  check('同一视图重复请求命中', computer.invocations === 1);

  await settle(cache.run(base, { scale: 1 }), computer);
  check('同底层不同视图形状不会误命中', computer.invocations === 2);

  tracker.mutate(base, (b) => {
    b[1] = 20;
  });
  const after = await settle(cache.run(view, { scale: 1 }), computer);
  check('通过其他视图修改后旧缓存失效', computer.invocations === 3);
  check('新结果反映新数据', Array.from(after).join(',') === '20,3');
  printLedger(cache.ledger);
}

async function scenario4(): Promise<void> {
  console.log('\n场景 4: 旧任务晚到不得发布（新输入覆盖）');
  const tracker = new VersionTracker();
  const computer = newComputer();
  const cache = newCache(computer, tracker);
  const data = new Float64Array([1, 2, 3]);

  const slow = cache.run(data, { scale: 1 });
  tracker.mutate(data, (d) => {
    d[0] = 100;
  });
  const fresh = cache.run(data, { scale: 1 });
  check('两个版本各自在途计算', computer.pendingCount === 2, `pending=${computer.pendingCount}`);

  computer.completeNext();
  await flush();
  check('旧任务晚到未发布、未触发新计算', computer.invocations === 2, `invocations=${computer.invocations}`);
  check(
    '账本记录 discard-stale',
    cache.ledger.all().some((e) => e.kind === 'discard-stale' && e.key.includes('@v0')),
  );
  check(
    '旧版本键未入库',
    !cache.ledger.all().some((e) => e.kind === 'store' && e.key.includes('@v0')),
  );

  computer.completeAll();
  const [slowResult, freshResult] = await Promise.all([slow, fresh]);
  check('旧任务的等待者改拿新数据结果', slowResult[0] === 100);
  check('新任务结果正确', freshResult[0] === 100);

  await settle(cache.run(data, { scale: 1 }), computer);
  check('当前版本结果已缓存（命中）', computer.invocations === 2);
  printLedger(cache.ledger);
}

async function scenario5(): Promise<void> {
  console.log('\n场景 5: 取消任务不能发布半成品');
  const tracker = new VersionTracker();
  const computer = newComputer();
  const cache = newCache(computer, tracker);
  const data = new Float64Array([1, 2, 3]);

  const controller = new AbortController();
  const cancelled = cache.run(data, { scale: 1 }, { signal: controller.signal });
  const outcome = cancelled.then(
    () => 'resolved',
    (err: unknown) => (err instanceof AbortError ? 'aborted' : 'other'),
  );
  controller.abort();
  check('取消后调用方收到 AbortError', (await outcome) === 'aborted');

  computer.completeAll();
  await flush();
  check('账本记录 discard-cancelled', cache.ledger.all().some((e) => e.kind === 'discard-cancelled'));
  check('半成品未入库', !cache.ledger.all().some((e) => e.kind === 'store'));

  await settle(cache.run(data, { scale: 1 }), computer);
  check('取消后重新请求会重算', computer.invocations === 2);

  const second = new AbortController();
  const params = { scale: 5 };
  const quitter = cache.run(data, params, { signal: second.signal });
  const quitterOutcome = quitter.then(
    () => 'resolved',
    () => 'aborted',
  );
  const stayer = cache.run(data, params);
  second.abort();
  check('仍有订阅者时任务不取消', computer.pendingCount === 1);
  computer.completeAll();
  check('退出者收到取消', (await quitterOutcome) === 'aborted');
  check('留下者拿到结果', Array.from(await stayer).join(',') === '5,10,15');
  check(
    '任务正常发布',
    cache.ledger.all().some((e) => e.kind === 'store' && e.key.includes('"scale":5')),
  );
  printLedger(cache.ledger);
}

async function scenario6(): Promise<void> {
  console.log('\n场景 6: 调用方改返回数组不污染缓存（结果不可变）');
  const tracker = new VersionTracker();
  const computer = newComputer();
  const cache = newCache(computer, tracker);
  const data = new Float64Array([1, 2, 3]);

  const first = await settle(cache.run(data, { scale: 2 }), computer);
  first[0] = 9999;
  const second = await settle(cache.run(data, { scale: 2 }), computer);
  check('缓存快照未被污染', second[0] === 2);
  check('每次命中发放独立副本', first !== second && first[0] === 9999);

  const objCache = new ResultCache<Float64Array, Params, { total: number }>({
    capacity: 4,
    compute: (input) => ({ total: Array.from(input).reduce((sum, n) => sum + n, 0) }),
    tracker,
  });
  const obj = await objCache.run(data, { scale: 1 });
  check('对象结果被深冻结', Object.isFrozen(obj));
  try {
    (obj as { total: number }).total = -1;
  } catch {
    // 严格模式下赋值冻结对象抛 TypeError，符合预期
  }
  check('冻结结果不可改写', obj.total === 6);
  printLedger(cache.ledger);
}

async function scenario7(): Promise<void> {
  console.log('\n场景 7: 额度回收（LRU 驱逐可核对）');
  const tracker = new VersionTracker();
  const computer = newComputer();
  const cache = newCache(computer, tracker, 2);
  const data = new Float64Array([1, 2, 3]);

  await settle(cache.run(data, { scale: 1 }), computer);
  await settle(cache.run(data, { scale: 2 }), computer);
  await settle(cache.run(data, { scale: 1 }), computer);
  await settle(cache.run(data, { scale: 3 }), computer);
  const firstEvictions = cache.ledger.all().filter((e) => e.kind === 'evict');
  check(
    '额度超限驱逐最久未用项',
    firstEvictions.length === 1 && firstEvictions[0].key.includes('"scale":2'),
    firstEvictions.map((e) => e.key).join(' '),
  );

  await settle(cache.run(data, { scale: 2 }), computer);
  const allEvictions = cache.ledger.all().filter((e) => e.kind === 'evict');
  check('被驱逐项重新请求需重算', computer.invocations === 4, `invocations=${computer.invocations}`);
  check(
    '第二次驱逐的是 scale=1',
    allEvictions.length === 2 && allEvictions[1].key.includes('"scale":1'),
  );
  check('缓存大小不超过额度', cache.size === 2, `size=${cache.size}`);
  printLedger(cache.ledger);
}

async function main(): Promise<void> {
  console.log('版本化不可变结果缓存 — 可控计算器验证');
  await scenario1();
  await scenario2();
  await scenario3();
  await scenario4();
  await scenario5();
  await scenario6();
  await scenario7();
  if (failures === 0) {
    console.log('\n全部场景通过');
  } else {
    console.log(`\n${failures} 项断言失败`);
    process.exitCode = 1;
  }
}

main().catch((err: unknown) => {
  console.error(err);
  process.exitCode = 1;
});
