# 握手降级日志证明

协商日志声称双方只支持弱算法，安全测试需要核对是否遗漏了原始能力声明。写供Go验证宿主调用的转录检查包，双方能力列表、选择与最终确认都带原始字节和认证标记；选择必须是双方受认证能力的交集，并满足输入偏好序。未认证能力只能作为线索不能证明安全。用删项、重排和跨会话转录拼接测试。

## 包结构

`netsec` 是纯标准库实现的转录检查包，供 Go 验证宿主调用：

- `message.go`：三类消息（`capabilities` / `selection` / `confirmation`）的严格
  JSON 编解码，`Envelope` 同时携带原始字节与宿主给出的 `AuthMark`；
  `TranscriptDigest` / `PrefixDigest` 对原始字节做 SHA-256 摘要。
- `findings.go`：`Transcript` 输入、`Report` 输出、严重级别（`error` / `clue`）
  与全部发现项代码。
- `check.go`：核心检查器 `Check`。
- `*_test.go`：删项、重排、跨会话拼接等攻击测试与一个宿主集成示例。

## 检查规则

- 只有 `AuthMark.Authenticated == true` 且 `Signer == Sender` 的消息才作为证据；
  未认证能力声明进入 `Report.UnauthenticatedCapabilities`，仅作线索（`clue`），
  永远不能证明对端不支持某算法。
- 选择算法必须属于双方受认证能力集合的交集（`Report.Intersection`），
  否则报 `selection_not_in_capabilities`。
- 选择必须是交集中按输入 `PreferenceOrder` 排名最靠前的算法，
  否则报 `preference_violation`；未上榜算法报 `algorithm_not_ranked`。
- 双方都必须有受认证的能力声明、唯一受认证的选择消息和受认证的最终确认。
- 确认消息必须绑定选择算法，并让 `transcript_hash` 等于其之前所有消息原始字节的
  前缀摘要；删项、重排或跨会话拼接都会改变摘要并报
  `confirmation_hash_mismatch`，不同 `session_id` 的拼接另报 `session_mismatch`。
- 任何 `error` 级发现都会使 `Report.Secure == false`。

## 运行测试

```sh
go test ./... -race -count=1
```
