# uniedit：按（加权）编辑距离检索 Unicode 词集

纯 Python 3.11 标准库实现（不使用任何搜索服务或全文检索库）。
在本地合成的 UTF-8 文本上构建词集索引，按编辑距离查询，支持：

- Unicode 码点级精确加权编辑距离（插入 / 删除 / 替换 / 相邻换位）
- BK 树剪枝索引（仅在度量性质**可证明成立**时启用）
- 非度量成本下的安全替代方案：带可证明下界的全扫描，保证不漏词
- TopK 与距离阈值（含等号）查询；等距候选完整、排序稳定
- 重复规范词（casefold 后相同）不重复输出

## 目录结构

```
uniedit/
  costs.py     成本模型（均一 / 表驱动），声明 min_insert/min_delete 下界
  distance.py  精确加权编辑距离 DP（OSA 风格相邻换位项）+ 长度差下界
  metric.py    度量性质校验：BK 树剪枝可证明安全的有限充分条件
  bktree.py    BK 树（阈值查询 + TopK 分支限界）
  tokenize.py  本地 UTF-8 文本分词
  index.py     WordIndex：度量时走 BK 树，否则回退安全全扫描
  demo.py      运行样例：python3 -m uniedit.demo
tests/         unittest 测试（36 个用例）
```

## 分词规则（tokenize.py）

1. 输入为已解码的 `str`；先做 **NFC** 规范化，使组合序列（`e + U+0301`）
   与预组合形式（`é`）得到相同词元。
2. **词元** = Unicode 类别以 `L`（字母）、`M`（记号）、`N`（数字）开头的
   码点的最大连续串。`M` 类保证组合附加符号跟随基字符留在词元内。
3. 其余码点（空白、标点、符号、emoji 等）一律视为分隔符；连字符与撇号
   也分词（`don't` → `don`, `t`），下划线是分隔符。
4. 连续 CJK 表意文字属 `L` 类，构成一个词元（无空格语言按最大连续字母
   串处理，不做词典切分）。
5. **规范形式** = 词元的 `casefold`（如 `Straße` → `strasse`），用于去重；
   索引保留首次出现的原始形式与位置。查询串做同样的 NFC + casefold。

## 距离定义（distance.py）

对码点串 `a`(长 m)、`b`(长 n)，`distance(a, b, costs)` 是如下 DP 的精确值：

```
d[0][j] = Σ ins(b[<j])          d[i][0] = Σ del(a[<i])
d[i][j] = min( d[i-1][j]   + del(a[i-1]),
               d[i][j-1]   + ins(b[j-1]),
               d[i-1][j-1] + (0 若相等否则 sub(a[i-1], b[j-1])),
               d[i-2][j-2] + trans(a[i-2], a[i-1])   若 a[i-2..i-1] 与 b[j-2..j-1] 互为交换 )
```

时间 O(mn)，空间 O(n)。返回的是该良定义函数的**精确值**（非近似）。

## 为什么需要度量校验（metric.py）

BK 树剪枝依赖距离的三角不等式与对称性。但加权 + 相邻换位时它们可能失效：

- **便宜换位**：单位成本 + `trans=1` 时 `d(ca,ac)=1`、`d(ac,abc)=1`，
  但 OSA 风格 DP 给出 `d(ca,abc)=3 > 2`（换位后的子串不再重编）。
- **符号级三角不等式失效**：`ins(c)=3, ins(b)=1, sub(b,c)=1` 时
  `d('','c')=3 > d('','b')+d('b','c')=2`——脚本"插入 b 再替换为 c"比直接
  插入 c 便宜，而 DP 的对齐结构表达不了"插入后再改"。
- **不对称**：`ins(c) != del(c)` 或 `sub` 不对称时 `d(x,y) != d(y,x)`。

### 充分条件（有限字母表 Σ = 词典字符 ∪ 查询字符 上可穷举验证）

记 `sub̂(a,b) = min(sub(a,b), del(a)+ins(b))`（DP 对 sub 超出 del+ins 的
部分天然不变：矩阵中已含"删除再插入"的两步路径，故可无损吸收）。

1. **非负**：所有相关成本 ≥ 0。
2. **对称**：`ins(c)=del(c)`；`sub`、`trans` 对称。
3. **符号级三角不等式**（吸收后）：
   `sub̂(a,c) ≤ sub̂(a,b)+sub̂(b,c)`，且 `ins(b) ≤ ins(a)+sub̂(a,b)`（∀a,b,c∈Σ）。
4. **换位被支配**：`trans(a,b) ≥ D₀("ab","ba")`（无换位 DP 的两字符距离）。

### 定理与证明

**T1（条件 1–3 ⇒ 无换位 DP 是伪度量）。**
符号成本满足三角不等式时，任何"同一位置被编辑多次"的脚本都对应符号图
上的一条路径，可由直接边替代而不更贵；故脚本最小成本 = 对齐最小成本
= DP 值。任意脚本 `x→y` 与 `y→z` 顺序拼接即得 `x→z` 的脚本，成本相加，
故 `d(x,z) ≤ d(x,y)+d(y,z)`；对称性由条件 2 保证。∎

