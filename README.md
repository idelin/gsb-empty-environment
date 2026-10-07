# ts-interpreter-debugger

一个小型 TypeScript 子集解释器，附带支持单步（step）、继续（continue）和条件断点的调试器。

## 运行环境

需要 Node.js >= 22.6（利用原生 TypeScript 类型擦除运行 `.ts`，无需编译）。

```sh
npm test    # node --test，运行全部测试
npm run demo
```

## 语言子集

- 变量声明 `let` / `const`（类型标注会被解析并忽略）
- 算术 / 比较 / 逻辑运算符、一元 `!` `-`、赋值表达式
- `if` / `else`、`while`、经典 `for`、块作用域
- 函数声明、参数、`return`、递归（闭包捕获声明环境）
- 内建 `print(...)`，输出收集到 `output` 数组

## 调试器语义

- **断点在节点执行前触发**：解释器以生成器实现，在每个 AST 节点求值/执行前
  产生 `before` 事件；暂停时该节点尚未执行（暂停处可读到执行前的变量值）。
- **继续一次不会立刻重复命中同一节点**：每个节点执行实例带唯一序号
  （`seq`），`continue()` 从暂停处恢复后跳过当前实例，下一次命中必须是新的
  执行实例（例如循环的下一轮迭代）。
- **条件求值无副作用**：条件表达式求值前快照所有可达作用域与输出缓冲，
  求值后恢复；条件中的赋值、函数调用、`print` 均不会影响真实执行状态。
- **条件错误单列**：条件解析或求值出错时不中断程序、不触发暂停，错误记录到
  `debugger.conditionErrors`（含 `nodeId`、`line`、`condition`、`message`、`seq`）。

## API

```ts
import { Debugger } from './src/debugger.ts';

const dbg = new Debugger(source);
dbg.setBreakpoint(nodeId, condition?);        // 按 AST 节点 id
dbg.setBreakpointAtLine(line, condition?);    // 按行号（该行第一条语句）
dbg.clearBreakpoint(nodeId);

dbg.step();       // 推进一个节点并暂停（忽略断点）
dbg.continue();   // 运行到下一个命中的断点或程序结束
dbg.getVariable(name); dbg.setVariable(name, value); dbg.variables();
dbg.output; dbg.trace; dbg.conditionErrors; dbg.done;
```

`step()` / `continue()` 返回 `PauseInfo`：`{ state, reason, nodeId, kind, line }`。

## 项目结构

- `src/lexer.ts` — 词法分析
- `src/parser.ts` — 递归下降语法分析（每个节点分配唯一 id 与行号）
- `src/interpreter.ts` — 生成器式求值器（`run()` 为无调试的完整运行入口）
- `src/debugger.ts` — 调试器（步进 / 继续 / 条件断点 / 变量读写）
- `test/debugger.test.ts` — 用完整运行结果与步进轨迹核对循环、递归及暂停后变量修改
- `demo/main.ts` — 演示脚本
