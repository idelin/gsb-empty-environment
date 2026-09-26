# editindex：Unicode 加权编辑距离的 BK 树剪枝检索

纯 Python 3.11 标准库实现（仅 `unittest` 做测试），不依赖任何搜索服务或
全文检索库。在本地合成的 UTF-8 文本上分词、建索引，支持：

- 以 **Unicode 码点** 为单位的加权编辑距离（插入 / 删除 / 替换 / 相邻换位）；
- **BK 树剪枝索引**：键是精确距离的可证明下界（证明见下），剪枝安全；
- **阈值查询**（距离 ≤ max_dist，**含等号**）与 **TopK 查询**
  （第 k 名等距候选**完整保留**，返回数可超过 k）；
- 结果按 **(距离, 词表首次出现序号)** 排序，稳定且确定；
- 重复规范词（NFC + casefold 后相同）**只输出一次**；
- 索引结果与暴力 DP 全扫描参考**逐项一致**（测试覆盖）。

## 目录结构

```
editindex/
  distance.py    # 加权 OSA 距离 DP + 真 Damerau–Levenshtein（下界键）
  tokenizer.py   # 分词与规范化（规则见下）
  index.py       # BK 树索引：search / topk
  reference.py   # 暴力全扫描参考实现（对照用）
  demo.py        # 运行样例
tests/           # unittest 测试（31 个）
```

## 距离定义

精确距离 `weighted_distance(a, b, w)` 是 OSA 风格的 Wagner–Fischer DP，
以码点为单位：

```
d[i][j] = min( d[i-1][j] + w.delete,
               d[i][j-1] + w.insert,
               d[i-1][j-1] + (a[i]==b[j] ? 0 : w.substitute),
               d[i-2][j-2] + w.transpose   # 相邻两码点互为换位时
             )
```

## 为什么不能直接拿它做 BK 树键

BK 树剪枝依赖键函数的**三角不等式**。但加权 OSA 距离一般不满足：

- **三角不等式反例**（全单位权重）：
  `d(ca,ac)=1`，`d(ac,abc)=1`，但 `d(ca,abc)=3 > 2`
  （OSA 限制同一子串不可重复编辑；真最短路径 ca→ac→abc 只需 2 步）。
- **对称性反例**（`insert=1, delete=5`）：
  `d("","ab")=2 ≠ d("ab","")=10`。

因此直接用精确距离做键会漏词。本实现改用**安全下界键**：

```
key(x, y) = c_min · d_DL(x, y)，  c_min = min(w.insert, w.delete, w.substitute, w.transpose)
```

其中 `d_DL` 是单位成本的**真 Damerau–Levenshtein 距离**
（Lowrance–Wagner，允许同一子串多次编辑）。

### 证明

**引理 1（d_DL 是度量）。** 取所有 Unicode 码点串为顶点，若两串相差一次
插入 / 删除 / 替换 / 相邻换位则连边。每种操作都有同成本逆操作
（插入↔删除、替换 a→b ↔ b→a、换位自逆），故图为**无向图**；
`d_DL` 即该图最短路径长度。无向图最短路径满足：非负、同一性
（距离 0 ⟺ 路径长 0 ⟺ 同串）、对称（边无向）、三角不等式
（x⇝y 与 y⇝z 的路径拼接是 x⇝z 的通路）。∎

**引理 2（下界）。** 对任意串 x, y 与非负权重，
`weighted_distance(x, y, w) ≥ c_min · d_DL(x, y)`。
证明：精确距离是某组编辑脚本集合 S（OSA 受限）上的最小成本。
任一脚本 s ∈ S 含 ops(s) 个操作，每个操作成本 ≥ c_min，故
cost(s) ≥ c_min·ops(s) ≥ c_min·d_DL(x,y)——因为 d_DL 是**所有**
脚本（含 S）上的最小操作数，ops(s) ≥ d_DL。对 S 取 min 不等式仍成立。∎

**引理 3（剪枝安全）。** `key = c_min·d_DL` 是度量（度量的正数倍仍是度量；
c_min=0 时退化为零伪度量，三角不等式仍成立，索引退化为全扫描但**仍正确**）。
BK 树不变式：节点 n 的边权 e 子树中**每个** x 都满足 key(n,x)=e。
对查询 q、半径 r，记 dk=key(q,n)，则子树中任意 x 满足

```
exact(q,x) ≥ key(q,x) ≥ |key(q,n) − key(n,x)| = |dk − e|   （引理 2 + 三角不等式）
```

故当 `|dk − e| > r` 时 exact(q,x) > r，整棵子树可剪；
阈值含等号，因此仅在**严格大于**时剪枝，等距候选不会被剪掉。∎

