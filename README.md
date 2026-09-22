# stacklang：条件 / 循环 / break / 短路表达式 → 栈机

一个从零实现的小编译器与栈式虚拟机（Python 3.11，仅标准库）：

- 自行实现词法分析、递归下降语法分析、AST、字节码汇编与栈机执行；
  **不使用 `eval` / `exec` / 内建 `compile` 或任何现成编译器**。
- 支持 `if/else`、`while`、分层 `break`、`and` / `or` 短路求值。
- 源码位置全部使用 **UTF-8 字节区间** `[start, end)`（词法、语法、运行期错误都带）。
- 字节码在运行前必须通过静态验证器：解码校验、跳转边界校验、
  以及**每条控制流路径的栈高度一致性**校验（含下溢与汇合点不一致）。
- 虚拟机支持 `step()` 单步和 `run(budget=...)` 指令预算，预算耗尽后
  保留现场可继续运行。

## 目录结构

- `stacklang/errors.py`：带字节区间的异常类型。
- `stacklang/lexer.py`：词法分析器，偏移量按 UTF-8 字节计算。
- `stacklang/ast_nodes.py`、`stacklang/parser.py`：AST 与递归下降分析。
- `stacklang/bytecode.py`：操作码定义、汇编器（`Label` 回填）、解码器。
- `stacklang/compiler.py`：AST → 栈机指令；短路用 `DUP` + 条件跳转；
  `break` 用 break 标签栈保证嵌套时跳到正确层。
- `stacklang/validator.py`：静态验证（含 CFG 栈高度数据流分析）。
- `stacklang/vm.py`：栈机，单步 / 预算 / 续跑。
- `examples/*.sl`：示例源码（嵌套 break、短路求值）。
- `run_demo.py`：样例运行脚本。
- `tests/test_stacklang.py`：35 个 `unittest` 测试。

## 语言速览

```
let x = 0;
while (x < 3) {
  if (x == 1) { break; }
  x = x + 1;
}
let a = false and (1 / 0 == 0);   # 右支不执行，不报错
let b = true  or  (1 / 0 == 0);   # 右支不执行，不报错
```

整数除法语义为向零截断（C 风格），`%` 与之配套。注释以 `#` 开头。

## 字节码与控制流

变长编码：无操作数 1 字节；`PUSH_CONST/LOAD/STORE` 为操作码 + uint16；
`JF/JUMPT/JUMP` 为操作码 + uint32 目标字节偏移。跳转目标必须是
**指令起始边界**。短路表达式的两条路径都恰好留下一个值：

```
left
DUP          # 复制左值用于判定
JF done      # and：左值为假就保留左值跳过右支（or 用 JUMPT）
POP          # 丢弃判定副本
right
done:
```

验证器对控制流图做 worklist 数据流分析：每条边携带栈深度，
汇合点深度不一致、栈下溢、终止深度非 0、跳转落入指令中间或越过
代码末尾都会被拒绝。

## 运行样例

```
python3.11 run_demo.py
python3.11 -m unittest discover -s tests -v
```

`run_demo.py` 输出（节选）：

```
VARIABLES: {'sum': 22, 'i': 1, 'j': 2}      # 嵌套 break：i=0 累加 0,1；i=1 累加 10,11 后跳出外层
INSTRUCTIONS EXECUTED: 134

SINGLE STEPPING
  step  1: PUSH_CONST stack=[2] env={}
  step  2: STORE      stack=[] env={'x': 2}
  ...
  step  9: HALT       stack=[] env={'x': 5}

BUDGET (infinite loop, 12 instructions per slice)
  slice 1: paused at steps=12, n=1
  slice 2: paused at steps=24, n=3
  slice 3: paused at steps=36, n=5

TAMPERED JUMP -> VALIDATOR REJECTION
  rejected: jump at byte 13 lands at byte 1, in the middle of an instruction starting at byte 0
  VM also refuses to run it: jump at byte 13 lands at byte 1, in the middle of an instruction starting at byte 0
```

人工损坏的方式：编译正常程序后把某个条件跳转的 uint32 目标改成
3 字节 `PUSH_CONST` 指令内部的字节偏移；解码仍然成功，但验证器
在跳转边界检查阶段拒绝它，VM 构造时同样拒绝。

## 测试结果

```
Ran 35 tests in 0.008s

OK
```

覆盖：运算优先级、向零截断除法、if/else、while、赋值、
单层与嵌套 break 层级、break 越层编译错误、短路右支不执行/该执行时执行、
UTF-8 字节区间（含中文注释）、跳转落入指令中间/越界、栈高度不一致/下溢/
终止残留、未知操作码与截断操作数、操作数索引越界、单步、预算中断与续跑。
