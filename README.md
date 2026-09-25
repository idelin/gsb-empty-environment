# 规则集语义比对工具（ruleset-diff）

比较**旧 / 新两个规则集**，找出**判定（allow/deny）发生改变的输入区域**，为每个区域给出**代表样例**，并把「**结果改变**」与「**仅解释改变**」区分开。

这不是文本 diff：工具会在声明的**有限属性域**上枚举每个具体输入点，分别用旧、新规则集求值，再按「判定 + 命中规则」做语义分区，因此规则顺序、AND/OR 操作数交换、规则改名、规则拆分都不会产生误报。

## 运行

要求 Node.js ≥ 22.6（使用原生 TypeScript type-stripping，无第三方依赖）。

```bash
npm test          # Node.js 内置测试运行器（node:test）
npm run demo      # 对 src/policies.ts 中的旧/新规则集跑一次完整比对
```

最近一次测试结果：31 个测试全部通过；demo 输出保存在 `examples/sample-output.txt`。

## 规则语言（受约束的策略模型）

求值器（`src/eval.ts`）严格遵守以下边界：

- **只读显式属性**：只读取输入对象上已声明的属性，缺失属性按不命中处理；不读取环境、时间、网络，也**不接外部身份服务**。有测试用 `Proxy` 断言求值过程不会访问条件中未点名的属性。
- **有限集合条件**：`in(attr, [字面量...])`。
- **整数条件**：`age >= 2` 等六种比较，仅对整数生效（`"2"`、`2.5` 不命中）。
- **布尔运算**：`and / or / not` 任意组合。
- **组合语义**：默认 **deny-overrides**——收集所有命中规则，任一 deny 命中即为 deny，否则任一 allow 命中为 allow，否则取默认效果。该语义与规则顺序无关。

一条规则形如：

```ts
{ id: "r_prod_no_mfa_deny", effect: "deny",
  when: and(inSet("env", ["prod"]), inSet("mfa", [false])) }
```

## 有限分析范围（重要）

整数轴天然是无限域，本工具**不声称覆盖无限域**。分析范围由 `DomainSpec` 显式声明（`src/policies.ts`），枚举其笛卡尔积：

```
role ∈ {guest, viewer, editor, admin}
env  ∈ {prod, staging, dev}
resource ∈ {dashboard, report, logs, admin_panel}
mfa  ∈ {true, false}
age  ∈ [0, 2]（整数）
risk ∈ [0, 1]（整数）
=> 共 4·3·4·2·3·2 = 576 个输入点
```

报告抬头会打印该范围。整数边界（如 `age >= 3` 在 `age ∈ [0,2]` 内恒不命中）只在声明范围内成立；若要分析更大取值，扩大对应 `intRange` 后重跑即可。

## 输出分类

对输入空间做语义分区，每个区域给出：类别、点数、代表样例、旧/新判定依据（命中的规则 id 列表，或默认效果），以及区域在各属性上的投影（仅列出受限属性）。

- **decision-changed / `allow -> deny`**：旧放行、新拒绝，单独成节列出。
- **decision-changed / `deny -> allow`**：旧拒绝、新放行，单独成节列出。
- **explanation-only**：allow/deny 结果不变，但命中的规则 id 变化——典型来源是规则改名、一条规则拆成两条不交规则、新增一条与默认拒绝效果相同的显式 deny 规则、以及年龄阈值放松后「显式 deny 变成默认 deny」。
- **unchanged**：结果与解释都不变（报告中仅汇总数量）。

「重排 / 交换律」场景由语义求值直接吸收：把新规则集整体逆序后再比较，摘要与区域数完全一致（有对应测试）。

## 代码结构

| 文件 | 作用 |
| --- | --- |
| `src/types.ts` | 条件、规则、规则集、判定、有限域的类型定义 |
| `src/conditions.ts` | 条件构造助手 |
| `src/eval.ts` | 条件求值 + 规则集 deny-overrides 求值（只读显式属性） |
| `src/domain.ts` | 有限域枚举、区域投影、范围描述 |
| `src/compare.ts` | 语义分区、分类统计、`regionForInput` 定位 |
| `src/report.ts` | 文本报告渲染 |
| `src/policies.ts` | 示例旧 / 新规则集与分析域 |
| `src/demo.ts` | `npm run demo` 入口 |
| `test/*.test.ts` | `node:test` 测试（求值、顺序无关、交换律、三类变更、示例策略） |
