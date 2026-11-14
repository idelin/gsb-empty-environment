# 打印作业属性安全组件

本包（`example.org/netsec/q0394`）提供可由 Go 打印服务导入的作业属性与文档绑定组件。它修复的问题是：打印服务信任作业属性中的 `job-uri`/队列字段，攻击者篡改后可读取其他用户队列中的文档。

## 安全模型

- **提交时绑定**：作业绑定认证后的 `submitter`、队列、文档 URI 和 SHA-256 内容摘要。绑定字段只来自服务端记录，不取自打印请求。
- **唯一规范 URI**：每个作业只接受 `job:///<queue>/<submitter>/<job-id>/<document-name>` 这一种规范形式。非规范转义、路径穿越、额外路径段、查询串、片段和 authority 均拒绝。
- **白名单属性**：属性名精确匹配、不做大小写归一化。未知键、空值、控制字符、非法身份片段、非法摘要、别名冲突全部整体拒绝。`ParseAttributesJSON` 还拒绝重复 JSON 成员，避免“后者覆盖”式注入。
- **只读原记录**：`Print` 永不用请求中的 URI 或队列作为存储键，只使用提交时记录的作用域与文档名；读取后重新计算并比对 SHA-256，摘要不符不返回任何字节。
- **原作业授权**：打印、取消、重打都要求调用者等于原作业提交者。取消后不可打印或重打；重打不会接受新的队列、URI、摘要或提交者，只是把原作业重新置为待打印。
- **物理隔离**：`FileStorage` 为每个作业创建独立目录，目录名对队列/提交者/作业 ID 做确定性哈希，避免大小写不敏感文件系统上的命名空间混叠；所有路径保持在存储根内。

## 集成示例

```go
store, err := netsec.NewFileStorage("/var/lib/print/jobs")
if err != nil {
    return err
}
service, err := netsec.NewService(store)
if err != nil {
    return err
}

// submitter 必须来自认证上下文；queue 应由服务端路由/授权上下文选择。
job, err := service.Submit(ctx, authenticatedUser, netsec.SubmitRequest{
    Queue:        "queue-a",
    DocumentName: "report.pdf",
    Content:      documentBytes,
    Attributes:   map[string]string{"copies": "2"},
})
if err != nil {
    return err
}

result, err := service.Print(ctx, authenticatedUser, job.ID, requestAttributes)
if err != nil {
    return err
}
// result.Document 已经过原作业授权、作用域校验和内容摘要校验，可交给打印管线。
_ = result.Document

// 取消与重打同样按原作业提交者授权。
_, err = service.Cancel(ctx, authenticatedUser, job.ID)
_, err = service.Reprint(ctx, authenticatedUser, job.ID)
```

主要入口：

- `ParseAttributes` / `ParseAttributesJSON`：白名单属性解析。
- `CanonicalJobURI` / `ParseJobURI` / `ValidateJobURI`：唯一 URI 与作用域校验。
- `BindJob` / `Job.Verify`：构造并复核作业安全绑定。
- `Service.Submit` / `Print` / `Cancel` / `Reprint`：完整生命周期。
- `Service.PrintByAttributes` / `CancelByAttributes` / `ReprintByAttributes`：供按属性路由的打印队列直接调用，仍从原作业记录取存储作用域。

HTTP/JSON 入口应直接调用 `ParseAttributesJSON(rawBody)`，不要先用
`json.Unmarshal` 解成 map，否则重复键在进入组件前就已经被“最后一个值覆盖”。

## 验证

```sh
go test ./...
go test -race ./...
```

测试覆盖以下场景，并断言攻击失败时不返回任何文档字节：

- URI 替换（`job-uri`/`document-uri` 指向他人文档）
- 跨队列、跨用户引用与越权取消/重打
- 属性注入（未知/大小写变体键、控制字符、重复 JSON 键、非法值）
- 合法打印只返回本作业内容
- 取消后打印/重打被拒绝
- 重打保持原绑定
- 存储内容被篡改时摘要校验拒绝
