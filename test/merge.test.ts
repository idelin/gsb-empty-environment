import { test } from "node:test";
import assert from "node:assert/strict";
import {
  ABSENT,
  DELETE,
  MergeInputError,
  formatPath,
  jsonEqual,
  merge3,
} from "../src/index.ts";
import type { JsonValue } from "../src/index.ts";

test("对象递归合并：双方修改不同键，互不干扰", () => {
  const base = { a: 1, b: 2, nested: { x: "x", y: "y" } };
  const ours = { a: 10, b: 2, nested: { x: "x2", y: "y" } };
  const theirs = { a: 1, b: 20, nested: { x: "x", y: "y2" } };
  const result = merge3(base, ours, theirs);
  assert.equal(result.ok, true);
  assert.deepEqual(result.tree, {
    a: 10,
    b: 20,
    nested: { x: "x2", y: "y2" },
  });
});

test("对象键无序：键顺序不同不视为变更", () => {
  const base = { a: 1, b: { c: 2, d: 3 } };
  const ours = { b: { d: 3, c: 2 }, a: 1 };
  const theirs = { a: 1, b: { c: 2, d: 3 } };
  const result = merge3(base, ours, theirs);
  assert.equal(result.ok, true);
  assert.deepEqual(result.tree, base);
  assert.equal(jsonEqual({ p: 1, q: 2 }, { q: 2, p: 1 }), true);
});

test("同一键双方改成不同值：产生冲突并记录三方值", () => {
  const base = { name: "base" };
  const ours = { name: "ours" };
  const theirs = { name: "theirs" };
  const result = merge3(base, ours, theirs);
  assert.equal(result.ok, false);
  assert.equal(result.conflicts.length, 1);
  const conflict = result.conflicts[0];
  assert.equal(formatPath(conflict.path), "$.name");
  assert.equal(conflict.base, "base");
  assert.equal(conflict.ours, "ours");
  assert.equal(conflict.theirs, "theirs");
});

test("一侧删除节点、另一侧修改同节点：必须冲突", () => {
  const base = { keep: 1, server: { host: "a", port: 80 } };
  const ours = { keep: 1 };
  const theirs = { keep: 1, server: { host: "b", port: 80 } };
  const result = merge3(base, ours, theirs);
  assert.equal(result.ok, false);
  assert.equal(result.conflicts.length, 1);
  const conflict = result.conflicts[0];
  assert.equal(formatPath(conflict.path), "$.server");
  assert.deepEqual(conflict.base, { host: "a", port: 80 });
  assert.equal(conflict.ours, ABSENT);
  assert.deepEqual(conflict.theirs, { host: "b", port: 80 });
});

test("双方都删除同一节点：静默删除，无冲突", () => {
  const base = { a: 1, gone: { x: 1 } };
  const ours = { a: 1 };
  const theirs = { a: 1 };
  const result = merge3(base, ours, theirs);
  assert.equal(result.ok, true);
  assert.deepEqual(result.tree, { a: 1 });
});

test("一侧删除、另一侧未改动：删除生效", () => {
  const base = { a: 1, b: 2 };
  const ours = { a: 1 };
  const theirs = { a: 1, b: 2 };
  const result = merge3(base, ours, theirs);
  assert.equal(result.ok, true);
  assert.deepEqual(result.tree, { a: 1 });
});

test("数组：不同 id 的项可独立合并", () => {
  const base = {
    items: [
      { id: "a", v: 1 },
      { id: "b", v: 2 },
    ],
  };
  const ours = {
    items: [
      { id: "a", v: 100 },
      { id: "b", v: 2 },
    ],
  };
  const theirs = {
    items: [
      { id: "a", v: 1 },
      { id: "b", v: 200 },
    ],
  };
  const result = merge3(base, ours, theirs);
  assert.equal(result.ok, true);
  assert.deepEqual(result.tree, {
    items: [
      { id: "a", v: 100 },
      { id: "b", v: 200 },
    ],
  });
});

