// Package netsec 为 Go TLS 服务端握手路径提供证书压缩 (RFC 8879):
// 只使用客户端 compress_certificate 扩展列出的算法; 压缩结果按
// (算法, 字典 ID, 证书链 SHA-256) 缓存; 字典为本题约定的扩展,
// 按租户 SNI 隔离; 解压长度严格校验, 异常一律以 TLS 警报中止握手。
package netsec
