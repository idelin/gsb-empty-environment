# 设备管理访问控制规则次序与默认策略

NETCONF 服务器按 NACM（RFC 8341）授权，攻击者所在的用户组借规则次序和默认策略拿到写权限。为Go设备管理组件写可导入的访问判定包：按用户所属组选出规则列表，列表与规则均按序取首条命中的 permit 或 deny；规则路径覆盖其子树，read、create、update、delete、exec 分别匹配；无命中时用三个默认值，节点带 default-deny-all 时不适用默认放行。
