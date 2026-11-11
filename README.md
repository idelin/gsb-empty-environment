# TCP选项策略一致重写

安全网关清除未授权TCP选项后仍需保持两端能正确处理报文。实现可嵌入Go转发路径的报文重写包，按给定允许集合保留选项，处理EOL与NOP填充，拒绝长度小于二或越过首部的选项；更新首部长度和IPv4/TCP校验和，载荷原样保留。变更前先完整验证，失败不修改输入。用独立校验和计算和逐字节快照测试奇数载荷及最大首部。

## 用法

```go
import "example.org/netsec/uid1298"

// 仅保留 MSS(2)、WScale(3)、SACK-Permitted(4)、Timestamps(8)。
allowed := netsec.NewOptionSet(2, 3, 4, 8)

// packet 为完整 IPv4 TCP 报文（可包含报文尾部的链路层附加字节）。
n, err := netsec.RewriteIPv4TCPPacket(packet, allowed)
if err != nil {
    // 报文不合法：packet 保证逐字节未被修改。
    return err
}
forward(packet[:n])
```

## 语义

- NOP(kind 1) 始终保留；EOL(kind 0) 终止选项解析，其后的字节按填充丢弃。
- 重写后的选项区按 4 字节边界对齐，空余字节以 EOL(0x00) 填充。
- 长度型选项缺少长度字节、长度小于 2 或越过 TCP 首部时返回 `ErrBadOption`。
- 校验全部在独立缓冲中完成；只有完全成功后才回写输入，失败时输入不变。
- 重新计算 TCP data offset、IPv4 总长度与首部校验和、TCP 校验和（含伪首部）。
- TCP 载荷与 IPv4 选项逐字节保留；IPv4 总长度之外的尾部字节不受影响。
