// 运行: node tests.js
"use strict";
const assert = require("assert");
const { solve, formatSolution } = require("./anonymize.js");

const ageCol = {
  name: "age",
  hierarchy: {
    "23": "20-24",
    "24": "20-24",
    "25": "25-29",
    "26": "25-29",
    "20-24": "20-29",
    "25-29": "20-29",
    "20-29": "*",
  },
};
const cityCol = {
  name: "city",
  hierarchy: {
    北京: "华北",
    天津: "华北",
    上海: "华东",
    杭州: "华东",
    华北: "*",
    华东: "*",
  },
};

// 1. 基本场景：最优统一方案为 age=2, city=1
{
  const spec = {
    columns: [ageCol, cityCol],
    rows: [
      ["23", "北京"], ["24", "北京"], ["25", "天津"], ["26", "天津"],
      ["23", "上海"], ["24", "上海"], ["25", "杭州"], ["26", "杭州"],
      ["23", "北京"], ["25", "上海"],
    ],
  };
  const r = solve(spec);
  assert.strictEqual(r.ok, true, "应当有解");
  assert.deepStrictEqual(r.solution.levels, [2, 1], "最优层级应为 [2, 1]");
  const counts = r.solution.groups.map((g) => g.count).sort();
  assert.deepStrictEqual(counts, [5, 5], "分组应为 5+5");
  assert.ok(r.solution.groups.every((g) => g.count >= 3), "每组至少 3 人");
  assert.ok(Math.abs(r.solution.totalLoss - (10 * (2 / 3) + 10 * (1 / 2))) < 1e-9);
  assert.strictEqual(r.solution.rawLoss, 30);
  console.log("用例 1 通过：基本场景，最优方案 [2, 1]");
}

// 2. 空值独立处理：空值不泛化，只与空值成组
{
  const spec = {
    columns: [ageCol, cityCol],
    rows: [
      ["23", "北京"], ["24", "北京"], ["25", "天津"],
      ["26", "天津"], ["23", "上海"], ["24", "上海"],
      ["25", null], ["26", null], ["23", null], ["24", null],
    ],
  };
  const r = solve(spec);
  assert.strictEqual(r.ok, true, "应当有解");
  assert.deepStrictEqual(r.solution.levels, [2, 2], "最优层级应为 [2, 2]");
  const nullGroup = r.solution.groups.find((g) => g.tuple[1] === null);
  assert.ok(nullGroup, "应存在空值分组");
  assert.strictEqual(nullGroup.count, 4, "空值组应为 4 人");
  assert.strictEqual(nullGroup.tuple[0], "20-29", "空值行的 age 仍按层级泛化");
  console.log("用例 2 通过：空值独立成组，不参与泛化合并");
}

// 3. 总人数不足 k：明确无解
{
  const spec = { columns: [ageCol, cityCol], rows: [["23", "北京"], ["24", "北京"]] };
  const r = solve(spec);
  assert.strictEqual(r.ok, false);
  assert.ok(r.reason.includes("总人数 2 小于 k=3"), "应说明人数不足");
  console.log("用例 3 通过：总人数不足，明确无解");
}

// 4. 空值无法合并导致无解：即使全部泛化到顶层仍有小组
{
  const spec = {
    columns: [ageCol, cityCol],
    rows: [
      ["23", "北京"], ["24", "北京"], ["25", "北京"],
      ["26", "天津"], ["23", "天津"], ["25", null],
    ],
  };
  const r = solve(spec);
  assert.strictEqual(r.ok, false);
  assert.ok(r.reason.includes("最高层级"), "应说明顶层仍不可行");
  assert.ok(r.reason.includes("(空值)"), "应指出空值分组");
  console.log("用例 4 通过：空值孤立分组导致无解，并给出原因");
}

// 5. 自定义 k 与默认 k=3
{
  const spec = {
    k: 2,
    columns: [ageCol, cityCol],
    rows: [["23", "北京"], ["24", "北京"], ["25", "天津"], ["26", "天津"]],
  };
  const r = solve(spec);
  assert.strictEqual(r.ok, true);
  assert.deepStrictEqual(r.solution.levels, [1, 0], "k=2 时 age 泛化 1 级即可");
  console.log("用例 5 通过：自定义 k=2");
}

// 6. 层级成环：抛错
{
  const bad = { name: "x", hierarchy: { a: "b", b: "a" } };
  assert.throws(() => solve({ columns: [bad], rows: [["a"], ["a"], ["a"]] }), /环/);
  console.log("用例 6 通过：层级成环被检测");
}

// 7. 格式化输出包含关键信息
{
  const spec = {
    columns: [ageCol, cityCol],
    rows: [
      ["23", "北京"], ["24", "北京"], ["25", "天津"], ["26", "天津"],
      ["23", "上海"], ["24", "上海"], ["25", "杭州"], ["26", "杭州"],
      ["23", "北京"], ["25", "上海"],
    ],
  };
  const text = formatSolution(spec, solve(spec));
  assert.ok(text.includes("k = 3"));
  assert.ok(text.includes("统一列级泛化方案"));
  assert.ok(text.includes("总泛化损失"));
  console.log("用例 7 通过：输出格式完整");
}

console.log("\n全部测试通过。");
