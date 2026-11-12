// Package netsec 为基于 NTS（RFC 8915）的 NTP 时间服务提供扩展字段校验。
//
// Server.ProcessPacket 供 UDP 请求处理调用，强制以下安全性质：
//   - Unique Identifier 至少 32 字节且在响应中原样回显，重放的 UID 被拒绝；
//   - NTS Cookie 只能消费一次，且每条成功响应都补发一枚新 Cookie；
//   - NTS Authenticator 的 AEAD 校验覆盖完整 48 字节 NTP 头以及
//     Authenticator 之前的全部扩展字段，任何篡改都导致认证失败；
//   - 任一校验失败均返回错误且不产生响应字节，调用方静默丢弃、不回复时间。
package netsec
