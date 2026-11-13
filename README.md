# sudoers 通配符与参数约束

`netsec` 是可导入的 Go 授权匹配包，用于提权网关按 sudoers 风格规则作最终决策。

## 规则语义

- 规则格式为 `用户 主机=(目标用户) 程序 参数模式`；`ALL` 可匹配任意用户、主机或目标用户。
- 程序必须是绝对路径，并按字符串逐字比较；不会清理路径，也不会解析符号链接。
- 省略参数模式表示任意参数；`""` 表示必须没有参数。
- 实参按单个空格连接后做 fnmatch；`*` 可匹配任意字符，包括 `/` 和空格。
- 含 `..` 路径段的参数在通配符规则下拒绝。
- 任一参数以 `-` 开头时，只有模式中逐字包含该完整选项才允许。
- 匹配按源码顺序进行；同一程序最后一条匹配规则生效，因此否定的 `!` 规则也可被更晚的允许规则覆盖。
- `Cmnd_Alias` 会递归展开；同一程序的多个条目各自保留参数模式与顺序。

## 使用

```go
policy, err := netsec.Parse(`
Cmnd_Alias CAT = /bin/cat *, !/bin/cat /etc/shadow
alice web01=(root) CAT
`)
if err != nil {
	log.Fatal(err)
}

decision := policy.Authorize(netsec.Request{
	User:    "alice",
	Host:    "web01",
	RunAs:   "root",
	Program: "/bin/cat",
	Args:    []string{"/etc/hostname"},
})
if !decision.Allowed {
	log.Fatal(decision.Reason)
}
```

也可以使用 `netsec.NewPolicy` 直接构造并校验已解析的 `[]Rule`。未命中任何规则时默认拒绝。

## 验证

```sh
go test ./...
go test -race ./...
go vet ./...
```
