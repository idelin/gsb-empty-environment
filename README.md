# QEMU 监控套接字命令授权

`netsec` 包供 QMP 代理在转发命令前做授权判定，输入为对端凭据
（`SO_PEERCRED` 的 uid/gid/pid 与套接字文件权限位）、命令 JSON
（`execute`、`arguments`、`id`）和角色策略。

## 授权规则

- 对端先经 uid/gid 映射到角色；无映射的对端一律拒绝。套接字权限位含
  其他用户写位（`mode&0o002`）时整体拒绝，防止全局可写套接字被借用。
- 每个会话必须先成功执行一次精确命名的 `qmp_capabilities`，其余命令在
  协商完成前全部拒绝；重复协商拒绝；连接关闭时调用 `Reset` 清除状态。
- `human-monitor-command` 仅 `admin` 角色可用。
- 只读角色（`QueryOnly`）仅允许 `query-` 前缀命令。
- `getfd`、`add-fd`、`blockdev-add` 的路径参数（递归识别
  `filename`/`path`/`dir`/`dirname` 字段）必须是绝对路径且位于该角色
  登记的目录内；词法 `..` 穿越按清洗后的路径判定。
- 任意命令的 `arguments` 中，`driver` 为 `host_device` 或 `file` 且
  路径指向 `/dev`、`/proc` 时拒绝（支持嵌套节点）。
- 命令名与 `driver` 取值均区分大小写，大小写变形不能绕过名称类规则。
- 批量消息（JSON 数组）逐条独立判定，一条被绝不影响后续命令；
  `id` 字段原样透传，便于把结果对应回请求。

## 用法

```go
authorizer, err := netsec.NewAuthorizer(&netsec.Policy{
	Roles: map[string]*netsec.RolePolicy{
		netsec.RoleAdmin: {
			Name: netsec.RoleAdmin, Admin: true,
			AllowedDirs: []string{"/srv/vm/admin"},
		},
		netsec.RoleReadonly: {Name: netsec.RoleReadonly, QueryOnly: true},
	},
	UIDRoles:              map[uint32]string{0: netsec.RoleAdmin, 1000: netsec.RoleReadonly},
	RequireSafeSocketMode: true,
})
if err != nil {
	log.Fatal(err)
}

peer := netsec.PeerCred{UID: cred.Uid, GID: cred.Gid, PID: cred.Pid, Mode: socketMode}
for _, decision := range authorizer.AuthorizeBatch(peer, rawMessage) {
	if !decision.Allowed {
		log.Printf("deny %s: %s", decision.Command, decision.Reason)
		continue // 拒绝该条，其余条目继续独立处理
	}
	forward(rawMessage)
}
```

## 验证

`go test ./...` 覆盖未协商访问、越权角色、`/dev` 与 `/proc` 路径、
大小写变形、合规命令、批量独立判定及策略配置校验；
`go test -race` 确认并发安全。
