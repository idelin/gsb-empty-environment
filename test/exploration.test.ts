import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  Store,
  LoopBudgetExceededError,
  type Notification,
  type Selector,
  type Equality,
  type Subscription,
} from '../src/store.ts';

/**
 * 状态探索核验：用确定性 PRNG 生成大量随机操作序列，
 * 全程断言系统不变量：
 *  - 版本号严格递增、无重复、无回退
 *  - 通知只引用已提交版本，值与该版本状态的选择器结果一致
 *  - 同一轮内同一订阅者不会收到两次通知
 *  - 取消订阅后不会被旧队列复活（之后的轮次再无通知）
 *  - 无丢最终状态：收尾后每个活跃订阅者的最近值与最终状态一致
 *  - 回放可重建最终状态
 *
 * 注意：订阅者抛错会被聚合吞入 onNotificationError，因此监听器内的
 * 断言全部延迟（pendingChecks）到顶层操作完成后统一执行，避免被吞。
 */

interface Shape {
  a: number;
  b: number;
  list: number[];
}

const SELECTORS: Array<Selector<Shape, unknown>> = [
  (s) => s.a,
  (s) => s.b,
  (s) => s.list.length,
  (s) => s.a + s.b,
];

const EQUALITIES: Array<Equality<unknown>> = [
  Object.is,
  (x, y) => x === y,
  (x, y) => Number(x) % 2 === Number(y) % 2, // 奇偶相等策略
];

function mulberry32(seed: number): () => number {
  let t = seed >>> 0;
  return () => {
    t += 0x6d2b79f5;
    let r = Math.imul(t ^ (t >>> 15), 1 | t);
    r ^= r + Math.imul(r ^ (r >>> 7), 61 | r);
    return ((r ^ (r >>> 14)) >>> 0) / 4294967296;
  };
}

interface SubModel {
  id: number;
  handle: Subscription;
  selector: Selector<Shape, unknown>;
  equality: Equality<unknown>;
  lastValue: unknown;
  active: boolean;
  unsubRound: number; // 取消时所处的轮次；之后的轮次不允许再通知
}

