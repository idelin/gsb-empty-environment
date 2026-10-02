# 最低成本转导（finite-state transducer, Python 3.11，仅标准库）

显式给定字母表与转移结构的转导器：消费输入符号、输出字符串，每条边带非负整数成本。
求最低成本转导结果与前 K 个不同输出，不使用任何现成正则/自动机库。

## 文件

- `transducer.py` — 核心库：`Edge` / `Transducer.best()` / `Transducer.k_best()` /
  `brute_force_best()` / `brute_force_k_best()`，以及路径证据 `Evidence`
- `main.py` — CLI，从 JSON 规格显式读入字母表与转移结构
- `examples/*.json` — 可独立执行的样例与反例
- `test_transducer.py` — unittest 套件（含小图穷举对照）

## 规格格式（显式字母表 + 转移结构）

```json
{
  "states": ["q0", "q1", "q2"],
  "alphabet": ["a", "b", "x", "y"],
  "initial": "q0",
  "finals": ["q2"],
  "edges": [
    {"id": 0, "src": "q0", "dst": "q1", "in": "a", "out": "x", "cost": 2}
  ]
}
```

`"in": null` 表示 epsilon 边（不消费输入）；`cost` 必须为非负整数；
输入符号与输出的每个字符都必须在 `alphabet` 中，否则拒绝构造。

## 排序与决胜规则

路径键为 `(总成本, 输出长度, 输出字典序, 边ID序列)`：

1. 总成本小者优先；
2. 同成本，输出短者优先；
3. 同成本同长，输出字典序小者优先；
4. 仍相同，边 ID 序列字典序小者优先。

**为什么长度必须先于字典序**：若零成本环在输出中段产出符号
（如环输出 `a`、后缀为 `b`，可得 `b, ab, aab, ...`），纯字典序下这是严格
下降链、不存在最小解。加入长度分量后，键在可达输出上是良基的：
成本为非负整数只能有限下降；同成本下长度只能有限下降；同成本同长下
固定长度的字符串只有有限个。因此最小解必然存在。

## 终止性论证

- **`best()`（状态上的 Dijkstra）**：每条边的松弛使键严格增大
  （边 ID 序列变长，而序列总是小于其真扩展），故每个状态至多 settle 一次，
  过期堆项被跳过。状态有限 ⇒ 终止。零成本 epsilon 环与零成本空输出环
  不能改进任何键，不会导致重复 settle。
- **`k_best()`（最佳优先路径枚举）** 两道保险：
  1. **支配零环剪枝**：成本 0 且输出为空的边若指向当前路径上已出现的状态，
     绝不扩展。此类环不改变成本与输出，且更短前缀路径的边 ID 序列严格更小，
     被剪路径的任何延拓都被前缀路径的对应延拓支配。只有零成本空输出
     （epsilon）环的图因此可以有限枚举完毕（堆会排空）。
  2. **预算**：堆弹出次数有上界。若可达的零成本环在通往终态的路上产出
     非空输出，则不同输出有无穷多个，任何有限枚举都不可能完备；预算保证
     终止，且结果保留剩余前沿（`frontier_size` / `frontier_sample`）供检查。
- **`brute_force_best()`（对照用）**：只枚举简单路径。任何接受路径删去环后
  成本与输出长度不增；若两者都不增，则被删的环成本为 0 且输出为空，输出
  字符串不变。故简单路径上的最小值等于全体路径上的最小值，枚举有限。

## 用法

```bash
python3 main.py SPEC.json              # 最优转导（Dijkstra）
python3 main.py SPEC.json --k 5        # 前 5 个不同输出（去重枚举）
python3 main.py SPEC.json --k 5 --budget 200
python3 -m unittest test_transducer -v # 测试
```

无接受路径时打印 `FAILURE: no accepting path exists` 并以退出码 1 结束。

## 运行样例（实际输出）

```
$ python3 main.py examples/basic.json
BEST transduction:
total_cost=3 output='xy' input_consumed=['a', 'b'] edge_ids=[0, 1]
  q0 -[e0 in=a out='x' cost=2]-> q1
  q1 -[e1 in=b out='y' cost=1]-> q2

$ python3 main.py examples/epsilon_cycle.json     # 零成本 epsilon 环：终止
BEST transduction:
total_cost=1 output='x' input_consumed=['a'] edge_ids=[0, 2]
  q0 -[e0 in=eps out='' cost=0]-> q1
  q1 -[e2 in=a out='x' cost=1]-> q2

$ python3 main.py examples/zero_output_cycle.json --k 3   # 零成本输出环
TOP-3 distinct outputs (complete=True, pops=8, frontier_size=2):
[1] total_cost=0 output='b'   edge_ids=[0, 2]          # 长度优先 ⇒ 'b' 最小
[2] total_cost=0 output='ab'  edge_ids=[0, 1, 2]
[3] total_cost=0 output='aab' edge_ids=[0, 1, 1, 2]

$ python3 main.py examples/zero_output_cycle.json --k 10 --budget 6
TOP-10 distinct outputs (complete=False, pops=6, frontier_size=2):
[1] total_cost=0 output='b' ...
[2] total_cost=0 output='ab' ...
budget 6 exhausted; frontier retained (2 candidates), best remaining keys:
  frontier key=(0, 3, 'aaa') edge_ids=[0, 1, 1, 1]
  frontier key=(0, 3, 'aab') edge_ids=[0, 1, 1, 2]

$ python3 main.py examples/tie_break.json --k 3   # 决胜：成本→长度→字典序→边ID
[1] total_cost=3 output='c'  edge_ids=[0, 1]     # 长度 1 优先
[2] total_cost=3 output='aa' edge_ids=[4, 5]     # 三条 'aa' 路径中边ID最小者
[3] total_cost=3 output='ab' edge_ids=[2, 3]

$ python3 main.py examples/no_path.json; echo $?
FAILURE: no accepting path exists
1
```

证据（`Evidence`）包含每一步的边 ID、源/目的状态、消费的输入符号
（epsilon 显示为 `eps`）、产出的输出与成本，以及汇总的总成本、完整输出、
输入消费序列。

## 测试结果

```
$ python3 -m unittest test_transducer
Ran 16 tests in 0.079s
OK
```

覆盖：基本最优、epsilon 零环终止、零成本输出环的长度优先最小解、
四级决胜顺序、K-best 输出去重（同输出多路径只计一次）、预算耗尽保留前沿、
零输出环不无限枚举、无接受路径返回失败、空路径（初态即终态）、
非法输入校验（负成本/未知状态/重复边ID/字母表外符号），以及两类穷举对照：

- `best()` vs 简单路径穷举：150 个固定种子随机小图（含零成本边与 epsilon），
  完整键（含边 ID）逐一相等；
- `k_best()` vs 限长穷举：正成本随机小图上，当第 k 个输出的成本不超过
  枚举步界时（此时更长的路径必然更贵，界是完备的），前 k 个不同输出
  及其证据键逐一相等；
- `examples/` 全部规格与穷举对照一致。
