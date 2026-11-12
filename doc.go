// Package netsec 提供文件访问前调用的 EVM（Extended Verification Module）
// security.evm 扩展属性评估能力。
//
// security.evm 保护文件的安全相关属性：security.ima、security.selinux、
// uid、gid、mode，以及本机 HMAC 形式下的 inode 号与 i_generation。
// 本包支持两种载荷形式（首字节为类型）：
//
//   - TypeHMAC（2，本机 HMAC）：按固定顺序
//     类型 → security.ima → security.selinux → inode → i_generation
//     → uid → gid → mode 计算 HMAC-SHA256，用主机密钥重算并常量时间比对。
//   - TypePortableSignature（5，可移植签名）：规范输入刻意不含
//     inode 与 i_generation，使文件可跨主机/inode 迁移，用受信公钥验证。
//
// 评估结论通过 Result 表达，默认拒绝（fail-closed）：
//   - 所需密钥未加载时一律拒绝，绝不退回只检查 security.ima；
//   - 未知类型字节、畸形或缺失的扩展属性一律拒绝；
//   - HMAC 不匹配时，借助签发期 inode 记录区分“属性被篡改”
//     （ReasonAttrMismatch）与“文件连合法 HMAC 被复制到新 inode”
//     （ReasonInodeMismatch）；
//   - 硬链接只改路径不改 inode，规范输入不含路径，因此正常通过。
//
// 本包只对调用方采集的 FileMeta 做纯元数据评估，不直接读取文件系统，
// 因而可在任意平台上进行确定性测试。
package netsec
