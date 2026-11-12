// Package netsec 实现 BEP 9 ut_metadata 扩展交换时的对端输入校验，
// 供 BitTorrent 客户端的对端消息循环直接调用。
//
// 防护点：
//   - 扩展握手 metadata_size 超过调用方上限（或为负值）立即拒绝并计数；
//   - 分片序号限定在 [0, ceil(metadata_size/16384)-1]；
//   - 每个分片长度必须精确匹配，末片按剩余字节数严格校验；
//   - 重复分片内容必须一致，冲突即判定投毒；
//   - 收齐后拼接结果的 SHA-1 必须等于 info-hash；
//   - 任意校验失败通过 BanBook 给对端累计失败次数，达到阈值即封禁。
package netsec
