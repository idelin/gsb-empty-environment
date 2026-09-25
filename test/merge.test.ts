import { test } from "node:test";
import assert from "node:assert/strict";
import {
  MergeError,
  formatPath,
  merge3,
  resolveAll,
  resolveConflict,
  type Conflict,
} from "../src/index.ts";

test("对象：双方修改不同键时递归合并，无冲突", () => {
  const base = { a: 1, b: 2, c: 3 };
  const ours = { a: 10, b: 2, c: 3 };
  const theirs = { a: 1, b: 20, c: 3 };
  const { merged, conflicts } = merge3(base, ours, theirs);
  assert.deepEqual(merged, { a: 10, b: 20, c: 3 });
  assert.equal(conflicts.length, 0);
});

test("对象：双方改同一标量产生 modify-modify 冲突，记录三方值与路径", () => {
  const { merged, conflicts } = merge3({ v: 1 }, { v: 2 }, { v: 3 });
  assert.equal(conflicts.length, 1);
  const c = conflicts[0];
  assert.equal(c.reason, "modify-modify");
  assert.equal(formatPath(c.path), "$.v");
  assert.equal(c.base, 1);
  assert.equal(c.ours, 2);
  assert.equal(c.theirs, 3);
  assert.deepEqual(merged, { v: 2 });
});

test("冲突可逐项人工解决：take ours/theirs/base 或自定义值", () => {
  const { merged, conflicts } = merge3({ v: 1 }, { v: 2 }, { v: 3 });
  const c = conflicts[0];
  assert.deepEqual(resolveConflict(merged, c, { take: "ours" }), { v: 2 });
  assert.deepEqual(resolveConflict(merged, c, { take: "theirs" }), { v: 3 });
  assert.deepEqual(resolveConflict(merged, c, { take: "base" }), { v: 1 });
  assert.deepEqual(resolveConflict(merged, c, { value: 42 }), { v: 42 });
});

test("一方删除节点、另一方修改同节点：delete-modify 冲突", () => {
  const base = { user: { name: "a", age: 1 }, keep: true };
  const ours = { keep: true };
  const theirs = { user: { name: "b", age: 1 }, keep: true };
  const { merged, conflicts } = merge3(base, ours, theirs);
  assert.equal(conflicts.length, 1);
  const c = conflicts[0];
  assert.equal(c.reason, "delete-modify");
  assert.equal(formatPath(c.path), "$.user");
  assert.deepEqual(c.base, { name: "a", age: 1 });
  assert.equal(c.ours, undefined);
  assert.deepEqual(c.theirs, { name: "b", age: 1 });
  assert.deepEqual(merged, { keep: true });
  assert.deepEqual(resolveConflict(merged, c, { take: "theirs" }), {
    user: { name: "b", age: 1 },
    keep: true,
  });
});

test("镜像 delete-modify：theirs 删除、ours 修改", () => {
  const base = { n: { x: 1 } };
  const ours = { n: { x: 2 } };
  const theirs = {};
  const { merged, conflicts } = merge3(base, ours, theirs);
  assert.equal(conflicts[0].reason, "delete-modify");
  assert.equal(conflicts[0].theirs, undefined);
  assert.deepEqual(merged, { n: { x: 2 } });
});

test("双方删除同一节点：无冲突，节点消失", () => {
  const { merged, conflicts } = merge3({ a: 1, b: 2 }, { b: 2 }, { b: 2 });
  assert.deepEqual(merged, { b: 2 });
  assert.equal(conflicts.length, 0);
});

test("双方新增同键：同值直接合并，不同值产生 add-add 冲突", () => {
  const same = merge3({}, { x: 1 }, { x: 1 });
  assert.deepEqual(same.merged, { x: 1 });
  assert.equal(same.conflicts.length, 0);

  const diff = merge3({}, { x: 1 }, { x: 2 });
  assert.equal(diff.conflicts.length, 1);
  assert.equal(diff.conflicts[0].reason, "add-add");
  assert.equal(diff.conflicts[0].base, undefined);
});

test("双方新增同键对象：递归合并各自字段", () => {
  const { merged, conflicts } = merge3({}, { cfg: { x: 1 } }, { cfg: { y: 2 } });
  assert.deepEqual(merged, { cfg: { x: 1, y: 2 } });
  assert.equal(conflicts.length, 0);
});

test("类型变化不一致（对象 vs 标量）产生冲突", () => {
  const { merged, conflicts } = merge3({ v: { x: 1 } }, { v: "s" }, { v: 5 });
  assert.equal(conflicts.length, 1);
  assert.equal(conflicts[0].reason, "modify-modify");
  assert.deepEqual(merged, { v: "s" });
});

