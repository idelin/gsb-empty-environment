# 逐级创建符号链接竞争

上传服务要在租户目录下逐级创建嵌套目录并写文件，攻击者可并发地把已创建的某一级换成指向目录外的符号链接。写供 Go 上传服务调用的目录创建包，从租户根目录的 fd 起按路径段 mkdirat 与 openat，每段都使用 O_NOFOLLOW 并校验类型，不允许 ..，已存在的同名普通文件或链接拒绝。通过可注入的文件系统接口在两段之间替换目录、创建链接和删除重建，验证最终写入仍位于租户根内。

## 用法

```go
tenant, err := os.Open(tenantDir)
if err != nil {
	return err
}
defer tenant.Close()

uploadDir, err := netsec.MkdirAllAt(int(tenant.Fd()), "accounts/2026/11/12")
if err != nil {
	return err
}
defer uploadDir.Close()

upload, err := netsec.CreateFileAt(int(uploadDir.Fd()), "payload.bin", 0o600)
if err != nil {
	return err
}
defer upload.Close()
```

`MkdirAllAt` 返回最终目录的已打开 fd。目录遍历中的每一级都相对父目录 fd 执行
`mkdirat`，再以 `O_NOFOLLOW | O_DIRECTORY | O_CLOEXEC` 执行 `openat`，并对
打开后的 fd 做 `fstat` 类型校验。`CreateFileAt` 使用该目录 fd 和
`O_EXCL | O_NOFOLLOW` 创建上传文件，因此路径名祖先被并发替换时，写入仍锚定在
已经校验过的目录 fd 上。

`MkdirAllAtFS` 和 `CreateFileAtFS` 接受可注入的 `FileSystem`，用于在
`mkdirat` 与下一次 `openat` 之间确定性地模拟目录替换、链接创建和删除重建。
