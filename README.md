# 撤回式排名游标（Pinned-Version Leaderboard Cursor）

排行榜分页游标，排序为**分数降序、ID 升序**。纯 Python 3.11 标准库实现，无第三方依赖。所有示例数据均为合成数据。

## 核心语义

- **不可变版本**：每次 `set`/`delete` 真实变更产生一个新的不可变快照版本。
- **游标钉版**：`open` 打开的游标固定在打开时的最新版本上；之后的分数修订与删除不会导致该游标重复或漏项。新打开的游标看到新版本。
- **租约回收**：游标持有租约（默认 60 秒，每次成功翻页滑动续租）。租约过期后翻页**明确报错**（`cursor_expired`），绝不悄悄切换版本。`gc` 回收过期游标及无存活租约引用的旧版本；最新版本始终保留。
- **显式撤回**：`close` 可主动释放游标租约，使其钉住的版本可被回收。

## 运行

```sh
python3 leaderboard.py [--ttl 秒数]   # 从 stdin 读交互式读取命令
python3 -m unittest -v                # 运行测试
```

## 输入格式（stdin，每行一条命令）

| 命令 | 说明 |
| --- | --- |
| `set <id> <score>` | 插入或修订分数，产生新版本 |
| `del <id>` | 删除条目（不存在则不产生新版本） |
| `open` | 打开游标，固定在当前最新版本 |
| `page <cursor> <n>` | 取下一页，最多 n 条 |
| `close <cursor>` | 显式撤回游标 |
| `gc` | 回收过期游标与无引用旧版本 |
| `version` | 显示当前最新版本号 |
| `help` / `quit` | 帮助 / 退出 |

## 输出格式（stdout，JSON Lines，每行一个 JSON 对象）

- 成功：`{"ok": true, ...}`，如 `{"ok": true, "cursor": "c1", "version": 4}`、`{"ok": true, "entries": [{"id": "a", "score": 10.0}], "done": false}`
- 游标过期：`{"ok": false, "error": "cursor_expired: ..."}`
- 参数错误：`{"ok": false, "error": "bad_request: ..."}`

## 验收边界示例

```
set a 10 / set b 9 / set c 8        # 版本含 a10 b9 c8
open -> c1; page c1 1 -> [a]        # 第一页为 a
del b                               # 产生新版本
page c1 10 -> [b, c]                # 旧游标仍返回 b c
open -> c2; page c2 10 -> [a, c]    # 新游标返回 a c
```

## 文件

- `leaderboard.py` — 实现（`Leaderboard` 类）+ CLI
- `test_leaderboard.py` — unittest：验收边界、租约过期、版本回收、并发更新与分页、CLI 端到端
