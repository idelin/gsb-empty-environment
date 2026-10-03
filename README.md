# cdcundo — 反向补偿重放与数据回滚包生成器

基于 CDC（Change Data Capture）前后镜像的灾备引擎，纯 Go 标准库实现。
捕获每条变更的前置镜像（Before Image）与后置镜像（After Image），
在人为误操作后逆向生成补偿 DML 序列，执行快速冲正。

## 架构

| 文件 | 职责 |
| --- | --- |
| `event.go` | `ChangeEvent`（Before/After 镜像）、`CDCLog` 只追加日志 |
| `store.go` | 内存数据库：所有写操作自动捕获前后镜像写入 CDC 日志 |
| `undo.go` | 回滚包生成器：按事务 / 毫秒级时间窗口圈定事件，逆序生成补偿 DML |
| `sql.go` | SQL 渲染：标识符引用、字符串转义、NULL / 布尔 / 毫秒时间字面量 |
| `replay.go` | 补偿重放器：直通应用回滚包（不再污染 CDC 日志），快照比对校验 |
| `cmd/demo` | 验收场景运行样例 |

## 补偿规则

| 原操作 | 补偿操作 |
| --- | --- |
| INSERT | DELETE（按主键删除误插入行） |
| UPDATE | UPDATE（用 Before Image 反向赋值） |
| DELETE | INSERT（用 Before Image 整行补回） |

事件按 CDC 序号严格逆序处理，同一行上的多次变更被精确倒放。

## 运行样例

```sh
go run ./cmd/demo
```

输出：

```
[1] 初始化完成：users 表 1000 行，CDC 日志 1000 条
[2] 误操作发生：事务 txn-1001 清空 1000 行用户的 email/phone/status（窗口 09:27:11.368 ~ 09:27:11.389）
    破坏确认：id=1 -> email="" phone="" status=""
[3] 回滚包生成：1000 条反向补偿 DML（按 CDC 序号逆序）
    样例 SQL（前 2 条 / 后 1 条）：
      UPDATE "users" SET "balance" = 1498.5, "email" = 'user0999@example.com', ... WHERE "id" = 999;
      UPDATE "users" SET "balance" = 1497, "email" = 'user0998@example.com', ... WHERE "id" = 998;
      UPDATE "users" SET "balance" = 1.5, "email" = 'user0001@example.com', ... WHERE "id" = 1;
[4] 重放完成，耗时 41ms
[5] 校验通过：1000 行全部恢复到误操作前原貌，id=1 -> email="user0001@example.com" ...
```

## 使用示例

```go
log := cdcundo.NewCDCLog()
db := cdcundo.NewDatabase(log)
db.CreateTable("users", "id")

// ... 业务写入，CDC 自动捕获 Before/After 镜像 ...

// 误操作后：按事务 + 毫秒级时间窗口圈定事件，生成回滚包
script := cdcundo.Generate(log, cdcundo.Filter{
    Table: "users",
    TxnID: badTxn,
    From:  incidentStart,
    To:    incidentEnd,
})
fmt.Print(script.SQL())        // 导出 SQL 脚本交给真实数据库执行
cdcundo.Replay(db, script)     // 或直接对内存库重放冲正
```

## 测试

```sh
go test ./...
```

覆盖：1000 行误清空验收场景、INSERT/DELETE 补偿、同行多次变更逆序、
事务隔离（不误伤正常事务）、SQL 转义与 NULL/布尔/毫秒时间字面量、毫秒级时间窗口过滤。
