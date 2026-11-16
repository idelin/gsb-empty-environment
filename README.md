# IPMI RMCP+ 套件准入与用户枚举防护

`netsec` 是为 Go BMC 模拟固件提供的可导入包，供 RMCP+ 会话建立流程调用。
它集中处理以下准入策略：

- 拒绝认证算法为 0（none）或机密性算法为 0 的 cipher suite。
- 只允许调用方登记的套件编号，例如 3 和 17。
- 未知用户名与错误口令在 RAKP2 阶段返回同形响应，并使用调用方时钟落入同一响应时间档；
  不会提前返回表示“用户不存在”的错误码。
- 拒绝空用户名加空口令的匿名登录。
- 同一源地址 60 秒内 RAKP 失败超过 10 次后，丢弃后续会话请求。

## 使用

```go
package main

import (
	"fmt"
	"time"

	"netsec "example.org/netsec/q0757"
)

func main() {
	clock := netsec.NewManualClock(time.Now())
	admission, err := netsec.NewAdmissionWithConfig(netsec.Config{
		AllowedSuites: []uint8{3, 17},
		Users: map[string][]byte{
			"admin": []byte("correct horse battery staple"),
		},
		Clock:    clock,
		Timing:   &netsec.TimingProfile{Bucket: 100 * time.Millisecond},
		Throttle: &netsec.ThrottleConfig{Window: 60 * time.Second, MaxFailures: 10},
	})
	if err != nil {
		panic(err)
	}

	session, err := admission.OpenSession("192.0.2.10", 3)
	if err != nil {
		panic(err) // ErrSuiteRejected, ErrSuiteNotRegistered 或 ErrThrottled
	}
	if err := session.RAKP1(netsec.RAKP1Request{
		Username:     "admin",
		ClientRandom: make([]byte, 16),
	}); err != nil {
		panic(err)
	}

	challenge, err := session.RAKP2()
	if err != nil {
		panic(err)
	}
	fmt.Printf("challenge length: %d\n", challenge.Len())

	// 模拟客户端用口令计算 RAKP3 认证码。真实固件中该步骤由客户端完成。
	clientCode := session.ClientAuthCode([]byte("correct horse battery staple"))
	if err := session.RAKP3(clientCode); err != nil {
		panic(err) // 未知用户和错误口令都返回同一个 ErrAuthenticationFailed
	}
}
```

也可以直接调用完整模拟登录：

```go
result, err := admission.Login("192.0.2.10", 3, "admin", []byte("correct horse battery staple"))
```

认证失败时 `result.Challenge` 仍然非 nil，便于调用方检查 RAKP2 响应形状。

## 套件策略

`Suite{Number, Auth, Integrity, Confidentiality}` 表示 IPMI
认证/完整性/机密性算法三元组。`Suite.Validate` 和 `Admission.AdmitSuite`
对认证算法 0 或机密性算法 0 返回包含 `ErrSuiteRejected` 的错误，并同时返回
`ErrAuthAlgorithmNone` 或 `ErrConfidentialityNone`。

标准套件 0–17 的内置定义可用来校验套件编号，但内置定义本身不等于放行：

```go
admission, _ := netsec.NewAdmission(3, 17) // 仅登记 3 和 17
_, err := admission.AdmitSuite(2)          // ErrSuiteNotRegistered
_, err = admission.AdmitSuite(0)           // ErrAuthAlgorithmNone 且 ErrSuiteNotRegistered
```

非标准套件或覆盖定义通过 `Config.CustomSuites` 或
`Admission.RegisterSuite` 登记。即使登记了算法 0 的套件，准入时仍会被拒绝。

## RAKP2 同形响应

未知用户名在 RAKP2 阶段使用每个 `Admission` 实例随机生成的 dummy secret
计算 HMAC，因此响应字段长度与错误口令完全一致，并且不会在 RAKP2 返回
用户存在性错误。`RAKP3` 对未知用户名始终拒绝，即使客户端碰巧用 dummy
secret 算出了匹配的认证码。未知用户和错误口令都只返回
`ErrAuthenticationFailed`。

`TimingProfile.Bucket` 控制 RAKP2/RAKP3 的响应时间档。每个步骤从开始时计时，
由 `Clock.Sleep` 补齐到同一个 bucket；使用 `ManualClock` 可以在单元测试中
精确断言两个路径耗时相同。`Bucket <= 0` 关闭补齐，零值 `TimingProfile{}`
因此可用于快速测试。

需要注意：RAKP2 的 HMAC 仍是 IPMI 协议固有的离线字典攻击面。本包禁止
none 套件、统一未知用户/错误口令的响应并对失败节流，但无法让已存在用户的
弱口令免于离线破解；部署方仍应使用强口令并限制网络抓包面。

## 失败节流

`FailureThrottle` 按源地址维护滑动窗口。`host:port` 形式会归一化为 host，
因此同一 IP 的不同源端口共享失败预算。默认窗口为 60 秒、阈值为 10：

1. 第 1–10 次 RAKP 失败正常处理。
2. 第 11 次失败使窗口内失败数变为 11（超过 10）。
3. 后续 `OpenSession` 返回 `ErrThrottled`，不会进入套件或 RAKP 处理。
4. 失败记录超过 60 秒后自动失效。

可通过 `ThrottleConfig{Window: 0, MaxFailures: -1}` 的负阈值关闭节流；
零值字段使用默认值。

## 测试

```sh
go test ./...
```

测试覆盖 cipher 0、未登记套件、未知用户/错误口令同形与同档、匿名登录、
滑动窗口节流以及成功登录。
