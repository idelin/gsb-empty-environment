import { test } from "node:test";
import assert from "node:assert/strict";
import {
  ManualScheduler,
  OwnershipLedger,
  Scope,
  replay,
  ScopeDestroyedError,
  AlreadyCanceledError,
  DependencyCycleError,
  BorrowError,
} from "../src/index.ts";

function deferred<T>() {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

interface Ctx {
  scheduler: ManualScheduler;
  ledger: OwnershipLedger;
  disposed: string[];
  scope: Scope;
}

function setup(id = "root"): Ctx {
  const scheduler = new ManualScheduler();
  const ledger = new OwnershipLedger();
  const disposed: string[] = [];
  const scope = new Scope({ scheduler, ledger, id });
  return { scheduler, ledger, disposed, scope };
}

/** 运行一个 Promise，捕获其 AggregateError 拒绝（不依赖 assert.rejects 的返回值）。 */
async function catchAggregate(p: Promise<unknown>): Promise<AggregateError> {
  try {
    await p;
    throw new assert.AssertionError({ message: "expected promise to reject" });
  } catch (e) {
    assert.ok(e instanceof AggregateError, `expected AggregateError, got ${e}`);
    return e as AggregateError;
  }
}

test("依赖逆序清理：依赖者先释放，被依赖者后释放", async () => {
  const ctx = setup();
  const db = { conn: "db" };
  const svc = { svc: true };
  const api = { api: true };

  ctx.scope.acquire("db", () => Promise.resolve(db), { dispose: () => void ctx.disposed.push("db") });
  ctx.scope.acquire("svc", () => Promise.resolve(svc), {
    deps: ["db"],
    dispose: () => void ctx.disposed.push("svc"),
  });
  ctx.scope.acquire("api", () => Promise.resolve(api), {
    deps: ["svc"],
    dispose: () => void ctx.disposed.push("api"),
  });
  await ctx.scheduler.drain();

  await ctx.scope.destroy();
  assert.deepEqual(ctx.disposed, ["api", "svc", "db"]);
  assert.equal(ctx.scope.status, "destroyed");
  ctx.ledger.audit();
  assert.equal(ctx.ledger.releasesOf(db), 1);
  assert.equal(ctx.ledger.releasesOf(svc), 1);
  assert.equal(ctx.ledger.releasesOf(api), 1);
});

test("清理失败聚合：单个 destroyer 抛错不阻断其他资源", async () => {
  const ctx = setup();
  const boom = new Error("boom");
  ctx.scope.acquire("a", () => Promise.resolve({}), { dispose: () => ctx.disposed.push("a") });
  ctx.scope.acquire("b", () => Promise.resolve({}), {
    deps: ["a"],
    dispose: () => {
      throw boom;
    },
  });
  ctx.scope.acquire("c", () => Promise.resolve({}), { dispose: () => ctx.disposed.push("c") });
  await ctx.scheduler.drain();

  const err = await catchAggregate(ctx.scope.destroy());
  assert.equal(err.errors.length, 1);
  assert.equal(err.errors[0], boom);
  // b 清理失败，但 a、c 仍被清理；顺序确定（拓扑逆序，注册序 tie-break）。
  assert.deepEqual(ctx.disposed, ["c", "a"]);
  // 失败资源的物理释放仍只登记一次，无泄漏。
  ctx.ledger.audit();
});

test("重复销毁幂等：返回同一 Promise，资源只释放一次", async () => {
  const ctx = setup();
  const r = {};
  ctx.scope.acquire("r", () => Promise.resolve(r), { dispose: () => ctx.disposed.push("r") });
  await ctx.scheduler.drain();

  const p1 = ctx.scope.destroy();
  const p2 = ctx.scope.destroy();
  assert.equal(p1, p2);
  await p1;
  await ctx.scope.destroy(); // 销毁后再次调用仍安全
  assert.deepEqual(ctx.disposed, ["r"]);
  assert.equal(ctx.ledger.releasesOf(r), 1);
  ctx.ledger.audit();
});

test("迟到取得：作用域销毁后工厂才完成，资源立即归还", async () => {
  const ctx = setup();
  const d = deferred<object>();
  const lateDispose: string[] = [];
  const handle = ctx.scope.acquire("late", () => d.promise, {
    dispose: () => lateDispose.push("late"),
  });

  const destroyPromise = ctx.scope.destroy();
  // 销毁进行中，工厂此刻才完成。
  d.resolve({ late: true });
  await destroyPromise;

  await assert.rejects(handle.promise, ScopeDestroyedError);
  assert.deepEqual(lateDispose, ["late"]); // 立即归还
  ctx.ledger.audit(); // 恰好一次释放
});

test("取消：工厂未完成前取消，迟到结果立即归还；重复取消抛错", async () => {
  const ctx = setup();
  const d = deferred<object>();
  const released: string[] = [];
  const handle = ctx.scope.acquire("job", () => d.promise, {
    dispose: () => released.push("job"),
  });

  handle.cancel(new Error("user abort"));
  assert.throws(() => handle.cancel(), AlreadyCanceledError);
  await assert.rejects(handle.promise, AlreadyCanceledError);

  const resource = { job: 1 };
  d.resolve(resource); // 取消后工厂才完成
  await ctx.scheduler.drain();
  assert.deepEqual(released, ["job"]); // 立即归还，不进入作用域
  assert.equal(ctx.scope.journal.state.scope.nodes["job"].status, "canceled");

  await ctx.scope.destroy();
  assert.equal(ctx.ledger.releasesOf(resource), 1);
  ctx.ledger.audit();
});

test("借用：子作用域关闭不回收父仍借出的资源", async () => {
  const ctx = setup();
  const db = { conn: 1 };
  ctx.scope.acquire("db", () => Promise.resolve(db), { dispose: () => ctx.disposed.push("db") });
  await ctx.scheduler.drain();

  const child = ctx.scope.createChild("child");
  const lease = child.borrow<typeof db>("db");
  assert.equal(lease.value, db);
  assert.equal(ctx.scope.journal.state.scope.nodes["db"].borrows, 1);

  await child.destroy(); // 子关闭：只归还租约
  assert.deepEqual(ctx.disposed, []); // 父资源未被回收
  assert.equal(ctx.scope.journal.state.scope.nodes["db"].borrows, 0);

  lease.release(); // 幂等
  lease.release();
  assert.equal(ctx.scope.journal.state.scope.nodes["db"].borrows, 0);

  await ctx.scope.destroy();
  assert.deepEqual(ctx.disposed, ["db"]);
  assert.equal(ctx.ledger.releasesOf(db), 1);
  ctx.ledger.audit();
});

test("子作用域销毁只清理自己的资源，父资源不受影响", async () => {
  const ctx = setup();
  const parentRes = { p: 1 };
  const childRes = { c: 1 };
  ctx.scope.acquire("parent-res", () => Promise.resolve(parentRes), {
    dispose: () => ctx.disposed.push("parent-res"),
  });
  await ctx.scheduler.drain();

  const child = ctx.scope.createChild("child");
  child.acquire("child-res", () => Promise.resolve(childRes), {
    dispose: () => ctx.disposed.push("child-res"),
  });
  child.borrow("parent-res");
  await ctx.scheduler.drain();

  await child.destroy();
  assert.deepEqual(ctx.disposed, ["child-res"]); // 父资源保留
  assert.equal(ctx.ledger.releasesOf(parentRes), 0);
  assert.equal(ctx.ledger.releasesOf(childRes), 1);

  await ctx.scope.destroy();
  assert.deepEqual(ctx.disposed, ["child-res", "parent-res"]);
  ctx.ledger.audit();
});

test("依赖环：确定性破环（注册序）并聚合 DependencyCycleError", async () => {
  const ctx = setup();
  ctx.scope.acquire("a", () => Promise.resolve({}), { dispose: () => ctx.disposed.push("a") });
  ctx.scope.acquire("b", () => Promise.resolve({}), { dispose: () => ctx.disposed.push("b") });
  ctx.scope.acquire("c", () => Promise.resolve({}), { dispose: () => ctx.disposed.push("c") });
  ctx.scope.link("a", "b");
  ctx.scope.link("b", "a"); // 环 a <-> b
  ctx.scope.link("c", "a"); // c 依赖环成员
  await ctx.scheduler.drain();

  const err = await catchAggregate(ctx.scope.destroy());
  const cycle = err.errors.find((e) => e instanceof DependencyCycleError) as DependencyCycleError;
  assert.ok(cycle, "aggregated error contains DependencyCycleError");
  assert.deepEqual(cycle.members, ["a", "b"]);
  // 确定顺序：c（依赖者）先，环内按注册序 a、b。
  assert.deepEqual(ctx.disposed, ["c", "a", "b"]);
  ctx.ledger.audit(); // 环不导致泄漏或双重释放
});

test("销毁中/销毁后新增资源立即拒绝", async () => {
  const ctx = setup();
  ctx.scope.acquire("r", () => Promise.resolve({}), { dispose: () => ctx.disposed.push("r") });
  await ctx.scheduler.drain();

  const p = ctx.scope.destroy();
  assert.equal(ctx.scope.status, "destroying");
  assert.throws(() => ctx.scope.acquire("nope", () => Promise.resolve({})), ScopeDestroyedError);
  assert.throws(() => ctx.scope.createChild(), ScopeDestroyedError);
  assert.throws(() => ctx.scope.borrow("r"), BorrowError);
  await p;
  assert.equal(ctx.scope.status, "destroyed");
  assert.throws(() => ctx.scope.acquire("nope2", () => Promise.resolve({})), ScopeDestroyedError);
  ctx.ledger.audit();
});

test("快照隔离与可回放：读者看不到中间态，op 序列可确定性重放", async () => {
  const ctx = setup();
  const s0 = ctx.scope.journal.state;
  assert.deepEqual(Object.keys(s0.scope.nodes), []);

  ctx.scope.acquire("x", () => Promise.resolve({}), { dispose: () => ctx.disposed.push("x") });
  const s1 = ctx.scope.journal.state;
  assert.equal(s1.scope.nodes["x"].status, "pending");

  await ctx.scheduler.drain();
  const s2 = ctx.scope.journal.state;
  assert.equal(s2.scope.nodes["x"].status, "ready");

  // 旧快照不被后续提交污染（读者只见已提交状态）。
  assert.equal(s1.scope.nodes["x"].status, "pending");
  assert.deepEqual(Object.keys(s0.scope.nodes), []);
  assert.ok(Object.isFrozen(s2));
  assert.ok(Object.isFrozen(s2.scope.nodes["x"]));

  await ctx.scope.destroy();
  // 全量 op 可重放，且与当前状态一致。
  assert.ok(ctx.scope.journal.verifyReplay());
  const replayed = replay(ctx.scope.id, null, ctx.scope.journal.ops);
  assert.deepEqual(replayed, ctx.scope.journal.state);
  ctx.ledger.audit();
});

test("工厂失败：错误进入日志，销毁无错误，账本无泄漏", async () => {
  const ctx = setup();
  const failure = new Error("factory failed");
  const handle = ctx.scope.acquire("bad", () => Promise.reject(failure));
  await assert.rejects(handle.promise, (e: Error) => e === failure);
  await ctx.scheduler.drain();
  assert.equal(ctx.scope.journal.state.scope.nodes["bad"].status, "failed");

  await ctx.scope.destroy(); // 无资源可清理，无错误
  assert.equal(ctx.ledger.size, 0);
  ctx.ledger.audit();
});

test("错误汇总：子级清理异常 + 父级清理异常 + 环，全部聚合且无泄漏", async () => {
  const ctx = setup();
  const parentBoom = new Error("parent boom");
  const childBoom = new Error("child boom");

  ctx.scope.acquire("p1", () => Promise.resolve({}), {
    dispose: () => {
      throw parentBoom;
    },
  });
  ctx.scope.acquire("p2", () => Promise.resolve({}), { dispose: () => ctx.disposed.push("p2") });
  ctx.scope.acquire("p3", () => Promise.resolve({}), { dispose: () => ctx.disposed.push("p3") });
  ctx.scope.link("p2", "p3");
  ctx.scope.link("p3", "p2"); // 环

  const child = ctx.scope.createChild("child");
  child.acquire("c1", () => Promise.resolve({}), {
    dispose: () => {
      throw childBoom;
    },
  });
  await ctx.scheduler.drain();

  const err = await catchAggregate(ctx.scope.destroy());
  const kinds = err.errors.map((e: Error) => e.constructor.name).sort();
  assert.deepEqual(kinds, ["DependencyCycleError", "Error", "Error"]);
  assert.ok(err.errors.includes(parentBoom));
  assert.ok(err.errors.includes(childBoom));
  // 每个物理资源仍恰好释放一次。
  ctx.ledger.audit();
});

test("受控调度枚举：取得/借用/取消/双重关闭的交错组合，账本始终恰好一次释放", async () => {
  // 枚举关键交错：resolve/cancel/destroy/destroy 的排列子集。
  const scenarios: Array<{
    name: string;
    run: (s: {
      scope: Scope;
      scheduler: ManualScheduler;
      d: ReturnType<typeof deferred<object>>;
      handle: { cancel: (r?: unknown) => void };
    }) => Promise<void>;
    expectReleased: boolean;
  }> = [
    {
      name: "resolve -> destroy",
      run: async ({ scope, d, scheduler }) => {
        d.resolve({ v: 1 });
        await scheduler.drain();
        await scope.destroy();
      },
      expectReleased: true,
    },
    {
      name: "destroy -> resolve（迟到归还）",
      run: async ({ scope, d }) => {
        const p = scope.destroy();
        d.resolve({ v: 1 });
        await p.catch(() => {});
      },
      expectReleased: true,
    },
    {
      name: "cancel -> resolve -> destroy",
      run: async ({ scope, scheduler, d, handle }) => {
        handle.cancel(new Error("abort"));
        d.resolve({ v: 1 });
        await scheduler.drain();
        await scope.destroy().catch(() => {});
      },
      expectReleased: true,
    },
    {
      name: "destroy -> cancel -> resolve（取消在途 + 迟到归还）",
      run: async ({ scope, d, handle }) => {
        const p = scope.destroy();
        handle.cancel(new Error("abort during destroy"));
        d.resolve({ v: 1 });
        await p.catch(() => {});
      },
      expectReleased: true,
    },
    {
      name: "destroy -> destroy -> resolve（双重关闭幂等 + 迟到归还）",
      run: async ({ scope, d }) => {
        const p1 = scope.destroy();
        const p2 = scope.destroy();
        assert.equal(p1, p2);
        d.resolve({ v: 1 });
        await p1.catch(() => {});
      },
      expectReleased: true,
    },
    {
      name: "resolve 永不完成 -> destroy 挂起 -> resolve 后收敛",
      run: async ({ scope, scheduler, d }) => {
        const p = scope.destroy();
        await scheduler.drain();
        assert.equal(scope.status, "destroying"); // 仍在等待在途 acquire
        d.resolve({ v: 1 });
        await p.catch(() => {});
      },
      expectReleased: true,
    },
  ];

  for (const scenario of scenarios) {
    const scheduler = new ManualScheduler();
    const ledger = new OwnershipLedger();
    const scope = new Scope({ scheduler, ledger, id: `enum-${scenario.name}` });
    const d = deferred<object>();
    const released: object[] = [];
    const handle = scope.acquire("res", () => d.promise, {
      dispose: (v) => released.push(v),
    });
    handle.promise.catch(() => {});

    await scenario.run({ scope, scheduler, d, handle });
    await scheduler.drain();
    assert.equal(scope.status, "destroyed", scenario.name);
    if (scenario.expectReleased) {
      assert.equal(released.length, 1, `${scenario.name}: released exactly once`);
    }
    ledger.audit(); // 独立账本：每资源最终恰一次释放
  }
});

test("借用链：孙作用域可借祖父资源，逐级关闭不提前回收", async () => {
  const ctx = setup();
  const db = { conn: 1 };
  ctx.scope.acquire("db", () => Promise.resolve(db), { dispose: () => ctx.disposed.push("db") });
  await ctx.scheduler.drain();

  const child = ctx.scope.createChild("child");
  const grandchild = child.createChild("grandchild");
  const lease = grandchild.borrow<typeof db>("db");
  assert.equal(lease.value, db);

  await grandchild.destroy();
  assert.deepEqual(ctx.disposed, []);
  // 孙级关闭时自动归还其持有的租约，但不回收资源本体。
  assert.equal(ctx.scope.journal.state.scope.nodes["db"].borrows, 0);
  await child.destroy();
  assert.deepEqual(ctx.disposed, []);
  lease.release(); // 幂等：已归还，无操作
  assert.equal(ctx.scope.journal.state.scope.nodes["db"].borrows, 0);
  await ctx.scope.destroy();
  assert.deepEqual(ctx.disposed, ["db"]);
  ctx.ledger.audit();
});
