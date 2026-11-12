# 钥匙串访问组授权子集

共享钥匙串的访问组由 entitlements 决定，攻击者应用用相同包名后缀但不同团队前缀，或让描述文件通配把额外的组带进来。为Go签名检查组件实现可导入的钥匙串组判定包：entitlements 中 keychain-access-groups 每项形如 TEAMID.xxx，$(AppIdentifierPrefix) 只在开头展开一次并等于签名团队 ID；二进制 entitlements 必须是描述文件授权的子集，描述文件里的 TEAMID.* 通配可覆盖其下任何组但不覆盖其他团队；查询或写入时 kSecAttrAccessGroup 必须在已授权列表内，未指定时取第一项，列表为空则拒绝。用跨团队前缀、通配边界、默认组与超出子集验证。