TopK 用同一棵索引两阶段完成：阶段 1 以自适应半径（当前第 k 好的距离）
遍历求出第 k 小距离 d_k；阶段 2 以 d_k 为半径做阈值查询，收齐全部等距候选。
阶段 1 的半径单调不增且始终 ≥ 最终 d_k，被剪节点下界 > 当时半径 ≥ d_k，
不可能属于前 k 名的等距组，故两阶段结果与全扫描参考一致（测试验证）。

## 分词规则

1. 输入为已解码的 `str`（本地合成 UTF-8 文本）；
2. 全文先做 **NFC** 规范化（`unicodedata.normalize`），折叠可组合重音序列；
3. **词** = 连续词字符的最大段；码点 `ch` 是词字符当且仅当
   `ch.isalnum()`（Unicode 类别 L*/N*）或类别为 M*（组合附加符号，
   如 U+0301，使其依附前导字母）。空白、标点、emoji（So）等都是分隔符；
4. 每个词再做 **casefold** 作为规范形式（`STRASSE`→`strasse`）；
5. 词表按**首次出现顺序**去重；查询串做同样的 NFC+casefold 规范化。

## 运行

```bash
python3.11 -m unittest discover -s tests -t . -v   # 测试
python3.11 -m editindex.demo                        # 运行样例
```

API：

```python
from editindex import BKTreeIndex, Weights

index = BKTreeIndex.from_text("Café café 东京 南京 北京", Weights())
index.search("café", 1)   # [Match('café', 0.0), ...]，阈值含等号
index.topk("东京", 2)     # 第 2 名等距候选完整保留
```

## 运行样例（`python3.11 -m editindex.demo` 实际输出）

```
词表（去重后 20 个规范词）：
  café cafe naïve naive cat bat rat cats car cart dog 东京 南京 北京 西京 猫咪 猫 strasse 版本2 版本3

== 阈值查询 search('café', 1)，单位权重 ==
  'café'         距离=0
  'cafe'         距离=1
  剪枝统计：访问节点 2/20，精确距离计算 2 次

== TopK 查询 topk('cat', 3)（第 3 名等距者一并返回） ==
  'cat'          距离=0
  'bat'          距离=1
  'rat'          距离=1
  'cats'         距离=1
  'car'          距离=1
  'cart'         距离=1

== TopK 查询 topk('东京', 2) ==
  '东京'           距离=0
  '南京'           距离=1
  '北京'           距离=1
  '西京'           距离=1

== 加权成本 Weights(insert=1, delete=5, substitute=3, transpose=1) ==
  对称性被破坏：d('','ab') = 2 而 d('ab','') = 10
  三角不等式被破坏（OSA）：d('ca','abc') = 3.0 > d('ca','ac') + d('ac','abc') = 2.0
  索引 vs 暴力全扫描（25 词 × 6 查询 × 多阈值/TopK）：完全一致，无漏词 ✔

== 加权阈值查询 search('café', 4)，Weights(insert=1, delete=5, substitute=3, transpose=1) ==
  'café'         距离=0
  'cafe'         距离=3
  剪枝统计：访问节点 24/25，精确距离计算 24 次
```

## 测试结果（`python3.11 -m unittest discover -s tests -t .` 实际输出）

```
...............................
----------------------------------------------------------------------
Ran 31 tests in 1.5s

OK
```

覆盖要点：

- `test_distance.py`：已知距离值、Unicode 码点（CJK / emoji / 组合字符）、
  非对称权重、换位；OSA 三角不等式反例与非对称反例；
  `d_DL` 在 {a,b} 长度 ≤3 全部串上**穷举验证度量三公理**；
  500 组随机串/随机权重验证 `key ≤ 精确距离`。
- `test_index.py`：阈值含等号边界、TopK 等距完整且稳定、
  重复规范词只输出一次、空索引；
  **破坏三角不等式/对称性的成本**下与暴力全扫描逐项对照（不漏词）；
  150 组随机 Unicode 小词集 × 随机权重 × 多阈值/TopK 的模糊对照；
  剪枝有效性（精确距离计算次数 < 全扫描一半）。
- `test_tokenizer.py`：分词规则、NFC 折叠、casefold、组合符依附、
  emoji 分隔、去重稳定性。

## 局限

- 精确距离是 OSA 变体（同一子串不重复编辑）；下界用的是真 DL，二者
  不同正是本索引设计的出发点。
- 若某操作权重为 0（c_min=0），下界键退化为零伪度量，索引退化为
  全扫描——结果仍正确，只是不再剪枝。
- 距离以码点为单位，不合并字素簇（如 👨‍👩‍👧 是多个码点）。
