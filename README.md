# cartesian-expand

将引用模板与笛卡尔参数展开成具体记录的 TypeScript 库。零依赖，直接由 Node.js（>= 23.6，原生类型擦除）运行。

## 特性

- **模板展开**：模板是 JSON 兼容树，`{ "$ref": "轴名" }` 替换为当前组合值，`{ "$index": "轴名" }` 替换为值域下标。
- **确定顺序**：对象键无序（按键名字典序规范化），数组有序（值域按数组顺序迭代）；两个轴 2×3 必产 6 条且顺序固定，空轴产 0 条。
- **约束传播**：`forbid`（互斥）与 `require`/`allow`（有限域依赖）在前缀阶段做前向检查，整枝跳过不可行组合，从不展开完整积再过滤。
- **提前估计数量**：`countFeasible` 用同一套剪枝精确计数可行组合，不物化任何记录。
- **流式生成**：`generate` 是惰性生成器，1e10 空间只消费前几条也瞬时返回。
- **声明身份去重**：`identity` 声明记录内的点分路径，按键首次出现去重，去重状态随游标跨页保持。
- **额度与游标**：`collect` 的 `budget` 耗尽时返回可恢复游标（保留未探索分支的里程表位置），中断时 `total` 为 `null`，不伪报总量；穷举完毕时 `total` 精确。
- **游标绑定版本**：游标携带模板 + 参数 + 身份声明的 SHA-256 摘要（规范化序列化，键序无关），参数改变后续旧游标会报 `$.options.cursor` 错误。
- **排名定位**：`atRank` 按 0 基排名取记录，`rankOf` 反向求记录排名。
- **路径定位错误**：所有校验/运行错误抛出 `ExpandError`，`path` 定位到具体 JSON 路径（如 `$.params.axes.color`、`$.template.items[0].$ref`）。

## API

```ts
import { generate, collect, countFeasible, atRank, rankOf } from "./src/index.ts";

const template = { pair: [{ $ref: "left" }, { $ref: "right" }] };
const params = {
  axes: { left: [1, 2], right: ["x", "y", "z"] },
  constraints: [
    { forbid: { left: 1, right: "y" } },                        // 互斥
    { require: { left: 2 }, allow: { right: ["x", "z"] } },     // 有限域依赖
  ],
};

countFeasible(params);                    // 提前估计数量（精确）
[...generate(template, params)];          // 流式生成
collect(template, params, { budget: 2 }); // -> { records, done, cursor?, emitted, total }
atRank(template, params, 0);              // 按排名定位
rankOf(template, params, { pair: [1, "x"] }); // 反向排名
```

去重：`collect(template, params, { identity: ["user.id"], budget: 100 })`，重复值按声明身份去重，保留首次出现。

## 运行

```sh
npm test     # Node.js 内置测试运行器（node --test）
npm run sample
```

## 测试结果

```
ℹ tests 15
ℹ pass 15
ℹ fail 0
```

覆盖：2×3 顺序、空轴、键序无关、forbid/require 约束传播、整枝剪除、身份去重、
惰性流式（1e10 空间）、额度分页与游标续接、游标摘要绑定、排名/反向排名、
错误路径定位，以及两轮小域穷举核验（300 例随机小域上流式结果与暴力完整积过滤
完全一致；100 例随机分页拼接与一次性展开一致）。

## 设计要点

- **里程表枚举器**：组合空间用里程表位置（每轴一个下标）表示，约束传播在最小不可行前缀处进位，整枝跳过子树；游标即里程表快照 + 已见身份键 + 已产出计数。
- **窥视不提交**：`collect` 填满额度后窥视下一条以判断是否穷举；快照只含已提交状态，未提交的窥视记录在续接时重新计算，不会丢失。
- **规范化摘要**：对象键排序后的规范化 JSON 做 SHA-256，语义相同（键序不同）的输入摘要一致，参数实质改变则摘要失效。
