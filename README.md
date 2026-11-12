# VEX抑制采信条件

漏洞聚合依赖供应商的 VEX 声明抑制误报，攻击者可伪造 not_affected 压掉真实漏洞。为Go门禁实现供漏洞聚合调用的 VEX 采信包：CycloneDX 的 analysis.state 取 exploitable、in_triage、false_positive、not_affected、resolved；not_affected 须带 justification（如 code_not_present、code_not_reachable、requires_configuration、protected_by_mitigating_control），缺失按未采信；声明的 affects 须同时匹配 bom-ref 与 purl 版本范围，签发者须在该组件供应商的受信集合内，且声明时间不早于组件版本发布；被采信的 not_affected 才压制告警，其余保持原告警。用缺 justification、签发者不符、版本范围外、旧声明和合法抑制验证。
