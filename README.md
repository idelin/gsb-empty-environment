# cdc-replay

离线 CDC 重放器：从 stdin 读取交错事务事件，按提交顺序重放，输出最终公共表的嵌套 JSON。

## 输入

一个 JSON 数组，每个事件是字符串数组，前两列为事务 ID 与动作：

- `[txn, "set", row, field, value]` 缓冲一次字段写入
- `[txn, "del", row]` 缓冲一次整行删除
- `[txn, "save", name]` 建立保存点
- `[txn, "undo", name]` 回到该事务最近的同名保存点
- `[txn, "commit"]` / `[txn, "abort"]` 结束事务

## 语义

- 事务只缓冲操作日志（`set`/`del`），提交前不触碰公共表；abort 整体丢弃。
- 保存点只记录操作日志的偏移量，不复制整表。
- `undo` 从栈顶找最近同名保存点：操作日志截断到该点，保存点本身保留，
  其后建立的保存点全部丢弃；同名保存点形成遮蔽（栈顶优先命中）。
- `commit` 时把剩余操作按原序作用于当时的公共表：`set` 只改单个字段，
  `del` 才删整行，因此不会用启动时的整行副本覆盖并发提交的其他字段。

## 运行

```sh
go build -o cdc-replay .
cat events.json | ./cdc-replay
go test ./...
```
