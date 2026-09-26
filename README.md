# 可撤回身份合并（Retractable Identity Merge）

纯 Python 3.11 标准库实现，无第三方依赖。所有示例数据均为合成数据。

## 模型

- 主体别名关系为**无向边**，每条边可有**多条独立证据**支持。
- 规范身份（canonical ID）= 当前连通分量内的最小 ID。
- 撤回证据时，若某边不再有任何证据则该边消失，**连通分量整体重算**
  （BFS），因此天然支持分量分裂；未使用不支持删除的并查集。
- 出现过的主体在边消失后仍作为独立身份存在（规范 ID 为自身）。

## 运行

```sh
python3 identity_merge.py [commands_file]   # 省略文件则从 stdin 读取
python3 -m unittest -v test_identity_merge  # 运行测试
```

## 输入格式（每行一条命令，`#` 开头为注释）

| 命令 | 含义 |
|---|---|
| `add <id1> <id2> <evidence_id>` | 为边 id1–id2 添加一条证据 |
| `retract <evidence_id>` | 撤回一条证据 |
| `query <id1> <id2>` | 查询两 ID 是否同属一个规范身份，并输出路径证据 |
| `canon <id>` | 查询某 ID 当前的规范身份 |

## 输出格式（每行对应一条命令的结果）

- `OK add <id1> <id2> <evidence_id>`
- `OK retract <evidence_id>` / `ERROR retract <evidence_id> : unknown evidence`
- `SAME canonical=<c> path=a -[e1]- b -[e2]- c`（路径中每步标注支撑证据 ID）
- `DIFFERENT canonical_a=<c1> canonical_b=<c2>`
- `canon` 直接输出规范 ID；未知主体输出 `UNKNOWN <id>`
- 格式错误输出 `ERROR line <n>: ...`

## 示例（合成数据）

```sh
$ printf 'add a b e1\nadd b c e2\nquery a c\nretract e2\ncanon c\nquery a b\n' | python3 identity_merge.py
OK add a b e1
OK add b c e2
SAME canonical=a path=a -[e1]- b -[e2]- c
OK retract e2
c
SAME canonical=a path=a -[e1]- b
```

## 文件

- `identity_merge.py` — 核心 `IdentityMerger` 类 + 批量命令行接口
- `test_identity_merge.py` — 14 个 unittest 用例，覆盖全部验收边界
