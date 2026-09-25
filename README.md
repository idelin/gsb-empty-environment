# 声明式 JSON 树规范化（TypeScript / Node 内置测试）

在 **空仓库** 中用纯 TypeScript（Node v23 原生类型擦除，零第三方依赖）实现的声明式数据规范化系统。
输入为 JSON 兼容树：**对象键无序、数组有序**；字段用 RFC 6901 风格 JSON Pointer 定位，
单段 `*` 为通配（如 `/people/*/name`）。

## 运行

```bash
node --test 'tests/**/*.test.ts'   # 内置测试（node:test + node:assert/strict）
node examples/sample.ts            # 端到端运行样例
npm test                           # 同上
npm run sample
```

需要 Node ≥ 22.6（类型擦除）；本仓库在 Node v23.9.0 上验证。

## 三类声明规则（规则顺序 = 数组顺序，显式）

| 类型 | 字段 | 语义 |
| --- | --- | --- |
| `whitespace` | `mode: trim \| collapse` | 只作用于字符串；`trim` 去首尾空白，`collapse` 再折叠内部连续空白。**null 永不参与，字符串空白与 null 从不等同** |
| `date` | `formats[]`, `outputFormat` | 按声明顺序严格匹配 `YYYY MM DD HH mm ss` 格式；全部不匹配报 `DATE_UNPARSEABLE`（**不猜测年月日顺序**）；多格式解析出不同日期报 `DATE_AMBIGUOUS`；通过后统一按 `outputFormat` 输出 |
| `enum` | `map`, `onUnknown` | 别名原样精确命中即替换；未知值默认 `ENUM_UNKNOWN` 报错保留，`onUnknown:"keep"` 时原样通过 |

任一字段上规则失败：**回退并保留原始值**、记录可定位错误（路径 + 规则 id + 错误码），
后续规则不再作用于该字段。

## 目录结构

```
src/
  types.ts       全部公共类型（规则、记录、错误、逆补丁、分析与幂等报告）
  pointer.ts     JSON Pointer 解析/转义、* 模式、相交判断、有序叶子遍历
  rules.ts       三类规则的执行器 + 各自声明的有限输入域
  dependency.ts  静态分析：读写路径、别名冲突、触发边、环、顺序违例；幂等反例搜索
  normalize.ts   规范化引擎：逐字段管道、变更记录、身份、逆补丁、两遍稳定性
  registry.ts    版本化发布门禁（非法发布不影响激活版）与增量重算会话
  index.ts       统一导出
tests/           50 个 node:test 用例
examples/sample.ts
docs/            样例输出与测试结果存档
```

## 关键设计

- **规则顺序显式**：管道顺序即 `rules` 数组下标，`RuleSetAnalysis.order` 给出 id 序列。
- **读写路径依赖**：每条规则原地读/写其声明模式；`patternsOverlap` 判断两规则是否可能作用于同一字段。
- **触发边 A→B**：在声明有限域上，若 A 的某个成功输出会让 B 再次生效（改变值），
  则存在值级触发；配合路径相交构成真正的读写依赖。
- **提前发现的非法组合**（发布/规范化前静态拦截）：
  - 别名冲突：相交字段上同一别名映射到不同规范值；
  - 相互触发环：触发图上的有向环（含自环）；
  - 顺序违例：触发边的生产者排在消费者之后（单遍管道会漏改）；
  - 重复 id、非法 pointer/规则形状。
- **幂等反例搜索**：按“代表字段”把规则分组，在组内有限域（各规则声明域 ∪ 输出像集）上
  逐值模拟完整有序管道，再跑第二遍比较：
  - `refuted`：给出反例 `input -> once -> twice`；
  - `proved`：有限域在预算内穷尽且两遍一致（证明范围仅限该声明域）；
  - `undecided`：预算耗尽未穷尽，**绝不标 proved**。
- **字段身份与逆补丁**：每字段记录规范化前后 `FieldIdentity`（路径、值、kind、指纹）；
  逆补丁为 RFC 6902 风格的 `replace` 列表，应用到输出可还原输入。
- **版本化发布**：只有“静态分析通过 **且** 幂等 `proved`”才提升为激活版本；
  非法发布被拒绝并留存，上一激活版本及其结果不受影响；支持回滚。
- **局部更新增量重算**：`NormalizationSession.updateTo(v)` 只对“命中规则签名变化”的字段
  从原始值重算，其余字段复用上一版结果，并分别返回 `recomputedPaths` / `unchangedPaths`。

## 定位能力

- 错误：`FieldError { path, ruleId, ruleType, code, message, retained }`，可定位到 JSON Pointer 与规则；
- 变更：`FieldRecord.changes` 每步含 `ruleId / from / to`；
- 数组按数字下标、对象键遍历排序以保证输出稳定（不改变“键无序”语义）。
