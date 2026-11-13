# 资源指示符锁定受众

resource参数写成另一资源的前缀，令牌却能在更宽的API上使用。resource必须是绝对URI且不含片段；令牌aud只能等于被允许的资源，不能按前缀放大。登记过的完整资源URI放行。由授权服务调用可导入的Go库。在资源URI后追加路径再申请。

## 安全规则

- `resource` 必须是非空的绝对 URI（带 scheme），且不得包含 fragment（`#`）。
- 授权服务只放行已登记的**完整**资源 URI。
- 匹配始终是区分大小写的精确字符串比较，绝不做前缀/子串匹配。
- 令牌 `aud` 只能等于被允许的资源；在已登记 URI 后追加路径、查询串再申请会被拒绝，窄资源的令牌无法用于更宽的 API。

## 用法

```go
import "example.org/netsec/q0102"

registry := netsec.NewRegistry()
registry.MustRegister("https://api.example.com/v1/users")

// 发令牌：仅当资源 URI 合法且已登记时才签发，aud 精确等于该资源。
token, err := registry.IssueToken("client-1", []string{"https://api.example.com/v1/users"})

// 校验：精确匹配，前缀放大攻击会被拒绝。
err = registry.VerifyToken(token, "https://api.example.com/v1/users/admin")
// errors.Is(err, netsec.ErrAudienceDenied) 或 netsec.ErrResourceNotRegistered
```

## 主要 API

- `ValidateResource(resource string) error`：校验绝对 URI 且无 fragment。
- `(*Registry).Register / MustRegister / Registered`：登记与查询完整资源 URI。
- `(*Registry).Allow(resource string) error`：校验并确认资源已登记（精确匹配）。
- `(*Registry).IssueToken(subject string, resources []string) (Token, error)`：签发 `aud` 被锁定的令牌。
- `(*Registry).VerifyToken(token Token, resource string) error`：校验令牌 `aud` 与请求资源精确一致。
