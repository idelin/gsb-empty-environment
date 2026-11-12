# 金丝雀逐字节爆破识别

带自动重启的网络服务会 fork 子进程处理连接，攻击者可逐字节猜 stack canary，每次猜错使子进程崩溃。写供 Go 服务监督器调用的爆破识别组件：输入子进程退出事件、信号、载荷长度和来源地址，识别同一来源下载荷长度逐字节增加且崩溃签名一致的序列，达到调用方阈值时触发封禁回调。用逐字节爆破、多来源分散崩溃、真实偶发崩溃和成功后继续攻击验证不会误封正常客户端。

## 用法

```go
detector := netsec.New(netsec.Config{
    Threshold: 8,       // 同来源连续 8 次爆破崩溃即封禁
    MaxStep:   1,       // 相邻崩溃载荷长度最多递增 1 字节
    BanTTL:    time.Hour,
    OnBan: func(source string) {
        firewall.Block(source) // 在检测器锁之外执行，可做网络操作
    },
})

// 监督器每次回收子进程时录入事件
banned := detector.Observe(netsec.ExitEvent{
    Source:     remoteAddr,
    PayloadLen: len(payload),
    Signal:     processState.Sys().(syscall.WaitStatus).Signal(),
    ExitCode:   processState.ExitCode(),
})
```

## 判定规则

- 按来源独立计数；只有「崩溃签名（信号+退出码）一致」且「载荷长度以 1..MaxStep 严格递增」的连续崩溃才累积。
- 签名变化、长度不变/倒退/跳跃超过 `MaxStep` 都会重置序列；正常退出事件不参与也不中断序列。
- 达到 `Threshold` 触发一次 `OnBan`；`Banned`/`Unban`/`Reset` 管理封禁状态，`Stats` 提供计数快照。

## 验证场景

`detector_test.go` 覆盖：逐字节爆破命中阈值、多来源分散崩溃、偶发崩溃（签名变化/长度不增长/穿插正常退出）、成功连接穿插下的持续攻击与正常客户端不误封，以及 TTL 到期、并发安全（`-race`）等边界。
