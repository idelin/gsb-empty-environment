# lottery_assign — 约束式抽签可复核分配

纯 Python 3.11 标准库实现。给定候选人（带组别）、奖项（带组别配额与容量）、
禁配边与随机种子，程序：

1. 用**固定定义的整数 PRNG（SplitMix64）**为每条可行候选边生成 64 位随机数，
   按 `(随机数, 规范序号)` 排序得到候选边**全序**（跨进程、跨重启可复现，
   不依赖任何运行时随机性、时间源或哈希排序）；
2. 用最大流（Dinic）求**最大匹配数**（满足：每人最多一奖、奖项容量、
   每个奖项对每个组别的配额、禁配边）；
3. 在**所有最大匹配**中，选取边序列（按随机全序的位次升序）**字典序最小**者。
   做法是按随机序扫描边，若某条边加入已选集合后仍存在达到最优值的扩展，
   则强制选入（标准交换论证可证该贪心得到字典序最小解）。

## 运行

```bash
# 求解（结果输出到 stdout 或 -o 指定的文件）
python3 -m lottery_assign solve examples/boundary.json
python3 -m lottery_assign solve examples/demo.json -o result.json

# 覆盖输入文件中的种子
python3 -m lottery_assign solve examples/demo.json --seed 42

# 小规模实例的暴力枚举复核（确认字典序最优）
python3 -m lottery_assign solve examples/boundary.json --exhaustive-check

# 复核一份已产出的结果（用相同输入与种子确定性重算并比对）
python3 -m lottery_assign verify examples/demo.json result.json
```

## 输入格式（JSON）

```json
{
  "seed": 20261001,
  "candidates": [{"id": "c01", "group": "freshman"}],
  "awards": [
    {"id": "travel", "capacity": 2, "quotas": {"freshman": 1, "senior": 1}}
  ],
  "forbidden": [["c01", "research"]]
}
```

- `seed`：整数种子（可选，默认 0；命令行 `--seed` 可覆盖）。
- `candidates[].id / .group`：候选人 id 与组别，id 不可重复。
- `awards[].id`：奖项 id，不可重复。
- `awards[].capacity`：该奖项最多发放人数（可选，默认 1）。
- `awards[].quotas`：组别 → 该奖项最多授予该组别的人数；
  未列出的组别配额为 0（即该组别不可获此奖）。
- `forbidden`：禁配边 `[候选人id, 奖项id]` 列表。

## 输出格式（JSON）

- `seed` / `prng`：实际使用的种子与 PRNG 名称（`splitmix64`）。
- `edge_order`：候选边全序，每条含 `candidate`、`award` 与 PRNG 抽得的
  `draw`（64 位无符号整数），可供第三方独立复核排序。
- `assignments`：中选边，含 `candidate`、`award`、`group` 及该边在全序中的
  位次 `edge_rank`，按 `edge_rank` 升序排列（即字典序最小的边序列）。
- `num_assigned` / `optimum`：发放数与最大匹配数（二者相等即最优性证据）。
- `exhaustive_check`：仅在 `--exhaustive-check` 时出现，值为 `passed`。

## 可复核性

- PRNG 定义固定在 `lottery_assign/prng.py`（SplitMix64），测试锁定了
  种子 0 与 12345 的参考输出序列。
- 任何人均可用同一输入文件与种子重新运行 `solve` 或 `verify`，
  逐字节复现分配结果与边全序；测试证明结果与 `PYTHONHASHSEED` 无关、
  跨进程重启一致。
- 字典序最优性除交换论证外，还可用 `--exhaustive-check` 在小实例上
  以暴力枚举全部最大匹配进行独立确认。

## 测试

```bash
python3 -m unittest discover -s tests -v
```

覆盖：PRNG 参考序列、边全序确定性与种子敏感性、验收边界用例
（a 仅能 x、b 可 x/y，多种种子下必得 a-x b-y，且存在使朴素贪心
少发一个奖的种子而求解器仍达最优）、约束满足（禁配/配额/一人一奖）、
随机小实例上与暴力枚举的字典序最小性比对、CLI 求解/复核/篡改拒绝、
跨重启与跨 `PYTHONHASHSEED` 复现。

`examples/` 与测试中的数据全部为合成数据。
