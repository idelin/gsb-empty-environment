# S7comm作业功能码门控

攻击者向 PLC 发 S7comm 作业：下载块、停止 CPU 或写变量。为Go工业网关写供转发循环调用的 S7 准入包：TPKT 长度须等于 4 加 COTP 的 3 字节再加 S7 PDU 长度，S7 头 10 字节加参数与数据长度须与 PDU 一致；ROSCTR 为 1 的作业按功能码放行，默认只允许 0x04 读变量；0x05 写变量、0x1A 到 0x1C 下载、0x28 与 0x29 启停只允许来自工程站地址。用长度不符、非工程站写变量、停止 CPU 和读变量验证。

## 使用

```go
policy := netsec.NewS7Admission("192.168.10.5")

// srcIP 是转发循环中该帧的来源地址；nil 表示允许转发。
if err := policy.Check(srcIP, packet); err != nil {
    // errors.Is(err, netsec.ErrS7Malformed) 可区分畸形帧与策略拒绝。
    log.Printf("drop S7 frame: %v", err)
    return
}
forward(packet)
```

`S7Admission` 默认只放行 ROSCTR=1 的 `0x04` 读变量作业。写变量
`0x05`、下载 `0x1a`–`0x1c`、PI 服务 `0x28` 和停止 CPU `0x29`
仅当来源 IP 在工程站白名单中才放行；其它 ROSCTR=1 功能码默认拒绝。
结构合法的响应和 userdata 帧会放行，以便转发循环回传 PLC 应答。