test("数组：不同 ID 的项独立合并，新增项各就各位", () => {
  const base = { users: [{ id: 1, name: "a" }] };
  const ours = { users: [{ id: 1, name: "a" }, { id: 2, name: "b" }] };
  const theirs = { users: [{ id: 3, name: "c" }, { id: 1, name: "a" }] };
  const { merged, conflicts } = merge3(base, ours, theirs);
  assert.equal(conflicts.length, 0);
  const users = (merged as { users: { id: number }[] }).users;
  assert.deepEqual(users.map((u) => u.id), [3, 1, 2]);
});

test("数组：同 ID 项双方改不同字段时递归合并", () => {
  const base = { users: [{ id: 1, name: "a", age: 1 }] };
  const ours = { users: [{ id: 1, name: "b", age: 1 }] };
  const theirs = { users: [{ id: 1, name: "a", age: 2 }] };
  const { merged, conflicts } = merge3(base, ours, theirs);
  assert.deepEqual(merged, { users: [{ id: 1, name: "b", age: 2 }] });
  assert.equal(conflicts.length, 0);
});

test("数组：同 ID 项双方改同字段产生冲突，携带 itemId 与路径", () => {
  const base = { users: [{ id: 7, name: "a" }] };
  const ours = { users: [{ id: 7, name: "b" }] };
  const theirs = { users: [{ id: 7, name: "c" }] };
  const { merged, conflicts } = merge3(base, ours, theirs);
  assert.equal(conflicts.length, 1);
  const c = conflicts[0];
  assert.equal(c.reason, "modify-modify");
  assert.equal(c.itemId, 7);
  assert.equal(formatPath(c.path), "$.users[0].name");
  assert.deepEqual(resolveConflict(merged, c, { take: "theirs" }), {
    users: [{ id: 7, name: "c" }],
  });
});

test("数组：一方删除项、另一方修改同项，delete-modify 冲突", () => {
  const base = { users: [{ id: 1, name: "a" }, { id: 2, name: "b" }] };
  const ours = { users: [{ id: 2, name: "b" }] };
  const theirs = { users: [{ id: 1, name: "a2" }, { id: 2, name: "b" }] };
  const { merged, conflicts } = merge3(base, ours, theirs);
  assert.equal(conflicts.length, 1);
  const c = conflicts[0];
  assert.equal(c.reason, "delete-modify");
  assert.equal(c.itemId, 1);
  assert.equal(c.ours, undefined);
  assert.deepEqual((merged as { users: unknown[] }).users, [{ id: 2, name: "b" }]);
  const fixed = resolveConflict(merged, c, { take: "theirs" });
  assert.deepEqual(fixed, {
    users: [{ id: 1, name: "a2" }, { id: 2, name: "b" }],
  });
});

test("数组：双方删除同项，无冲突", () => {
  const base = { users: [{ id: 1 }, { id: 2 }] };
  const { merged, conflicts } = merge3(base, { users: [{ id: 2 }] }, { users: [{ id: 2 }] });
  assert.deepEqual(merged, { users: [{ id: 2 }] });
  assert.equal(conflicts.length, 0);
});

test("数组：仅一方重排时采用该方顺序", () => {
  const base = { list: [{ id: 1 }, { id: 2 }, { id: 3 }] };
  const ours = { list: [{ id: 3 }, { id: 1 }, { id: 2 }] };
  const theirs = { list: [{ id: 1 }, { id: 2 }, { id: 3 }] };
  const r1 = merge3(base, ours, theirs);
  assert.equal(r1.conflicts.length, 0);
  assert.deepEqual((r1.merged as { list: { id: number }[] }).list.map((i) => i.id), [3, 1, 2]);

  const r2 = merge3(base, theirs, ours);
  assert.equal(r2.conflicts.length, 0);
  assert.deepEqual((r2.merged as { list: { id: number }[] }).list.map((i) => i.id), [3, 1, 2]);
});

test("数组：双方以不同方式重排产生 reorder 冲突，可解决", () => {
  const base = { list: [{ id: 1 }, { id: 2 }, { id: 3 }] };
  const ours = { list: [{ id: 3 }, { id: 1 }, { id: 2 }] };
  const theirs = { list: [{ id: 2 }, { id: 3 }, { id: 1 }] };
  const { merged, conflicts } = merge3(base, ours, theirs);
  assert.equal(conflicts.length, 1);
  const c = conflicts[0];
  assert.equal(c.reason, "reorder");
  assert.deepEqual(c.base, [1, 2, 3]);
  assert.deepEqual(c.ours, [3, 1, 2]);
  assert.deepEqual(c.theirs, [2, 3, 1]);
  const ids = (doc: unknown) => (doc as { list: { id: number }[] }).list.map((i) => i.id);
  assert.deepEqual(ids(merged), [3, 1, 2]);
  assert.deepEqual(ids(resolveConflict(merged, c, { take: "theirs" })), [2, 3, 1]);
});

