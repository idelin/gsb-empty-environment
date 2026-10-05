import { test } from "node:test";
import assert from "node:assert/strict";
import { Store } from "../src/store.ts";
import { ReferenceModel, mulberry32, runOpLog, type Op } from "./harness.ts";
import { CAPACITY, INITIAL_STATE, OP_LOG } from "./oplog.ts";

test("操作日志回放：两次执行产生完全一致的事件流，且与深拷贝参考模型逐步一致", () => {
  const recordA: string[] = [];
  const recordB: string[] = [];
  const storeA = new Store(INITIAL_STATE, { capacity: CAPACITY });
  const storeB = new Store(INITIAL_STATE, { capacity: CAPACITY });
  const ref = new ReferenceModel(INITIAL_STATE);

  runOpLog(storeA, OP_LOG, recordA, ref); // 第一遍：边执行边对照参考模型
  runOpLog(storeB, OP_LOG, recordB); // 第二遍：纯回放

  assert.deepEqual(recordA, recordB, "同一操作日志两次回放的事件流必须一致");
  assert.deepEqual(storeA.state, ref.headState);
  assert.equal(storeA.parentOf(5), 2, "v5 是从 v2 开出的分支");
  assert.equal(storeA.hasVersion(1), false, "v1 已被容量回收");
  assert.equal(storeA.hasVersion(2), false, "快照释放后 v2 也被回收");
  // B 每轮抛错：5 次提交（v2..v6）各留下一条错误，但不影响他人。
  assert.equal(storeA.listenerErrors.length, 5);
  // 手工核对的账本：v1 在 v4 提交时被逐出，v2 在快照释放后的 v6 提交时被逐出。
  assert.deepEqual(storeA.ledger.versions, [5, 6]);
  assert.equal(storeA.ledger.retainedNodes, 10);
  assert.equal(storeA.ledger.retainedNodes, storeA.debugReachableNodes());
});

test("随机操作序列对照：300 个提交与深拷贝参考模型逐步一致，账本守恒", () => {
  for (const [seed, capacity] of [[7, Infinity], [11, 512]] as const) {
    const rand = mulberry32(seed);
    const ref = new ReferenceModel(INITIAL_STATE);
    const genRef = new ReferenceModel(INITIAL_STATE); // 生成期镜像，保证随机操作合法
    const store = new Store(INITIAL_STATE, { capacity });
    const slices = ["cart", "inventory"];
    const keys = ["items", "a", "b", "c", "meta"];

    const ops: Op[] = [];
    let txSeq = 0;
    for (let round = 0; round < 300; round += 1) {
      const tx = `tx${txSeq++}`;
      ops.push({ op: "begin", tx });
      genRef.begin(tx);
      const writes = 1 + Math.floor(rand() * 4);
      for (let w = 0; w < writes; w += 1) {
        const slice = slices[Math.floor(rand() * slices.length)];
        const key = keys[Math.floor(rand() * keys.length)];
        if (rand() < 0.2) {
          // 删除：只在当前事务工作区里确实存在该键时生成。
          if (genRef.get(tx, [slice, key]) === undefined) continue;
          ops.push({ op: "delete", tx, path: [slice, key] });
          genRef.delete(tx, [slice, key]);
        } else {
          const value = rand() < 0.5
            ? Math.floor(rand() * 100)
            : { note: `r${round}w${w}`, flag: rand() < 0.5 };
          ops.push({ op: "set", tx, path: [slice, key], value: value as never });
          genRef.set(tx, [slice, key], value as never);
        }
      }
      if (rand() < 0.1) {
        ops.push({ op: "abort", tx });
        genRef.abort(tx);
      } else {
        ops.push({ op: "commit", tx });
        genRef.commit(tx);
      }
    }

    const record: string[] = [];
    runOpLog(store, ops, record, ref);

    // 头部状态与完整深拷贝参考一致。
    assert.deepEqual(store.state, ref.headState);
    // 账本守恒：等于从全部保留版本可达的节点数；有限容量时不超上限。
    assert.equal(store.ledger.retainedNodes, store.debugReachableNodes());
    assert.ok(store.ledger.retainedNodes <= capacity);
    if (capacity !== Infinity) {
      assert.ok(store.ledger.versions.length < ref.headVersion, "有限容量下应发生历史回收");
    }
    // 保留中的每个版本都与参考模型一致（抽查最老/最新以外的中间版本）。
    for (const version of store.ledger.versions) {
      const probe = store.checkout(version);
      assert.deepEqual(probe.state, ref.stateAt(version));
      probe.release();
    }
  }
});
