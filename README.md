# 撤回式排名游标（Snapshot-Pinned Ranking Cursor）

排行榜分页游标组件。排序规则：**分数降序、ID 升序**。仅依赖 Python 3.11 标准库。

## 语义

- 每次 `set` / `delete` 发布一个**不可变版本**（v1, v2, ...）。
- 游标在打开时**钉住当前版本**：之后的分数修订与删除不会导致该游标重复或漏项。
- **新游标看到新版本**。
- 版本由**租约**保护：活跃游标钉住的版本不会被 `gc` 回收；租约过期后游标被回收，
  再次取页**显式报 `CURSOR_EXPIRED`**，绝不悄悄切换到新版本。
- 成功取页会续租；当前版本永不回收。
- 线程安全：所有公共操作由同一把锁保护。

## 命令行用法

```
python3 leaderboard.py [--lease-seconds 60]
```

从 stdin 逐行读命令，结果逐行写 stdout。

### 输入命令

| 命令 | 说明 |
|---|---|
| `set <id> <score>` | 写入/修订分数 |
| `delete <id>` | 删除条目 |
| `open` | 在当前版本打开游标 |
| `page <token> <n>` | 取下一页（n 条） |
| `gc` | 清理过期游标、回收无引用旧版本 |
| `snapshot` | 打印当前版本内容 |
| `quit` | 退出 |

### 输出格式

| 输出 | 含义 |
|---|---|
| `OK v<version>` | set/delete 成功 |
| `CURSOR <token> v<version>` | open 成功 |
| `PAGE <id:score> ... NEXT <token\|END>` | 一页结果；`END` 表示到底 |
| `GC v<id> ...` / `GC NONE` | 被回收的版本 |
| `SNAP v<version> <id:score> ...` | 当前快照 |
| `ERROR <CODE> <msg>` | `CURSOR_EXPIRED` / `UNKNOWN_CURSOR` / `NOT_FOUND` / `BAD_INPUT` / `UNKNOWN_COMMAND` |

### 示例（验收边界）

```
$ printf 'set a 10\nset b 9\nset c 8\nopen\npage cur-1 1\ndelete b\npage cur-1 1\npage cur-1 1\nopen\npage cur-2 10\nquit\n' | python3 leaderboard.py
OK v2
OK v3
OK v4
CURSOR cur-1 v4
PAGE a:10 NEXT cur-1        <- 版本1第一页为 a
OK v5                        <- 删除 b，发布 v5
PAGE b:9 NEXT cur-1          <- 旧游标仍返回 b
PAGE c:8 NEXT END            <- 旧游标仍返回 c
CURSOR cur-2 v5
PAGE a:10 c:8 NEXT END       <- 新游标返回 a c（无 b）
```

## 测试

```
python3 -m unittest test_leaderboard -v
```

覆盖：验收边界、排序与分数修订、租约过期/续租/回收、过期游标显式报错、
多线程并发更新下的分页一致性（无重复、无遗漏）、CLI 端到端流程。

所有示例数据均为合成数据。
