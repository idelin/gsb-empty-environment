# Cephx 能力串授权

Ceph 的 caps 字符串决定用户能读写哪些存储池和命名空间。本包为 Go 认证服务提供严格的 caps 解析、精确范围匹配、续期子集校验和两代密钥窗口校验。

## 语法与策略

```text
allow <权限> [pool=名] [namespace=名]
```

- 权限只能是 `r`、`w`、`x` 的互不重复组合，或单独的 `*`；关键字和权限区分大小写。
- 多条规则以逗号分隔，权限按范围取并集；同一范围的请求权限可以由不同规则组合提供。
- `pool` 和 `namespace` 按原始字节精确匹配，不做大小写折叠或 Unicode 归一化；名称中的 `*`、`?` 不是通配符，而是直接判为非法。
- 省略 `pool` 或 `namespace` 表示该维度不受限。规则顺序固定为 `pool` 在前、`namespace` 在后。
- 未知关键字、重复 `pool`/`namespace`、空名称、两种作用域顺序颠倒、空规则、不以小写 `allow` 开头，都会使**整串**失败。解析是全有或全无，不会只保留合法规则。
- `Ticket` 在到期时刻（`now >= ExpiresAt`）即视为过期；零值到期时间也视为过期。
- 密钥代际窗口保留当前代和上一代。当前代为 N 时，代际 N-2 及更旧的签名密钥已退出窗口，未知或未来代际密钥同样拒绝。
- 续期时每条新规则都必须被一条旧规则覆盖：旧权限必须是新权限的超集，且旧范围相等或更宽。任何新增权限或扩大范围都返回 `ErrNotSubset`。

## 使用

```go
import (
	"time"

	netsec "example.org/netsec/q0348"
)

caps, err := netsec.ParseCaps("allow rw pool=app namespace=prod, allow x")
if err != nil {
	// 整串无效
}

ok := caps.Allows("app", "prod", netsec.PermRead|netsec.PermWrite)

ring := netsec.NewKeyRing(7)
ring.AddKey("key-6", 6) // 上一代，仍在窗口内
ring.AddKey("key-7", 7) // 当前代

auth := &netsec.Authority{Keys: ring, TTL: time.Hour}
old := netsec.Ticket{
	Caps:      caps,
	ExpiresAt: time.Now().Add(30 * time.Minute),
	KeyID:     "key-7",
}
requested, _ := netsec.ParseCaps("allow r pool=app namespace=prod")

renewed, err := auth.Renew(old, requested, time.Now())
if err != nil {
	// ErrExpired、ErrKeyRetired、ErrNotSubset 或 ErrInvalidCaps
}
_ = renewed // 调用方应使用 renewed.KeyID 对应的密钥签名
```

主要入口：

- `ParseCaps`（别名 `Parse`）解析并返回 `Caps`；`ParsePermission` 可单独解析权限。
- `Caps.Allows`/`Match` 按并集匹配；`Caps.PermissionsFor` 返回某范围的合并权限。
- `Caps.IsSubsetOf`/`SubsetOf` 与 `Caps.Covers` 实现续期子集判断。
- `Authority.ValidateRenewal` 只做策略校验，`Authority.Renew`/`RenewUntil` 在校验通过后签发新票据对象。
- `KeyRing.KeyActive` 校验密钥是否仍在两代窗口内。

## 测试

```sh
go test ./...
go test -race ./...
```

测试覆盖权限并集、精确范围与大小写、未知关键字及整串失败、续期放大、过期边界、当前/上一代密钥与退出窗口的密钥。
