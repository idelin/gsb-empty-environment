# pmatch — 模式匹配编译器与解释器

纯标准库实现（Python 3.11），不使用 `eval` 或任何现成编译器：词法分析、
递归下降语法分析、决策树编译（Maranget 矩阵算法）与树遍历解释器全部手写。
所有源码位置均为 **UTF-8 字节区间**（半开区间 `[start, end)`）。

## 语言

```
union Shape = Circle(r) | Rect(w, h) | Point     # 标签联合（声明全部标签）
match Rect(3, 4):
  | Point -> 0
  | Circle(r) -> 3 * r * r
  | Rect(w, h) if w > 0 -> w * h                  # if 守卫
  | _ -> -1                                       # 通配
```

- 模式：`_` 通配、小写变量绑定、字面量（int / `"str"` / `true` / `false` /
  `none`）、构造器 `Tag(p1, p2)`（大写开头）、元组 `(p1, p2)` / `(p,)`，
  可任意嵌套。
- 表达式：算术 `+ - * / %`（`/` 为整除）、比较、`and` / `or` / `not`、
  构造器调用、元组、内置函数 `len`。

## 静态检查

- **不可达分支**：通配在前时其后的具体分支报 `unreachable` 警告。
- **非穷尽**：报缺失分支并给出反例见证（如 ``Rect(_, _)``）。
  守卫不参与无条件穷尽证明：仅带 `if` 的标签分支不算覆盖该标签。
- **重复绑定**：同一模式（含嵌套）中同名变量绑定报错。
- 未知构造器 / 字段数不符 / 未绑定变量 / 未知函数均报错并带字节区间。

## 运行

```
python3.11 main.py                        # 运行样例
python3.11 -m unittest discover -s tests  # 运行测试
```

## 结构

| 文件 | 职责 |
| --- | --- |
| `pmatch/lexer.py` | 词法分析，token 携带 UTF-8 字节区间 |
| `pmatch/parser.py` | 递归下降语法分析，产生模式/表达式 AST |
| `pmatch/compiler.py` | 校验 + 决策树编译 + 不可达/非穷尽诊断 + 反例生成 |
| `pmatch/interpreter.py` | 表达式求值与决策树执行 |
| `pmatch/api.py` | `compile_source` / `run_source` 入口 |
| `main.py` | 演示样例 |
| `tests/test_pmatch.py` | unittest 测试 |
