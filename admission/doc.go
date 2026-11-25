// Package admission 是 Go 应用分发网关使用的安装包准入控制库。
//
// 背景：旧版 v1（JAR）签名只对压缩包内各条目做摘要，不覆盖文件头部的字节。
// 攻击者把一段 DEX 拼在安装包前面，可使同一文件既是合法 DEX 又是合法 ZIP：
// 旧签名校验依旧通过，而系统会加载攻击者前缀里的代码（Janus 类攻击）。
//
// 本包在分发侧做结构准入，规则包括：
//
//   - 文件以 DEX 魔数 "dex\n" 开头，或前 8 字节不是 ZIP 本地文件头魔数
//     却存在合法 EOCD 时，判为多态文件并拒绝；
//   - 第一个本地文件头必须位于偏移 0，否则拒绝；
//   - 解析 EOCD 时拒绝 Zip64，拒绝伪造 EOCD 注释长度导致的偏移溢出；
//   - 目标最低系统版本 >= 24（Android 7.0）却没有 v2 以上签名时拒绝，
//     仅 v1 签名只在最低版本 < 24 时接受；
//   - 存在 v2/v3 签名（ID 0x7109871a / 0xf05368c0）时，调用方回调必须确认
//     摘要覆盖「偏移 0 到签名分块之前」「中央目录」「结束记录」三段，
//     且签名分块前后两个长度字段一致，缺任何一段都拒绝；
//   - 签名分块之后必须紧跟中央目录。
//
// 典型用法：
//
//	import "prescreen.local/ps-57aac11a51/admission"
//
//	layout, err := admission.ParseLayout(file, fileSize)
//	if err != nil {
//	    return reject(err)
//	}
//	err = admission.AdmitLayout(layout, minSDK, func(c admission.DigestCoverage, b admission.SigBlock) error {
//	    // 调用方在此完成 v2/v3 摘要与签名的密码学校验，并核对实际摘要
//	    // 保护的字节区间与 c 的三段完全一致。
//	    return verifyCryptographically(file, c, b)
//	})
package admission
