# DNS 压缩名字单次解码包

`taskpkg` 是供 Go 宿主调用的单包 DNS 名字解码器。一次输入只解码一次，安全检查回调和业务回调拿到同一个不可变的 `*Name`，避免检查层与业务层看到不同名字。

## 严格规则

- 标签载荷最多 63 字节；`0x40`、`0x80` 等保留标签类型直接拒绝。
- 压缩指针固定两字节，目标必须严格小于指针自身位置。
- 指针目标必须是同一条报文中已经验证过的完整标签起点；指向标签中部、报文外或未验证位置都拒绝。
- 同一解码路径重复到达同一指针位置判为环；展开后的 wire 长度（含长度字节和根标签）最多 255 字节。
- 每个展开字节都保留原始报文偏移，包括标签长度字节和根标签。
- 解码失败或安全检查拒绝时，业务回调调用次数为零。
- `Identity()` 只由展开后的标签内容决定，与压缩布局、原始偏移和 ASCII 大小写写法无关。

## 宿主接入

短指针只能信任已经解析过的标签起点。同一报文内解析多个名字时，必须复用一个 `Message` 实例，并按报文中名字的顺序解码，使它记住前面成功解码的标签起点。

```go
message := taskpkg.NewMessage(packet)

question, err := message.DecodeName(questionOffset)
if err != nil {
    return err
}

err = message.Process(answerOffset, taskpkg.Callbacks{
    Check: func(name *taskpkg.Name) error {
        return policy.Authorize(name.Identity())
    },
    Business: func(name *taskpkg.Name) error {
        return handler.Resolve(name)
    },
})
```

`NewMessage` 复制输入字节，解码后的 `Name` 也不暴露内部切片。因此调用方事后修改原报文或修改 `ExpandedWire()` / `SourcePositions()` 的返回值，都不会改变两个回调共享的名字对象。

独立调用 `taskpkg.DecodeName(msg, offset)` 只信任当前名字解码路径中验证到的起点，因此适合单名字报文；跨名字的合法压缩指针应使用 `*Message`。

## 验证

`go test ./...` 包含短报文穷举、指针目标 0..255 枚举、每个切块/截断位置、环与标签中部指针、业务回调零调用、别名身份一致性，以及模糊测试入口。
