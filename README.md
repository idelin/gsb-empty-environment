# 无 SecurityManager 的栈权限求交

JVM 移除 SecurityManager 后，插件宿主要自己判定调用栈上的权限。本包提供一个供文件操作检查点调用的权限图实现，防止低权限插件借助高权限库帧实施混淆代理（confused deputy）攻击。

## 模型

- `ProtectionDomain` 带一组 `FilePermission` 授权。
- `FilePermission` 支持精确路径、`/dir/*` 和 `/dir/-` 三种形式，动作支持 `read`、`write`。
- 栈帧使用 `StackFrame`，按**栈顶在前**排列：`frames[0]` 是当前执行帧，后面的元素是它的调用者。
- 有效权限是栈上所有保护域授权的交集。
- 遇到第一个 `Privileged` 帧时，只检查 `frames[0]` 到该帧（含），更老的调用者被截断。
- 带 `LimitedDomain` 的 `Privileged` 帧还要额外与限定域求交。
- 路径按组件匹配：`/dir/*` 只含 `/dir` 的直接子项；`/dir/-` 含 `/dir` 的全部后代；二者都不含 `/dir` 自身，也不匹配 `/directory`、`/dir2` 等同前缀兄弟路径。比较前会清除 `..` 等词法路径成分。

## 使用

```go
plugin := netsec.NewProtectionDomain(
	"plugin",
	netsec.NewFilePermission("/plugins/p1/*", netsec.Read),
)
library := netsec.NewProtectionDomain(
	"library",
	netsec.NewFilePermission("/data/-", netsec.Read, netsec.Write),
)

// 普通调用：两个域求交，库不能替插件越权读取 /data/secret。
frames := []netsec.StackFrame{
	netsec.Frame(library),
	netsec.Frame(plugin),
}
ok := netsec.CheckPath(frames, "/data/secret", netsec.Read) // false

// 库显式使用 doPrivileged 时，更老的插件调用者被截断。
frames = []netsec.StackFrame{
	netsec.DoPrivileged(library),
	netsec.Frame(plugin),
}
ok = netsec.CheckPath(frames, "/data/secret", netsec.Read) // true
```

`Check` 在栈为空、请求非法或授权无法满足时一律拒绝，便于检查点采用失败关闭（fail closed）策略。授权匹配是词法路径匹配，不解析符号链接；若宿主允许符号链接，还应在实际打开文件前做规范化或解析。

## 测试

```sh
go test ./...
```

测试覆盖混淆代理求交、`doPrivileged` 截断、限定域求交，以及 `*`/`-` 的直接子项、递归后代和同前缀兄弟目录边界。
