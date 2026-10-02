/**
 * 声明式约束断言器自身的语义测试
 */
import { test } from "node:test";
import assert from "node:assert/strict";
import { Database, ConstraintViolationError } from "../src/index.ts";

test("checkConstraints 在已提交状态上校验全部不变量", async () => {
  const db = new Database();
  db.addConstraint({
    name: "inventory.non_negative",
    tables: ["inventory"],
    predicate: (view) =>
      view.scan("inventory").every((row) => (row.qty as number) >= 0),
  });

  const tx = db.begin();
  tx.put("inventory", "item1", { qty: 5 });
  await tx.commit();
  db.checkConstraints(); // 不抛异常

  const bad = db.begin();
  bad.put("inventory", "item1", { qty: -1 });
  await assert.rejects(bad.commit(), ConstraintViolationError);
});

test("删除行同样触发约束重验", async () => {
  const db = new Database();
  db.addConstraint({
    name: "team.nonempty",
    tables: ["team"],
    predicate: (view) => view.count("team") >= 1,
  });
  const seed = db.begin();
  seed.put("team", "m1", { name: "成员1" });
  await seed.commit();

  const tx = db.begin();
  tx.delete("team", "m1");
  await assert.rejects(tx.commit(), ConstraintViolationError);
  assert.equal(db.committedView().count("team"), 1);
});

test("多约束独立判定，报错信息包含被违反的约束名", async () => {
  const db = new Database();
  db.addConstraint({
    name: "a.at_least_one",
    tables: ["ta"],
    predicate: (view) => view.count("ta") >= 1,
  });
  db.addConstraint({
    name: "b.at_least_one",
    tables: ["tb"],
    predicate: (view) => view.count("tb") >= 1,
  });
  const seed = db.begin();
  seed.put("ta", "x", {});
  seed.put("tb", "y", {});
  await seed.commit();

  const tx = db.begin();
  tx.delete("tb", "y");
  await assert.rejects(tx.commit(), (err: unknown) => {
    assert.ok(err instanceof ConstraintViolationError);
    assert.equal(err.constraintName, "b.at_least_one");
    return true;
  });
});

test("read-your-writes：事务内能读到自己未提交的写入", async () => {
  const db = new Database();
  const tx = db.begin();
  tx.put("t", "k", { n: 1 });
  assert.deepEqual(tx.get("t", "k"), { n: 1 });
  assert.equal(tx.count("t"), 1);
  tx.rollback();
});

test("事务结束后禁止再操作", async () => {
  const db = new Database();
  const tx = db.begin();
  await tx.commit();
  assert.throws(() => tx.get("t", "k"), /事务已结束/);
});
