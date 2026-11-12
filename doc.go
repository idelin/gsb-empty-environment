// Package netsec 提供 TLS 握手后使用的证书 SCT（Signed Certificate
// Timestamp）合规策略组件。
//
// 策略只负责规则判定，签名验证（SignatureVerifier）与 CT 日志表
// （LogTable）均由调用方注入，便于接入各自的公钥与日志清单管理。
//
// 判定规则：
//   - 证书有效期不超过 180 天的至少 2 份有效 SCT，超过 180 天的至少 3 份；
//   - 有效 SCT 须来自至少两个不同运营方；
//   - 同一 log_id 只计一份，重复 SCT 不重复计数；
//   - 日志已退休且 SCT 时间戳晚于退休时间、日志被拒绝或待收录的不计；
//   - SCT 时间戳晚于验证时间 5 分钟以上的不计；
//   - SCT 版本不是 v1 的不计。
//
// SCTPolicy 实现了 tls.Config.VerifyConnection 回调所需的签名，
// 可直接挂到握手后的连接校验流程。
package netsec
