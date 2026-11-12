// Package netsec 提供可导入的 Web Push 订阅与发送组件，内置两类核心防护：
//
// SSRF 防护（Guard）：订阅端点强制 https、主机必须命中推送服务白名单、
// 解析后的每个 IP 都要通过公网地址复核；自定义拨号器在每次建连前再次
// 解析复核以抵御 DNS 重绑定；发送过程中的重定向逐跳重新执行同一套校验。
//
// 越权防护（Registry / TopicStore）：订阅绑定租户 ID 与 VAPID 公钥指纹，
// 跨租户访问统一按“不存在”处理；Topic 限定为不超过 32 位的 URL 安全
// Base64 字符，替换语义以 (订阅, Topic) 为键，只在同一订阅内生效，
// 无法借同名 Topic 覆盖他人待投递通知。
//
// 载荷按 RFC 8291 的 aes128gcm 方案加密，鉴权按 RFC 8292 生成 VAPID JWT。
package netsec
