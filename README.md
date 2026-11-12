# Diameter AVP必需位处理

Diameter对端可把带M位的未知AVP伪装成可忽略项，或让嵌套AVP超出父长度，来绕过计费和授权约束。为Go AAA代理实现AVP解码包，供转发前调用：长度含头并四字节填充，V位与Vendor-Id一致，Grouped嵌套不超过8层且不越过父边界；未知且置M位的AVP返回5001并带Failed-AVP，未知非必需AVP保留转发。用越界、清除M位和合法报文验证。

## 用法

```go
avps, err := netsec.DecodeAVPs(rawAVPs)
if err != nil {
	var derr *netsec.DecodeError
	if errors.As(err, &derr) && derr.ResultCode == netsec.ResultCodeAVPUnsupported {
		// 使用 derr.ResponseAVPs() 构造带 Result-Code 5001 和 Failed-AVP 的错误应答。
	}
	// 不要转发。
}
// avps 可原样转发；未知的非必需 AVP 会保留在结果中。
```

也可从 `example.org/netsec/q0260/avp` 导入同一套 API。需要扩展应用私有 AVP
字典时使用 `NewDecoderWithKnown`。

## 校验规则

- `AVP Length` 包含 8 字节基础头（V 位置位时包含 12 字节头），不包含尾部填充；
  整个 AVP 必须按四字节对齐，嵌套 AVP 的填充也不得越过父 AVP 数据边界。
- V 位置位时读取 `Vendor-Id`，且其值不得为 0；V 位清零时不存在 `Vendor-Id` 字段。
- 已知 Grouped AVP 递归解析，最多 8 层；长度越界返回 `5014` 和 `Failed-AVP`。
- 未知且 M 位置位：返回 `5001`（`DIAMETER_AVP_UNSUPPORTED`）和包含原 AVP 的
  `Failed-AVP`；未知且 M 位清零：保留在解码结果中。

运行验证：

```sh
go test ./...
```
