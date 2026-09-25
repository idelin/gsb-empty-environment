import { test } from "node:test";
import assert from "node:assert/strict";
import {
  generate,
  collect,
  countFeasible,
  atRank,
  rankOf,
  canonicalize,
  ExpandError,
} from "../src/index.ts";
import type { Constraint, JsonValue, ParamSpec } from "../src/index.ts";

const TEMPLATE_2X3 = {
  pair: [{ $ref: "left" }, { $ref: "right" }],
  leftIndex: { $index: "left" },
};

const PARAMS_2X3: ParamSpec = {
  axes: {
    left: [1, 2],
    right: ["x", "y", "z"],
  },
};

test("两个轴 2x3 产 6 条且顺序确定（轴名字典序、值域数组序）", () => {
  const records = [...generate(TEMPLATE_2X3, PARAMS_2X3)];
  assert.equal(records.length, 6);
  assert.deepEqual(records, [
    { pair: [1, "x"], leftIndex: 0 },
    { pair: [1, "y"], leftIndex: 0 },
    { pair: [1, "z"], leftIndex: 0 },
    { pair: [2, "x"], leftIndex: 1 },
    { pair: [2, "y"], leftIndex: 1 },
    { pair: [2, "z"], leftIndex: 1 },
  ]);
  assert.equal(countFeasible(PARAMS_2X3), 6);
});

test("对象键无序：键序不同语义相同，产出与摘要一致", () => {
  const shuffled: ParamSpec = {
    constraints: [],
    axes: {
      right: ["x", "y", "z"],
      left: [1, 2],
    },
  };
  const a = [...generate(TEMPLATE_2X3, PARAMS_2X3)];
  const b = [...generate({ leftIndex: { $index: "left" }, pair: [{ $ref: "left" }, { $ref: "right" }] }, shuffled)];
  assert.deepEqual(a, b);
});

test("空轴产 0 条", () => {
  const params: ParamSpec = { axes: { left: [1, 2], right: [] } };
  assert.equal([...generate(TEMPLATE_2X3, params)].length, 0);
  assert.equal(countFeasible(params), 0);
  const result = collect(TEMPLATE_2X3, params, { budget: 5 });
  assert.equal(result.done, true);
  assert.equal(result.total, 0);
});

test("互斥条件 forbid：跳过指定组合", () => {
  const params: ParamSpec = {
    axes: { a: [1, 2], b: ["x", "y"] },
    constraints: [{ forbid: { a: 1, b: "y" } }],
  };
  const template = {
    a: { $ref: "a" },
    b: { $ref: "b" },
    ai: { $index: "a" },
    bi: { $index: "b" },
  };
  const records = [...generate(template, params)];
  assert.deepEqual(records, [
    { a: 1, b: "x", ai: 0, bi: 0 },
    { a: 2, b: "x", ai: 1, bi: 0 },
    { a: 2, b: "y", ai: 1, bi: 1 },
  ]);
  assert.equal(countFeasible(params), 3);
});

test("有限域依赖 require/allow：when 匹配时限制目标轴取值", () => {
  const params: ParamSpec = {
    axes: { region: ["cn", "us"], currency: ["cny", "usd", "eur"] },
    constraints: [
      { require: { region: "cn" }, allow: { currency: ["cny"] } },
      { require: { region: "us" }, allow: { currency: ["usd", "eur"] } },
    ],
  };
  const template = { region: { $ref: "region" }, currency: { $ref: "currency" } };
  const records = [...generate(template, params)];
  assert.deepEqual(records, [
    { region: "cn", currency: "cny" },
    { region: "us", currency: "usd" },
    { region: "us", currency: "eur" },
  ]);
  assert.equal(countFeasible(params), 3);
});

test("约束传播：allow 与值域交集为空时整枝剪除（不逐叶过滤）", () => {
  // cn 下 currency 只允许 ["jpy"]，与值域交集为空 -> 整个 cn 分支被剪掉。
  const params: ParamSpec = {
    axes: { region: ["cn", "us"], currency: ["cny", "usd"] },
    constraints: [{ require: { region: "cn" }, allow: { currency: ["jpy"] } }],
  };
  assert.equal(countFeasible(params), 2);
  const records = [...generate({ r: { $ref: "region" }, c: { $ref: "currency" } }, params)];
  assert.deepEqual(records, [
    { r: "us", c: "cny" },
    { r: "us", c: "usd" },
  ]);
});

test("声明身份去重：保留首次出现，不展开完整积", () => {
  const params: ParamSpec = {
    axes: { id: ["u1", "u2"], tag: ["a", "b", "c"] },
  };
  const template = { id: { $ref: "id" }, tag: { $ref: "tag" } };
  const records = [...generate(template, params, { identity: ["id"] })];
  assert.deepEqual(records, [
    { id: "u1", tag: "a" },
    { id: "u2", tag: "a" },
  ]);
});

