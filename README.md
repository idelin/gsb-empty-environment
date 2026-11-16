# UNC与设备路径的出站认证泄漏拦截

应用对用户给的路径做 stat 或打开文件时，Windows 遇到网络共享路径会主动向远端主机发起 SMB 认证，攻击者借此收集服务账户的 NTLM 哈希。为Go文件服务写可导入的路径准入包，在任何文件系统调用之前判定：以两个反斜杠或两个斜杠开头的路径，含 UNC 设备前缀、设备命名空间前缀或 GLOBALROOT 的路径，带主机部分的 file URL，带 @SSL 或 @443 的 WebDAV 写法，含 DavWWWRoot 的路径，一律拒绝；前导双分隔符在折叠连续分隔符之前判定，不得被折叠绕过；盘符路径只允许调用方白名单里的盘符，驱动器相对路径（C:foo）拒绝；路径里含备用数据流标记 :$DATA 或盘符之外的冒号时拒绝；保留设备名（CON、NUL、COM1 等，含扩展名、尾部的点与空格）拒绝；百分号编码不解码，含 %5c 的名称拒绝；解析为符号链接或重解析点的结果由调用方回调给出，指向网络共享时拒绝。用 //evil/share、UNC 设备前缀、file://host/x、C:foo、NUL.txt 与 a.txt:$DATA 验证。

## 用法

```go
import "example.org/netsec/q0799"

checker := netsec.NewChecker(netsec.Config{
	AllowedDrives: []string{"C"},
	Resolve: func(path string) (string, error) {
		// 仅在词法检查通过后执行。调用方在这里 lstat/EvalSymlinks，
		// 并把符号链接或重解析点的最终目标返回给准入器复查。
		return resolveLinkOrReparsePoint(path)
	},
})

if err := checker.Check(userPath); err != nil {
	// 在 os.Stat、os.Open 等文件系统调用之前返回。
	return err
}
```

`AllowedDrives` 为空时拒绝所有盘符路径。`Resolve` 可选；若提供，返回空路径会失败，返回与输入不同的路径会用同一套规则复查，因此指向 `\\host\share`、设备路径或未授权盘符的目标会被拒绝。

包本身不执行 `stat`、`open`、`Clean` 或 `EvalSymlinks`，也不会解码百分号编码。

## 验证

```sh
go test ./...
```
