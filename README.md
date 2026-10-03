# rule-sandbox

多租户规则沙箱与执行限额守护器：对租户提交的自定义布尔/算术表达式进行
AST 白名单校验后在解释器中执行，全程受指令步数、内存申请量与墙钟超时三重
限额守护，超限立即中断，宿主进程不受影响。

## 运行要求

- Node.js ≥ 23.6（原生 TypeScript 类型擦除，零依赖、零构建）
- Node 22.6+ 亦可：`node --experimental-strip-types ...`

## 快速开始

```bash
npm test    # node --test test/sandbox.test.ts
npm run demo
```

## 使用示例

```ts
import { evaluate, BudgetExceeded } from './src/sandbox.ts';

// 正常租户规则
evaluate('age >= 18 && (country == "CN" || vip)', {
  variables: { age: 20, country: 'CN', vip: false },
}); // => { value: true, stepsUsed, memoryUsed }

// 恶意无限递归：在 1000 步预算处强制中断
evaluate('eval(self)'); // throws BudgetExceeded (stepsUsed 1001 > maxSteps 1000)

// 按租户定制限额
evaluate(expr, { limits: { maxSteps: 500, maxMemory: 10_000, timeoutMs: 50 } });
```

## 表达式语言

- 字面量：数字、字符串、`true` / `false` / `null`
- 运算符：`+ - * / %`、`== != < <= > >=`、`&& || !`、括号
- 白名单内建函数：`min max abs floor ceil round len contains startsWith
  endsWith lower upper`，以及元循环 `eval`（对字符串或 `self` 引用的
  表达式在同一守护器预算内重新求值）
- `self`：自动绑定为当前表达式的引用值（QuotedExpr）
- 语言中**不存在**成员访问、赋值、循环等语法，宿主对象在词法层即不可达

## 安全架构

| 层 | 模块 | 职责 |
| --- | --- | --- |
| 词法/语法 | `src/lexer.ts` `src/parser.ts` | 仅识别表达式文法；嵌套深度 ≤ 200，调用参数 ≤ 32 |
| 静态校验 | `src/validator.ts` | AST 白名单：标识符/函数名鉴权，深度 ≤ 256，节点 ≤ 500，字面量长度 ≤ 1024 |
| 执行守护 | `src/guard.ts` | `ResourceGuard`：步数预算（默认 1000）、内存配额（默认 100k 单位）、超时（默认 100ms）、eval 递归深度 ≤ 4096 |
| 解释执行 | `src/interpreter.ts` | 树遍历解释器，每个 AST 节点计费 1 步 + 1 内存单位，字符串按长度计费 |

关键性质：

- **预算不可放大**：`eval` 递归重入共享同一个 `ResourceGuard`，
  动态生成的表达式也会被重新解析与校验。
- **租户隔离**：每次 `evaluate()` 使用独立的守护器、变量环境与策略，
  无任何跨调用共享状态。
- **宿主安全**：错误（`BudgetExceeded` / `MemoryLimitExceeded` /
  `TimeoutExceeded` / `ValidationError` / `ParseError`）均为普通异常，
  中断后宿主进程可继续正常求值。

## 验收用例

`test/sandbox.test.ts` 中
`ACCEPTANCE: eval(self) is force-stopped at the 1000-step budget`：
执行 `eval(self)`，断言抛出 `BudgetExceeded`（`stepsUsed > 1000`，
`maxSteps === 1000`），随后在同一进程内再次求值 `1 + 1` 成功，
证明宿主无死锁、无内存溢出。