test("重复 ID 被拒绝，错误带路径与来源侧", () => {
  assert.throws(
    () => merge3({ list: [] }, { list: [{ id: 1 }, { id: 1 }] }, { list: [] }),
    (e: unknown) => {
      assert.ok(e instanceof MergeError);
      assert.equal(e.code, "DUPLICATE_ID");
      assert.equal(e.side, "ours");
      assert.equal(formatPath(e.path), "$.list[1]");
      assert.match(e.message, /duplicate "id" value 1/);
      return true;
    },
  );
});

test("嵌套数组中的重复 ID：错误定位到深层路径", () => {
  const doc = { a: { b: [{ id: "x" }, { id: "x" }] } };
  assert.throws(
    () => merge3({ a: { b: [] } }, doc, { a: { b: [] } }),
    (e: unknown) => {
      assert.ok(e instanceof MergeError);
      assert.equal(formatPath(e.path), '$.a.b[1]');
      return true;
    },
  );
});

test("缺少 ID 的数组项被拒绝", () => {
  assert.throws(
    () => merge3({ tags: [] }, { tags: ["x"] }, { tags: [] }),
    (e: unknown) => {
      assert.ok(e instanceof MergeError);
      assert.equal(e.code, "MISSING_ID");
      assert.equal(e.side, "ours");
      assert.equal(formatPath(e.path), "$.tags[0]");
      return true;
    },
  );
  assert.throws(
    () => merge3({ items: [{ uid: 1 }] }, { items: [{ uid: 1 }] }, { items: [{ uid: 1 }] }),
    (e: unknown) => e instanceof MergeError && e.code === "MISSING_ID",
  );
});

test("非法 ID 类型被拒绝", () => {
  assert.throws(
    () => merge3({ list: [{ id: true }] }, { list: [{ id: true }] }, { list: [{ id: true }] }),
    (e: unknown) => e instanceof MergeError && e.code === "INVALID_ID",
  );
});

test("自定义 idKey", () => {
  const base = { rows: [{ uid: "a", v: 1 }] };
  const ours = { rows: [{ uid: "a", v: 2 }] };
  const theirs = { rows: [{ uid: "a", v: 1 }, { uid: "b", v: 9 }] };
  const { merged, conflicts } = merge3(base, ours, theirs, { idKey: "uid" });
  assert.equal(conflicts.length, 0);
  assert.deepEqual(merged, { rows: [{ uid: "a", v: 2 }, { uid: "b", v: 9 }] });
});

test("onConflict 回调在合并时内联解决，冲突仍被记录", () => {
  const seen: Conflict[] = [];
  const { merged, conflicts } = merge3(
    { a: 1, b: 1 },
    { a: 2, b: 2 },
    { a: 3, b: 3 },
    {
      onConflict: (c) => {
        seen.push(c);
        return { take: "theirs" };
      },
    },
  );
  assert.deepEqual(merged, { a: 3, b: 3 });
  assert.equal(conflicts.length, 2);
  assert.equal(seen.length, 2);
});

test("resolveAll 按数组或回调逐项解决全部冲突", () => {
  const base = { a: 1, b: 1, c: { d: 1 } };
  const ours = { a: 2, b: 2, c: { d: 2 } };
  const theirs = { a: 3, b: 3, c: { d: 3 } };
  const { merged, conflicts } = merge3(base, ours, theirs);
  assert.equal(conflicts.length, 3);

  const byArray = resolveAll(merged, conflicts, [
    { take: "theirs" },
    { take: "base" },
    { value: 99 },
  ]);
  assert.deepEqual(byArray, { a: 3, b: 1, c: { d: 99 } });

  const byFn = resolveAll(merged, conflicts, (c) =>
    formatPath(c.path) === "$.a" ? { take: "theirs" } : { take: "ours" },
  );
  assert.deepEqual(byFn, { a: 3, b: 2, c: { d: 2 } });
});

test("合并不修改输入（不可变性）", () => {
  const base = { list: [{ id: 1, v: 1 }], x: 1 };
  const ours = { list: [{ id: 1, v: 2 }], x: 2 };
  const theirs = { list: [{ id: 1, v: 1 }, { id: 2, v: 0 }], x: 1 };
  const snapshot = JSON.stringify({ base, ours, theirs });
  merge3(base, ours, theirs);
  assert.equal(JSON.stringify({ base, ours, theirs }), snapshot);
});
