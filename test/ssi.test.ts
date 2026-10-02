import test from "node:test";
import assert from "node:assert/strict";
import { Database, SerializationError } from "../src/ssi.ts";

/**
 * 验收场景：账户 A=100, B=100，约束 A+B>=0。
 * T1 读 A、B 并扣 A 80；T2 并发读 A、B 并扣 B 80。
 */
function writeSkewInterleaving(db: Database, amount: number) {
  db.seed("A", 100);
  db.seed("B", 100);

  const t1 = db.begin();
  const t2 = db.begin();

  const a1 = t1.read("A")!;
  const b1 = t1.read("B")!;
  const a2 = t2.read("A")!;
  const b2 = t2.read("B")!;

  // 两个事务都基于各自快照校验约束 A+B>=0 后扣款。
  assert.ok(a1 + b1 - amount >= 0);
  assert.ok(a2 + b2 - amount >= 0);
  t1.write("A", a1 - amount);
  t2.write("B", b2 - amount);

  t1.commit();
  let t2Error: unknown = null;
  try {
    t2.commit();
  } catch (err) {
    t2Error = err;
  }
  return { t1, t2, t2Error };
}

test("SSI 精准识别写倾斜危险结构并中止 T2（验收场景）", () => {
  const db = new Database(); // SSI 默认开启
  const { t1, t2, t2Error } = writeSkewInterleaving(db, 80);

  assert.equal(t1.state, "committed");
  assert.equal(t2.state, "aborted");
  assert.ok(t2Error instanceof SerializationError);
  assert.match((t2Error as Error).message, /dangerous structure/);

  // T2 被中止，其扣款未生效：A=20, B=100，约束成立。
  assert.equal(db.committedValue("A"), 20);
  assert.equal(db.committedValue("B"), 100);
  assert.ok(db.committedValue("A")! + db.committedValue("B")! >= 0);

  // 重试 T2：基于新快照重新校验后串行成功。
  const t2Retry = db.begin();
  const a = t2Retry.read("A")!;
  const b = t2Retry.read("B")!;
  assert.ok(a + b - 80 >= 0);
  t2Retry.write("B", b - 80);
  t2Retry.commit();
  assert.equal(db.committedValue("A"), 20);
  assert.equal(db.committedValue("B"), 20);
  assert.ok(db.committedValue("A")! + db.committedValue("B")! >= 0);
});

test("纯 SI 对照：同样的交错执行发生写倾斜（无中止）", () => {
  const db = new Database({ ssi: false });
  const { t1, t2, t2Error } = writeSkewInterleaving(db, 80);

  assert.equal(t1.state, "committed");
  assert.equal(t2.state, "committed");
  assert.equal(t2Error, null);
  // 双重扣款均生效（非可串行化结果）。
  assert.equal(db.committedValue("A"), 20);
  assert.equal(db.committedValue("B"), 20);
});

test("纯 SI 下放大扣款可直接击穿约束 A+B>=0，SSI 则阻止", () => {
  // 扣 150：任一事务单独执行都合法（200-150=50>=0），
  // 但两者同时生效得到 -50 + -50 = -100 < 0，约束被击穿。
  const siDb = new Database({ ssi: false });
  writeSkewInterleaving(siDb, 150);
  assert.equal(siDb.committedValue("A")! + siDb.committedValue("B")!, -100);

  const ssiDb = new Database();
  const { t2 } = writeSkewInterleaving(ssiDb, 150);
  assert.equal(t2.state, "aborted");
  assert.ok(ssiDb.committedValue("A")! + ssiDb.committedValue("B")! >= 0);
});

test("无误伤：写不相交 key 的并发事务都可提交", () => {
  const db = new Database();
  db.seed("X", 1);
  db.seed("Y", 2);
  const t1 = db.begin();
  const t2 = db.begin();
  t1.write("X", 10);
  t2.write("Y", 20);
  t1.commit();
  t2.commit();
  assert.equal(db.committedValue("X"), 10);
  assert.equal(db.committedValue("Y"), 20);
});

test("无误伤：单条 rw 边（读旧值 + 并发写）不构成环，双方提交", () => {
  const db = new Database();
  db.seed("K", 1);
  const reader = db.begin();
  const writer = db.begin();
  assert.equal(reader.read("K"), 1);
  writer.write("K", 2);
  reader.commit();
  writer.commit();
  assert.equal(db.committedValue("K"), 2);
});

test("三事务危险结构 T1-rw->T2-rw->T3（T3 先提交）=> 中止枢轴 T2", () => {
  const db = new Database();
  db.seed("k2", 0);
  db.seed("k3", 0);
  const t1 = db.begin();
  const t2 = db.begin();
  const t3 = db.begin();

  t3.write("k3", 3);
  t2.read("k3"); // T2 -rw-> T3（T2 读不到 T3 的未提交写）
  t3.commit(); // T3 最先提交
  t2.write("k2", 2);
  t1.read("k2"); // T1 -rw-> T2，危险结构闭合 => 中止 T2

  assert.equal(t2.state, "aborted");
  assert.throws(() => t2.commit(), SerializationError);
  t1.commit();
  assert.equal(t1.state, "committed");
  assert.equal(t3.state, "committed");
});

test("写/写冲突：first-committer-wins", () => {
  const db = new Database();
  db.seed("K", 0);
  const t1 = db.begin();
  const t2 = db.begin();
  t1.write("K", 1);
  t2.write("K", 2);
  t1.commit();
  assert.throws(() => t2.commit(), /first committer wins/);
  assert.equal(db.committedValue("K"), 1);
});

test("被 SSI 中止的事务拒绝后续操作", () => {
  const db = new Database();
  db.seed("A", 100);
  db.seed("B", 100);
  const t1 = db.begin();
  const t2 = db.begin();
  t1.read("A");
  t1.read("B");
  t2.read("A");
  t2.read("B");
  t1.write("A", 20);
  t2.write("B", 20);
  t1.commit(); // 触发危险结构检测，中止 T2

  assert.equal(t2.state, "aborted");
  assert.throws(() => t2.read("A"), SerializationError);
  assert.throws(() => t2.write("A", 0), SerializationError);
  assert.throws(() => t2.commit(), SerializationError);
});

test("快照隔离：事务看不到开始之后的提交", () => {
  const db = new Database();
  db.seed("K", 1);
  const t1 = db.begin();
  const t2 = db.begin();
  t2.write("K", 99);
  t2.commit();
  assert.equal(t1.read("K"), 1); // 仍是快照中的旧值
  t1.abort();
  const t3 = db.begin();
  assert.equal(t3.read("K"), 99); // 新事务可见
});
