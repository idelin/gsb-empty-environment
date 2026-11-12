# 安全压缩块引用计费

存储解码器允许块引用此前字典块，攻击者可用极小引用图触发大量重复解码。实现供Go存储宿主调用的按需读取包，先校验引用图无环，再按实际解码字节与引用展开次数双重计费；共享子块可缓存但不得绕过逻辑输出上限。用菱形依赖、超长声明及取消后重入测试资源账与返回前缀。

## 设计

- `Open` 在解码任何载荷字节前，对根块可达的引用图做带色 DFS：拒绝环、缺失目标、
  超深/超块数，并以后序方式用字面量与引用展开尺寸核对每个块的 `Declared`，
  超长或不足声明直接失败。
- 读取阶段由两台相互独立的表同时计费：
  - 逻辑输出字节表 `Limits.MaxOutputBytes`；
  - 引用展开表 `Limits.MaxExpansions`，即使目标命中缓存也计数。
- 字典块解码后进入容量受限的缓存供菱形等共享结构复用；缓存只省解码工作，
  每次向外发射仍走同一输出计费路径，因此无法绕过逻辑输出上限。
- `ReadContext` 支持取消：已返回的前缀与已记账字节保留，取消时回滚尚未解析的
  展开计数，之后用新 context 重入会从原位置继续且不重复计费。

## 用法

```go
r, err := netsec.Open(ctx, store, rootID, netsec.Limits{
    MaxOutputBytes: 1 << 20,
    MaxExpansions:  10000,
})
if err != nil { /* 图非法：环 / 超长声明 / 超预算 */ }
defer r.Close()

buf := make([]byte, 4096)
for {
    n, err := r.ReadContext(ctx, buf)
    host.Write(buf[:n])
    if errors.Is(err, io.EOF) { break }
    if err != nil { /* context.Canceled 或达到计费上限 */ break }
}
fmt.Println(r.Account()) // 输出字节、展开次数、缓存占用与命中
```
