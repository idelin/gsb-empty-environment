import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  runTransaction,
  TransactionScope,
  TransactionError,
  ScopeClosedError,
  type ResourceSpec,
  type CommittedResource,
} from '../src/transaction.ts';

interface Deferred<T> {
  promise: Promise<T>;
  resolve(value: T): void;
  reject(error: unknown): void;
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

function tick(): Promise<void> {
  return new Promise((resolve) => setImmediate(resolve));
}

async function waitFor(cond: () => boolean, label: string): Promise<void> {
  for (let i = 0; i < 1000; i++) {
    if (cond()) return;
    await tick();
  }
  throw new Error(`waitFor timeout: ${label}`);
}

function resource(
  name: string,
  events: string[],
  options: { failAcquire?: boolean; failRelease?: boolean } = {},
): ResourceSpec<string> {
  return {
    name,
    acquire: () => {
      events.push(`acquire:${name}`);
      if (options.failAcquire) throw new Error(`acquire failed: ${name}`);
      return name;
    },
    release: () => {
      events.push(`release:${name}`);
      if (options.failRelease) throw new Error(`release failed: ${name}`);
    },
  };
}

test('成功路径：按顺序获取，提交后不释放，所有权随句柄移交', async () => {
  const events: string[] = [];
  let committed: readonly CommittedResource[] | undefined;

  const result = await runTransaction(
    async (scope) => {
      await scope.use(resource('A', events));
      await scope.use(resource('B', events));
      await scope.use(resource('C', events));
      return 'ok';
    },
    { onCommitted: (resources) => { committed = resources; } },
  );

  assert.equal(result, 'ok');
  assert.deepEqual(events, ['acquire:A', 'acquire:B', 'acquire:C']);
  assert.equal(committed?.length, 3);
  assert.deepEqual(committed?.map((r) => r.name), ['A', 'B', 'C']);

  // 移交后由调用方负责释放
  await committed?.[2]?.release();
  assert.deepEqual(events.at(-1), 'release:C');
});

test('获取失败：逆序释放已获取资源，原始错误被保留', async () => {
  const events: string[] = [];
  const err = await runTransaction(async (scope) => {
    await scope.use(resource('A', events));
    await scope.use(resource('B', events));
    await scope.use(resource('C', events, { failAcquire: true }));
  }).then(
    () => { throw new Error('should not resolve'); },
    (e: unknown) => e,
  );

  assert.ok(err instanceof TransactionError);
  assert.deepEqual(events, [
    'acquire:A', 'acquire:B', 'acquire:C',
    'release:B', 'release:A',
  ]);
  assert.ok(err.acquireError instanceof Error);
  assert.equal((err.acquireError as Error).message, 'acquire failed: C');
  assert.equal((err.cause as Error).message, 'acquire failed: C');
  assert.deepEqual(err.releaseErrors, []);
});

test('body 抛出业务错误同样触发逆序回滚', async () => {
  const events: string[] = [];
  const err = await runTransaction(async (scope) => {
    await scope.use(resource('A', events));
    await scope.use(resource('B', events));
    throw new Error('business boom');
  }).catch((e: unknown) => e);

  assert.ok(err instanceof TransactionError);
  assert.deepEqual(events, ['acquire:A', 'acquire:B', 'release:B', 'release:A']);
  assert.equal((err.cause as Error).message, 'business boom');
});

test('释放错误全部收集进 releaseErrors，获取错误不丢失', async () => {
  const events: string[] = [];
  const err = await runTransaction(async (scope) => {
    await scope.use(resource('A', events, { failRelease: true }));
    await scope.use(resource('B', events, { failRelease: true }));
    await scope.use(resource('C', events, { failAcquire: true }));
  }).catch((e: unknown) => e);

  assert.ok(err instanceof TransactionError);
  assert.equal((err.acquireError as Error).message, 'acquire failed: C');
  // 逆序释放：B(序号1) 先于 A(序号0)
  assert.deepEqual(events.filter((e) => e.startsWith('release')), ['release:B', 'release:A']);
  assert.equal(err.releaseErrors.length, 2);
  assert.deepEqual(err.releaseErrors.map((f) => f.index), [1, 0]);
  assert.deepEqual(err.releaseErrors.map((f) => f.name), ['B', 'A']);
  assert.deepEqual(
    err.releaseErrors.map((f) => (f.error as Error).message),
    ['release failed: B', 'release failed: A'],
  );
});

test('取消竞态：迟到资源必被接管释放，事务结算前不留责任空隙', async () => {
  const events: string[] = [];
  const gateC = deferred<string>();
  const cStarted = deferred<void>();
  const controller = new AbortController();
  const cancelReason = new Error('用户取消');

  const cSpec: ResourceSpec<string> = {
    name: 'C',
    acquire: () => {
      events.push('acquire:C:start');
      cStarted.resolve();
      return gateC.promise; // 挂起，由测试手动放行
    },
    release: () => { events.push('release:C'); },
  };

  let settled = false;
  const tx = runTransaction(async (scope) => {
    await scope.use(resource('A', events));
    await scope.use(resource('B', events));
    try {
      await scope.use(cSpec);
      events.push('body:unexpected');
    } catch (e) {
      events.push(`body:${(e as Error).name}`);
      throw e;
    }
  }, { signal: controller.signal });
  const txSettled = tx.then(
    () => ({ ok: true as const, value: undefined }),
    (e: unknown) => ({ ok: false as const, value: e }),
  );
  void txSettled.then(() => { settled = true; });

  await cStarted.promise;          // C 的获取已在飞行中
  controller.abort(cancelReason);  // 取消：回滚开始

  // 已入栈的 B、A 立即逆序释放，不被在途的 C 阻塞
  await waitFor(() => events.includes('release:A'), 'B/A released');
  assert.deepEqual(
    events.filter((e) => e.startsWith('release')),
    ['release:B', 'release:A'],
  );
  // C 尚未落地：事务绝不能结算，否则 C 将落入责任空隙
  assert.equal(settled, false);

  gateC.resolve('c-value');        // C 迟到完成：获取成功但回调未处理
  const outcome = await txSettled; // 事务只有在 C 被接管后才允许结束

  assert.equal(outcome.ok, false);
  const err = (outcome as { ok: false; value: unknown }).value;
  assert.ok(err instanceof TransactionError);
  // 迟到资源被接管释放，且发生在事务结算之前
  assert.deepEqual(
    events.filter((e) => e.startsWith('release')),
    ['release:B', 'release:A', 'release:C'],
  );
  // body 侧的迟到 use 以 ScopeClosedError 拒绝
  assert.ok(events.includes('body:ScopeClosedError'));
  // 中止原因作为事务起因保留
  assert.equal((err as TransactionError).cause, cancelReason);
  assert.deepEqual((err as TransactionError).releaseErrors, []);
  assert.equal(settled, true);
});

test('迟到资源的接管释放失败：错误同样进入 releaseErrors', async () => {
  const gate = deferred<string>();
  const started = deferred<void>();
  const controller = new AbortController();

  const tx = runTransaction(async (scope) => {
    await scope.use({
      name: 'slow',
      acquire: () => { started.resolve(); return gate.promise; },
      release: () => { throw new Error('late release failed'); },
    });
  }, { signal: controller.signal });
  const txSettled = tx.then(
    () => { throw new Error('should not resolve'); },
    (e: unknown) => e,
  );

  await started.promise;
  controller.abort();
  gate.resolve('v');
  const err = (await txSettled) as TransactionError;

  assert.ok(err instanceof TransactionError);
  assert.equal(err.releaseErrors.length, 1);
  assert.equal(err.releaseErrors[0]?.name, 'slow');
  assert.equal(err.releaseErrors[0]?.index, 0);
  assert.equal((err.releaseErrors[0]?.error as Error).message, 'late release failed');
});

test('提交时的在途获取：迟到资源被接管释放，不进入移交列表', async () => {
  const events: string[] = [];
  const gate = deferred<string>();
  const scope = new TransactionScope();

  const late = scope.use({
    name: 'slow',
    acquire: () => gate.promise,
    release: () => { events.push('release:slow'); },
  });
  late.catch((e: unknown) => { events.push(`use:${(e as Error).name}`); });

  const commitPromise = scope.commit();
  let committed = false;
  void commitPromise.then(() => { committed = true; });

  await tick();
  assert.equal(committed, false); // 提交必须等待在途获取落地

  gate.resolve('v');
  const handles = await commitPromise;

  assert.deepEqual(handles, []); // 迟到资源未进入移交列表
  assert.deepEqual(events, ['release:slow', 'use:ScopeClosedError']);
  assert.equal(scope.isOpen, false);
});

test('作用域关闭后 use 快速失败；回滚幂等', async () => {
  const scope = new TransactionScope();
  const events: string[] = [];
  await scope.use(resource('A', events));

  await Promise.all([scope.rollback(), scope.rollback()]);
  assert.deepEqual(events, ['acquire:A', 'release:A']);
  assert.equal(scope.isOpen, false);

  assert.throws(
    () => scope.use(resource('B', events)),
    (e: unknown) => e instanceof ScopeClosedError,
  );
  await assert.rejects(() => scope.commit(), ScopeClosedError);
});

test('已提交作用域不能回滚', async () => {
  const scope = new TransactionScope();
  const events: string[] = [];
  await scope.use(resource('A', events));
  const handles = await scope.commit();
  assert.equal(handles.length, 1);
  await assert.rejects(() => scope.rollback(), ScopeClosedError);
  assert.deepEqual(events, ['acquire:A']); // 未发生释放
});

test('预先中止的信号：事务以中止原因失败', async () => {
  const controller = new AbortController();
  const reason = new Error('already aborted');
  controller.abort(reason);

  const err = await runTransaction(async () => 'never', {
    signal: controller.signal,
  }).catch((e: unknown) => e);

  assert.ok(err instanceof TransactionError);
  assert.equal(err.cause, reason);
});
