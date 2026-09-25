# 多层位置映射：原文 → 宏展开 → 字节码

纯 Python 3.11 标准库实现（无 eval、无现成编译器），演示一个带宏的玩具
语言从原文到宏展开文本再到字节码的多层位置映射，以及诊断回溯、宏定义
更新后的局部重编译与旧诊断查询。

## 层次与区间模型

```
program(用户原文)  <--M1--  expanded(宏展开文本)  <--M2--  code(字节码 pc)
macro:NAME(宏定义) --^
```

- 所有位置都是 **UTF-8 字节区间** `Span(doc, start, end)`（半开区间），
  不是单个偏移；`doc` 标识所在层/文档。
- 映射是**区间关系**：`Block(src, dst, tag)` 的集合（`IntervalMap`），
  组合 `compose` 即关系复合——中间层区间相交即保留 `(src, dst)` 对，
  tag 顺序拼接。因此一对多、重叠来源、生成片段（无对应块的区间）在
  组合后原样保留。
- 指令 `i` 在 code 层占区间 `[i, i+1)`。

## 宏展开与来源帧

文本级宏展开在 token 流上进行，每个 token 携带来源帧链 `Frame`：

- `user`：直接来自用户原文；
- `macro_body`：宏定义体中的生成片段，附 `call_site`（调用处）与
  `def_span`（定义处）；
- `call_context`：作为宏实参的 token，附外层调用处与定义。

实参在宏体中出现 N 次 → N 个展开 token 指回同一源区间（一对多）；
嵌套宏使来源区间互相重叠；递归/互递归宏由 active 集合截断。

## 诊断回溯

`Session.explain(diag)` 返回**全部候选**及选择原因，并按策略选出
"最合适的用户片段"：`user` > `call_site` > `macro_body` > `definition`。
一个宏调用展开成的多条指令都回到调用范围内（实参→调用内用户片段，
宏体符号→调用处），并附宏定义位置。无来源指令（如生成的 `HALT`）
不伪造行号，直接报告"无来源"。

## 局部重编译与版本快照

每条语句独立展开/编译为块（chunk）。`update_macro` 只重编译**传递依赖**
该宏的语句块，其余块原样复用（链接时仅平移坐标），并报告：

- `touched_blocks`：重编译块新建的 M1+M2 映射块数；
- `shifted_blocks`：复用但坐标平移的块数；
- `reused_blocks`：完全未动的块数。

每次更新产生新版本号与不可变快照（展开文本、字节码、M1/M2/组合映射）。
诊断记录产生它的版本号；解释旧诊断永远查旧快照——**新字节码坐标绝不
套到旧源文上**，查询不存在的版本直接报错。

## 运行

```bash
python3.11 main.py                        # 运行样例
python3.11 -m unittest discover -v        # 测试（28 个）
```

## 运行样例（节选）

```
=== 字节码及其最佳来源（pc -> 用户/宏片段） ===
  2 LOAD   甲     <- [user] program 第3行第16列: 'let b = SQUARE(甲) + 1;'
  3 LOAD   甲     <- [user] program 第3行第16列: 'let b = SQUARE(甲) + 1;'
  4 MUL          <- [call_site] program 第3行第9列: 'let b = SQUARE(甲) + 1;'
 16 HALT         <- (无来源，不伪造行号)

=== 第 1 次运行（v0）：触发除零 ===
诊断: division by zero @ pc=14 op=DIV (版本 v0)
最佳定位: program 第5行第9列: 'print b / (甲 - 3);'
    print b / (甲 - 3);
            ^

=== 更新宏 SQUARE：只局部重编译受影响语句 ===
版本: v0 -> v1
重编译语句: [1]
触及映射块数: 27
仅平移坐标的复用块数: 20
完全复用块数: 7

=== 旧诊断仍按旧快照 v0 解释（新坐标不套旧源文） ===
解释使用版本: v0，定位不变:
program 第5行第9列: 'print b / (甲 - 3);'
```

## 测试结果

```
$ python3.11 -m unittest discover
............................
----------------------------------------------------------------------
Ran 28 tests in 0.449s

OK
```

覆盖：有限区间模型上组合结合律（500 组随机映射）、组合映射与逐层查询
一致性（随机模型 + 真实流水线每个 pc）、一对多/重叠来源保留、宏递归
截断、UTF-8 字节区间、宏调用多指令回溯并附定义、HALT 无来源不伪造、
局部重编译触及块数、旧诊断按旧快照解释、更新失败回滚。

## 代码结构

- `posmap/intervals.py` — `Span`/`Block`/`IntervalMap` 与关系复合
- `posmap/lexer.py` — UTF-8 字节偏移词法器
- `posmap/macros.py` — 宏定义解析、token 级展开、来源帧
- `posmap/parser.py` — 递归下降语法分析（`let`/`print`/表达式）
- `posmap/compiler.py` — AST → 栈式字节码，指令带来源区间
- `posmap/vm.py` — 字节码解释器（除零/未定义变量故障）
- `posmap/session.py` — 流水线、组合映射、诊断解释、局部重编译、快照
- `main.py` — 运行样例；`tests/` — unittest 测试
