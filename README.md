# 会话生命周期组件

`example.org/netsec/q0485` 提供一个可导入、并发安全的 Go 会话管理器。它在认证成功后创建会话，并在认证成功、权限提升和租户切换时原子地更换会话标识，使旧标识立即失效。

## 快速使用

```go
import session "example.org/netsec/q0485"

manager, err := session.NewManager(session.Config{
	IdleTimeout:           30 * time.Minute,
	AbsoluteLifetime:      12 * time.Hour,
	ConcurrentLoginPolicy: session.ConcurrentLoginRevokeExisting,
})
if err != nil {
	// handle configuration error
}

// 只有在凭据校验成功后才调用 Authenticate。该方法不接收客户端提供的
// 会话标识，新标识完全由服务端生成。
issued, err := manager.Authenticate(session.LoginRequest{
	UserID:      "alice",
	TenantID:    "tenant-a",
	DeviceID:    deviceFingerprint,
	AuthLevel:   1,
	Roles:       []string{"member"},
	Permissions: []string{"read"},
	AuthMethod:  "password",
})
if err != nil {
	// handle authentication/session-policy error
}

// 后续请求必须同时校验标识和设备绑定。成功后只刷新空闲超时，不会延长
// 绝对生命周期。
current, err := manager.Validate(issued.ID, deviceFingerprint)
if err != nil {
	// ErrSessionInvalid, ErrSessionExpired, or ErrDeviceMismatch
}

// 权限提升或租户切换都会返回新标识，旧标识立即不可用。
elevated, err := manager.Elevate(current.ID, deviceFingerprint, session.Elevation{
	AuthLevel:   2,
	Roles:       []string{"admin"},
	Permissions: []string{"write"},
})
if err != nil {
	// handle error
}

switched, err := manager.SwitchTenant(elevated.ID, deviceFingerprint, session.TenantSwitch{
	TenantID:  "tenant-b",
	AuthLevel: 0,
	Roles:     []string{"viewer"},
})
if err != nil {
	// handle error
}

_ = switched
```

## 安全语义

- 认证前不分配标识。`LoginRequest` 没有客户端会话标识字段，固定 ID 无法进入认证流程。接入方在认证成功后必须丢弃旧的匿名 Cookie，并写入 `Authenticate` 返回的新标识。
- 标识采用 `crypto/rand` 的 256 位随机值，base64url 编码后为 43 个字符；服务端只按 SHA-256 哈希索引会话。
- 每次成功认证都创建全新标识；`Rotate`、`Elevate` 和 `SwitchTenant` 在同一临界区内删除旧标识并写入新标识。旧标识重放返回 `ErrSessionInvalid`。
- 会话绑定认证时的设备指纹。设备不一致返回 `ErrDeviceMismatch`，不匹配的请求不能刷新、轮换或提升会话。
- 同时执行空闲超时和绝对生命周期。`Validate` 只刷新空闲超时；轮换不会延长绝对生命周期；过期会话被移除并返回 `ErrSessionExpired`。
- 租户切换会显式替换角色、权限和认证等级，避免跨租户继承权限。
- 所有方法并发安全。并发轮换同一旧标识时只有一个请求成功。

## 并发登录策略

策略在配置中显式选择，零值等价于最严格的拒绝策略：

- `ConcurrentLoginRejectNew`：用户已有活跃会话时拒绝新登录，返回 `ErrConcurrentLoginRejected`。
- `ConcurrentLoginRevokeExisting`：撤销该用户所有活跃会话后签发新会话，实现单活登录。
- `ConcurrentLoginAllow`：允许同一用户拥有多个独立会话。

策略按用户在所有租户范围内生效，并在判断前清理已过期会话。`DefaultConfig()` 明确使用 `ConcurrentLoginRejectNew`。

## 认证服务接入要点

```go
// 凭据校验成功后，不要再读取或沿用认证前的 Cookie。
issued, _ := manager.Authenticate(req)
http.SetCookie(w, &http.Cookie{
	Name:     "__Host-session",
	Value:    issued.ID,
	Path:     "/",
	Secure:   true,
	HttpOnly: true,
	SameSite: http.SameSiteLaxMode,
	MaxAge:   int(time.Until(issued.ExpiresAt).Seconds()),
})

// 状态变更后必须用返回的新标识覆盖 Cookie。
next, _ := manager.Elevate(issued.ID, deviceID, elevation)
http.SetCookie(w, &http.Cookie{
	Name: "__Host-session", Value: next.ID, Path: "/", Secure: true,
	HttpOnly: true, SameSite: http.SameSiteLaxMode,
	MaxAge: int(time.Until(next.ExpiresAt).Seconds()),
})
```

到期时间以 `Session.ExpiresAt` 和 `Session.IdleExpiresAt` 为准。管理器为进程内存储，不启动后台清理协程；可定期调用 `PruneExpired`，多实例部署时应将同一生命周期语义接入共享存储。

## 验证

```sh
go test ./...
go test -race ./...
go vet ./...
```

测试覆盖会话固定、旧标识复用、权限提升、租户切换、合法登录、设备绑定、空闲与绝对过期，以及三种并发登录策略。
