// Package netsec 实现父区（注册局/注册商侧）对子区提交的 CDS/CDNSKEY
// 记录的自动化接纳判定（RFC 8078），面向“被攻陷权威服务器注入 CDS”的
// 威胁模型。
//
// 接纳一道 CDS/CDNSKEY 信号必须同时满足：
//
//   - CDS 与 CDNSKEY RRset 都带有效 RRSIG，且签名钥是子区已发布、
//     置 SEP 位的 KSK，并且该 KSK 被父区当前 DS 集逐字节指向；
//   - 每一轮观察中全部权威服务器返回的 CDS/CDNSKEY 集完全一致
//     （顺序无关的规范指纹比较）；
//   - 同一记录集连续观察满 Config.RequireHours 小时，轮询间隔超窗、
//     任一轮校验失败或时钟漂移都会清空观察状态并重新计时；
//   - CDS 与 CDNSKEY 并存时，二者必须能互相推导（同一组 KSK、同样的
//     摘要类型，逐字节相同）；
//   - 删除信号只能是单独一条规范的 CDS 0 0 0 00（CDNSKEY 为 0 3 0 00），
//     与其他记录混放或字段变体一律拒绝；
//   - 非删除信号中的每条新 DS 都必须对应子区已发布的 DNSKEY；
//     仅提交 CDNSKEY 时其 RDATA 必须逐字节已发布。
//
// 典型用法：
//
//	tr, err := netsec.NewTracker(cfg, parentDS, publishedSigners)
//	// 每小时对全部权威服务器做一轮轮询：
//	decision, err := tr.Observe(netsec.Poll{Zone: zone, At: now, Servers: obs})
//	switch decision.Action {
//	case netsec.ActionReplace:
//		// 用 decision.NewDS 更新父区 DS
//	case netsec.ActionDelete:
//		// 移除父区 DS
//	case netsec.ActionPending:
//		// err（若有）携带具体拒绝规则，可用 errors.Is 匹配哨兵错误
//	}
//
// 本包只依赖 Go 标准库，支持 RSA/SHA-1/256/512、ECDSA P-256/P-384
// 与 Ed25519 的 RRSIG 校验及 SHA-1/SHA-256/SHA-384 DS 摘要。
package netsec
