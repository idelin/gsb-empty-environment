# nestedmp：嵌套多部件流式解析与事务隔离

`nestedmp` 把验证后的嵌套表单附件以流式方式交给对象库，重点解决多个同时
开启的嵌套表单使用相同前缀分隔符时的隔离问题。它只依赖 Go 标准库。

## 线格式（按字节定义）

- 行尾只接受 CRLF；单独的 LF 或 CR 不构成行尾。
- 分隔行只在行首匹配两个连字号、完整边界名和 CRLF：`--<name>\r\n`。
- 结束行在边界名后另有两个连字号：`--<name>--\r\n`。
- 边界名为 1–70 个 ASCII 字母数字，严格区分大小写。
- 同一活动栈中内外层禁止使用完全相同的边界名。
- 每个部件头含 `Type`、唯一 `Name`、`SHA-256` 摘要；空行后的正文允许
  任意字节；`container` 部件通过 `Boundary` 头给出子边界。
- 紧邻分隔行的 CRLF 属于分隔行，不属于正文；空正文与以 CRLF 结尾的
  正文都能精确表示。
- 每层摘要绑定去除该层自身定界符后的原始正文。容器层直接对线上原始
  字节（含内层定界行）计算，不使用任何重新编码的子对象。

## 宿主接口

宿主实现 `StagingHost` 与 `StagingTxn`：

```go
type StagingHost interface {
    Begin(ctx context.Context) (StagingTxn, error)
}
type StagingTxn interface {
    Put(ctx context.Context, partName string) (io.WriteCloser, error)
    Commit(ctx context.Context) error
    Rollback(ctx context.Context) error
}
```

一个解析器生命周期内只使用一个事务：成功才 `Commit`，任何失败（摘要不符、
语法错误、取消、内存超限、写入失败）都 `Rollback`。`Commit` 可以迟到
（阻塞）；在它成功返回前，已写入底层的暂存对象必须保持不可读。

## 使用

```go
p, err := nestedmp.New(ctx, nestedmp.Options{
    RootBoundary: "AB",
    Host:         myHost,
    Window:       4096,
    MemoryBudget: 1 << 20,
    Tracer:       myTracer, // 可选：接收字节位置与写入回调序列
})
// Write 接受任意切块；宿主写入端阻塞时 Write 同步阻塞，形成背压。
if _, err := p.WriteCtx(ctx, chunk); err != nil { /* 已自动回滚 */ }
res, err := p.CloseCtx(ctx) // 成功时提交并返回各部件字节位置
```

取消可从其他 goroutine 调用 `p.Cancel()`，或让传入的 ctx 结束；后续
写入/关闭快速失败并回滚整个事务。

## 内存模型

常驻内存为固定写入窗口，加上各活跃边界匹配器的结构性持有（单匹配器
上限 `len(name)+8` 字节）与头块缓冲。推入新一层导致超过 `MemoryBudget`
时，解析立即终止并回滚。

## 参考解析与差分

`ReferenceParse` 是独立的一次性全量解析器，基于切片与 `bytes` 索引的
递归下降实现，不共享流式匹配器的状态机代码。测试对同一原始输入在
所有切点和大量随机分割下同时跑两套解析，逐部件比对摘要、字节位置与
暂存字节，覆盖父子边界互为前缀、空正文、正文内部分分隔行、父摘要失败、
提交迟到等重点场景。
