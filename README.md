# ISO 8583位图与PIN块域准入

攻击者在ISO 8583位图里置未定义字段，或给不该带PIN块的消息夹带52域。给Go收单网关写供报文解码调用的位图准入包：位1置位才读第二位图，字段表由调用方给出，LLVAR与LLLVAR长度不得超过剩余字节和字段上限；52域须恰8字节且只出现在调用方登记的消息类型，日志输出必须置空。用未定义位、长度越界和越权52域验证。

## 使用

字段表由调用方给出；`Fixed` 的 `MaxLength` 是精确长度，`LLVAR`/`LLLVAR`
的 `MaxLength` 是不含长度前缀的上限。变长长度前缀按两位/三位 ASCII 十进制解析。

```go
admitter, err := netsec.New(netsec.FieldTable{
	2:  {Kind: netsec.Fixed, MaxLength: 19},
	3:  {Kind: netsec.Fixed, MaxLength: 6},
	4:  {Kind: netsec.Fixed, MaxLength: 12},
	52: {Kind: netsec.Fixed, MaxLength: 8},
}, []string{"0100", "0120"})
if err != nil {
	return err
}

// message = 4字节MTI || 位图 || 字段值
result, err := admitter.AdmitMessage(message)
if err != nil {
	return err
}
pinBlock, ok := result.Field(52)
if !ok {
	return errors.New("field 52 missing")
}
encryptedPINBlock := pinBlock.Bytes(message)
```

`AdmitMessage` 接收完整报文；`Admit` 接收不含 MTI、从主位图开始的字节。
返回值只给字段偏移与长度，不复制字段内容。准入失败返回
`*netsec.AdmissionError`，其中只有操作、域号、偏移和哨兵错误，不含域值。
包本身不写日志，也不向标准输出写任何内容。

默认拒绝最后一个已选域之后的尾随字节；如调用方协议确实包含尾随数据，可用
`Config.AllowTrailing` 显式放开，并通过 `Result.TrailingLength` 检查。
