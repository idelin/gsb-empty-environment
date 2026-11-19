# Kerberoasting TGS 票据请求检测

`netsec` 是一个可导入 Go 域控日志检测宿主的 TGS 事件判定包。它从 Windows
4769 等 TGS 事件中提取请求账号、服务账号、SPN 和票据加密类型，识别批量
RC4 服务票据请求以及违反 AES 强制策略的请求。

## 检测规则

- 只处理 `EType == 0x17`（RC4-HMAC）的事件。
- 忽略服务账号以 `$` 结尾的机器账号，以及 `krbtgt`（含 `krbtgt/...` SPN）。
- 同一请求账号在 10 分钟滑动窗口内取得 5 个及以上不同 SPN 的 RC4 票据时，
  产生 `bulk_rc4_service_tickets` 告警。
- SPN 比较不区分大小写；同一 SPN 的重复请求或续票不会重复增加计数。
- 服务账号已被域策略强制 AES 时，任意一张 `0x17` 票据立即产生
  `aes_enforced_rc4_service_ticket` 告警。
- 窗口边界包含恰好 10 分钟前的事件；超过 10 分钟的事件会先过期再计数。

## 使用

```go
package main

import (
	"fmt"
	"time"

	netsec "example.org/netsec/q0697"
)

func main() {
	detector := netsec.NewDetector(netsec.Config{
		Window:               10 * time.Minute,
		DistinctSPNThreshold: 5,
		AESEnforcedAccounts:  []string{"svc_legacy"},
	})

	event := netsec.Event{
		Time:              time.Now(),
		RequestingAccount: `CORP\alice`,
		ServiceAccount:    "svc_sql",
		SPN:               "MSSQLSvc/sql01.corp.local:1433",
		EType:             netsec.RC4EType,
	}

	if alert := detector.Observe(event); alert != nil {
		fmt.Printf("alert: %v %+v\n", alert.Reasons, alert)
	}
}
```

`Evaluate` 返回 `(Alert, bool)`，`Observe` 返回 `*Alert`，`Process` 返回
零或一条告警。对于无序导入的日志，使用 `ProcessBatch`，它会先按时间稳定
排序，再复用同一个检测器状态。

### 字段映射

| Event 字段 | 4769 字段 | 说明 |
| --- | --- | --- |
| `RequestingAccount` | `TargetUserName` | 发起 TGS 请求的普通域账号 |
| `ServiceAccount` | `ServiceName` | SPN 所属服务账号，用于策略判断和机器账号过滤 |
| `SPN` | `ServiceID` | 请求的服务主体名称 |
| `EType` | `TicketEncryptionType` | `0x17` 为 RC4-HMAC |
| `Renewal` | 续票标记 | 可作为审计字段；同一 SPN 续票自然去重 |

`ServiceAccount` 为空时，会从 SPN 的主机部分推导，例如
`krbtgt/CORP.LOCAL` 推导为 `krbtgt`，`CIFS/fileserver$` 推导为
`fileserver$`。

### AES 策略来源

服务账号的 AES 强制状态可按优先级从以下来源提供：

1. `Event.AESEnforced`：事件源已完成账号策略关联。
2. `Config.AESEnforcedAccounts` 或 `Detector.SetAESEnforced`：在包内维护账号集合。
3. `Config.AESEnforced` 或 `Detector.SetAESPolicy`：回调外部账号策略库。

回调在内部锁之外执行，可以安全查询外部存储；不要在回调中递归调用同一个
`Detector`。

## 验证

```sh
go test ./...
go test -race ./...
go vet ./...
```

测试覆盖阈值触发、同 SPN 去重、续票、机器账号与 `krbtgt` 排除、AES 强制
策略、请求账号隔离、乱序批处理以及 10 分钟窗口内外边界。
