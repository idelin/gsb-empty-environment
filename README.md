# 确定长度CBOR

COSE头用不确定长度的数组或映射，重新编码后边界变化。受保护内容拒绝不确定长度、拒绝浮点和未定义值；出现则失败。确定长度的映射可以进入签名验证。验证组件调用可导入的Go解码器做这次裁决。用不确定长度映射表示同一个头。

## 用法

验证组件在签名验证前调用裁决入口：

```go
hdr, err := netsec.AdjudicateProtectedHeader(protected)
if err != nil {
	// 不确定长度、浮点、未定义值或非映射：拒绝
	return err
}
// hdr 为确定长度映射，进入签名验证
```

`netsec.Decode` 是可导入的严格 CBOR 解码器：任何不确定长度项
（`ErrIndefiniteLength`）、浮点（`ErrFloat`）或未定义值
（`ErrUndefined`）都会失败，另拒绝重复键、尾部垃圾与截断输入。
