# Linkerd服务器默认拒绝与探针

Linkerd 里某端口一旦被 Server 资源选中就默认拒绝，除非有授权策略；攻击者利用未选中的端口、探针路径放行或身份通配去访问。为Go的网格控制器写可导入的策略解析包：Pod 端口被某个 Server 选中后，请求须命中某条 AuthorizationPolicy，其认证引用支持 MeshTLSAuthentication（身份后缀如 `*.ns.serviceaccount.identity.linkerd.cluster.local`，`*` 只替代最左一个完整标签）与 NetworkAuthentication（CIDR），多个条件任一满足即可；未被任何 Server 选中的端口走集群默认策略；探针（来自调用方给出的节点 CIDR）仅对声明的探针路径与 GET 方法放行，其他路径依然拒绝；一个端口被多个 Server 选中视为配置错误，该端口一律拒绝。测通配跨标签、探针的其他路径、重复 Server、未选中端口。
