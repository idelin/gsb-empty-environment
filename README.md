# Gradle校验元数据绕过检测

构建网关用 verification-metadata.xml 校验依赖，攻击者利用过宽的 trusted-artifacts 正则、未转义的点号，或让动态版本解析出锁文件之外的版本。为Go网关实现可导入的校验判定包：解析 components 的 sha256 与 trusted-artifacts 的 group、name、version、file 正则（按整串匹配，regex=true 才是正则，否则字面），把 .* 或匹配全部 group 的规则标为风险；解析结果里存在不在 lockfile 的版本，或 1.+ 之类动态版本未被锁文件覆盖时给出绕过告警；also-trust 备用校验和只在列出的算法内有效。用宽正则、点号转义、动态版本和多校验和验证。
