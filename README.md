# Layered Compression Container

仅使用 Go 标准库；压缩算法是包内自带的小型 RLE，没有调用 `compress/*` 或其他同功能压缩库。

## 预算

`Limits` 在一次分层解码中由所有层共享：

- `MaxInputBytes`：所有层编码输入长度之和。
- `MaxOutputBytes`：所有中间解码输出和最终解码输出长度之和。
- `MaxLayers`：允许的层数。
- `MaxExpansionBasisPoints`：扩展上限，按基点表示；`1000` 为 10 倍，`10000` 为 100 倍。

解码先读取固定头和层表，在分配 payload、层缓冲或创建输出临时文件前完成预算预检；每层运行时还会再次预留输入和输出额度。因此单层合法但跨层累计超预算，或逐层扩展合法但端到端扩展超限时，都会被拒绝。默认限制为 10 倍扩展；大压缩比必须通过 `MaxExpansionBasisPoints` 明确允许。

## 格式

所有整数使用大端序，层表按“最外层到最内层”排列。

| 区域 | 大小 | 内容 |
| --- | --- | --- |
| Magic | 4 | `LCC1` |
| Version | 2 | 当前为 `1` |
| Layer Count | 2 | uint16 |
| Payload Length | 4 | 最外层编码 payload 长度 |
| Header CRC32 | 4 | 上述 12 字节的 IEEE CRC32 |
| Layer Records | 每层 16 | codec、reserved、encoded length、decoded length、reserved |
| Payload | Payload Length | 最外层 RLE 数据 |
| Payload CRC32 | 4 | 层表和 payload 的 IEEE CRC32 |

RLE 标签低 0 位为 `0` 表示字面量，为 `1` 表示重复单字节；高 7 位为 `count-1`，每段最多 128 字节。

## 运行

```sh
go test -v ./...
go vet ./...
go run ./cmd/lccdemo -workdir /tmp/lcc-demo -layers 2 -expansion-basis-points 10000
```

本环境示例输出：

```text
workdir: /tmp/lcc-demo
raw=10000 bytes container=212 bytes layers=2
round-trip: ok (/tmp/lcc-demo/sample.out)
tight shared output budget: rejected: shared output budget exceeded: 10158 > 10000
temporary outputs before/after rejection: 0/0
```

文件解码先写入同目录临时文件，全部成功并 `fsync` 后再原子重命名；任意错误都会关闭并删除临时文件。
