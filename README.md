# unitlang：带量纲检查的表达式语言

一个用 Python 3.11（仅标准库）实现的微型表达式语言解释器。词法分析、
递归下降语法分析、静态量纲检查和树遍历求值全部手写，不使用 `eval`
或任何现成编译器。所有数值以有理数（`fractions.Fraction`）精确表示。

## 特性

- 数值、变量、四则运算（`+ - * /`）、括号、一元负号
- 单位：数字后紧跟单位（`3 m`、`2.5 kg`），`to` 做单位换算，目标可为复合单位（`to m / s`）
- 量纲在执行前检查：先对整个程序做静态量纲分析，通过后才求值
- 仿射温度与温差分离：`°C / °F / K` 为绝对温度，`Δ°C / Δ°F / ΔK` 为温差
- 比较运算（`== != < <= > >=`）：同量纲不同单位先换算到基准单位再比较
- 精确有理数：`0.1 + 0.2` 严格等于 `0.3`；不能整除时输出 `p/q`
- 所有源码位置均为 UTF-8 字节区间，错误信息带行号与 `^` 定位

## 规则要点

- `1 m + 1 s` → 静态量纲错误（长度加时间被拒绝），定位到 `+`
- `2 °C * 3` → 静态错误：摄氏绝对温度不能直接倍乘（温差 `Δ°C` 可以）
- `20 °C - 5 °C` → 温差 `15 K`；`20 °C + 5 Δ°C` → 绝对温度 `25 °C`
- `1 °C + 2 °C` → 错误：两个绝对温度不能相加
- `10 / 0` → 运行时除零错误，`^` 定位到 `/` 运算符
- `1 m == 100 cm` → `true`（比较前自动换算）

## 运行

```sh
python3 main.py examples/demo.ul     # 运行脚本文件
python3 -m unitlang                  # 从标准输入读取
echo '1 km to m' | python3 main.py
```

每行是一条语句：`名 = 表达式` 为赋值（不输出），其余表达式语句输出其值。
`#` 开始为注释。

## 运行样例

`examples/demo.ul` 的输出：

```
0.5
0.3
5.5 m
7.5 m^2
1000 m
550 cm
25 m/s
true
true
13 K
23.4 Δ°F
26 °C
78.8 °F
20 °C
true
735 N
```

错误样例：

```
$ echo '1 m + 1 s' | python3 main.py
error: cannot add length and time
  line 1: 1 m + 1 s
      ^

$ echo '2 °C * 3' | python3 main.py
error: absolute temperature cannot be multiplied or divided; use a temperature difference
  line 1: 2 °C * 3
       ^

$ printf 'x = 10\nx / (5 - 5)\n' | python3 main.py
error: division by zero
  line 2: x / (5 - 5)
    ^
```

## 内置单位

- 长度：`m mm cm km in ft mi`；时间：`s ms min h day`；质量：`kg g t lb`
- 派生：`Hz N Pa J W`
- 温度（绝对/仿射）：`K °C °F`；温差：`ΔK Δ°C Δ°F`

## 代码结构

- `unitlang/lexer.py` — 手写词法分析，token 携带 UTF-8 字节区间
- `unitlang/parser.py` — 手写递归下降语法分析，AST 节点携带字节区间
- `unitlang/units.py` — 量纲向量、单位注册表（比例 + 仿射偏移）
- `unitlang/interp.py` — 第一遍静态量纲分析，第二遍求值（有理数）
- `unitlang/cli.py` — 命令行入口与错误定位渲染
- `test_unitlang.py` — unittest 测试（29 个用例）

## 测试

```sh
python3 -m unittest -v
```

测试结果：`Ran 29 tests ... OK`（涵盖精确有理数、单位换算、量纲拒绝、
仿射温度规则、除零定位、跨单位比较、UTF-8 字节区间等）。
