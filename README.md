# 按主体撤回的聚合物化视图

仅依赖 Python 3.11 标准库。所有示例数据均为合成数据。

## 语义

- **upsert**：事件携带主体 `subject`、事件 `event_id`、分组 `group`、整数值
  `value`、修订号 `revision`、同意代次 `generation`。同一 `(subject, event_id)`
  上，更高 `revision` 替换旧贡献；`<=` 旧修订号的事件被忽略。
- **retract**：删除主体全部贡献并留下墓碑（记录代次 `generation`）；此后代次
  `<=` 墓碑代次的事件一律忽略，旧贡献无法复活。
- **consent**：重新同意，必须携带**严格更高**的代次才生效。
- **查询**：`sum`（总和）、`count`（计数）、`median`（精确中位数，偶数个取中间
  两值均值），支持全局或按 `--group` 分组过滤。
- **对照**：`verify` 将增量维护的聚合结果与从贡献记录全量重算结果逐一比对。
- **持久化**：命令日志（JSONL）追加持久化，重启后回放恢复；亦可导出/加载快照。

## 输入格式（JSONL，每行一条命令）

```json
{"type":"upsert","subject":"s1","event_id":"e1","group":"g","value":2,"revision":1,"generation":1}
{"type":"retract","subject":"s1","generation":1}
{"type":"consent","subject":"s1","generation":2}
```

字段说明：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `type` | string | `upsert` / `retract` / `consent` |
| `subject` | string | 主体标识 |
| `event_id` | string | 事件标识（仅 upsert） |
| `group` | string | 分组（仅 upsert） |
| `value` | int | 整数值（仅 upsert） |
| `revision` | int | 修订号（仅 upsert） |
| `generation` | int | 同意代次 |

## 输出格式

- `apply` 每行输出：
  `{"line": 行号, "applied": 是否生效, "reason": 原因, "result": {"sum","count","median"}}`
  `reason` 取值：`applied`、`stale_generation`、`subject_retracted`、
  `stale_revision`、`generation_not_higher`。
- `query` 输出：`{"sum": int, "count": int, "median": number|null}`。
- `verify` 输出各分组 `incremental` 与 `recomputed` 对照及 `match`；全部一致时
  退出码为 0，否则为 1。

## 命令行用法

```bash
python3 cli.py apply --db view.journal examples/acceptance.jsonl   # 应用命令并输出增量结果
python3 cli.py apply --db view.journal < commands.jsonl            # 从 stdin 读取
python3 cli.py query --db view.journal                             # 重启回放后查询（全局）
python3 cli.py query --db view.journal --group g                   # 按分组查询
python3 cli.py verify --db view.journal                            # 增量 vs 全量重算对照
python3 cli.py snapshot --db view.journal --out snap.json          # 导出快照
python3 cli.py query --snapshot snap.json                          # 从快照重启查询
```

## 验收边界示例

`examples/acceptance.jsonl`（合成数据）覆盖：同主体两事件值 2 和 8 → 中位数 5；
撤回后计数 0；旧代次值 100 重放仍被拒绝（计数 0）；更高代次重新同意后值 3 只计 3。

## 测试

```bash
python3 -m unittest test_retractable_view -v
```

## 文件

- `retractable_view.py` — 核心视图（增量聚合、墓碑、全量重算对照、快照）
- `cli.py` — 命令行入口
- `test_retractable_view.py` — unittest 测试
- `examples/acceptance.jsonl` — 验收边界示例数据（合成）
