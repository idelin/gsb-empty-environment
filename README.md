# 审计策略首条匹配与敏感资源记录级别

集群审计策略按规则顺序取首个命中规则的级别，把 Secret 写成 Request 或 RequestResponse 级别，会让令牌明文进入日志，能读日志的低权限用户即可拿到；而一条放得太靠前的宽泛规则会盖掉后面的精细规则。Go配置审查服务需要可导入的审计策略检查包，输入已解析的规则列表（level、users、userGroups、verbs、resources 的 group 与 resources 与 resourceNames、namespaces、nonResourceURLs）和一组探测请求：字段为空表示任意，非空须命中；resources 里 * 匹配任意资源，子资源写作 secrets/status，secrets/* 匹配其全部子资源；首个命中者决定级别，没有命中视为 None；检查项：对 secrets、configmaps、tokenreviews、serviceaccounts/token 的任何探测，最终级别不得是 Request 或 RequestResponse；被前面规则完全遮蔽、永远不会命中的规则要报告；没有任何规则命中某个探测时，报告缺默认规则。用 secrets 被前置宽泛规则覆盖成 RequestResponse、子资源通配、遮蔽规则和缺默认规则验证。
