import { Store, TransactionConflictError } from "./src/mvcc.ts";

function show(title: string): void {
  console.log(`\n=== ${title} ===`);
}

// 场景 1：读事务固定版本，看不到事务中间态
{
  show("1. 快照隔离：读者看不到中间态与新版本");
  const store = new Store();
  const init = store.begin();
  init.set("balance", 100);
  init.commit();

  const reader = store.begin();
  console.log(`reader 固定于 v${reader.readVersion}, balance =`, reader.get("balance"));

  const writer = store.begin();
  writer.set("balance", 50); // 中间态：尚未提交
  console.log("writer 已写入但未提交, reader 读到:", reader.get("balance"));
  writer.commit();
  console.log(`writer 已提交 (v${store.currentVersion}), reader 仍读到:`, reader.get("balance"));
  reader.rollback();
}

// 场景 2：两事务读同值后写，后者冲突回滚，不丢更新
{
  show("2. 丢失更新防护：读-改-写冲突");
  const store = new Store();
  const init = store.begin();
  init.set("balance", 100);
  init.commit();

  const t1 = store.begin();
  const t2 = store.begin();
  t1.set("balance", Number(t1.get("balance")) - 30);
  t2.set("balance", Number(t2.get("balance")) - 50);

  t1.commit();
  try {
    t2.commit();
    console.log("t2 提交成功（不应发生）");
  } catch (error) {
    if (error instanceof TransactionConflictError) {
      console.log(`t2 冲突回滚: ${error.message}`);
    } else {
      throw error;
    }
  }
  const snap = store.snapshot();
  console.log("最终 balance =", snap.get("balance"), "（t2 的写入未残留）");
  snap.release();
}

// 场景 3：不同键无冲突
{
  show("3. 不同键并行写：均可提交");
  const store = new Store();
  const t1 = store.begin();
  const t2 = store.begin();
  t1.set("a", 1);
  t2.set("b", 2);
  console.log("t1 提交于 v" + t1.commit(), ", t2 提交于 v" + t2.commit());
}

// 场景 4：只读快照与垃圾回收
{
  show("4. 只读快照与 GC");
  const store = new Store();
  const t = store.begin();
  t.set("k", "v1");
  t.commit();

  const old = store.snapshot();
  for (const value of ["v2", "v3", "v4"]) {
    const w = store.begin();
    w.set("k", value);
    w.commit();
  }
  console.log(`已提交到 v${store.currentVersion}，旧快照钉住 v${old.version}`);
  console.log("GC 回收（快照钉住中）:", store.gc(), "个版本");
  console.log("旧快照读到:", old.get("k"), "| 新快照读到:", store.snapshot().get("k"));
  old.release();
  console.log("释放后 GC 回收:", store.gc(), "个版本");
}

// 场景 5：操作日志回放
{
  show("5. 操作日志回放");
  const store = new Store();
  const t1 = store.begin();
  t1.set("user:1", "alice");
  t1.commit();
  const t2 = store.begin();
  t2.set("user:2", "bob");
  t2.commit();

  const journal = JSON.stringify(store.getJournal());
  console.log("日志:", journal);

  const replica = Store.replay(JSON.parse(journal));
  const snap = replica.snapshot();
  console.log(`回放副本 v${replica.currentVersion}: user:1 =`, snap.get("user:1"),
    ", user:2 =", snap.get("user:2"));
  snap.release();
}
