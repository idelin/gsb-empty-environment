# Earley + 共享解析森林

纯 Python 3.11 标准库实现，支持：

- 直接左递归与一般上下文无关文法；
- epsilon 产生式与可空非终结符；
- 歧义文法的共享 packed parse forest（SPPF）；
- 接受/拒绝结果与拒绝时可到达的最远输入位置；
- 不枚举指数数量解析树，用共享 family 记录不同证明边；
- 显式终结符字母表和文法转移，不使用正则或自动机库。

## 运行

```sh
python3.11 -m unittest -v
python3.11 example.py
```

## 用法

```python
from earley_sppf import Grammar, Production

grammar = Grammar(
    alphabet=("a",),
    productions=(
        Production("S", ("S", "S")),
        Production("S", ("a",)),
    ),
    start_symbol="S",
)

result = grammar.parse("aaa")
result.accepted              # True
result.farthest_position     # 3
result.derivation_count()    # 2
result.forest_summary()
```

`Production(lhs, rhs)` 是一条显式文法转移；`Production("S", ())` 表示 `S → ε`。

## SPPF 节点

- `terminal`：单个终结符；
- `symbol`：某个非终结符在一个输入区间上的成功推导；
- `rule`：某条产生式带点位置的部分匹配；
- 每个非终结符节点的 `families` 保存不同产生式/分割点证明；
- 每条规则节点的 family 最多连接“之前前缀”和“最新孩子”两个节点。

因此 `S → SS | a` 对 `aaa` 的两种括号化保存在同一个有限共享森林中，而不是生成两棵完整树。

对于存在可空循环的文法（如 `S → SS | ε`），空输入可被接受；SPPF 仍有限，`derivation_count()` 会报告 `math.inf`。
