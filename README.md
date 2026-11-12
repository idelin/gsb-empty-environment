# ValidatingAdmissionPolicy匹配范围覆盖分析

Kubernetes 的 ValidatingAdmissionPolicy 用匹配约束声明规则作用的资源，攻击者经 pods/ephemeralcontainers 这类子资源、旧版 API 版本，或 CREATE 以外的操作，绕过只对 pods 的 CREATE 生效的校验。为 Go 集群治理组件写供策略保存前调用的覆盖分析包：输入策略的包含规则与排除规则（apiGroups、apiVersions、operations、resources，含 * 与 pods/* 通配、scope）、匹配策略（Exact 或 Equivalent）、集群启用的资源版本表（含等价版本）和组织要求被覆盖的清单（资源、子资源、操作）；报告未覆盖项；Equivalent 下按等价版本补全，Exact 下旧版本不算；排除规则先从包含规则里扣除；* 只匹配主资源，要含子资源须写 */* 或 pods/*。用子资源遗漏、旧版本、仅 CREATE、排除规则扣除和完整覆盖验证。
