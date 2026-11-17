# 请求去重与响应重放隔离（`dedup`）

面向可靠 UDP 服务的请求去重包，解决重传报文导致的两个问题：

1. 同一请求的重传不应重复执行昂贵的处理器、重复产生大响应；
2. 已产生的大响应只能重放给同一“验证身份”，不能被未验证或已变化的来源冒领。

## 验证身份

每个请求由 `dedup.Identity` 三元组唯一确定，任一不同都不会命中去重或缓存：

| 字段 | 含义 |
| --- | --- |
| `AddrGen` | 来源地址验证代次。地址所有权校验通过后递增；未验证来源使用不与有效代次冲突的临时代次。地址一变（源 IP/端口变化、重新验证），旧代次缓存即无法重放。 |
| `Digest` | 请求正文摘要（建议 SHA-256），防止同序号不同正文互相撞缓存。 |
| `Seq` | 客户端在该验证身份下分配的请求序号。 |

身份在内部编码为 `AddrGen(8) || DigestLen(8) || Digest || Seq(8)` 的定长键，
不存在分隔符歧义；传入的摘要会被复制，调用方之后复用缓冲区不影响去重。

## 语义

- **进行中合并**：同一身份的请求正在处理时，重传成为等待者，只执行一次
  `Handler`，所有等待者收到顺序一致的响应分片流。
- **缓存重放隔离**：只有成功完成的响应才按身份缓存；重放只认同一身份。
  失败响应不缓存，重传会重新执行处理器。
- **取消隔离**：`Execute` 的 `ctx` 或 `Stream.Close` 只关闭当前等待者的流；
  处理器绑定 `Deduper` 生命周期，单个等待者取消不影响处理器和其他等待者。
- **流式部分发送**：处理器通过 `ResponseWriter.Write` 逐片写出大响应，
  等待者实时按序收到分片；缓存也保留分片边界，重放顺序与首次发送一致。
- **内存边界**：`WithCacheCapacity` 限制缓存条目数（FIFO 淘汰，默认 1024），
  设为 `0` 可只做进行中合并不缓存；所有分片在入缓存和投递时都会复制，
  读取方修改缓冲区不会污染缓存或其他流。

## UDP 服务集成示例

```go
type Server struct {
    deduper *dedup.Deduper
    // addrGen 按来源地址维护当前验证代次；未通过地址验证的来源使用临时代次。
    addrGen map[string]uint64
}

func (s *Server) handle(conn *net.UDPConn, addr *net.UDPAddr, seq uint64, body []byte) {
    gen := s.addrGen[addr.String()] // 未验证来源返回 0 等临时代次
    sum := sha256.Sum256(body)

    id := dedup.Identity{AddrGen: gen, Seq: seq, Digest: sum[:]}
    stream, err := s.deduper.Execute(context.Background(), id, sum[:])
    if err != nil {
        return // 例如 Deduper 已关闭
    }
    defer stream.Close()

    for event := range stream.Events() {
        if event.Err != nil {
            return // 处理器失败，不发送响应
        }
        if event.Chunk == nil {
            return // 正常结束
        }
        // 分片只发往当前报文的来源地址；地址变化会得到不同代次的身份，
        // 根本拿不到这个流。
        if _, err := conn.WriteToUDP(event.Chunk, addr); err != nil {
            return
        }
    }
}
```

## 测试

```sh
go test -race -count=1 ./dedup/
```

覆盖场景：来源地址（验证代次）变化时进行中与缓存均不串用、相同序号不同正文各自独立去重、响应分片的部分发送与有序重放、取消单个等待者不影响其他人、失败不缓存以及 FIFO 缓存淘汰。
