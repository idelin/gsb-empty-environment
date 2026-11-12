# LFS 指针与对象替换防护

`netsec` 是可直接导入的 Go 校验包，用于 Git LFS 网关：

- 只接受 `version`、`oid`、`size` 三行；`version` 必须在首行，其余键按字母序，整体不超过 1024 字节。
- `oid` 必须是规范的小写 `sha256:<64 hex>`，并通过调用方按仓库维护的 `OIDSet` 授权。
- 下载响应边写同目录临时文件边计算 SHA-256；只有长度和摘要同时匹配才通过 `os.Rename` 原子发布。
- 任何解析、授权、读取、长度、摘要或取消错误都不会改动目标文件，并清理临时文件。

## 用法

```go
pointerBytes, err := io.ReadAll(io.LimitReader(request.Body, netsec.MaxPointerSize+1))
if err != nil {
	return err
}

// repoOIDs 必须是“当前仓库”的授权集合，不能使用全局或跨仓库集合。
pointer, err := netsec.ValidatePointer(pointerBytes, repoOIDs)
if err != nil {
	return err
}

// response.Body 来自对象存储；dest 应位于网关管理的对象目录。
if err := netsec.PublishObject(ctx, response.Body, dest, pointer); err != nil {
	return err
}
```

可用 `errors.Is` 区分 `ErrInvalidPointer`、`ErrPointerTooLarge`、
`ErrOIDNotAuthorized`、`ErrObjectSizeMismatch` 和
`ErrObjectDigestMismatch`。

测试覆盖跨仓库 OID、过长/过短响应、同长度摘要替换、下载中途换流、
上下文取消、读取中断和临时文件清理。
