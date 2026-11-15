# 签名包的重复条目走私

Go制品安装服务需要可导入的签名包验证器。包由有序条目组成，每项包含原始UTF-8路径、文件字节及声明SHA256，另有可信Ed25519公钥和签名清单。清单编码为每条规范路径的UTF-8字节长度十进制、冒号、路径、32字节摘要，按路径字节序串接，重复路径拒绝；路径用斜线分段，点与空段折叠、双点弹栈但越根拒绝，反斜线和NUL拒绝，不做Unicode或大小写折叠。攻击者控制条目顺序、路径、摘要和签名。验证原始流与最终安装映射一致，所有冲突、额外或缺失条目及错误签名必须在任何安装回调前拒绝；支持分块读取且每文件和总量限额由调用方提供。给出重复名覆盖、规范化碰撞、零字节文件、流截断及有效签名的端到端测试。

## 使用方式

安装服务先实现 `EntrySource`：每次 `Next` 返回一个条目，`Entry.Content`
只在当前条目的字节范围内读取，并在 EOF 处结束；所有条目读完后返回
`io.EOF`。实现可以来自 tar、zip 或任意自定义制品格式。

```go
verifier, err := netsec.NewVerifier(trustedPublicKey, netsec.Limits{
	MaxFileBytes:  64 << 20,
	MaxTotalBytes: 512 << 20,
})
if err != nil {
	return err
}

err = verifier.VerifyAndInstall(ctx, signedManifest, signature, source,
	func(files map[string][]byte) error {
		return installAtomically(files)
	})
```

`Limits` 按字节计且包含边界；零值只允许零字节文件。`VerifyAndInstall`
仅在签名、清单元数据、全部条目、摘要、额外/缺失条目和路径冲突都通过后
调用一次安装回调。需要先取得验证结果时，可调用 `Verify`。

## 安全约定

- 清单签名覆盖原始清单字节，并由可信 Ed25519 公钥验证。
- 清单按规范路径字节序编码；解析时拒绝重复、乱序、非规范路径和截断记录。
- 条目可乱序到达；验证器按规范路径建立最终映射，并拒绝重复路径及
  “文件同时也是另一路径父目录”的冲突。
- 路径规范化只按 ASCII `/` 分段，折叠空段和 `.`，处理 `..`，拒绝越根、
  反斜线、NUL 和非法 UTF-8；不执行 Unicode 规范化或大小写折叠。
- 条目声明的摘要、实际内容和签名清单三方必须一致；读取失败或截断直接拒绝。
- 所有文件内容在回调前验证并受调用方限额约束，不会边读边安装。

运行测试：

```sh
go test -race ./...
```
