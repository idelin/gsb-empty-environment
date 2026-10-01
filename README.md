# 约束式抽签可复核分配

在约束下做可复现、可复核的抽签分配：候选人带组别，奖项带组别配额，
每人最多一个奖，支持禁配边。给定整数种子后结果完全确定，跨重启可复现。

## 运行环境

- Python 3.11+，仅使用标准库，无第三方依赖。

## 使用方法

```bash
python3 lottery.py 输入.json              # 结果输出到标准输出
python3 lottery.py 输入.json -o 输出.json # 结果写入文件
cat 输入.json | python3 lottery.py        # 从标准输入读取
python3 -m unittest -v                    # 运行测试
```

## 输入格式（JSON）

```json
{
  "seed": 20261001,
  "candidates": [
    {"id": "c01", "group": "engineering", "awards": ["laptop", "book"]}
  ],
  "awards": [
    {"id": "laptop", "quotas": {"engineering": 1, "design": 0}}
  ],
  "forbidden": [["c04", "cash"]]
}
```

- `seed`：整数随机种子（必填）。同一种子同一输入，输出逐字节一致。
- `candidates[].id`：候选人唯一 id；`group`：所属组别；
  `awards`：该候选人可接受的奖项 id 列表（即可行候选边）。
- `awards[].id`：奖项唯一 id；`quotas`：该奖项对每个组别最多可发数量。
  未列出的组别视为配额 0（该组别候选人不能获得此奖）。
- `forbidden`：禁配边列表，`[候选人id, 奖项id]`，即使候选人声明可接受也禁止分配。可省略。

## 输出格式（JSON）

```json
{
  "seed": 20261001,
  "prng": "splitmix64",
  "edge_order": [["c01", "book"], ["c02", "laptop"]],
  "matching": [["c01", "book"], ["c02", "laptop"]],
  "matching_size": 2,
  "maximum_matching_size": 2,
  "is_maximum": true
}
```

- `edge_order`：由种子生成的候选边全序（先见者优先），可用于第三方复核。
- `matching`：最终分配的边，按 `edge_order` 的先后排列。
- `matching_size` / `maximum_matching_size`：实际发出数 / 理论最大可发数。
- `is_maximum`：两者相等即为 `true`，可作为最优性凭证。

## 算法与可复核性

1. **固定整数 PRNG**：SplitMix64。状态为 64 位无符号整数，
   每步 `state += 0x9E3779B97F4A7C15 (mod 2^64)`，输出为对该状态做两轮
   xor-shift 与乘法混合（常数 `0xBF58476D1CE4E5B9`、`0x94D049BB133111EB`，
   最后 `z ^ (z >> 31)`）。纯整数定义，与语言、进程、机器无关。
2. **候选边全序**：按 `(候选人, 奖项)` 字典序遍历所有可行边，依次取 PRNG
   输出作为排序键，按 `(键, 候选人, 奖项)` 排序。不依赖 set/dict 迭代顺序，
   不使用任何运行时随机 hash（在任意 `PYTHONHASHSEED` 下输出一致，有测试保证）。
3. **选解**：先求最大可分配数（最大流建模：源→候选人容量 1 保证每人最多一奖；
   候选人→(奖项,组别) 槽位；槽位→汇容量为组别配额）。然后按全序逐边尝试，
   若"强制选入该边后剩余问题仍能补满到最大基数"则选入，否则跳过。
   结果即所有最大匹配中、按全序排列后边序列字典序最小者。

### 最优性说明

- 字典序最小性在小规模随机合成实例上与暴力枚举全部匹配对拍验证
  （`TestOptimalityAgainstBruteForce`，60 个实例全部一致）。
- 贪心选边 + 最大流可行性判定的构造对任意规模在理论上给出字典序最小的
  最大匹配（标准的"逐位确定 + 可行性检查"论证），但大规模实例未做独立
  对拍验证。
- 验收边界（a 仅能拿 x、b 能拿 x 或 y、配额各 1）对 128 个种子全部输出
  `a-x, b-y`；测试同时确认其中存在使朴素按序贪心少发一个奖的种子，
  即边界确实被覆盖。

## 示例

```bash
python3 lottery.py examples/boundary.json     # 验收边界：a-x, b-y
python3 lottery.py examples/lottery_demo.json # 多组别多奖项合成示例
```

所有示例数据均为合成数据。