test("数组：同一 id 双方改不同字段做字段级合并，改同字段则冲突", () => {
  const base = { items: [{ id: 1, enabled: false, weight: 1 }] };
  const ours = { items: [{ id: 1, enabled: false, weight: 5 }] };
  const theirs = { items: [{ id: 1, enabled: true, weight: 1 }] };
  const merged = merge3(base, ours, theirs);
  assert.equal(merged.ok, true);
  assert.deepEqual(merged.tree, { items: [{ id: 1, enabled: true, weight: 5 }] });

  const clash = merge3(
    { items: [{ id: 1, v: "base" }] },
    { items: [{ id: 1, v: "ours" }] },
    { items: [{ id: 1, v: "theirs" }] },
  );
  assert.equal(clash.ok, false);
  assert.equal(formatPath(clash.conflicts[0].path), '$.items[id=1].v');
  assert.equal(clash.conflicts[0].base, "base");
  assert.equal(clash.conflicts[0].ours, "ours");
  assert.equal(clash.conflicts[0].theirs, "theirs");
});

test("数组：一侧删除某 id、另一侧修改同 id，产生冲突", () => {
  const base = { items: [{ id: "x", v: 1 }, { id: "y", v: 2 }] };
  const ours = { items: [{ id: "y", v: 2 }] };
  const theirs = { items: [{ id: "x", v: 9 }, { id: "y", v: 2 }] };
  const result = merge3(base, ours, theirs);
  assert.equal(result.ok, false);
  assert.equal(result.conflicts.length, 1);
  const conflict = result.conflicts[0];
  assert.equal(formatPath(conflict.path), '$.items[id="x"]');
  assert.deepEqual(conflict.base, { id: "x", v: 1 });
  assert.equal(conflict.ours, ABSENT);
  assert.deepEqual(conflict.theirs, { id: "x", v: 9 });
});

test("数组：双方各自新增不同 id，按 base、ours、theirs 顺序保留", () => {
  const base = { items: [{ id: "a" }] };
  const ours = { items: [{ id: "a" }, { id: "b", from: "ours" }] };
  const theirs = { items: [{ id: "c", from: "theirs" }, { id: "a" }] };
  const result = merge3(base, ours, theirs);
  assert.equal(result.ok, true);
  assert.deepEqual(result.tree, {
    items: [{ id: "a" }, { id: "b", from: "ours" }, { id: "c", from: "theirs" }],
  });
});

test("数组：双方新增同一 id，内容一致则合并，不一致则逐项冲突", () => {
  const same = merge3(
    { items: [] },
    { items: [{ id: "n", v: 1 }] },
    { items: [{ id: "n", v: 1 }] },
  );
  assert.equal(same.ok, true);
  assert.deepEqual(same.tree, { items: [{ id: "n", v: 1 }] });

  const diff = merge3(
    { items: [] },
    { items: [{ id: "n", v: 1 }] },
    { items: [{ id: "n", v: 2 }] },
  );
  assert.equal(diff.ok, false);
  assert.equal(formatPath(diff.conflicts[0].path), '$.items[id="n"].v');
});

test("重复 id 拒绝：三个来源分别报错并定位路径", () => {
  const good = { items: [{ id: "a" }] };
  const bad = { items: [{ id: "a" }, { id: "a" }] };
  for (const source of ["base", "ours", "theirs"] as const) {
    const args = { base: good, ours: good, theirs: good };
    args[source] = bad;
    assert.throws(
      () => merge3(args.base, args.ours, args.theirs),
      (err: unknown) => {
        assert.ok(err instanceof MergeInputError);
        assert.equal(err.source, source);
        assert.equal(formatPath(err.path), '$.items[id="a"]');
        assert.match(err.message, /duplicate id/);
        return true;
      },
    );
  }
});

