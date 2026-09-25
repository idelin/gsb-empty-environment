# policy-diff：规则集语义对比工具

比较新旧两套规则集，**按语义（而非文本 diff）**求出判定发生改变的输入区域，给出代表样例，并区分「结果改变」与「仅解释改变」。

## 模型约束

- 策略只读取输入中显式给出的属性，不接任何外部身份服务。
- 属性域仅支持两类有限集合：
  - `enum`：显式枚举值集合；
  - `int`：整数闭区间 `[min, max]`（必须显式限定，否则拒绝分析——无限域无法穷举，分析范围会在报告中明确列出）。
- 条件仅由整数比较（`eq/neq/in/lt/le/gt/ge/between`）与布尔运算（`and/or/not/always`）组成。
- 规则组合是**交换律**的（`deny-overrides` 或 `allow-overrides`）：判定只取决于命中规则的集合，因此重排规则顺序不会改变任何输入的结果，对比时自然判等。

## 判定语义

对每条输入 `(属性赋值)`，分别在新旧策略下求值得到 `(decision, explanation)`：

- `decision`：`allow` / `deny`（按组合策略 + 默认值得出）；
- `explanation`：命中规则 id 集合（排序后）及其 reason 文本。

分类：

| 类别 | 含义 |
| --- | --- |
| `allow→deny` | 判定收紧，**单独列出** |
| `deny→allow` | 判定放宽 |
| `explanation-only` | 判定不变，仅命中规则/理由文本改变 |
| `unchanged` | 判定与解释完全一致 |

## 运行

需要 Node.js >= 22.6（直接运行 TypeScript，无需编译、无第三方依赖）：

```bash
# 对比示例
node src/cli.ts examples/attrs.json examples/old-policy.json examples/new-policy.json

# 测试（Node 内置 test runner）
node --test
```

## 输出说明

报告包含：

1. **有限分析范围**：每个属性的取值域与输入组合总数，域外输入未覆盖；
2. **统计**：四类输入的数量；
3. **改变区域**：按类别分组，给出按属性投影的覆盖摘要（可能为超集，精确内容以样例与计数为准）和代表样例（含新旧判定与命中理由）。

## 文件结构

- `src/model.ts` — 属性域、条件 AST、规则与策略类型
- `src/evaluate.ts` — 条件求值与策略求值（交换律组合）
- `src/compare.ts` — 有限域穷举、分类、区域聚合、属性域校验
- `src/cli.ts` — 命令行报告
- `examples/` — 示例属性定义与新旧策略
- `test/compare.test.ts` — Node 内置测试
