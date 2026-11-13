# 验证摘要验证者与策略版本

制品准入服务靠 SLSA VSA 判断上游已验证，攻击者提交自签 VSA、别的 resourceUri 的 VSA 或策略放宽前的旧 VSA。为Go准入服务写可导入的 VSA 判定包：verifier.id 在受信表内，subject 摘要与 resourceUri 等于请求，结果为 PASSED 且 verifiedLevels 含所需等级，policy.digest 等于当前策略，timeVerified 在 24 小时内。
