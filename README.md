# 可撤回身份合并（Retractable Identity Merge）

主体别名关系建模为**无向多重图**：每条边携带若干独立证据 ID，同一条边只要还剩至少一条证据就仍然成立。查询返回连通分量内最小 ID 作为规范身份；撤回证据可能导致分量分裂，此时受影响的规范身份会被重算。

**为什么不并查集**：并查集不支持删边。本实现直接维护邻接多重图，查询时对当前图 BFS 重算连通分量（脏标记缓存，批量查询只算一次），删除语义天然正确。

## 运行

```bash
python3.11 identity_merge.py [输入文件]   # 缺省读 stdin
python3.11 -m unittest test_identity_merge -v
```

仅依赖 Python 3.11 标准库。

## 输入格式

每行一条命令；`#` 开头为注释，空行忽略。ID 为不含空白的字符串。

| 命令 | 含义 |
|---|---|
| `ADD <id_a> <id_b> <evidence_id>` | 添加一条证据，支持两主体的别名边（重复边需不同证据 ID） |
| `RETRACT <evidence_id>` | 撤回证据；边无剩余证据时边消失，分量可能分裂 |
| `QUERY <id>` | 查询规范身份并输出路径证据 |
| `BATCH <id_1> <id_2> ...` | 离线批量查询，连通分量只重算一次 |

## 输出格式

- `ADD` / `RETRACT` 成功输出 `OK`；失败（重复证据 ID、未知证据、自环）输出错误到 stderr 并以退出码 1 终止。
- `QUERY` / `BATCH` 每行一个结果：

```
QUERY <id> canonical=<规范ID> path=<id> -[证据ID]-> <中间ID> -[证据ID]-> ... -> <规范ID>
```

- `id` 即规范身份（或图中未知）时 `path` 仅含其自身，未知 ID 的规范身份是其自身。
- 路径证据为 BFS 最短路径，每条边引用一条当前仍存在的证据 ID。

## 示例（合成数据）

输入：

```
ADD alice ali e1
ADD ali al  e2
ADD alice ali e4
QUERY al
RETRACT e2
RETRACT e1
BATCH alice ali al
```

输出：

```
OK
OK
OK
QUERY al canonical=al path=al
OK
OK
QUERY alice canonical=ali path=alice -[e4]-> ali
QUERY ali canonical=ali path=ali
QUERY al canonical=al path=al
```

撤回 `e2` 后 `al` 独立成组；`alice-ali` 边因仍有证据 `e4` 而保持连通，规范身份由 `al` 重算为 `ali`。

## 文件

- `identity_merge.py` — 实现与 CLI
- `test_identity_merge.py` — unittest（17 个用例，含全部验收边界）
