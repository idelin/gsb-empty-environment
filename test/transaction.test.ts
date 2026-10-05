import {
  transaction,
  TransactionError,
  TransactionScope,
} from '../src/transaction';

declare const process: {
  stdout: { write(text: string): void };
  exit(code: number): void;
};

/* ---------- 极简测试框架（Node 16 无 node:test） ---------- */

type TestFn = () => void | Promise<void>;
const suite: Array<{ name: string; fn: TestFn }> = [];

function test(name: string, fn: TestFn): void {
  suite.push({ name, fn });
}

function assert(condition: unknown, message: string): asserts condition {
  if (!condition) {
    throw new Error(`断言失败: ${message}`);
  }
}

function assertEq<T>(actual: T, expected: T, message: string): void {
  if (actual !== expected) {
    throw new Error(
      `断言失败: ${message}（期望 ${String(expected)}，实际 ${String(actual)}）`,
    );
  }
}

function assertDeepEq(actual: unknown, expected: unknown, message: string): void {
  const a = JSON.stringify(actual);
  const b = JSON.stringify(expected);
  if (a !== b) {
    throw new Error(`断言失败: ${message}（期望 ${b}，实际 ${a}）`);
  }
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

async function expectReject(
  promise: Promise<unknown>,
): Promise<unknown> {
  try {
    await promise;
  } catch (error) {
    return error;
  }
  throw new Error('断言失败: 期望 Promise reject，但它 resolve 了');
}

/* ---------- 测试 ---------- */

test('成功路径：按顺序获取，提交后不释放，所有权移交调用方', async () => {
  const events: string[] = [];
  const result = await transaction(async (scope) => {
    const a = await scope.acquire(
      async () => { events.push('acquire:a'); return 'A'; },
      async () => { events.push('release:a'); },
    );
    const b = await scope.acquire(
      async () => { events.push('acquire:b'); return 'B'; },
      async () => { events.push('release:b'); },
    );
    return [a, b];
  });
  assertDeepEq(result, ['A', 'B'], '应返回业务结果');
  assertDeepEq(events, ['acquire:a', 'acquire:b'], '提交后不得释放任何资源');
});

test('获取中途失败：已取得资源逆序释放，抛出原始获取错误', async () => {
  const events: string[] = [];
  const acquireError = new Error('acquire failed: c');
  const error = await expectReject(transaction(async (scope) => {
    await scope.acquire(
      async () => { events.push('acquire:a'); return 'A'; },
      async () => { events.push('release:a'); },
    );
    await scope.acquire(
      async () => { events.push('acquire:b'); return 'B'; },
      async () => { events.push('release:b'); },
    );
    await scope.acquire(
      async () => { events.push('acquire:c'); throw acquireError; },
      async () => { events.push('release:c'); },
    );
  }));
  assertEq(error, acquireError, '应原样抛出获取错误');
  assertDeepEq(
    events,
    ['acquire:a', 'acquire:b', 'acquire:c', 'release:b', 'release:a'],
    '已取得的资源必须严格逆序释放，失败步骤自身不释放',
  );
});

test('业务回调抛错：逆序释放全部资源，抛出原始错误', async () => {
  const events: string[] = [];
  const bodyError = new Error('body boom');
  const error = await expectReject(transaction(async (scope) => {
    for (const name of ['a', 'b', 'c']) {
      await scope.acquire(
        async () => name,
        async (v) => { events.push(`release:${v}`); },
      );
    }
    throw bodyError;
  }));
  assertEq(error, bodyError, '应原样抛出业务错误');
  assertDeepEq(
    events,
    ['release:c', 'release:b', 'release:a'],
    '必须逆序释放',
  );
});

test('释放错误聚合：cause 与 releaseErrors 完整保留', async () => {
  const bodyError = new Error('primary failure');
  const releaseErrorB = new Error('release failed: b');
  const releaseErrorA = new Error('release failed: a');
  const error = await expectReject(transaction(async (scope) => {
    await scope.acquire(
      async () => 'A',
      async () => { throw releaseErrorA; },
    );
    await scope.acquire(
      async () => 'B',
      async () => { throw releaseErrorB; },
    );
    throw bodyError;
  }));
  assert(error instanceof TransactionError, '应抛出 TransactionError');
  const txError = error as TransactionError;
  assertEq(txError.cause, bodyError, 'cause 必须保留首个失败原因');
  assertDeepEq(
    txError.releaseErrors,
    [releaseErrorB, releaseErrorA],
    'releaseErrors 必须按发生顺序保留全部释放错误',
  );
});

test('预取消的 signal：不获取任何资源，直接拒绝', async () => {
  const controller = new AbortController();
  controller.abort();
  let acquired = 0;
  const error = await expectReject(
    transaction(async (scope) => {
      await scope.acquire(async () => { acquired++; return 1; }, async () => undefined);
    }, { signal: controller.signal }),
  );
  assertEq(acquired, 0, '预取消时不得获取任何资源');
  assert((error as Error).name === 'AbortError', '应以 AbortError 拒绝');
});

test('窗口期取消：迟到资源必被接管释放，且严格逆序', async () => {
  const events: string[] = [];
  const controller = new AbortController();
  const promise = transaction(async (scope) => {
    await scope.acquire(
      async () => { events.push('acquire:r1'); return 'r1'; },
      async () => { events.push('release:r1'); },
    );
    // acquire 的 Promise 立即 resolve，但其 continuation 尚未运行时发生取消：
    // 这是“获取已成功但回调尚未处理”的窗口期。
    const late = scope.acquire(
      async () => { events.push('acquire:late'); return 'late'; },
      async () => { events.push('release:late'); },
    );
    controller.abort(); // 同步触发 abort 事件，回滚立即开始
    await late; // 迟到资源已被接管，此处应抛出取消原因
    events.push('unreachable');
  }, { signal: controller.signal });

  const error = await expectReject(promise);
  assert((error as Error).name === 'AbortError', '应以 AbortError 拒绝');
  assertDeepEq(
    events,
    ['acquire:r1', 'acquire:late', 'release:late', 'release:r1'],
    '迟到资源必须先被接管释放，随后其余资源逆序释放',
  );
});

test('窗口期取消 + 接管释放失败：取消原因与释放错误都被保留', async () => {
  const controller = new AbortController();
  const lateReleaseError = new Error('late release failed');
  const promise = transaction(async (scope) => {
    const late = scope.acquire(
      async () => 'late',
      async () => { throw lateReleaseError; },
    );
    controller.abort();
    await late;
  }, { signal: controller.signal });

  const error = await expectReject(promise);
  assert(error instanceof TransactionError, '应抛出 TransactionError 聚合');
  const txError = error as TransactionError;
  assertEq(
    (txError.cause as Error).name,
    'AbortError',
    'cause 必须保留取消原因',
  );
  assertDeepEq(
    txError.releaseErrors,
    [lateReleaseError],
    '迟到资源的接管释放错误必须被保留',
  );
});

test('作用域关闭后再 acquire：立即拒绝，不触碰资源', async () => {
  let captured: TransactionScope | undefined;
  await transaction(async (scope) => {
    captured = scope;
  });
  assert(captured !== undefined, '应捕获作用域');
  const scope = captured as TransactionScope;
  let touched = false;
  const error = await expectReject(
    scope.acquire(
      async () => { touched = true; return 1; },
      async () => undefined,
    ),
  );
  assert(!touched, '关闭后不得再调用获取函数');
  assert((error as Error).message.includes('closed'), '应抛出已关闭错误');
});

test('defer 清理参与逆序回滚', async () => {
  const events: string[] = [];
  const boom = new Error('boom');
  await expectReject(transaction(async (scope) => {
    scope.defer(() => { events.push('cleanup:1'); });
    await scope.acquire(
      async () => 'x',
      async () => { events.push('release:x'); },
    );
    scope.defer(() => { events.push('cleanup:2'); });
    throw boom;
  }));
  assertDeepEq(
    events,
    ['cleanup:2', 'release:x', 'cleanup:1'],
    'defer 清理必须与资源一起严格逆序执行',
  );
});

test('压力测试：随机取消时机下无泄漏、无重复释放、严格逆序', async () => {
  let rollbackCount = 0;
  for (let iter = 0; iter < 300; iter++) {
    const acquired: number[] = [];
    const released: number[] = [];
    const controller = new AbortController();
    const total = 5;
    const promise = transaction(async (scope) => {
      for (let i = 0; i < total; i++) {
        await scope.acquire(
          async () => {
            await sleep(Math.floor(Math.random() * 3));
            acquired.push(i);
            return i;
          },
          async (value) => {
            released.push(value);
          },
        );
      }
      return 'committed';
    }, { signal: controller.signal });
    setTimeout(() => controller.abort(), Math.floor(Math.random() * 10));
    const outcome = await promise.then(
      (value) => ({ ok: true as const, value }),
      (error) => ({ ok: false as const, error }),
    );
    if (outcome.ok) {
      assertEq(released.length, 0, `iter ${iter}: 提交后不得释放`);
      assertEq(acquired.length, total, `iter ${iter}: 提交时全部获取成功`);
    } else {
      rollbackCount++;
      assertDeepEq(
        released,
        acquired.slice().reverse(),
        `iter ${iter}: 每个已获取资源必须恰好逆序释放一次（无泄漏/无重复/无责任空隙）`,
      );
    }
  }
  assert(rollbackCount > 0, '压力测试应至少覆盖一次取消路径');
});

/* ---------- 运行器 ---------- */

async function main(): Promise<void> {
  let failed = 0;
  for (const { name, fn } of suite) {
    try {
      await fn();
      process.stdout.write(`✓ ${name}\n`);
    } catch (error) {
      failed++;
      process.stdout.write(`✗ ${name}\n  ${String((error as Error).stack ?? error)}\n`);
    }
  }
  process.stdout.write(`\n${suite.length - failed}/${suite.length} 通过\n`);
  if (failed > 0) {
    process.exit(1);
  }
}

void main();
