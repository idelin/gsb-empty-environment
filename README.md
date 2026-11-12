# Avro写入模式嵌入攻击限制

消息总线收Avro对象容器文件，攻击者在文件头嵌入写入模式，用递归记录、超大块计数让读取方耗尽内存。给Go读取方写供摄取流程调用的受限解码包：写入模式须在调用方的指纹白名单内且递归有深度上限；块计数为负时取绝对值并读块字节数，二者与剩余字节核对；同步标记错、枚举或union越界拒绝。用递归模式、虚高块计数和篡改标记验证。

## 受限解码包 `avroocf`

纯标准库实现，入口为 `avroocf.Open`。调用方在 `Config` 中提供写入模式的
指纹白名单和各项资源上限，未通过校验的容器在读出任何对象前即被拒绝。

```go
schema, _ := avroocf.ParseSchema(schemaJSON, 32)
config := avroocf.Config{
    AllowedFingerprints: map[string]bool{schema.FingerprintSHA256(): true},
    MaxSchemaDepth:      32,
    MaxBlockObjects:     10_000,
    MaxBlockBytes:       4 << 20,
    // AllowDeflate 与 MaxDecompressedBlockBytes 同时设置才启用 deflate。
}
reader, err := avroocf.Open(bytes.NewReader(containerBytes), config)
if err != nil {
    return err // 含 ErrSchemaRejected / ErrDepthLimit 等哨兵错误
}
defer reader.Close()

for {
    count, err := reader.NextBlock()
    if errors.Is(err, io.EOF) {
        break
    }
    if err != nil {
        return err // 含 ErrBlockCount / ErrSyncMarker
    }
    for i := int64(0); i < count; i++ {
        value, err := reader.Decode() // 或 reader.Skip() 零分配跳过
        if err != nil {
            return err // 含 ErrEnumOutOfRange / ErrUnionOutOfRange
        }
        ingest(value)
    }
}
```

### 强制的安全检查

- 写入模式指纹必须在白名单内（默认 SHA-256 规范化指纹，可自定义指纹函数）。
- 模式文本嵌套与运行时解码嵌套均受 `MaxSchemaDepth` 限制，递归记录可解析但
  不会被深层数据撑爆栈或内存。
- 负块计数按规范取绝对值作为对象数，并读取紧随的块字节数；对象数、声明字节数
  都与流内剩余字节核对，虚高计数或字节数不符直接返回 `ErrBlockCount`。
- 每块结尾的 16 字节同步标记与文件头标记逐字节比对，不符返回 `ErrSyncMarker`。
- 枚举 ordinal、union 分支索引越界分别返回 `ErrEnumOutOfRange`、
  `ErrUnionOutOfRange`。
- deflate 默认禁用；启用时解压输出受 `MaxDecompressedBlockBytes` 硬上限约束。

### 验证

`go test ./avroocf` 覆盖递归模式深度限制、虚高（正/负）块计数、声明块字节数
篡改、同步标记篡改、枚举与 union 越界、截断流、deflate 解压炸弹及正常往返解码。
