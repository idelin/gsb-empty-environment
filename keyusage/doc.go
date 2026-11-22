// Package keyusage 为密钥服务提供用途约束（key usage constraints）组件。
//
// 核心能力：

//   - 按密钥声明用途与算法白名单校验每一次调用（Enforcer.Check）；
//   - 拒绝签名密钥用于加解密、或加密密钥用于签名（跨用途族）；
//   - 拒绝弱算法与非白名单算法，并把每次拒绝写入审计 Sink；
//   - 派生密钥只能继承父密钥用途的子集（Enforcer.Derive）；
//   - 包装密钥（仅声明 wrap/unwrap）不得加解密业务数据。
//
// 典型用法：
//
//	sink := keyusage.NewMemorySink()
//	enf := keyusage.NewEnforcer(sink)
//	err := enf.Register(keyusage.Key{
//		ID:        "sig-1",
//		Algorithm: keyusage.AlgECDSAP256,
//		Usages:    []keyusage.Usage{keyusage.UsageSign, keyusage.UsageVerify},
//	})
//	if err := enf.Check(key, keyusage.UsageEncrypt); err != nil {
//		// err 为 *keyusage.DenialError，拒绝事件已写入 sink。
//	}
package keyusage
