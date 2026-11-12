# Notation信任策略范围选择

镜像准入使用 Notation 风格的信任策略，策略的 registryScopes 写错或重叠会让不该被信任的仓库套用宽松策略。为Go准入组件实现可导入的信任策略选择包：scope 形如 registry/repo，仅允许末尾 /* 通配且不跨仓库边界（example.com/prod/* 不匹配 example.com/prod-evil/app），同一镜像命中多条具体 scope 即报配置错误，具体 scope 优先于全局 *；verificationLevel 为 strict 时签名过期或吊销未知即失败，permissive 仅对吊销未知放行，audit 只记录；签名必须绑定镜像摘要而不是标签。用重叠 scope、前缀仿冒、过期与标签签名验证。
