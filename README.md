# blockdict

独立块压缩格式（纯 Go 标准库，自实现 LZ77，不调用压缩库）：

- 共享可选只读字典，块头绑定字典 SHA-256 摘要，摘要不匹配即拒绝解码
- 每块独立，随机解块不依赖前块状态；空字典仍可编码
- 块头定长 54 字节：magic、格式版本、flags、字典摘要、解压长度、
  载荷长度、载荷 CRC32、头 CRC32（全大端）
- 解码输出额度有界（`maxOut`），载荷上限 `MaxPayload` = 4 MiB
- 载荷损坏：头 CRC 保证长度可信，可按长度跳过坏块定位下一块
- 头部损坏：`FindNext` 扫描“magic + 头 CRC 合法”重新同步

## API

```go
dict := blockdict.NewDict(dictBytes)        // nil 即空字典
stream := blockdict.AppendBlock(nil, dict, src)
out, next, err := blockdict.DecodeBlockAt(stream, off, dict, maxOut)
off := blockdict.FindNext(stream, from)      // 头损坏后重新同步
```

## 运行

```sh
go test ./...    # 测试
go run ./cmd/demo
```
