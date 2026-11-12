# SFTP扩展请求权限

SFTP 服务器对基本操作做了只读和路径限制，攻击者改走 SSH_FXP_EXTENDED 里的 posix-rename@openssh.com、hardlink@openssh.com、copy-data 或 fsetstat@openssh.com 绕过。为Go的 SFTP 服务端写可导入的请求授权包：每个扩展请求先映射为等价的基本权限（重命名需源目录与目标目录写权限，硬链接需源读权限与目标目录写权限，copy-data 需源读与目标写）再走同一套路径规范化与只读开关；未知扩展名一律返回 SSH_FX_OP_UNSUPPORTED，不因扩展名带 @ 域名而放行；规范化后越出根目录即拒绝，符号链接解析后的真实路径也须在根内（文件系统接口由调用方注入）。测只读模式下的各扩展、越根重命名、硬链接到无读权限的文件。

## 使用

```go
auth, err := netsec.New(netsec.Config{
    Root:     "/srv/sftp",
    ReadOnly: session.ReadOnly,
    FS:       netsec.OSFS{},
    // Policy: mySessionPolicy{}, // 可选，默认使用文件 mode 位
})
if err != nil {
    return err
}

err = auth.AuthorizeExtension(netsec.ExtensionRequest{
    Name:   extName,   // 来自 SSH_FXP_EXTENDED
    Source: source,    // 或 handle 对应的路径
    Target: target,
    Path:   path,      // fsetstat 使用的单路径
})
if err != nil {
    // 将 netsec.ErrorStatus(err) 写入 SSH_FXP_STATUS 的 code 字段
    return writeStatus(netsec.ErrorStatus(err))
}
```

授权顺序：

1. 精确匹配扩展名；未知名称立即返回 `SSH_FX_OP_UNSUPPORTED`。
2. 将扩展映射为基本权限：rename 为两个目录的 `Write`，hardlink 为源
   `Read` 加目标目录 `Write`，copy-data 为源 `Read` 加目标 `Write`，
   fsetstat 为目标 `Write`。
3. 只读模式拒绝所有需要 `Write` 的请求。
4. 规范化路径，拒绝越过根目录的 `..`。
5. 逐段解析符号链接并确认最终真实路径仍在根内。
6. 调用注入的 `AccessPolicy` 完成基础读写权限判断。

`FS` 只要求 `Lstat`、`Stat` 和 `Readlink` 三个方法，路径使用该文件系统
命名空间中的绝对路径，便于替换为内存文件系统或带竞态保护的 `os.Root`
适配器。`Authorize*` 返回的错误可用 `netsec.ErrorStatus` 映射为 SFTP
状态码。
