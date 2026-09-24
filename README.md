# posmap — 多层位置映射（原文 → 宏展开 → 字节码）

纯 Python 3.11 标准库实现（仅用 `dataclasses` / `unittest` 等），不使用
`eval`、`compile` 或任何现成编译器。所有源码位置都是 **UTF-8 字节区间**
`[start, end)`。

## 结构

- `posmap/intervals.py` — 区间关系映射。映射是**区间关系**（方块集合
  `(src, dst)`）而非单个偏移：一对多、重叠来源、生成片段（`src=None`）
  都能表示。`compose` 为逐块关系复合，未被覆盖的右侧块成为生成块；
  复合满足结合律（有限区间模型性质测试验证）。
- `posmap/lang.py` — 自研语言：词法（字节区间）、递归下降语法、宏展开
  （实参包裹生成括号、嵌套宏、深度限制）、字节码编译器、栈式 VM。
- `posmap/pipeline.py` — 版本化管线：
  - 每个版本独立保存源文 / 展开缓冲 / 字节码 / 三层映射（`map1`、
    `map2` 及复合映射），诊断是 `(version, pc)`，**新字节码坐标绝不
    会套到旧源文**（跨版本查询直接报错）；
  - `update_macro` 只重展开/重编译（传递地）用到该宏的语句，其余编译
    单元原样复用，并报告触及的映射块数（`UpdateReport.touched_blocks`）；
  - 反查返回**全部候选**（各自带原因），并选出"最合适用户片段"：
    宏展开的指令回到**调用范围**并附定义；生成代码不伪造行号。
- `tests/test_mapping.py` — 21 个 unittest：结合律随机有限模型
  （500 组）、一对多/重叠/生成片段保持、组合映射与逐层查询一致性、
  UTF-8 字节区间、诊断回溯、局部重编译与旧诊断查询。
- `demo.py` — 运行样例。

## 运行

```sh
python3.11 demo.py                              # 运行样例
python3.11 -m unittest discover -s tests -v     # 测试
```

## 样例输出（节选）

宏内除零的运行时错误，反查 pc=6 的 DIV 指令：

```
runtime error at pc=6 (version 0): division by zero
all candidates from the reverse lookup:
  - macro_body line 4 'safe_div(a, b)' [macro 'safe_div', definition attached]
  - source     line 4 'a'
  - source     line 4 'b'
  - generated  (no source)
chosen fragment: line 4 'safe_div(a, b)'
```

宏定义更新后的局部重编译与旧诊断：

```
update: recompiled statements [1], touched 34 mapping blocks
old diagnostic still resolves: line 2 'd(1)' (version 0 source)
cross-version guard: pc 6 is not a valid coordinate for version 0 ...
```

## 测试结果

```
Ran 21 tests in ~0.7s
OK
```
