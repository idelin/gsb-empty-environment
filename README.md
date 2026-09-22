# 基于 NULL 拒绝性质的安全查询改写（Python 3.11+，纯标准库，无 SQL 引擎）

在本地合成关系表上分析谓词的 null 拒绝（null-rejecting）性质，只有在
语义等价有保证时才执行谓词下推或外连接化内连接；每条改写都附带前提
检查与反例测试。NULL 用 `None` 表示，关系为多重集合（bag），重复行
重数有意义。

## 1. 语义约定

- **三值逻辑（3VL）**：TRUE / FALSE / UNKNOWN。任何含 NULL 的比较
  （`NULL > 3`、`NULL = NULL` 等）结果都是 UNKNOWN；`WHERE` 只保留
  TRUE，UNKNOWN 与 FALSE 都被过滤。
- **NULL 拒绝**：谓词 p 在属性集 A 上拒绝 NULL，当且仅当 A 全为 NULL
  时 p 永不为 TRUE（即 p ∈ {FALSE, UNKNOWN}）。例如 `x > 3` 拒绝 NULL；
  `x IS NULL` 不拒绝（NULL 时为 TRUE）。
- **多重集合语义**：连接重数为两侧匹配行重数之积；过滤不去重；左外
  连接中每个未匹配的左行（含重复）都保留并补 NULL。

## 2. 改写规则与等价前提

| 规则 | 改写 | 等价前提 | 说明 |
|---|---|---|---|
| R1 | `σp(A ⋈ B) → σp(A) ⋈ B` | p 只引用 A 的列 | 内连接下推 |
| R2 | `σp(A ⟕ B) → σp(A) ⟕ B` | p 只引用**保留侧** A 的列 | 外连接只能下推到保留侧 |
| R3 | `σp(A ⟕ B) → σp(A ⋈ B)` | p 在 B（NULL 补给侧）上**拒绝 NULL** | 外连接化内连接 |
| R4 | R3 之后 `σp(A ⋈ B) → A ⋈ σp(B)` | R3 前提 + p 只引用 B 的列 | 化内后才能下推到右侧 |

前提检查在 `rewrites.py` 的 `pushdown_filter` / `outer_to_inner` /
`pushdown_after_conversion` 中强制执行；前提不满足时抛出
`PreconditionError`，拒绝改写。

## 3. 反例：右表 `IS NULL` 不可下推到右输入

把 `x IS NULL` 从 `L ⟕ R` 之上推到 `R` 之下会改变结果：

- 正确：先 `L ⟕ R`（未匹配左行补 NULL），再过滤 `x IS NULL` → 5 行
  （3 个未匹配行 + 2 个匹配到 `x=NULL` 的行）。
- 错误：先 `σ_{x IS NULL}(R)`（R 只剩 1 行），再左外连接 → 6 行
  （原本能匹配 `x=1`/`x=4` 的左行 id=1 被变成未匹配，多出 1 行）。

原因：连接后右表的 NULL 有两种来源——"没有匹配"（补的 NULL）与
"匹配了但值为 NULL"（数据里的 NULL），下推把前者也错误地制造了
出来。

## 4. 正例：右表 `x > 3` 可化内连接并下推

`x > 3` 在 `x` 为 NULL 时为 UNKNOWN（被 WHERE 过滤），即拒绝 NULL。
因此外连接产生的 NULL 填充行必然被过滤掉，外连接与内连接在过滤后
等价：

```
σ[x > 3](L ⟕[lk = rk] R)  ≡  σ[x > 3](L ⋈[lk = rk] R)  ≡  L ⋈[lk = rk] σ[x > 3](R)
```

样例数据下三者都恰好得到 3 行（id=1 匹配 x=5；id=2、3 匹配 x=4）。

## 5. NULL 拒绝分析判定（`rewrites.is_null_rejecting`）

- `x > 3`（比较/算术遇 NULL → UNKNOWN）→ 拒绝
- `x IS NOT NULL`（NULL 时为 FALSE）→ 拒绝
- `x IS NULL`（NULL 时为 TRUE）→ 不拒绝
- `p AND q`：任一侧拒绝即拒绝；`p OR q`：两侧都拒绝才拒绝
- `NOT (x > 3)`（NULL 时 UNKNOWN）→ 拒绝；`NOT (x IS NULL)`（NULL 时 TRUE）→ 不拒绝
- `x > 3 OR x IS NULL` → 不拒绝（NULL 时为 TRUE）

## 6. 运行

```bash
python3 demo.py            # 运行样例
python3 -m unittest -v     # 24 个测试：等价性 + 反例 + 重数保持
```

### 运行样例（`python3 demo.py` 输出节选）

```
-- null-rejection analysis on R's columns --
  x > 3        null-rejecting on {rk, x}: True
  x IS NULL    null-rejecting on {rk, x}: False

-- R3: sigma[x > 3](L LEFT JOIN R) --
  original : sigma[x > 3]((L LEFT JOIN[lk = rk] R))
  R3       : sigma[x > 3]((L JOIN[lk = rk] R))
  R3+R4    : (L JOIN[lk = rk] sigma[x > 3](R))

== result of original plan / rewritten plan (identical bag) ==
id | lk | rk | x
---+----+----+--
1  | 1  | 1  | 5
2  | 2  | 2  | 4
3  | 2  | 2  | 4

-- R2 refusal: sigma[x IS NULL](L LEFT JOIN R) --
  pushdown to right side: refused -> cannot push a predicate onto the
  null-supplying side of an outer join ...
  outer-to-inner: refused -> predicate is not null-rejecting ...

== counterexample: 正确 5 行 vs 朴素下推 6 行 ==
```

### 测试结果

```
Ran 24 tests in 0.001s
OK
```

覆盖：3VL 行为（NULL 比较 → UNKNOWN → 被 WHERE 过滤）、NULL 拒绝
分析 7 例、R1/R2 下推等价与拒绝、R3/R4 化内与下推等价与拒绝、
2 个反例（IS NULL 下推错误 5≠6 行、IS NULL 化内错误 5≠2 行）、
重数保持（内连接乘积重数 7 行、左外连接未匹配重复行保留 3 行、
改写前后 bag 完全一致、NULL 连接键永不匹配）。

## 文件

- `relalg.py` — 3VL、谓词表达式 AST、bag 关系与 select/inner/left join
- `rewrites.py` — NULL 拒绝分析、计划树、带前提检查的改写规则 R1–R4
- `test_rewrites.py` — 24 个 unittest（等价性、反例、重数）
- `demo.py` — 运行样例
