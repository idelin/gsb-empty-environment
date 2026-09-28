# unitconv — 事务性精确有理单位转换

纯 Python 3.11 标准库实现。单位转换规则构成一张有向有理转换图：

- 所有因子用 `fractions.Fraction` **精确**表示，多跳推导不产生浮点误差，
  代码中**不存在任何 epsilon / 容差阈值**——两个因子只有“完全相等”或“矛盾”。
- 每次 `batch` 是一次**事务**：批量语句中只要有一条解析失败、形成矛盾环或
  跨维度连接，整批拒绝，原图与磁盘文件都保持不变，版本号也不增加。
- 所有已提交版本永久保留，可用 `-v/--at-version` 对旧版本重放查询。
- 维度（dimension）是可选的显式标签（如 `LENGTH`、`MASS`）；跨维度查询/规则
  被明确拒绝。无标签的连通分量不做任何隐式维度猜测。

所有示例数据均为合成数据。

## 运行环境

- Python 3.11（仅标准库；已在 `python3.11` 上实测，3.12+ 同样可运行）
- 无第三方依赖

## 命令行

```bash
python3.11 -m unitconv [--file PATH] [--json] <command> [options]
```

默认存储文件为当前目录的 `unitconv_store.json`，可用 `-f/--file` 指定
（放在子命令前后均可）。

| 命令 | 说明 |
| --- | --- |
| `init` | 创建空存储文件（已存在则报错） |
| `batch STMT... [--from-file FILE]` | 原子地提交一批语句 |
| `query SOURCE TARGET [AMOUNT] [-v N]` | 精确换算，默认数量 1，可查历史版本 |
| `versions` | 列出所有保留版本 |
| `rules [-v N]` | 列出该版本已提交的语句 |
| `units [-v N]` | 列出已知单位与维度标签 |

`--json` 对 `init/batch/query/versions/rules/units` 输出机器可读 JSON；
其中所有有理数以 `p/q` 字符串给出（分母为 1 时就是整数串）。

### 语句语法（大小写敏感，空白不敏感）

```text
dim: LENGTH m        # 声明单位 m 属于维度 LENGTH
A = 2 B              # 断言 A = 2 * B
A = B / 6            # 断言 A = B / 6，即 A = (1/6) B
A = 1/6 B            # 等价写法
A = 0.1 B            # 精确小数 1/10（不是二进制浮点 0.1）
A = B                # A 与 B 相等
```

- 单位名：非空、不含空白和 `=`、`:`、`/`。
- 因子：正整数、正小数或 `p/q`；必须为正，`0`、负数、无法解析者直接拒绝。
- 一个 batch 内维度声明先于规则生效，所以语句顺序不影响一致性判定。
- `--from-file` 每行一条语句，空行与 `#` 开头的行会被忽略。

### 查询输出（文本）

```text
$ python3.11 -m unitconv query A C
1 A = 6 C
factor: 1 A = 6 C
path: A =2> B =3> C
version: 1
```

路径上的 `=2>` 表示沿该有向边“源单位 = 2 × 下一单位”；反向走边时会自动给出
倒数（如 `=1/3>`）。

### 矛盾时的输出

```text
$ python3.11 -m unitconv batch "A = 5 C"
error: contradictory rule rejected; existing graph is unchanged
  existing derivation : A = 6 C
  rejected rule       : A = 5 C
  contradiction path  : A =2> B =3> C
  closed cycle        : existing path A -> C conflicts with new edge A -> C
# 退出码 3，存储文件未改动，版本号不变
```

### 退出码（稳定约定，便于脚本判断）

| 码 | 含义 |
| --- | --- |
| 0 | 成功 |
| 2 | 语句/参数非法（解析失败、维度标签冲突） |
| 3 | 矛盾环，整批拒绝 |
| 4 | 跨维度查询/规则 |
| 5 | 两单位不在同一连通分量（无转换路径） |
| 6 | 查询引用了未定义单位 |
| 7 | 存储文件不存在（先 `init`） |
| 8 | `init` 时存储文件已存在 |

## 存储文件格式

JSON，仅保存语句日志；加载时逐条重放并重新校验，天然保证版本可重建：

```json
{
  "format": "unitconv-store-v1",
  "batches": [
    {"version": 1, "timestamp": "2026-09-28T21:02:18+00:00",
     "statements": ["A = 2 B", "B = 3 C"]}
  ]
}
```

写入采用“临时文件 + `os.replace`”原子替换；被拒绝的批次不会落盘。

## Python API

```python
from fractions import Fraction
from unitconv import VersionedGraph, ContradictionError

g = VersionedGraph()
g.batch(["A = 2 B", "B = 3 C"])

g.convert("A", "C").factor                 # Fraction(6, 1)
g.convert("C", "A", Fraction(6)).result    # Fraction(1, 1)
g.state(0).units()                         # [] —— 版本 0 恒为空图

try:
    g.batch(["A = 5 C"])
except ContradictionError as exc:
    exc.expected     # Fraction(6)，图上已有的精确因子
    exc.claimed      # Fraction(5)，被拒绝的因子
    exc.path         # [("A", 2), ("B", 3), ("C", None)]，矛盾路径
```

`VersionedGraph.convert(source, target, amount=Fraction(1), version=None)`
返回 `ConversionResult(factor, amount, result, path)`，全部是精确 `Fraction`。

## 内部模型（简述）

- **加权并查集**：`ratio[u] = u / parent[u]`，路径压缩时做精确分数乘除；
  两单位在同一集合即可在 O(α(n)) 内得到精确比值。
- **邻接表 + BFS**：仅用于输出可审计的“矛盾路径/推导路径”，不参与数值计算。
- **维度**：每个单位可带显式标签；合并两个集合时若两边标签不同则拒绝，
  否则标签向合并后的根传播。
- **事务**：`batch` 在 `GraphState.clone()` 上操作，成功才追加为新版本；
  失败丢弃副本，活动状态字节级不变。

## 测试

```bash
python3.11 -m unittest discover -s tests -v
```

覆盖：两跳推导、矛盾整批回滚（含“新单位必须随失败批次消失”）、倒数转换、
维度冲突/不匹配、历史版本重放、0.1 十连跳精确性、存储落盘/重放/原子性、
CLI 端到端与退出码。

## 已知边界（未做的事）

- 未做单位量纲代数（例如自动推出 `m/s`）；维度只是等价类标签，不是可组合的
  量纲表达式——这是显式的范围取舍。
- 版本以“每次成功 batch 追加一份完整状态”保留在内存中；日志可无限增长，
  未实现压缩/截断，以换取旧查询随时可重放。
- 未声明线程安全；多进程写同一存储文件未加文件锁。
