# SSZ 变长字段偏移校验

面向共识客户端网络消息处理的防御性 SSZ（Simple Serialize）解码包。它针对
“恶意偏移表导致不同客户端解出不同内容”这一类共识分歧攻击，在解码阶段强制执行
严格、确定性的边界与长度校验。

## 安全保证

- **首个偏移固定**：容器/变长集合的第一个偏移必须恰好等于固定部分（fixed part）
  的字节长度，否则返回 `ErrOffsetFirst`。
- **偏移单调**：偏移表必须非递减；任何倒退（后一个偏移小于前一个）返回
  `ErrOffsetOrder`。
- **偏移不越界**：每个偏移都不得超过当前区域总长，越界返回 `ErrOffsetRange`；
  所有偏移在解码任何子值之前一次性校验。
- **长度上限**：列表元素数、Bitlist 位数超过类型声明上限时返回
  `ErrLengthExceeded`；整体编码超过类型最大线长返回 `ErrMaxSize`。
- **Bitlist 终止位**：Bitlist 必须包含合法的终止（delimiter）位，缺失或末字节
  为零时返回 `ErrBitlistDelimiter`；Bitvector 的填充位必须为零。
- **Union 选择子**：选择子超出成员类型范围时返回 `ErrUnionSelector`；变量成员
  的相对偏移必须为固定的 4。
- **无尾部多余字节**：顶层值必须恰好耗尽全部输入；容器最后字段、集合最后元素
  都必须精确结束在声明边界上，不可达字节返回 `ErrTrailingBytes`。
- **布尔严格**：布尔字节只接受 `0` 或 `1`，其余返回 `ErrBoolValue`。

所有错误都通过 `errors.Is` 暴露为 `errors.go` 中的哨兵错误，并附带上下文信息。

## 用法

```go
import netsec "example.org/netsec/q0449"

schema := netsec.MustContainerType([]netsec.Field{
    {Name: "index", Type: netsec.Uint32Type},
    {Name: "items", Type: netsec.MustListType(netsec.Uint64Type, 8)},
})

value, err := netsec.Decode(schema, wireBytes)
if err != nil {
    // 丢弃该网络消息；不要基于部分结果继续处理。
    return err
}
index, _ := value.FieldByName("index")
n, _ := index.Uint64()
```

支持的类型：基础类型（bool、uint8/16/32/64、uint256）、定长向量 Vector、
变长列表 List、Bitvector、Bitlist、Container 和 Union。

## 验证

`go test ./...` 覆盖倒退偏移、越界偏移、首个偏移错误、尾部多余字节、空列表、
长度上限、Bitlist 终止位、Union 越界选择子、布尔取值与嵌套容器等场景。