test("数组项缺 id 或不是对象：拒绝并给出索引路径与来源", () => {
  const good = { items: [{ id: "a" }] };
  assert.throws(
    () => merge3(good, { items: [{ id: "a" }, { v: 1 }] }, good),
    (err: unknown) => {
      assert.ok(err instanceof MergeInputError);
      assert.equal(err.source, "ours");
      assert.equal(formatPath(err.path), "$.items[1]");
      return true;
    },
  );
  assert.throws(
    () => merge3(good, good, { items: [42] }),
    (err: unknown) => {
      assert.ok(err instanceof MergeInputError);
      assert.equal(err.source, "theirs");
      assert.equal(formatPath(err.path), "$.items[0]");
      return true;
    },
  );
});

test("非 JSON 兼容输入：拒绝并定位到具体路径", () => {
  assert.throws(
    () => merge3({ a: 1 }, { a: Number.NaN } as unknown as JsonValue, { a: 1 }),
    (err: unknown) => {
      assert.ok(err instanceof MergeInputError);
      assert.equal(err.source, "ours");
      assert.equal(formatPath(err.path), "$.a");
      return true;
    },
  );
  assert.throws(
    () => merge3({ a: 1 }, { a: 1 }, { a: () => 1 } as unknown as JsonValue),
    (err: unknown) => {
      assert.ok(err instanceof MergeInputError);
      assert.equal(err.source, "theirs");
      assert.equal(formatPath(err.path), "$.a");
      return true;
    },
  );
});

test("冲突可人工逐项解决：resolutions 按路径生效", () => {
  const base = { name: "base", server: { tls: false }, n: 1 };
  const ours = { name: "ours", server: {}, n: 1 };
  const theirs = { name: "theirs", server: { tls: true }, n: 1 };
  const first = merge3(base, ours, theirs);
  assert.equal(first.ok, false);
  assert.deepEqual(
    first.conflicts.map((c) => formatPath(c.path)).sort(),
    ["$.name", "$.server.tls"],
  );

  const resolved = merge3(base, ours, theirs, {
    resolutions: [
      { path: "$.name", value: "chosen" },
      { path: ["server", "tls"], value: DELETE },
    ],
  });
  assert.equal(resolved.ok, true);
  assert.deepEqual(resolved.tree, { name: "chosen", server: {}, n: 1 });
  assert.equal(resolved.unusedResolutions.length, 0);
});

test("未命中的 resolution 会被报告为 unusedResolutions", () => {
  const result = merge3(
    { a: 1 },
    { a: 2 },
    { a: 1 },
    { resolutions: [{ path: "$.nope", value: 0 }] },
  );
  assert.equal(result.ok, true);
  assert.deepEqual(result.tree, { a: 2 });
  assert.equal(result.unusedResolutions.length, 1);
  assert.equal(result.unusedResolutions[0].path, "$.nope");
});

test("根节点为标量或数组时也可合并与冲突", () => {
  const scalar = merge3("base", "ours", "theirs");
  assert.equal(scalar.ok, false);
  assert.equal(formatPath(scalar.conflicts[0].path), "$");

  const arr = merge3([{ id: 1, v: 1 }], [{ id: 1, v: 2 }], [{ id: 1, v: 1 }]);
  assert.equal(arr.ok, true);
  assert.deepEqual(arr.tree, [{ id: 1, v: 2 }]);
});

test("嵌套数组中的错误路径可定位到深层位置", () => {
  const bad = {
    level1: {
      level2: [{ id: "a" }, { id: "b" }, { id: "b" }],
    },
  };
  assert.throws(
    () => merge3({ level1: { level2: [] } }, bad, { level1: { level2: [] } }),
    (err: unknown) => {
      assert.ok(err instanceof MergeInputError);
      assert.equal(err.source, "ours");
      assert.equal(formatPath(err.path), '$.level1.level2[id="b"]');
      return true;
    },
  );
});

test("自定义 idKey", () => {
  const base = { rows: [{ key: "a", v: 1 }] };
  const ours = { rows: [{ key: "a", v: 2 }] };
  const theirs = { rows: [{ key: "a", v: 1 }, { key: "b", v: 3 }] };
  const result = merge3(base, ours, theirs, { idKey: "key" });
  assert.equal(result.ok, true);
  assert.deepEqual(result.tree, {
    rows: [
      { key: "a", v: 2 },
      { key: "b", v: 3 },
    ],
  });
});
