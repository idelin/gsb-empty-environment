# 角色创建与绑定的提权预防检查

能创建角色或绑定的低权限用户，可能给自己写出更大的权限。为Go准入组件写可导入的 Kubernetes 授权检查包：创建或更新 Role 时其规则须被请求者现有权限覆盖，否则需要 roles 的 escalate 动词；创建绑定时被引用角色的权限须被覆盖，否则需要该角色的 bind 动词；覆盖按 apiGroups、resources（含 pods/exec 与 *）、verbs、resourceNames 判定，命名空间内绑定只看该命名空间权限。用多一个动词、子资源、resourceNames 缩窄和 bind 豁免验证。
