# Kerberoasting票据请求检测

攻击者拿到普通域账号后批量请求服务票据，再离线破解 RC4 加密的服务口令。为Go域控日志检测宿主写可导入的 TGS 事件判定包：只看服务账号（排除以 $ 结尾的机器账号和 krbtgt）；同一请求账号 10 分钟内对 5 个以上不同 SPN 取得 etype 0x17 票据时告警，同一 SPN 重复请求不重复计数；域策略已强制 AES 的账号一旦出现 0x17 即告警。用续票、机器账号和跨窗口验证。

## 包说明

`netsec` 包针对域控安全日志 **Event ID 4769（Kerberos 服务票据请求，TGS）**
做有状态判定，宿主只需把归一化后的事件逐条喂给 `TGSDetector.Process`。

### 判定规则

1. **作用域过滤**：只处理成功（`Status=0x0`）且 `TicketEncryptionType=0x17`
   （RC4-HMAC / ARCFOUR-HMAC）的 TGS 事件；服务名以 `$` 结尾的机器账号
   （自动剥离 `@REALM` 后再判断）和 `krbtgt` 直接排除，不进入任何窗口状态。
2. **SPN 批量请求（Kerberoasting 扫描）**：按请求账号维护 10 分钟滑动窗口，
   窗口内不同 SPN 达到 **5 个**（含 5，可通过 `SPNThreshold` 配置为严格大于 5）
   即告警；同一 SPN 的重复请求只记一次，且不刷新该 SPN 在窗口中的存活时间。
   每轮窗口只告警一次，旧 SPN 全部滑出后自动重新布防。
3. **AES 强制账号出现 RC4**：`AESOnlyServiceAccounts` 中的服务账号一旦被签发
   0x17 票据立即告警，与窗口/阈值无关，每次出现都告警（支持纯账号名与
   `SPN/host`、`账号@REALM` 形式的大小写无关匹配）。
4. **续票过滤**：`TicketOptions` 含 `KDC_OPT_RENEW`（`0x00000002`）的续票
   请求默认整体跳过；注意普通 Windows 票据普遍带 `KDC_OPT_RENEWABLE`
   （`0x00800000`，仅表示“可续期”），不会被误判为续票。可用
   `IgnoreRenewals: false` 关闭该过滤。

### 接入示例

```go
import "example.org/netsec/q0697"

cfg := netsec.DefaultDetectorConfig()
cfg.AESOnlyServiceAccounts = []string{"svc_mssql", "svc_web"}
detector := netsec.NewTGSDetector(cfg)

// 宿主从 4769 事件 XML/JSON 映射出 TGSEvent 后逐条调用：
alerts := detector.Process(netsec.TGSEvent{
    Time:          eventTime,
    AccountName:   targetUserName,        // 请求账号
    ServiceName:   serviceName,           // 请求的 SPN
    EType:         netsec.ParseEType(ticketEncryptionType),
    TicketOptions: netsec.ParseTicketOptions(ticketOptions),
    StatusCode:    statusCode,
})
for _, alert := range alerts {
    // alert.Kind:
    //   netsec.AlertKerberoastingSPNSweep   批量 SPN 扫描
    //   netsec.AlertAESEnforcedRC4Ticket    AES 强制账号出现 RC4
    forward(alert)
}
```

### 设计要点

- 检测器并发安全（内部互斥），支持事件乱序到达：窗口按事件自身时间戳计算。
- `Reset()` 清空窗口状态；长时间运行的宿主可周期性调用 `Purge(now)`
  回收已完全过期的账号状态。
- `ParseEType` / `ParseTicketOptions` 兼容 `0x` 前缀十六进制与纯十进制
  两种日志编码；无 `0x` 前缀时按十进制解析，避免歧义。

## 验证

```sh
go test ./...
```

测试覆盖：阈值边界（4 个不告警 / 5 个告警）、同 SPN 重复不计数、10 分钟
窗口滑出与重新布防、机器账号与 krbtgt 排除、AES 强制账号即时告警、续票
跳过与普通可续期票据不误杀、失败请求与 AES etype 过滤、请求账号隔离、
大小写归一、乱序事件以及解析函数边界。