test("流式生成是惰性的：1e10 空间只消费前 3 条", () => {
  const axes: Record<string, JsonValue[]> = {};
  for (let i = 0; i < 10; i++) axes[`axis${i}`] = [0, 1, 2, 3, 4, 5, 6, 7, 8, 9];
  const template: Record<string, JsonValue> = {};
  for (let i = 0; i < 10; i++) template[`axis${i}`] = { $ref: `axis${i}` };
  const it = generate(template, { axes });
  const first3 = [it.next().value, it.next().value, it.next().value];
  assert.equal(first3.length, 3);
  assert.deepEqual(first3[0], Object.fromEntries(Object.keys(axes).map((k) => [k, 0])));
});

test("额度不足返回可恢复游标；中断不伪报总量；续接后总量精确", () => {
  const template = { a: { $ref: "a" }, b: { $ref: "b" } };
  const params: ParamSpec = { axes: { a: [1, 2], b: ["x", "y", "z"] } };

  const page1 = collect(template, params, { budget: 2 });
  assert.equal(page1.records.length, 2);
  assert.equal(page1.done, false);
  assert.equal(page1.total, null); // 预算中断：不伪报总量
  assert.equal(typeof page1.cursor, "string");

  const page2 = collect(template, params, { budget: 2, cursor: page1.cursor });
  assert.equal(page2.done, false);
  assert.equal(page2.total, null);

  const page3 = collect(template, params, { budget: 2, cursor: page2.cursor });
  assert.equal(page3.done, true);
  assert.equal(page3.total, 6);
  assert.equal(page3.emitted, 6);
  assert.equal(page3.cursor, undefined);

  const all = [...page1.records, ...page2.records, ...page3.records];
  assert.deepEqual(all, [...generate(template, params)]);
});

test("游标跨页保持去重状态", () => {
  const template = { id: { $ref: "id" }, tag: { $ref: "tag" } };
  const params: ParamSpec = { axes: { id: ["u1", "u2"], tag: ["a", "b"] } };
  const page1 = collect(template, params, { identity: ["id"], budget: 1 });
  assert.deepEqual(page1.records, [{ id: "u1", tag: "a" }]);
  const page2 = collect(template, params, { identity: ["id"], budget: 10, cursor: page1.cursor });
  assert.deepEqual(page2.records, [{ id: "u2", tag: "a" }]);
  assert.equal(page2.total, 2);
});

test("游标绑定输入摘要：参数改变不能续旧展开", () => {
  const template = { a: { $ref: "a" } };
  const paramsV1: ParamSpec = { axes: { a: [1, 2, 3] } };
  const paramsV2: ParamSpec = { axes: { a: [1, 2, 4] } };
  const page1 = collect(template, paramsV1, { budget: 1 });
  assert.throws(
    () => collect(template, paramsV2, { budget: 1, cursor: page1.cursor }),
    (err: unknown) => {
      assert.ok(err instanceof ExpandError);
      assert.equal((err as ExpandError).path, "$.options.cursor");
      return true;
    }
  );
  // 键序不同但语义相同 -> 摘要一致，可以续接。
  const sameSemantics: ParamSpec = { axes: { a: [1, 2, 3] }, constraints: [] };
  const resumed = collect(template, sameSemantics, { budget: 5, cursor: page1.cursor });
  assert.deepEqual(resumed.records, [{ a: 2 }, { a: 3 }]);
});

test("按排名定位与反向排名", () => {
  const template = { pair: [{ $ref: "left" }, { $ref: "right" }] };
  for (let rank = 0; rank < 6; rank++) {
    const record = atRank(template, PARAMS_2X3, rank);
    assert.ok(record !== null);
    assert.equal(rankOf(template, PARAMS_2X3, record!), rank);
  }
  assert.equal(atRank(template, PARAMS_2X3, 6), null);
  assert.equal(rankOf(template, PARAMS_2X3, { pair: [9, "x"] }), -1);
  assert.throws(() => atRank(template, PARAMS_2X3, -1), (err: unknown) => {
    assert.equal((err as ExpandError).path, "$.rank");
    return true;
  });
});

test("错误定位到具体路径", () => {
  const cases: Array<[() => unknown, string]> = [
    [() => [...generate({ x: { $ref: "nope" } }, PARAMS_2X3)], "$.template.x.$ref"],
    [() => countFeasible({ axes: { a: "not-array" as unknown as JsonValue[] } }), "$.params.axes.a"],
    [
      () => countFeasible({ axes: { a: [1] }, constraints: [{ forbid: { ghost: 1 } }] }),
      "$.params.constraints[0].forbid.ghost",
    ],
    [
      () => countFeasible({ axes: { a: [1] }, constraints: [{ require: { a: 1 }, allow: {} }] }),
      "$.params.constraints[0].allow",
    ],
    [() => collect({}, PARAMS_2X3, { cursor: "!!!not-base64!!!" }), "$.options.cursor"],
    [() => collect({}, PARAMS_2X3, { identity: [""] }), "$.options.identity[0]"],
    [() => collect({}, PARAMS_2X3, { budget: -1 }), "$.options.budget"],
  ];
  for (const [fn, expectedPath] of cases) {
    assert.throws(fn, (err: unknown) => {
      assert.ok(err instanceof ExpandError, `expected ExpandError for path ${expectedPath}`);
      assert.equal((err as ExpandError).path, expectedPath);
      return true;
    });
  }
});

