/**
 * 验收测试：写倾斜（write skew）检测与精准中止
 */
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  Database,
  ConstraintViolationError,
  WriteConflictError,
} from "../src/index.ts";

const MIN_ONE_ON_CALL = {
  name: "on_call.min_coverage",
  description: "值班表任意时刻至少有 1 名医生在岗",
  tables: ["on_call"],
  predicate: (view: {
    count: (t: string, f?: (r: Record<string, unknown>) => boolean) => number;
  }) => view.count("on_call", (row) => row.onCall === true) >= 1,
};

async function seedTwoDoctors(db: Database): Promise<void> {
  const tx = db.begin();
  tx.put("on_call", "A", { doctor: "A", onCall: true });
  tx.put("on_call", "B", { doctor: "B", onCall: true });
  await tx.commit();
}

function requestLeave(db: Database, doctor: string) {
  const tx = db.begin();
  const onCallCount = tx.count("on_call", (row) => row.onCall === true);
  assert.ok(onCallCount >= 2, `${doctor} 在快照中应看到对方在岗`);
  tx.put("on_call", doctor, { doctor, onCall: false });
  return tx;
}

test("验收边界：A、B 同时请假，系统精准中止其中一人，不变量保持", async () => {
  const db = new Database();
  db.addConstraint(MIN_ONE_ON_CALL);
  await seedTwoDoctors(db);

  // A、B 基于同一快照各自读到对方在岗，然后并发提交
  const txA = requestLeave(db, "A");
  const txB = requestLeave(db, "B");
  const [resultA, resultB] = await Promise.allSettled([
    txA.commit(),
    txB.commit(),
  ]);

  const succeeded = [resultA, resultB].filter(
    (r) => r.status === "fulfilled",
  );
  const aborted = [resultA, resultB].filter((r) => r.status === "rejected");

  // 恰好一个成功、一个被中止
  assert.equal(succeeded.length, 1, "应恰好一个事务提交成功");
  assert.equal(aborted.length, 1, "应恰好一个事务被中止");
  assert.ok(
    aborted[0].status === "rejected" &&
      aborted[0].reason instanceof ConstraintViolationError,
  );
  assert.match(
    (aborted[0] as PromiseRejectedResult).reason.message,
    /on_call\.min_coverage/,
  );

  // 最终值班人数为 1，不变量成立
  const finalCount = db
    .committedView()
    .count("on_call", (row) => row.onCall === true);
  assert.equal(finalCount, 1);
  db.checkConstraints();
});

test("写倾斜是确定性的：后进入提交临界区的一方被中止", async () => {
  const db = new Database();
  db.addConstraint(MIN_ONE_ON_CALL);
  await seedTwoDoctors(db);

  const txA = requestLeave(db, "A");
  const txB = requestLeave(db, "B");

  await txA.commit(); // A 先提交成功（B 仍在岗，人数 2 -> 1）
  await assert.rejects(txB.commit(), ConstraintViolationError); // B 重验发现 1 -> 0

  const onCall = db
    .committedView()
    .scan("on_call")
    .filter((row) => row.onCall === true)
    .map((row) => row.doctor);
  assert.deepEqual(onCall, ["B"]); // A 走了，B 被留下
});

test("无并发时不误伤：单事务违反不变量同样被拦截", async () => {
  const db = new Database();
  db.addConstraint(MIN_ONE_ON_CALL);
  const seed = db.begin();
  seed.put("on_call", "A", { doctor: "A", onCall: true });
  await seed.commit();

  const tx = db.begin();
  tx.put("on_call", "A", { doctor: "A", onCall: false });
  await assert.rejects(tx.commit(), ConstraintViolationError);

  assert.equal(
    db.committedView().count("on_call", (row) => row.onCall === true),
    1,
  );
});

test("互不相关的并发事务不产生误中止", async () => {
  const db = new Database();
  db.addConstraint(MIN_ONE_ON_CALL);
  await seedTwoDoctors(db);

  // 事务 1 修改与约束无关的表；事务 2 正常请假（2 -> 1 合法）
  const tx1 = db.begin();
  tx1.put("shifts", "s1", { id: "s1", note: "换班备注" });
  const tx2 = db.begin();
  tx2.put("on_call", "A", { doctor: "A", onCall: false });

  await Promise.all([tx1.commit(), tx2.commit()]);
  assert.equal(
    db.committedView().count("on_call", (row) => row.onCall === true),
    1,
  );
});

test("写-写冲突：同一行被并发修改时 first-committer-wins", async () => {
  const db = new Database();
  const seed = db.begin();
  seed.put("accounts", "acc1", { balance: 100 });
  await seed.commit();

  const tx1 = db.begin();
  const tx2 = db.begin();
  tx1.put("accounts", "acc1", { balance: 80 });
  tx2.put("accounts", "acc1", { balance: 60 });

  await tx1.commit();
  await assert.rejects(tx2.commit(), WriteConflictError);
  assert.deepEqual(db.committedView().get("accounts", "acc1"), {
    balance: 80,
  });
});

test("快照隔离：事务读不到 begin 之后提交的并发写入", async () => {
  const db = new Database();
  const seed = db.begin();
  seed.put("kv", "k", { v: 1 });
  await seed.commit();

  const reader = db.begin();
  const writer = db.begin();
  writer.put("kv", "k", { v: 2 });
  await writer.commit();

  // reader 的快照早于 writer 的提交，仍应读到旧值
  assert.deepEqual(reader.get("kv", "k"), { v: 1 });
  reader.rollback();
  assert.deepEqual(db.committedView().get("kv", "k"), { v: 2 });
});

test("回滚安全：被中止的事务不留任何写入痕迹", async () => {
  const db = new Database();
  db.addConstraint(MIN_ONE_ON_CALL);
  await seedTwoDoctors(db);

  const txA = requestLeave(db, "A");
  const txB = requestLeave(db, "B");
  txB.put("audit_log", "log1", { msg: "B 的附带写入" }); // B 还有其他写入
  await txA.commit();
  await assert.rejects(txB.commit(), ConstraintViolationError);

  // B 被中止后，其所有写入（包括无关表的）都不得落库
  assert.equal(db.committedView().get("audit_log", "log1"), undefined);
  assert.equal(
    db.committedView().count("on_call", (row) => row.onCall === true),
    1,
  );
});
