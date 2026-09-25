import {
  generate,
  collect,
  countFeasible,
  atRank,
  rankOf,
  ExpandError,
} from "../src/index.ts";
import type { ParamSpec } from "../src/index.ts";

const line = (label: string, value: unknown) =>
  console.log(`\n== ${label} ==\n` + JSON.stringify(value, null, 2));

// 1. 两个轴 2x3，模板中带 $ref / $index 引用节点
const template = {
  pair: [{ $ref: "left" }, { $ref: "right" }],
  leftIndex: { $index: "left" },
};
const params: ParamSpec = {
  axes: {
    left: [1, 2],
    right: ["x", "y", "z"],
  },
};
line("2x3 -> 6 条，顺序确定", [...generate(template, params)]);

// 2. 有限域依赖 + 互斥条件，提前估计数量
const shopParams: ParamSpec = {
  axes: {
    region: ["cn", "us"],
    currency: ["cny", "usd", "eur"],
    tier: ["basic", "pro"],
  },
  constraints: [
    { require: { region: "cn" }, allow: { currency: ["cny"] } }, // 依赖
    { forbid: { region: "us", tier: "pro" } }, // 互斥
  ],
};
const shopTemplate = {
  region: { $ref: "region" },
  currency: { $ref: "currency" },
  tier: { $ref: "tier" },
};
line("提前估计数量（精确可行组合数）", countFeasible(shopParams));
line("约束传播后的记录", [...generate(shopTemplate, shopParams)]);

// 3. 额度分页 + 可恢复游标（中断不伪报总量）
const page1 = collect(shopTemplate, shopParams, { budget: 2 });
line("第 1 页（budget=2，total 为 null）", page1);
const page2 = collect(shopTemplate, shopParams, { budget: 2, cursor: page1.cursor });
line("第 2 页（续游标）", page2);
const page3 = collect(shopTemplate, shopParams, { budget: 2, cursor: page2.cursor });
line("第 3 页（穷举完毕，total 精确）", page3);

// 4. 游标绑定输入摘要：参数改变不能续旧展开
try {
  collect(shopTemplate, { ...shopParams, axes: { ...shopParams.axes, tier: ["basic"] } }, {
    budget: 1,
    cursor: page1.cursor,
  });
} catch (err) {
  line("参数改变后续旧游标 -> 报错并定位路径", {
    name: (err as ExpandError).name,
    path: (err as ExpandError).path,
    message: (err as ExpandError).message,
  });
}

// 5. 声明身份去重
line(
  "按 identity=[region] 去重",
  [...generate(shopTemplate, shopParams, { identity: ["region"] })]
);

// 6. 按排名定位与反向排名
const third = atRank(shopTemplate, shopParams, 2);
line("atRank(2)", third);
line("rankOf(上一条记录)", rankOf(shopTemplate, shopParams, third!));

// 7. 空轴产 0 条
line("空轴", {
  count: countFeasible({ axes: { a: [1], b: [] } }),
  records: [...generate({}, { axes: { a: [1], b: [] } })],
});
