# 标签授权跨对象事务

资源创建流程可同时设置访问标签，不能先建立对象再短暂赋予宽权限。为 Go
资源宿主实现创建事务组件，校验请求标签、继承标签和不可覆盖标签的最终合并结果后
才分配可见对象；权限检查绑定最终标签摘要。用可失败存储模拟对象创建与标签持久化
各点，确认外部读者只见到完整授权状态。

## 安全不变量

- 最终标签集在第一次调用存储前完成校验并计算摘要，失败请求不会创建任何对象。
- 合并优先级为 `不可覆盖标签 > 请求标签 > 继承标签`；请求或继承标签与不可覆盖
  标签冲突时直接拒绝，不会静默放宽或收窄权限。
- 对象先进入不可见的暂存区，标签持久化成功并通过摘要复核后才原子发布。读者在
  任何暂存阶段都只能得到 `ErrObjectNotFound`。
- 每次创建事务持有独立令牌，失败清理不能删除其他创建者或已发布的对象。
- 读取路径重新校验对象存储中的标签摘要；权限检查使用同一不可变快照的最终标签和
  摘要，`Access.Verify` 在摘要不一致时失败关闭。
- 标签使用带长度前缀的规范编码计算 SHA-256，避免不同键值组合产生相同输入。

## 用法

```go
store := netsec.NewMemoryStore()
host, err := netsec.NewHost(store,
    netsec.WithInheritedLabels(netsec.Labels{"tenant": "acme"}),
    netsec.WithMandatoryLabels(netsec.Labels{"classification": "secret"}),
    netsec.WithAuthorizer(netsec.LabelAuthorizer{
        Key: "classification", Value: "secret",
    }),
)

obj, err := host.Create(ctx, netsec.CreateRequest{
    ID:     "doc-1",
    Kind:   "document",
    Data:   []byte("hello"),
    Labels: netsec.Labels{"owner": "alice"},
})

read, err := host.Read(ctx, "alice", "doc-1")
```

`Create` 依次调用 `CreateObject -> PersistLabels -> Publish`。`Store.Get` 只返回
已发布对象，暂存对象对普通读者不可见。`MemoryStore.SetFaults` 和
`MemoryStore.FailNext` 可在每个操作边界注入错误，用于验证失败回滚和可见性。
故障在操作生效前返回；通过让后续步骤失败，可以覆盖“对象壳已暂存”“标签已持久化
但尚未发布”“回滚失败留下暂存对象”等中间态。
外部读取应使用会执行授权的 `Host.Read`；`Host.Get` 仅供可信内部调用，不执行
权限检查。

## 测试

```sh
go test ./...
go test -race ./...
```

测试覆盖标签校验与合并、摘要规范性、创建和持久化各点故障、清理失败、并发重复
创建、摘要篡改，以及读者在 `object-staged`、`labels-persisted`、
`before-publish` 三个协作点上的可见性。
