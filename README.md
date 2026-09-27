# 增量集合差异证明

两端各维护「键 → (版本, 值)」映射与删除墓碑，通过分层哈希摘要树定位差异，
支持概率 / 确定两种核验模式，并统计传输条目数。仅依赖 Python 3.11 标准库。
所有示例数据均为合成数据。

## 运行

```bash
python3 reconcile.py [--mode exact|probabilistic] [--hash sha256|constant] local.json remote.json
python3 -m unittest test_reconcile -v
```

## 输入格式（两端各一个 JSON 文件）

```json
{
  "entries":    [{"key": "k1", "version": 2, "value": "hello"}],
  "tombstones": [{"key": "k2", "version": 5}]
}
```

- `entries`：存活条目，`version` 为单调递增的版本号。
- `tombstones`：删除墓碑，仅含 `key` 与 `version`。

## 输出格式（stdout，JSON）

```json
{
  "mode": "exact",
  "hash": "sha256",
  "differences": [
    {"key": "k1",
     "local":  {"key": "k1", "version": 1, "value": "a"},
     "remote": {"key": "k1", "version": 2, "value": "b"},
     "resolution": {"key": "k1", "version": 2, "value": "b"}}
  ],
  "merged": {"entries": [...], "tombstones": [...]},
  "stats": {"mode": "exact", "digest_comparisons": 3,
            "nodes_visited": 5, "entries_transmitted": 4},
  "mode_semantics": "..."
}
```

- `differences`：可复核的差异列表，逐条给出双方原始记录与合并裁决。
- `merged`：按合并规则的收敛结果。
- `stats.entries_transmitted`：协议消息中携带的条目总数（双向），
  用于对比两种模式的传输开销。

## 概率模式 vs 确定模式

| | probabilistic（概率） | exact（确定） |
|---|---|---|
| 摘要相等时 | 信任摘要，跳过子树 | 不信任，仍下钻逐条核验 |
| 哈希碰撞（如注入恒定哈希） | 可能漏报差异 | 必然报告全部差异 |
| 传输量 | 约 O(差异数 × 树深) | 最坏 O(全量条目) |
| 正确性 | 依赖哈希抗碰撞性 | 确定性保证 |

## 合并规则（确定性）

1. 版本高者胜；
2. 版本相同，墓碑胜存活值；
3. 双方均存活且版本相同，取字典序较大的值（仅为确定性兜底）。

因此墓碑版本高于存活值时，合并结果为删除（墓碑保留）。

## 已知限制

- 同步为单次全树遍历的模拟实现，未建模多轮网络交互；
  `entries_transmitted` 按叶子比对的条目数统计。
- 树深固定为 8 层十六进制前缀；键数量极大时叶子可能含多条目，仍正确。
- 未证明传输量为理论最优；exact 模式保证正确性而非最小传输。
