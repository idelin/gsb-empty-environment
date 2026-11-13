# SYN Cookie恢复了错误的协商选项

监听器在半连接队列满时用SYN Cookie编码连接选项，攻击者可伪造ACK让服务端恢复出未协商的窗口缩放或SACK能力。给Go TCP监听宿主实现本题Cookie生成与核验包：宿主有当前与上一代32字节密钥、每代8位时间槽及固定MSS表最多8项。Cookie低24位为HMAC-SHA256(key,源/目的IP和端口、客户端ISN、时间槽、MSS索引、窗口缩放编码、SACK位)的低24位，高8位按从高到低3位MSS索引、4位窗口缩放和1位SACK打包；窗口缩放0至14，未提供编码15。SYN到达时只编码实际出现且语法有效的选项，重复或冲突选项拒绝；ACK核验从cookie高位取选项，分别用当前和上一密钥及对应时间槽重算低位，常量时间比较，且ACK号须为cookie序号加1模2^32。上一代只在一个槽宽限内接受，时间槽回绕必须结合宿主64位纪元判定，不能只比8位值。成功后恢复的MSS、窗口缩放和SACK必须恰为编码值，未知MSS取表中不大于通告值的最大项。用真实HMAC测试改高位、改四元组、旧代跨回绕、无窗口缩放与重复SACK，枚举所有编码验证不会恢复请求中不存在的能力。

## 实现说明

主要 API：

```go
host, err := netsec.NewHost(currentKey, previousKey, time.Second, []uint16{536, 1200, 1460, 9000})
opts, err := netsec.ParseSYNOptions(rawTCPOptions)
cookie, err := host.GenerateCookie(clientAddrPort, serverAddrPort, clientISN, opts)
restored, err := host.VerifyCookie(cookie, ack, clientAddrPort, serverAddrPort, clientISN)
```

- `SYNOptions` 用指针区分“选项不存在”和合法的零值（例如窗口缩放 0）。
- `ParseSYNOptions` 校验选项长度和取值；重复 MSS、窗口缩放或 SACK 选项会被拒绝，取值不同的重复 MSS 被归为冲突。
- Cookie 高 8 位从高到低为 3 位 MSS 索引、4 位窗口缩放编码（15 表示缺失）和 1 位 SACK。
- HMAC-SHA256 输入按固定顺序编码为：源 IP（IPv4 使用 IPv4-mapped 16 字节）、源端口、目的 IP、目的端口、客户端 ISN、宿主 64 位时间槽、MSS 索引、窗口缩放编码、SACK 位，均为网络字节序。缺失 MSS 时 MSS 索引字节的最高位置 1，避免与表项 0 混淆。
- Cookie 低 24 位取 HMAC 输出最后 3 字节；核验时同时尝试当前密钥/当前槽和上一密钥/上一槽，并用 `crypto/subtle` 比较。
- 上一代密钥只在紧接着的一个槽内有效；完整 64 位槽参与 MAC，因此 8 位槽计数回绕后不会把 256 个槽之前的旧 Cookie 当作当前 Cookie。
- MSS 匹配规则是选择表中不大于客户端通告值的最大项，核验恢复的就是该表项。