test('状态探索：随机操作序列下的版本/通知/取消不变量', () => {
  const SEEDS = 120;
  const OPS_PER_SEED = 120;

  for (let seed = 1; seed <= SEEDS; seed++) {
    const rnd = mulberry32(seed);
    const pick = <T>(arr: T[]): T => arr[Math.floor(rnd() * arr.length)];
    const autoFlush = rnd() < 0.5;
    const fail = (msg: string): never => {
      throw new Error(`seed ${seed}: ${msg}`);
    };

    const stateByVersion = new Map<number, Shape>([[0, undefined as unknown as Shape]]);
    const subs = new Map<number, SubModel>();
    const seenVersions = new Set<number>();
    const delivered = new Set<string>(); // `${round}:${subId}` 防重复
    const pendingChecks: Array<() => void> = []; // 延迟断言，避免被聚合错误吞掉
    const swallowed: unknown[] = []; // 聚合错误中的非预期错误
    const versionCap = 400; // 限制重入写总量，保证探索可终止
    let commitDepth = 0;

    const store = new Store<Shape>({
      initialState: { a: 0, b: 0, list: [] },
      maxRounds: 64,
      autoFlush,
      onNotificationError: (err) => {
        for (const e of err.errors) {
          if ((e.error as Error)?.message !== 'random listener failure') swallowed.push(e.error);
        }
      },
    });
    stateByVersion.set(0, store.getState());
    // 记录者订阅：identity 选择器逐轮捕获已提交状态，作为版本->状态证据
    store.subscribe((s) => s, (n) => {
      stateByVersion.set(n.toVersion, n.value as Shape);
    });

    const drainChecks = () => {
      const checks = pendingChecks.splice(0);
      for (const check of checks) check();
      if (swallowed.length > 0) {
        throw new Error(`seed ${seed}: 监听器内意外错误 ${swallowed[0]}`);
      }
    };

    const safeFlush = () => {
      for (let i = 0; i < 50 && store.pendingCount > 0; i++) {
        try {
          store.flush();
        } catch (error) {
          assert.ok(error instanceof LoopBudgetExceededError, `seed ${seed}: 只允许预算错误`);
        }
      }
      assert.equal(store.pendingCount, 0, `seed ${seed}: 预算保护后队列应能排空`);
    };

    const doCommit = (label: string) => {
      const mutations = 1 + Math.floor(rnd() * 3);
      const shouldFail = rnd() < 0.1;
      // 预抽取确定性变更集，保证操作日志可精确回放
      const ops = Array.from({ length: mutations }, () => Math.floor(rnd() * 3));
      const versionBefore = store.version;
      const stateBefore = store.getState();
      commitDepth += 1;
      let committedVersion = 0;
      try {
        const receipt = store.commit(label, (d) => {
          // 读者隔离：实时提交时，事务内只能读到提交前状态（回放期间版本已推进，跳过）
          if (store.version === versionBefore) {
            assert.equal(store.getState(), stateBefore, `seed ${seed}: 事务内读到中间态`);
          }
          for (const k of ops) {
            if (k === 0) d.a += 1;
            else if (k === 1) d.b += 2;
            else d.list.push(d.list.length);
          }
          if (shouldFail) throw new Error('random tx failure');
        });
        committedVersion = receipt.version;
      } catch (error) {
        if (error instanceof LoopBudgetExceededError) {
          // 提交本身已成功（版本已推进），仅通知轮次超预算；版本证据仍须记录
          assert.ok(store.version > versionBefore, `seed ${seed}: 预算错误但版本未推进`);
          committedVersion = versionBefore + 1;
        } else {
        assert.equal((error as Error).message, 'random tx failure', `seed ${seed}: 意外提交错误 ${error}`);
        // 失败事务原子回滚
        assert.equal(store.version, versionBefore, `seed ${seed}: 失败事务改了版本`);
        assert.equal(store.getState(), stateBefore, `seed ${seed}: 失败事务改了状态`);
        }
      } finally {
        commitDepth -= 1;
      }
      if (committedVersion > 0) {
        // 版本严格递增、无重复
        assert.equal(committedVersion, versionBefore + 1, `seed ${seed}: 版本未恰好 +1`);
        assert.ok(!seenVersions.has(committedVersion), `seed ${seed}: 重复版本 ${committedVersion}`);
        seenVersions.add(committedVersion);
      }
      if (commitDepth === 0) drainChecks();
    };

    const makeListener = (model: SubModel) => (n: Notification<unknown>) => {
      model.lastValue = n.value;
      pendingChecks.push(() => {
        // 通知只针对已提交版本
        assert.ok(stateByVersion.has(n.toVersion), `seed ${seed}: 通知引用未提交版本 v${n.toVersion}`);
        assert.ok(n.fromVersion <= n.toVersion, `seed ${seed}: 版本范围倒置`);
        assert.ok(n.toVersion <= store.version, `seed ${seed}: 通知版本超过已提交版本`);
        assert.equal(n.version, n.toVersion);
        // 通知值必须等于该已提交版本状态经当前选择器的结果
        assert.deepEqual(n.value, model.selector(stateByVersion.get(n.toVersion)!));
        // 同一轮同一订阅者不重复通知
        const key = `${n.round}:${model.id}`;
        assert.ok(!delivered.has(key), `seed ${seed}: 同轮重复通知`);
        delivered.add(key);
        // 取消后不被旧队列复活：通知轮次不得晚于取消时轮次
        if (!model.active) {
          assert.ok(n.round <= model.unsubRound, `seed ${seed}: 取消后复活`);
        }
      });
      // 随机重入写（进入下一轮）与随机抛错（不阻断他人）
      if (rnd() < 0.15 && store.version < versionCap) doCommit('reentrant');
      if (rnd() < 0.1) throw new Error('random listener failure');
    };

    for (let op = 0; op < OPS_PER_SEED; op++) {
      const roll = rnd();
      try {
        if (roll < 0.45) {
          doCommit('op');
        } else if (roll < 0.6 && subs.size < 6) {
          const selector = pick(SELECTORS);
          const equality = pick(EQUALITIES);
          const model: SubModel = {
            id: -1,
            handle: undefined as unknown as Subscription,
            selector,
            equality,
            lastValue: selector(store.getState()),
            active: true,
            unsubRound: -1,
          };
          const handle = store.subscribe(selector, makeListener(model), { equality });
          model.id = handle.id;
          model.handle = handle;
          subs.set(handle.id, model);
        } else if (roll < 0.72 && subs.size > 0) {
          const victim = pick([...subs.values()]);
          victim.active = false;
          victim.unsubRound = store.currentRound;
          victim.handle.unsubscribe();
          subs.delete(victim.id);
        } else if (roll < 0.84 && subs.size > 0) {
          const target = pick([...subs.values()]);
          if (rnd() < 0.5) {
            target.selector = pick(SELECTORS);
            target.lastValue = target.selector(store.getState());
            target.handle.setSelector(target.selector as Selector<never, unknown>);
          } else {
            target.equality = pick(EQUALITIES);
            target.handle.setEquality(target.equality as Equality<never>);
          }
        } else if (!autoFlush) {
          safeFlush();
        }
      } catch (error) {
        assert.ok(error instanceof LoopBudgetExceededError, `seed ${seed}: 意外错误 ${error}`);
      }
      if (commitDepth === 0) drainChecks();
    }

    safeFlush();
    drainChecks();

    // 无丢最终状态：活跃订阅者的最近值与最终状态在各自相等策略下一致
    const finalState = store.getState();
    for (const model of subs.values()) {
      assert.ok(
        model.equality(model.lastValue, model.selector(finalState)),
        `seed ${seed}: 订阅者 #${model.id} 丢失最终状态`,
      );
    }
    // 回放可重建最终状态
    assert.deepEqual(store.replay(), finalState, `seed ${seed}: 回放不一致`);
    // 版本日志连续无重复：1..version 全覆盖
    assert.equal(seenVersions.size, store.version, `seed ${seed}: 版本数量不符`);
    for (let v = 1; v <= store.version; v++) {
      assert.ok(seenVersions.has(v), `seed ${seed}: 版本 ${v} 缺失`);
    }
    const logged = store.opLog().map((e) => e.version);
    assert.deepEqual(logged, logged.map((_, i) => i + 1), `seed ${seed}: 日志版本断裂`);
  }
});
