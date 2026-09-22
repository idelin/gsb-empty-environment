# distagg — 可分解分布式聚合（纯标准库，无 SQL 引擎）

在本地合成关系表（Python 元组的多重集合），把聚合拆成**本地阶段**与**全局阶段**：

- **本地阶段** `local_aggregate`：每个分片把行折叠成小型中间状态
- **全局阶段** `merge_states` + `finalize`：合并各分片状态并产出最终结果

## 分解规则

| 聚合 | 中间状态 | 说明 |
| --- | --- | --- |
| `COUNT(*)` / `COUNT(col)` | 整数计数 | `COUNT(*)` 计行数（含 NULL），`COUNT(col)` 跳过 NULL |
| `SUM` | 累计和 | 未见非 NULL 值前为 `None`；全 NULL 求和返回 NULL |
| `AVG` | `(sum, count)` | 保留 sum 与 count，不能直接平均分片均值 |
| `MIN` / `MAX` | 当前最优值 | 忽略 NULL |
| `COUNT/SUM/AVG(DISTINCT col)` | 非 NULL 值集合 | DISTINCT 需要额外状态（集合），跨分片去重 |

## 语义约定

- **NULL 语义**：`None` 表示 NULL；除 `COUNT(*)` 外所有聚合忽略 NULL；
  全 NULL 列的 `SUM/AVG/MIN/MAX` 返回 NULL，`COUNT` 返回 0 —— 与空表语义一致
- **多重集合语义**：重复行是真实行，按出现次数计入；只有 DISTINCT 聚合去重
- **空分片**：只贡献单位元状态（如 AVG 的 `(0, 0)`），不进入分母
- **AVG 陷阱**：均值之均值 ≠ 全局均值（分片大小不等时），必须合并 `(sum, count)`

## 运行

```sh
python3.11 demo.py            # 运行样例
python3.11 -m unittest -v     # 运行测试（29 个用例）
```

## 文件

- `distagg.py` — 核心库：Table、聚合状态、本地/全局两阶段求值
- `test_distagg.py` — unittest：NULL 语义、空表、分解正确性、DISTINCT、多重集合
- `demo.py` — 运行样例：分片中间状态、AVG 陷阱、全 NULL SUM、空表
