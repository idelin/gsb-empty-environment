# SPPF 通用上下文无关文法解析器

纯标准库（Python 3.11+），不使用任何正则或自动机库。字母表与产生式（转移结构）显式输入。

## 特性

- **左递归**：Earley 风格识别，`S -> S S | 'a'` 等直接左递归/隐藏左递归均可终止。
- **epsilon 产生式**：空右部 `()`；空输入是否接受完全由文法可空性决定。
- **歧义保留**：构建共享压缩解析森林（SPPF，Scott 风格二叉化），不枚举指数级树；
  `S -> S S | 'a'` 解析 `aaa` 得到 2 棵打包树，`a`*10 对应 4862 棵树而森林仅 130 个节点。
- **最远失败位置**：拒绝时返回 `farthest`（最远到达位置）与 `expected`（该位置可继续的终结符）。
- **状态去重不丢证明边**：Earley 状态按 `(rule, dot, origin)` 去重，但 SPPF 节点按
  `(label, start, end)`  intern 共享，`families` 集合保留每一条不同的推导边。
- **无限歧义检测**：nullable 符号出现在递归环中时森林成环（文法无限歧义），
  `count_trees` 抛出 `ValueError` 而非死循环。

## 文件

- `sppf.py` — 解析器与森林工具；`python3.11 sppf.py` 运行全部样例。
- `test_sppf.py` — unittest 反例/性质测试，可独立执行：`python3.11 test_sppf.py`。

## 用法

```python
from sppf import Grammar, parse, count_trees

g = Grammar(terminals={"a"}, nonterminals={"S"},
            productions=[("S", ("S", "S")), ("S", ("a",))], start="S")
res = parse(g, "aaa")
res.accepted        # True
res.farthest        # 3
count_trees(res.root)  # 2 —— 歧义被完整保留
```
