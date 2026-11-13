# crit重复且未知

crit在受保护头和未受保护头各写一次，未知扩展被跳过。crit只能出现在受保护头，其中每一项都必须被实现理解并在受保护头中出现。crit列出的已知扩展确实存在时接受。可导入的Go库供资源服务调用。把未知扩展只放进未受保护头的crit。

## 实现说明

本包 `netsec`（导入路径 `example.org/netsec/q0110`）实现 RFC 7515
§4.1.11 对 JWS `crit`（critical）头参数的校验，供资源服务在验签前调用。
仅依赖标准库。

校验规则：

- `crit` 只能出现在受保护头（protected header）；出现在未受保护头
  （unprotected header）一律拒绝，即使两个头里各写了一次也拒绝。
- `crit` 必须是非空的 JSON 字符串数组。
- `crit` 中列出的每个扩展名都必须实际出现在受保护头中；只出现在
  未受保护头或完全缺失都拒绝。
- `crit` 中列出的每个扩展名都必须在调用方传入的 `knownExtensions`
  集合中（即实现理解该扩展），否则拒绝。未知扩展因此不会被“跳过”，
  而是按 RFC 要求使整个 JWS 被拒绝。
- 没有 `crit` 参数的 JWS 直接通过；本库不负责验签。

## API

- `ValidateHeaders(protected, unprotected map[string]any, known map[string]bool) error`
  适用于通用 JWS JSON 序列化或已自行解码头的场景；紧凑序列化传 `nil`
  作为未受保护头。
- `ValidateCompact(compactJWS string, known map[string]bool) error`
  适用于 RFC 7515 §7.1 的三段式紧凑 JWS，内部完成 base64url 解码。

错误以可导出的哨兵值返回（如 `ErrCritInUnprotected`、
`ErrCritExtensionUnknown` 等），用 `errors.Is` 匹配；具体扩展名通过
`%w` 包装在错误信息中。

## 资源服务用法

```go
import netsec "example.org/netsec/q0110"

var understood = map[string]bool{
	"http://example.com/ext/audience": true,
}

func resourceServer(token string) error {
	// 先做 crit 校验，再进行签名验证。
	if err := netsec.ValidateCompact(token, understood); err != nil {
		return err // 拒绝该 JWS
	}
	// ... 验签及其余业务校验
	return nil
}
```

## 测试

`go test ./...` 覆盖：受保护/未受保护头各写一次 `crit`、未知扩展仅
放进未受保护头的 `crit`、已知扩展存在时接受、未知/缺失/错位扩展拒绝，
以及非数组、空数组、非字符串元素和紧凑序列化的畸形输入。
