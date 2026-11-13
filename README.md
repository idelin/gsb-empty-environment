# 图表渲染中的集群读取函数与秘密外泄

图表仓库里的第三方模板可以调用 lookup 去读集群里的 Secret，再把值写进 ConfigMap 或注解，渲染产物一旦被日志、缓存或审查页面展示，凭据就泄漏了。Go部署服务需要一个可导入的渲染准入包，输入模板中的函数调用列表（函数名、参数字面量）、渲染后的资源列表（kind、命名空间、name、各字符串字段值）和调用方给的集群 Secret 值查询回调：lookup 的 kind 参数为 Secret，或是动态拼接、无法静态确定时，一律拒绝；getHostByName、env、expandenv、readFile 这类读取外部状态的函数出现即拒绝；渲染后的非 Secret 资源里，任何字符串字段（含标签与注解的值）包含某个 Secret 值（长度不少于 6）的子串，或其 base64、十六进制形式的子串，都拒绝；拒绝时报告资源与字段路径。用 lookup Secret、动态 kind、ConfigMap 里的 base64 子串、十六进制写法和合法的 lookup Deployment 验证。