// ---------- 小域穷举核验完整性 ----------

function mulberry32(seed: number): () => number {
  let state = seed >>> 0;
  return () => {
    state = (state + 0x6d2b79f5) >>> 0;
    let t = state;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function bruteForceRecords(params: ParamSpec): JsonValue[] {
  const axisNames = Object.keys(params.axes).sort();
  const domains = axisNames.map((n) => params.axes[n]!);
  const out: JsonValue[] = [];
  const combo: JsonValue[] = new Array(axisNames.length);
  const visit = (depth: number): void => {
    if (depth === axisNames.length) {
      const assignment: Record<string, JsonValue> = {};
      for (let i = 0; i < axisNames.length; i++) assignment[axisNames[i]!] = combo[i]!;
      const feasible = (params.constraints ?? []).every((c: Constraint) => {
        if ("forbid" in c) {
          return !Object.entries(c.forbid).every(([k, v]) =>
            canonicalize(assignment[k]!) === canonicalize(v)
          );
        }
        const whenMatches = Object.entries(c.require).every(
          ([k, v]) => canonicalize(assignment[k]!) === canonicalize(v)
        );
        if (!whenMatches) return true;
        return Object.entries(c.allow).every(([k, allowed]) =>
          allowed.some((v) => canonicalize(v) === canonicalize(assignment[k]!))
        );
      });
      if (feasible) out.push(assignment);
      return;
    }
    for (const value of domains[depth]!) {
      combo[depth] = value;
      visit(depth + 1);
    }
  };
  visit(0);
  return out;
}

test("小域穷举核验：随机小域上流式结果与暴力完整积过滤完全一致", () => {
  const rand = mulberry32(20260925);
  const valuePool: JsonValue[] = [0, 1, 2, "x", "y", null, true];
  for (let trial = 0; trial < 300; trial++) {
    const axisCount = 1 + Math.floor(rand() * 4);
    const axes: Record<string, JsonValue[]> = {};
    const names: string[] = [];
    for (let i = 0; i < axisCount; i++) {
      const name = `ax${i}`;
      names.push(name);
      const size = Math.floor(rand() * 4); // 含空轴
      const domain: JsonValue[] = [];
      for (let j = 0; j < size; j++) domain.push(valuePool[Math.floor(rand() * valuePool.length)]!);
      axes[name] = domain;
    }
    const constraints: Constraint[] = [];
    const constraintCount = Math.floor(rand() * 4);
    for (let i = 0; i < constraintCount; i++) {
      const pick = () => names[Math.floor(rand() * names.length)]!;
      const randValue = () => valuePool[Math.floor(rand() * valuePool.length)]!;
      if (rand() < 0.5) {
        constraints.push({ forbid: { [pick()]: randValue() } });
      } else {
        constraints.push({
          require: { [pick()]: randValue() },
          allow: { [pick()]: [randValue(), randValue()] },
        });
      }
    }
    const params: ParamSpec = { axes, constraints };
    const template: Record<string, JsonValue> = {};
    for (const name of names) template[name] = { $ref: name };

    const streamed = [...generate(template, params)].map(canonicalize);
    const brute = bruteForceRecords(params).map(canonicalize);
    assert.deepEqual(
      streamed,
      brute,
      `trial ${trial} mismatch: ${JSON.stringify(params)}`
    );
    assert.equal(countFeasible(params), brute.length, `trial ${trial} count mismatch`);
  }
});

test("小域穷举核验：游标分页拼接与一次性展开一致", () => {
  const rand = mulberry32(7);
  const valuePool: JsonValue[] = [0, 1, "x", "y"];
  for (let trial = 0; trial < 100; trial++) {
    const axes: Record<string, JsonValue[]> = {};
    const names = ["a", "b", "c"];
    for (const name of names) {
      const size = 1 + Math.floor(rand() * 3);
      const domain: JsonValue[] = [];
      for (let j = 0; j < size; j++) domain.push(valuePool[Math.floor(rand() * valuePool.length)]!);
      axes[name] = domain;
    }
    const params: ParamSpec = { axes };
    const template: Record<string, JsonValue> = {};
    for (const name of names) template[name] = { $ref: name };

    const expected = [...generate(template, params)];
    const budget = 1 + Math.floor(rand() * 3);
    const paged: JsonValue[] = [];
    let cursor: string | undefined;
    while (true) {
      const page = collect(template, params, { budget, cursor });
      paged.push(...page.records);
      if (page.done) {
        assert.equal(page.total, expected.length);
        break;
      }
      assert.equal(page.total, null);
      cursor = page.cursor;
    }
    assert.deepEqual(paged, expected);
  }
});