**L1（条件 4 ⇒ 换位项无效）。** 对 `|x|+|y|` 归纳。设 `x` 以 `a₁a₂` 结尾、
`y` 以 `a₂a₁` 结尾，换位项 `T = D_t(x[:-2],y[:-2]) + trans(a₁,a₂)`。
由归纳假设与条件 4：
`T ≥ D₀(x[:-2],y[:-2]) + D₀("a₁a₂","a₂a₁") ≥ D₀(x,y)`，
末步是对齐拼接给出的超加性（对齐的拼接仍是对齐，对 DP 值直接成立）。
故换位项从不小于无换位最小值，`D_t = D₀` 处处相等，度量性质随之成立。∎

**T2（BK 树剪枝规则）。** 节点 `p` 的槽位 `w` 的子树中所有词 `x` 满足
`d(p,x)=w`（构造不变式）。由三角不等式与对称性：
`d(q,x) ≥ d(q,p)−d(x,p) = d(q,p)−w` 且 `d(q,x) ≥ d(p,x)−d(p,q) = w−d(q,p)`，
即 `d(q,x) ≥ |d(q,p)−w|`。故阈值 `t` 下槽位落在 `[d(q,p)−t, d(q,p)+t]`
之外时整棵子树可安全剪掉。TopK 用当前第 k 小距离 τ 做分支限界，τ 单调
下降，被剪子树距离下界 > τ ≥ τ_final，安全；最后按 τ_final 做含等号的
阈值收集，保证等距候选完整。∎

**T3（长度差下界，回退方案用，不依赖三角不等式）。** 任何把 `a` 变成 `b`
的 DP 路径中，插入次数减去删除次数恰为 `len(b)−len(a)`（替换与换位不改
变长度）。故 `len(b)>len(a)` 时至少需 `len(b)−len(a)` 次插入，每次成本
≥ `min_insert`；对称地 `len(a)>len(b)` 时至少需 `len(a)−len(b)` 次删除。
因此 `d(a,b) ≥ max(0, ±Δlen)·min_ins/del`，对任意非负成本安全。∎

### 安全替代索引（index.py）

`WordIndex.search` 先对有效字母表运行 `check_metric`：

- **通过** → BK 树剪枝（阈值 / TopK 分支限界），返回精确距离；
- **不通过** → 回退全扫描：仅用 T3 下界剪枝（不依赖三角不等式），
  对其余词计算精确 DP 距离。结果与不剪枝的全量参考**逐条一致，不漏词**
  （测试 `test_cheap_transpose_falls_back_no_miss` 等验证）。

## 查询语义

- `search(query, max_dist=t)`：返回 `distance ≤ t`（**含等号**）的全部命中。
- `search(query, top_k=k)`：设 τ 为第 k 小距离，返回所有 `distance ≤ τ`
  的候选——**等距候选完整**；`k` 大于词数时返回全部。
- 排序键为 `(distance, 首次出现位置)`：稳定且确定。
- 重复规范词只输出一次，保留首次出现的原始形式（`Apple/apple/APPLE` → 一条）。
- 返回 `SearchResult` 含 `mode`（`bk-tree`/`full-scan`）与 `visited`
  （实际计算距离的词条数），可观察剪枝效果。

## 运行

```bash
python3 -m unittest discover -s tests -v   # 测试
python3 -m uniedit.demo                    # 运行样例
```

## 运行样例（python3 -m uniedit.demo，节选）

```
文档（合成 UTF-8）：Apple apple APPLE apply apples applet café CAFE caffe café-au-lait 中文 中文测试 中问 中问讯 Straße strasse STRASSE naïve naive résumé resume 北京 背景
索引词条数（规范词去重后）：20

== 阈值查询 q='apple' max_dist=1（度量，BK 树） ==
mode=bk-tree metric_ok=True visited=4/20
  Apple        norm=apple      dist=0.0  pos=0
  apply        norm=apply      dist=1.0  pos=3
  apples       norm=apples     dist=1.0  pos=4
  applet       norm=applet     dist=1.0  pos=5

== TopK 查询 q='café' k=3（等距候选完整） ==
mode=bk-tree metric_ok=True visited=18/20
  café         norm=café       dist=0.0  pos=6
  CAFE         norm=cafe       dist=1.0  pos=7
  caffe        norm=caffe      dist=2.0  pos=8

== 阈值查询 q='中文' max_dist=1（CJK） ==
mode=bk-tree metric_ok=True visited=9/20
  中文           norm=中文         dist=0.0  pos=12
  中问           norm=中问         dist=1.0  pos=14

== 换位被支配 trans=2：q='café' max_dist=2（仍走 BK 树） ==
mode=bk-tree metric_ok=True visited=8/20
  café / CAFE / caffe（dist 0/1/2）

== 换位过便宜 trans=1：q='café' max_dist=2（自动回退全扫描） ==
mode=full-scan metric_ok=False visited=19/20
  café / CAFE / caffe（dist 0/1/2）
  校验报告：702 条违规，示例：transpose('a','c')=1.0 < 无换位距离 2.0：换位未被支配，三角不等式可能失效
  与全扫描参考一致：True（不漏词）
```

## 测试结果

```
$ python3 -m unittest discover -s tests -v
...（36 个用例：DP 正确性与 Dijkstra 脚本最小成本对拍、引理 L1 经验验证、
     长度下界安全性、度量接受/拒绝、破坏三角不等式的成本构造、
     BK 树与全扫描参考一致性（含随机 Unicode 词集）、非度量回退不漏词、
     阈值含等号、规范词去重、TopK 等距完整、稳定排序、分词规则）
----------------------------------------------------------------------
Ran 36 tests in 0.200s

OK
```
