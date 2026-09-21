# 栈机小语言

把条件、循环、`break` 与短路表达式 (`and` / `or`) 编译为栈机字节码,
静态验证所有控制流路径的栈高度一致, 再由支持单步与指令预算的解释器执行。
仅使用 Python 3.11 标准库; 词法、语法、编译、验证、解释全部自行实现,
不使用 `eval` 或任何现成编译器/解析器。

## 运行

```sh
python3.11 demo.py                    # 端到端样例
python3.11 -m unittest discover -s tests -v
```

## 模块

- `stackvm/lexer.py` — 词法分析, 记号携带 UTF-8 字节区间 `[start, end)`
- `stackvm/parser.py` — 递归下降分析 (or < and < not < 比较 < 加减 < 乘除)
- `stackvm/compiler.py` — AST 编译为带标签的栈机指令, 两遍汇编成字节码
- `stackvm/isa.py` — 指令集、二进制编解码、反汇编 (跳转目标是字节偏移)
- `stackvm/verifier.py` — 跳转目标必须是指令边界; 各路径栈高度必须一致
- `stackvm/vm.py` — `Machine.step()` 单步, `Machine(budget=N)` 指令预算
- `tests/test_stackvm.py` — 25 个 unittest 用例

## 关键设计

- **嵌套 break**: 编译器维护各层循环退出标签栈, `break` 跳到最内层标签。
- **短路求值**: `a and b` 编译为 `DUP; JZ end; POP; <b>`; `or` 类似,
  跳过右支时其指令根本不会执行 (测试通过执行轨迹 `trace` 断言)。
- **栈高度验证**: 对控制流图做工作列表抽象解释, 每个可达指令入口只允许
  唯一栈高度; 同时检查栈下溢、HALT 时栈空、不允许执行流出代码末尾。
- **防损坏跳转**: 字节码顺序解码得到合法指令边界集合, 任何跳转目标若
  不在集合中 (例如多字节指令的操作数字节、越界偏移) 一律拒绝。
- **预算**: 死循环程序在执行满 `budget` 条指令时抛 `BudgetExhausted`。
