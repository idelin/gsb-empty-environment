# RAR授权细节子集判定

攻击者在授权请求里夹带比用户同意范围更宽的 authorization_details，或刷新时扩权。为Go授权服务器写可导入的 RAR 子集判定包：请求项被满足当且仅当存在同 type 的已授权项，其 locations 逐字节包含请求 URL（不做前缀匹配），actions、datatypes 为超集，identifier 相同或请求未给；未知 type 使整个请求被拒，刷新所得详情须是原授权的子集，重复项只计一次。测越权 URL、前缀仿冒、未知类型、刷新扩权。

## 包

可导入子包：`example.org/netsec/q0346/rar`，实现 RFC 9396 的公共字段子集
（`type`、`locations`、`actions`、`datatypes`、`identifier`）。

## 判定规则

请求项被满足，当且仅当存在一个同 `type` 的已授权项，且：

- 每个请求 `locations` 中的 URL 都与某个已授权 location 逐字节相等，不做前缀匹配，`/a` 不覆盖 `/a/b` 或 `/a/`；
- 已授权 `actions`、`datatypes` 分别是请求集合的超集；空请求集合视为被覆盖；
- `identifier` 相等，或请求未携带该字段；请求携带而授权未携带则拒绝；
- 没有任何同 `type` 已授权项即未知类型，整个请求被拒绝；
- 必须由同一个已授权项完整覆盖请求项，不能把多个授权项拼接；
- 请求与授权中的重复项先去重，只计一次，数组顺序不影响判定。

刷新令牌返回的详情必须是原授权详情的子集，使用同一个判定器检查。

## 用法

```go
import "example.org/netsec/q0346/rar"

requested, err := rar.ParseDetails(payload) // authorization_details JSON 数组
if err != nil { /* 拒绝畸形请求 */ }

if err := rar.Satisfy(requested, grantedFromConsent); err != nil {
    violation := rar.AsViolation(err) // violation.Field 指明 type/locations/...
    // 拒绝授权请求
}

// 刷新时：refreshed 必须是 original 的子集
if err := rar.RefreshAllowed(refreshed, original); err != nil { /* 拒绝刷新扩权 */ }
```

`Detail.Identifier` 为 `*string`，用于区分“未给 identifier”与“给了空字符串”。

## 测试

```sh
go vet ./...
go test ./... -cover
```

测试覆盖越权 URL、路径前缀仿冒（含尾斜杠差异）、未知类型整体拒绝、
actions/datatypes/identifier 扩权、刷新扩权、重复项去重及 JSON 解析校验。
