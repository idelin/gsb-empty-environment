# UNC与设备路径的出站认证泄漏拦截

应用对用户给的路径做 stat 或打开文件时，Windows 遇到网络共享路径会主动向远端主机发起 SMB 认证，攻击者借此收集服务账户的 NTLM 哈希。为Go文件服务写可导入的路径准入包，在任何文件系统调用之前判定：以两个反斜杠或两个斜杠开头的路径，含 UNC 设备前缀、设备命名空间前缀或 GLOBALROOT 的路径，带主机部分的 file URL，带 @SSL 或 @443 的 WebDAV 写法，含 DavWWWRoot 的路径，一律拒绝；前导双分隔符在折叠连续分隔符之前判定，不得被折叠绕过；盘符路径只允许调用方白名单里的盘符，驱动器相对路径（C:foo）拒绝；路径里含备用数据流标记 :$DATA 或盘符之外的冒号时拒绝；保留设备名（CON、NUL、COM1 等，含扩展名、尾部的点与空格）拒绝；百分号编码不解码，含 %5c 的名称拒绝；解析为符号链接或重解析点的结果由调用方回调给出，指向网络共享时拒绝。用 //evil/share、UNC 设备前缀、file://host/x、C:foo、NUL.txt 与 a.txt:$DATA 验证。

## 使用

```go
policy := netsec.Policy{
	AllowedDrives: []string{"C", "D"},
	ResolvePath: func(path string) (string, error) {
		// Caller-provided symlink/reparse-point resolution. The package
		// validates the returned target before allowing it.
		return resolveLocalLink(path)
	},
}

if err := policy.Check(userPath); err != nil {
	return fmt.Errorf("unsafe path: %w", err)
}
// Only now call os.Stat, os.Open, or another filesystem API.
```

`AllowedDrives` 为空时拒绝所有带盘符的路径，不会退化成“全部允许”。相对路径不受盘符白名单限制，调用方仍需将其限制在自己的根目录内。

包本身不调用 `os.Stat`、`os.Open`、`filepath.EvalSymlinks` 或任何其他文件系统 API。符号链接/重解析点解析由 `ResolvePath` 回调完成；回调拿到的是已通过词法检查的原路径，返回的目标会再次经过同一套准入规则，失败、解析环或过深解析都会按拒绝处理。

准入检查不会对路径做百分号解码、分隔符折叠或 Windows 路径规范化。调用方不得在检查之后再解码或重新规范化用户路径，否则会破坏“先判定、后访问”的安全顺序。

## 验证

```sh
go test ./...
```

测试覆盖 `//evil/share`、UNC/设备前缀、`file://host/x`、`C:foo`、`NUL.txt`、`a.txt:$DATA`，以及混合分隔符、连续分隔符、WebDAV、ADS、保留设备名和解析回调等绕过场景。
