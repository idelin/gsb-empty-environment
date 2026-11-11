# 握手降级日志证明

协商日志声称双方只支持弱算法，安全测试需要核对是否遗漏了原始能力声明。写供Go验证宿主调用的转录检查包，双方能力列表、选择与最终确认都带原始字节和认证标记；选择必须是双方受认证能力的交集，并满足输入偏好序。未认证能力只能作为线索不能证明安全。用删项、重排和跨会话转录拼接测试。

## 包结构

- `transcript.go`：记录、标记、策略与报告类型，`Verifier` 构造。
- `encoding.go`：原始字节编解码、Ed25519 签名载荷、转录链哈希。
- `verify.go`：验证主流程、交集与偏好序检查、最终裁决。
- `builder.go`：`Recorder`，供对端/测试生成带正确签名的转录。
- `*_test.go`：正常协商、仅弱算法、删项、重排、跨会话拼接等测试。

## 转录与信任模型

固定四条记录，每条都带不可变原始字节 `Raw` 和 Ed25519 认证标记 `Marks`：

1. A 能力列表（A 签名）
2. B 能力列表（B 签名）
3. 选择（B 签名，内嵌两份能力列表的 SHA-256）
4. 最终确认（A、B 双签，内嵌前三记录的链哈希）

签名载荷做域分离，并绑定会话 ID、记录序号与原始字节，因此删项、重排、
跨会话重放都会导致签名失效；选择与确认还通过内嵌哈希绑定上下文，防止
字节替换和转录拼接。

裁决分两级：

- `Report.Authentic`：全部记录结构合法、顺序正确、由必需的受信签名者认证。
- `Report.Secure`：在 `Authentic` 之上，选择位于双方受认证能力交集、满足
  输入偏好序，并且命中宿主 `Acceptable` 安全策略。

未通过认证的记录中的算法只进入 `Report.Clues`，永不进入
`AuthenticatedCapabilities*`、`Intersection` 或 `Selected`，只能作为排查线索。

## 宿主调用

```go
verifier, _ := netsec.New(netsec.Policy{
    SessionID:       sessionID,
    PartyAKey:       trustedPubA,
    PartyBKey:       trustedPubB,
    PreferenceOrder: []string{"AES-GCM-256", "CHACHA20"}, // 可选，缺省用 B 的认证顺序
    Acceptable:      map[string]bool{"AES-GCM-256": true}, // 可选安全策略
})
report := verifier.Verify(records)
// report.Authentic / report.Secure / report.Findings / report.Clues
```

运行测试：`go test -race -cover ./...`
