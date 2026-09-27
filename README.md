# 分层保留策略删除计划（Hierarchical Retention Deletion Planner）

Python 3.11，仅标准库。所有示例数据均为合成数据。

## 语义模型

文件的可删除时间同时受三类约束：

1. **目录继承保留期**：文件继承其所有祖先目录的保留下界（取最大）；
2. **文件自有保留期**：文件自身的保留下界；
3. **法律保留标记**：文件自身或任一祖先目录带有 legal hold 时，下界视为无限（不可删）。

**最早可删除时间 = 全部有效下界的最大值**；任一法律保留标记存在即为无限。

- 下界用绝对时间表示（`retention_until`）。
- 目录或文件**移动后**，其子树的继承约束立即重算（版本号递增、索引更新）。
- **计划（plan）基于版本快照**：每个条目记录 `(id, version, earliest)`。
  执行时若节点版本与快照不一致（移动/改保留期/改 hold 均会递增版本），
  该条目被跳过并给出理由；节点已删除、未到期的条目同样跳过并给出理由。
- **最早可删除索引**：最小堆 `(bound, version, id)`。时间推进为 O(1)，
  不做全量扫描；`due()` 只弹出已到期的堆顶条目，过期（stale）条目
  通过版本号比对惰性丢弃。每次变更的索引维护为 O(log n)。

## 命令行用法

```bash
python3.11 retention.py run examples/acceptance.json   # 运行场景文件
python3.11 retention.py run - < scenario.json              # 从 stdin 读取
```

输出为 JSON（stdout）：`{"results": [ <每个 op 的结果> ]}`。

## 输入格式（场景 JSON）

```json
{"ops": [ {"op": "..."}, ... ]}
```

| op | 字段 | 说明 |
|---|---|---|
| `mkdir` | `path`, `retention_until?`, `legal_hold?` | 创建目录（父目录须已存在） |
| `create` | `path`, `retention_until?`, `legal_hold?` | 创建文件 |
| `move` | `src`, `dst_dir` | 移动文件/目录，子树继承约束重算 |
| `set_retention` | `path`, `retention_until` | 修改保留下界 |
| `set_hold` | `path`, `on` | 设置/解除法律保留标记 |
| `delete` | `path` | 直接删除节点 |
| `tick` | `now` | 推进时钟（只允许前进） |
| `due` | — | 返回当前时刻可删除文件（来自堆索引） |
| `plan` | `id` | 生成版本快照计划并登记到 `id` |
| `execute` | `plan` | 在当前时刻执行指定计划 |

## 输出格式

- `mkdir`/`create`：`{"op", "path", "id"}`
- `move`/`set_retention`/`set_hold`：`{"op", "path", "version"}`
- `tick`：`{"op", "now"}`
- `due`：`{"op", "now", "due": [{"id", "path", "earliest"}, ...]}`（按到期时间升序）
- `plan`：`{"op", "plan": {"now", "entries": [{"id", "path", "version", "earliest"}]}}`；
  法律保留中的文件 `earliest` 为字符串 `"infinite"`
- `execute`：`{"op", "plan", "now", "results": [{"id", "path", "status", "reason"?}]}`；
  `status` 为 `deleted` 或 `skipped`，跳过时 `reason` 取值包括：
  `version changed: snapshot vX, current vY`、`node no longer exists`、
  `not yet due: earliest B > now T`、`legal hold in effect`

## 运行测试

```bash
python3.11 -m unittest -v
```

测试覆盖三条验收边界（父 20/文件 10/时刻 15 不可删；移到下界 5 目录后
时刻 15 可删；任一法律保留标记存在均不可删）、目录移动后子树重算、
版本快照跳过理由、以及堆索引的惰性过期与按需弹出（用 `heap_pops`
计数证明时间推进不做全量扫描）。
