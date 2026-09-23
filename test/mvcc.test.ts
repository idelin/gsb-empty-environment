import test from "node:test";
import assert from "node:assert/strict";
import {
  Store,
  TransactionConflictError,
  TransactionStateError,
} from "../src/mvcc.ts";

test("读事务固定状态版本：开始后看不到新提交", () => {
  const store = new Store();
  const t1 = store.begin();
  t1.set("counter", 1);
  t1.commit();

  const reader = store.begin();
  assert.equal(reader.readVersion, 1);
  assert.equal(reader.get("counter"), 1);

  const t2 = store.begin();
  t2.set("counter", 2);
  t2.commit();

  // 旧读者的读视图固定在 v1，看不到 v2 的写入。
  assert.equal(reader.get("counter"), 1);
  reader.rollback();

  // 新事务能看到最新值。
  const fresh = store.begin();
  assert.equal(fresh.get("counter"), 2);
  fresh.rollback();
});

test("读者看不到事务中间态", () => {
  const store = new Store();
  const init = store.begin();
  init.set("a", 0);
  init.set("b", 0);
  init.commit();

  const writer = store.begin();
  writer.set("a", 100);
  writer.set("b", 200);

  // 提交前：其他事务与快照都看不到缓冲中的写入。
  const reader = store.begin();
  assert.equal(reader.get("a"), 0);
  assert.equal(reader.get("b"), 0);
  const snap = store.snapshot();
  assert.equal(snap.get("a"), 0);

  writer.commit();

  // 提交后：新读者一次性看到全部写入（原子可见）。
  const after = store.begin();
  assert.equal(after.get("a"), 100);
  assert.equal(after.get("b"), 200);
  after.rollback();
  reader.rollback();
  snap.release();
});

test("两事务读同值后写：后者冲突失败且原子回滚，不丢更新", () => {
  const store = new Store();
  const init = store.begin();
  init.set("balance", 100);
  init.set("log", "init");
  init.commit();

  // 两个事务都读到 balance = 100，然后各自基于读到的值写回。
  const t1 = store.begin();
  const t2 = store.begin();
  assert.equal(t1.get("balance"), 100);
  assert.equal(t2.get("balance"), 100);

  t1.set("balance", 100 - 30);
  t1.set("log", "t1 withdrew 30");
  t2.set("balance", 100 - 50);
  t2.set("log", "t2 withdrew 50");

  t1.commit();

  // t2 的读集已过期：提交必须失败，且其全部写入（含 log）不得残留。
  assert.throws(() => t2.commit(), TransactionConflictError);
  assert.equal(t2.isActive, false);

  const check = store.begin();
  assert.equal(check.get("balance"), 70);
  assert.equal(check.get("log"), "t1 withdrew 30");
  check.rollback();

  // t2 失败后重试：读到新值再写，可成功提交。
  const retry = store.begin();
  assert.equal(retry.get("balance"), 70);
  retry.set("balance", 70 - 50);
  retry.commit();
  assert.equal(store.snapshot().get("balance"), 20);
});

test("不同键无冲突：两个写事务都可提交", () => {
  const store = new Store();
  const init = store.begin();
  init.set("x", 1);
  init.set("y", 1);
  init.commit();

  const t1 = store.begin();
  const t2 = store.begin();
  t1.set("x", 10);
  t2.set("y", 20);

  const v1 = t1.commit();
  const v2 = t2.commit();
  assert.ok(v2 > v1);

  const snap = store.snapshot();
  assert.equal(snap.get("x"), 10);
  assert.equal(snap.get("y"), 20);
  snap.release();
});

test("只读快照：旧读者不看新版本，且钉住期间 GC 不误删", () => {
  const store = new Store();
  const t1 = store.begin();
  t1.set("k", "v1");
  t1.commit();

  const oldReader = store.snapshot(); // 钉住 v1
  assert.equal(oldReader.version, 1);

  for (const value of ["v2", "v3", "v4"]) {
    const t = store.begin();
    t.set("k", value);
    t.commit();
  }
  assert.equal(store.currentVersion, 4);

  // 钉住 v1 期间：GC 只能回收无人可见的版本，v1 必须保留。
  const collectedWhilePinned = store.gc();
  assert.equal(collectedWhilePinned, 2); // v2、v3 被回收，v1（钉住）与 v4（最新）保留
  assert.equal(oldReader.get("k"), "v1"); // 旧读者仍看到旧版本
  assert.equal(store.snapshot().get("k"), "v4"); // 新读者看到新版本

  // 释放后：旧版本可被回收。
  oldReader.release();
  const collectedAfterRelease = store.gc();
  assert.equal(collectedAfterRelease, 1); // v1 被回收，只剩 v4
  assert.equal(store.snapshot().get("k"), "v4");
});

test("状态操作可回放：日志重放得到一致状态", () => {
  const store = new Store();
  const t1 = store.begin();
  t1.set("user:1", { name: "alice", tags: ["admin"] });
  t1.commit();

  const t2 = store.begin();
  t2.set("user:2", { name: "bob", tags: [] });
  t2.commit();

  const t3 = store.begin();
  t3.get("user:1");
  t3.set("user:1", { name: "alice", tags: ["admin", "owner"] });
  t3.commit();

  // 模拟持久化：序列化日志再回放。
  const journal = JSON.parse(JSON.stringify(store.getJournal()));
  assert.equal(journal.length, 3);

  const replica = Store.replay(journal);
  assert.equal(replica.currentVersion, store.currentVersion);
  const snap = replica.snapshot();
  assert.deepEqual(snap.get("user:1"), {
    name: "alice",
    tags: ["admin", "owner"],
  });
  assert.deepEqual(snap.get("user:2"), { name: "bob", tags: [] });
  snap.release();

  // 回放出的副本可以继续接受新事务。
  const t4 = replica.begin();
  t4.set("user:3", { name: "carol", tags: [] });
  t4.commit();
  assert.equal(replica.currentVersion, 4);
});

test("主动回滚：缓冲写入全部丢弃，存储不受影响", () => {
  const store = new Store();
  const t1 = store.begin();
  t1.set("k", "committed");
  t1.commit();

  const t2 = store.begin();
  t2.set("k", "dirty");
  t2.set("extra", "dirty");
  t2.rollback();

  const snap = store.snapshot();
  assert.equal(snap.get("k"), "committed");
  assert.equal(snap.get("extra"), undefined);
  snap.release();
  assert.equal(store.currentVersion, 1);
});

test("事务内读己之写", () => {
  const store = new Store();
  const t = store.begin();
  t.set("k", 1);
  assert.equal(t.get("k"), 1);
  t.set("k", 2);
  assert.equal(t.get("k"), 2);
  t.commit();
  assert.equal(store.snapshot().get("k"), 2);
});

test("事务结束后继续操作抛错", () => {
  const store = new Store();
  const t = store.begin();
  t.set("k", 1);
  t.commit();
  assert.throws(() => t.get("k"), TransactionStateError);
  assert.throws(() => t.set("k", 2), TransactionStateError);
  assert.throws(() => t.commit(), TransactionStateError);
});

test("快照释放后不可再读", () => {
  const store = new Store();
  const snap = store.snapshot();
  snap.release();
  assert.throws(() => snap.get("k"), TransactionStateError);
});
