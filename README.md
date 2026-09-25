# minilang：带验证的局部变换流水线

纯 Python 3.11 标准库实现（无第三方依赖、无 `eval`/现成编译器）：

- **自研前端**：词法器 + 递归下降语法分析器，所有源码位置为 **UTF-8 字节区间** `[start, end)`（多字节字符注释/标识符均按字节计数）。
- **栈式 IR**：`JMP`/`JZ` 携带绝对指令索引作为分支目标。
- **自研解释器**：逐步执行 IR，带燃料上限；除法/取模向零截断；运行时故障（除零、栈下溢、未定义变量、pc 越界）以 Trap 形式进入结果。
- **变换流水线**：每步记录**前后 IR**、结构验证结果、有限域差分结果；结构验证失败或差分不一致即**回滚该步**，后续安全步骤照常运行。
- **差分解释器**：在**有限测试域**上对比前后 IR 的可观察行为（输出、最终变量、陷阱状态），明确**不声称全域证明**。

## 目录结构

```
minilang/
  lexer.py       词法器（UTF-8 字节区间）
  parser.py      递归下降语法分析
  ast_nodes.py   AST 定义（含字节区间）
  compiler.py    AST -> IR（回填跳转目标）
  ir.py          IR 定义、结构验证、指令删除+目标重映射
  interp.py      手写解释器
  transforms.py  局部变换（含一个故意破坏分支目标的坏变换）
  diffcheck.py   有限测试域差分验证
  pipeline.py    串联流水线：验证 + 回滚
demo.py          端到端演示
tests/           unittest 测试（43 个用例）
```

## 运行

```bash
python3.11 demo.py                 # 运行样例
python3.11 -m unittest discover -s tests   # 运行测试
```

## 运行样例（节选）

```
== 3. 优化流水线（每步：前后 IR + 结构验证 + 有限域差分验证）==
  步骤 0: constant-fold -> 保留
  步骤 1: zero-add-peephole (UNSAFE: no jump fixup) -> 回滚
    结构错误: [24] JZ target 29 out of range [0, 28)
  步骤 2: const-prop -> 保留
  步骤 3: algebraic-peephole -> 保留

== 6. 在样例输入上运行最终 IR（与原始 IR 对照）==
  input n=  5: 原始输出=(15,)  优化后输出=(15,)  一致=True
  input n= 10: 原始输出=(55,)  优化后输出=(55,)  一致=True
```

坏变换删除 `CONST 0, ADD` 时未重映射跳转目标，导致 `JZ 29` 越界，
被结构验证拦截并回滚；其后的 `const-prop`、`algebraic-peephole` 仍正常生效。

## 测试结果

```
$ python3.11 -m unittest discover -s tests
Ran 43 tests in 0.227s
OK
```

覆盖：字节区间（多字节字符）、语法错误区间、解释器语义（循环/分支/截断除法/
各类陷阱/燃料上限）、结构验证（越界跳转、缺 HALT）、各变换正确性、坏变换拦截、
回滚后流水线继续、差分器对陷阱状态的比较、报告不含全域证明声明。

## 验证范围声明

差分验证仅在 有限测试域（演示中为 `n ∈ [-2, 20]`，共 23 个输入）内成立，
**不构成对全部输入空间的语义等价证明**。
