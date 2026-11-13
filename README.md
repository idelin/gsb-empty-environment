# 伪头重复

同一伪头出现两次时，路由和长度会各取一次。:method、:scheme、:authority、:path都只能出现一次，且方法和方案必须在路径之前给齐。四者各一次的普通请求放行。协议网关调用可导入的Go库做这次裁决。重复:path并交换伪头顺序。

## Go API

```go
import "example.org/netsec/q0083"

headers := []netsec.Header{
	{Name: ":method", Value: "GET"},
	{Name: ":scheme", Value: "https"},
	{Name: ":authority", Value: "example.test"},
	{Name: ":path", Value: "/"},
}

if err := netsec.ValidatePseudoHeaders(headers); err != nil {
	// Reject the request. The error is also available through errors.Is
	// using the sentinels such as netsec.ErrDuplicatePseudoHeader.
}
```

`ValidatePseudoHeaders` accepts an ordered `[]Header` (or `HeaderList`),
alternating name/value strings, `[][2]string`, header maps including
`http.Header`, and `RequestPseudoHeaders`. The four-argument form is
`(method, scheme, authority, path)`.

The validator rejects:

- any second occurrence of `:method`, `:scheme`, `:authority`, or `:path`;
- `:path` before both `:method` and `:scheme`;
- pseudo-headers after a regular header;
- missing `:method`, `:scheme`, or `:path`;
- unknown request pseudo-headers.

`:authority` is optional. All four pseudo-headers appearing exactly once, in
a valid order, are accepted.
