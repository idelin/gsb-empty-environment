# rule-sandbox

多租户规则沙箱与执行限额守护器（TypeScript，零依赖，Node.js 内置测试运行器验证）。

对用户提交的自定义布尔/算术表达式进行 **AST 校验 + 限额执行**：严禁调用宿主环境未授权函数，
执行过程限制最大指令步数、内存申请量与墙钟时间，超限立即中断。

## 架构

| 模块 | 职责 |
| --- | --- |
| `src/lexer.ts` | 词法分析。`.`、`[`、`=`、`;` 等字符直接无法通过分词，成员访问/赋值/索引在语法层面即不存在 |
| `src/parser.ts` | 递归下降解析为 AST；限制嵌套深度（默认 200）与节点数（默认 5000），抵御 `((((...))))` 炸弹 |
| `src/validator.ts` | 求值前静态遍历 AST：标识符与函数名必须在白名单内，否则抛 `UnknownIdentifierError` / `UnauthorizedFunctionError` |
| `src/evaluator.ts` | 树遍历解释器。每个 AST 节点计 1 步（共享账本），字符串产物计入内存账本，每 64 步检查一次墙钟截止时间 |
| `src/sandbox.ts` | `RuleSandbox` 门面：默认 1000 步 / 1MB / 100ms |
| `src/quota.ts` | `QuotaGuard`：按租户隔离的并发数、窗口内求值次数、窗口内累计步数三重限额；失败的求值同样计费 |
| `src/errors.ts` | `BudgetExceeded`、`MemoryExceeded`、`EvaluationTimeoutError`、`QuotaExceededError` 等错误类型 |

### 安全模型

- 表达式由专用文法解析，**从不经过宿主 `eval`/`Function`**；语法中只有裸标识符函数调用，
  `process.exit(1)`、`globalThis.constructor` 之类在分词阶段即被拒绝。
- 内置函数（`abs/min/max/floor/ceil/round/len/concat/repeat/eval`）均为纯沙箱函数，不触碰宿主环境。
- 沙箱内 `eval(src)` 会把 `src` 在同一预算上下文中重新解析、校验、求值——它不是宿主 `eval`，无法逃逸。
- 特殊变量 `self` 绑定当前表达式源码字符串。

## 验收边界

恶意无限递归公式 `eval(self)`：`self` 即表达式自身，沙箱内 `eval` 反复重入同一预算账本，
在第 1000 步指令预算处被强制中断并抛出 `BudgetExceeded`（实测约 8ms，宿主进程无死锁、无内存溢出）：

```
[mallory] eval(self)
  !! BudgetExceeded: execution aborted after exceeding the 1000-step instruction budget  (7.8ms)
Host process is alive and responsive after all attacks.
```

对应测试：`test/sandbox.test.ts` 中的
`ACCEPTANCE: eval(self) is force-aborted at the 1000-step budget`，
断言错误类型为 `BudgetExceeded`、限额 1000、中止时步数恰为 1001、耗时 < 2s、宿主堆内存正常；
`host remains fully functional after the malicious evaluation` 连续攻击 5 次后沙箱仍可正常求值。

## 运行

需要 Node.js ≥ 22.6（使用内置 type-stripping 直接运行 TypeScript，无需编译）：

```bash
npm test    # node --experimental-strip-types --test test/*.test.ts
npm run demo
```

## 使用示例

```ts
import { RuleSandbox, QuotaGuard } from './src/index.ts';

const sandbox = new RuleSandbox({ maxSteps: 1000, maxMemoryBytes: 1_000_000, timeoutMs: 100 });
const guard = new QuotaGuard({ maxConcurrent: 4, maxEvaluationsPerWindow: 100, maxStepsPerWindow: 100_000 });

guard.execute('tenant-acme', sandbox, 'balance > limit && tier == "gold"', {
  balance: 120, limit: 100, tier: 'gold',
}); // => true

sandbox.evaluate('eval(self)'); // => 抛出 BudgetExceeded（第 1000 步强制中断）
```

## 测试结果

```
1..19
# tests 19
# pass 19
# fail 0
# duration_ms ~2000
```

覆盖：算术/布尔求值与优先级、短路求值、租户变量、字符串内建函数、未授权函数拒绝
（`exit`/`require`/`setTimeout`/`process`）、成员访问与赋值语法拒绝、未知标识符、
万层括号嵌套炸弹、`eval(self)` → `BudgetExceeded`（验收用例）、内存超限 → `MemoryExceeded`、
超时 → `EvaluationTimeoutError`、自定义白名单函数、步数预算精确性，以及 `QuotaGuard` 的
并发限额、租户隔离、窗口求值次数限额、失败求值计入窗口步数配额、release 幂等。
