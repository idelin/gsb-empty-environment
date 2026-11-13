# CoAP分块传输的重组预算与ETag一致

## `block1` 重组包

可导入路径：`example.org/netsec/q0782/block1`。面向 CoAP 服务端的 Block1
（RFC 7959）请求体重组，内置抗滥用预算：

- SZX 只接受 `SZX16`..`SZX1024`（16–1024 字节），保留值 7 拒绝；同一传输中
  SZX 变化按 `4.00` 拒绝并丢弃该传输。
- 传输只能从 NUM 0 开始；大 NUM 首包不创建任何状态，跳号/重放旧号回
  `4.08`（`Result.WantNUM` 携带期望块号，可填入响应 Block1 选项）。
- 首包声明的 Size1 超过 64 KiB 直接回 `4.13`；未声明 Size1 时实际数据也受
  64 KiB 硬上限约束。
- 同一对端（`Fragment.Peer`）最多 4 个未完成传输，第 5 个回 `5.03`；完成或
  丢弃后自动释放配额，可用 `Prune` 清理僵尸传输。
- ETag 或 Content-Format 中途变化（包括选项有无的变化）回 `4.09` 并丢弃整个
  传输。

最小集成示例：

```go
import "example.org/netsec/q0782/block1"

var ra = block1.NewReassembler()

func handleBlock(f block1.Fragment) block1.Result {
    // f 的字段从 Block1/Size1/ETag/Content-Format 选项解析得到。
    return ra.Process(f)
    // StatusComplete 时 result.Body 即完整请求体；
    // 其余状态用 result.Code 作为 CoAP 响应码。
}
```

攻击者用 Block1 只发编号很大的块来占内存，或在传输中途换内容。为Go CoAP 服务端写可导入的 Block1 重组包：块大小 SZX 取 16 到 1024 字节且整个传输中不变；块必须按序，NUM 跳号拒绝并回 4.08；声明的总大小（Size1）超过 64 KiB 直接回 4.13；同一对端同时最多 4 个未完成传输；ETag 或 Content-Format 中途变化则丢弃整个传输。用大跳号、超限 Size1、第 5 个传输和中途换 ETag 验证。
