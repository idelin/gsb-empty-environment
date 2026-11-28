// Package pathverify 实现 RFC 5280 X.509 证书路径构建与严格验证引擎，
// 面向 TLS 与双向认证（mTLS）客户端的传输安全控制面场景。
//
// 与仅做线性签名回溯的简化实现不同，本包将“路径构建”与“策略验证”分离：
//
//   - 多路径图构建：以 (Subject, AuthorityKeyID -> SubjectKeyID) 为索引，
//     自叶证书向上执行带环检测的回溯搜索，枚举出所有可经密码学验签到达
//     受信任根（Trust Anchor）的候选链。同一 Subject、不同 Issuer 的跨签名
//     桥接证书会产生多条候选链；一条链因有效期、吊销、pathLen 或名称约束
//     失败时，引擎继续验证其他可达信任锚的链，从而实现跨签名自动回溯。
//   - RFC 5280 §6.1.4 pathLenConstraint：仅非自颁发（Subject != Issuer）
//     中间 CA 消耗一级深度；自颁发过渡证书（Subject == Issuer）不消耗；
//     终端叶证书绝不消耗 pathLenConstraint。
//   - NameConstraints：自顶向下累积，permittedSubtrees 逐层取交集、
//     excludedSubtrees 逐层取并集，对 DNS（严格子域边界）、IP CIDR、
//     Email、URI 逐一校验叶证书的全部 SAN。
//   - SAN 压倒 CN：叶证书一旦包含任何 DNS SAN，主机名匹配只校验 SAN，
//     彻底忽略 Subject CN；无 SAN 时才回退到 CN。
//   - KeyUsage/EKU：中间 CA 必须 CA=true 且（若携带 KeyUsage）具备
//     keyCertSign；目标 EKU 必须同时被链上每个携带非空 EKU 的证书放行，
//     AnyExtendedKeyUsage 作为豁免。
//   - 未知关键扩展：任何无法识别且 Critical=true 的扩展立即拒绝该路径。
//   - CRL：校验 CRL 自身签名与 ThisUpdate/NextUpdate 时间窗，命中吊销
//     序列号即拒绝，并可触发向其他候选链的回溯。
//
// 典型用法：
//
//	result, err := pathverify.Verify(pathverify.Options{
//	    Leaf:          leaf,
//	    Roots:         trustAnchors,
//	    Intermediates: intermediates,
//	    EKU:           []pathverify.ExtKeyUsage{pathverify.ExtKeyUsageServerAuth},
//	    DNSName:       "service.example.com",
//	    CurrentTime:   pathverify.AtTime(now),
//	    CRLs:          crls,
//	})
//
// 验证失败时返回 *Error，其 Reason 字段为机器可读原因（Reason* 常量），
// Attempts 字段保留每条候选链的失败诊断，可用于 errors.Is 与运维排障。
package pathverify
