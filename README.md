# unitlang：量纲安全的单位计算语言

一个用 Python 3.11（仅标准库）自实现的小型解释语言：支持数值、单位、变量、
四则运算、单位换算与比较。词法、语法、静态量纲检查与解释执行全部手写，
不使用 `eval` 或任何现成编译器/解析器。

## 设计要点

- **两阶段执行**：`tokenize → parse → check → eval`。整个程序先由
  `unitlang/checker.py` 做量纲与仿射温度规则检查，全部通过后才逐条执行；
  因此 `1 m + 1 s` 这类错误在执行任何语句之前就被拒绝。
- **精确有理数**：所有数值用 `fractions.Fraction` 表示，`0.1 + 0.2` 精确等于
  `0.3`；无限循环小数以分数加近似值形式输出（如 `1/3 (≈ 0.333333)`）。
- **仿射温度与温差分离**：`°C`/`°F`（别名 `degC`/`degF`）是仿射绝对温度，
  `Δ°C`/`Δ°F`/`ΔK`（别名 `ddegC`/`ddegF`/`dK`）是温差，`K` 是线性温标。
  - 绝对温度 ± 温差 → 绝对温度；绝对温度 − 绝对温度 → 温差；温差 ± 温差 → 温差。
  - 摄氏/华氏绝对温度**不能直接乘除**（静态报错，可先 `-> K` 再运算）；温差可以。
  - 绝对温度 ↔ 温差单位的换算被拒绝。
- **源码位置用 UTF-8 字节区间**：词法器直接跟踪字节偏移，所有错误（包括
  多字节单位 `°C`、`Δ°C` 之后的运算符）都以 `[start, end)` 字节区间定位，
  渲染时再映射回行列并画出 `^` 光标。
- **除零定位运算符**：除零是运行时错误，错误区间指向 `/` 运算符本身。
- **比较前转换**：同量纲不同单位比较时，先把右操作数换算到左操作数的单位再比较，
  如 `20 °C == 68 °F` 为 `true`。

## 运行

```sh
python3 -m unitlang examples/demo.ul   # 运行文件
echo '1 mi -> km' | python3 -m unitlang # 或从标准输入读
```

`examples/demo.ul` 的输出：

```text
0.5
0.3
distance = 42 km
time = 1.5 h
70/9 (≈ 7.77778) m/s
28 km/h
g = 9.8 m/s^2
t = 3 s
29.4 m/s
1.609344 km
2.54 cm
60 min
morning = 8 °C
noon = 21 °C
13 Δ°C
13 °C
69.8 °F
373.15 K
0 °C
5 Δ°C
true
true
true
```

## 语法

```text
stmt     := "let" 名 "=" expr | expr          # 语句以换行或 ; 分隔，# 为注释
expr     := conv (("<"|"<="|">"|">="|"=="|"!=") conv)*
conv     := add ("->" 单位表达式)?             # 单位换算
add      := mul (("+"|"-") mul)*
mul      := unary (("*"|"/") unary)*
unary    := ("-"|"+") unary | postfix
postfix  := primary 单位表达式?                # 仅数字字面量后可跟单位
primary  := 数 | 名 | "(" expr ")"
单位表达式 := 单位项 (("*"|"/") 单位项)*，单位项 := 单位名 ("^" 整数)?
```

示例：`let v = 9.8 m/s^2`、`distance / time -> km/h`、`1 km > 999 m`。

## 内置单位

- 长度：`m km cm mm um nm in ft yd mi`；时间：`s ms us min h day`
- 质量：`kg g mg t lb`；体积：`L mL`；力学：`N J W Pa`
- 温度：`K`（线性）；`°C °F`（仿射绝对温度，别名 `degC degF`）；
  `Δ°C Δ°F ΔK`（温差，别名 `ddegC ddegF dK`）

## 错误示例

```text
$ echo '1 m + 1 s' | python3 -m unitlang
错误：量纲不同，不能相加：长度 与 时间
  第 1 行第 5 列（字节区间 [4, 5)）
  1 m + 1 s
      ^

$ echo '2 * 20 °C' | python3 -m unitlang
错误：绝对温度（仿射温标）不能直接参与乘除运算，请先换算为 K
  第 1 行第 3 列（字节区间 [2, 3)）
  2 * 20 °C
    ^

$ echo '1 / 0' | python3 -m unitlang
错误：除数为零
  第 1 行第 3 列（字节区间 [2, 3)）
  1 / 0
    ^
```

## 测试

```sh
python3 -m unittest discover -v
```

结果：`Ran 39 tests ... OK`（39 个测试全部通过，覆盖词法字节区间、
精确有理数、单位换算、仿射温度规则、比较前转换、除零定位、静态检查
先于执行、示例程序输出与 CLI 行为）。

## 项目结构

```text
unitlang/
  errors.py    # LangError（UTF-8 字节区间）与错误渲染
  lexer.py     # 词法分析，跟踪字节偏移
  units.py     # 量纲运算、单位注册表、单位换算
  parser.py    # AST 与递归下降解析
  checker.py   # 静态量纲/仿射规则检查（执行前）
  interp.py    # 解释执行与精确值格式化
  __main__.py  # CLI
examples/demo.ul
tests/test_unitlang.py
```
