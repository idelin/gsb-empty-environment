# NEL策略来源限制

Network Error Logging 的策略来自响应头，攻击者在能控制的明文页面或子域上发出 NEL，想收集受害站点失败请求的地址。为TypeScript浏览器网络栈写可导入的 NEL 策略存储（W3C Network Error Logging）：只接受 HTTPS 响应里的 NEL 头，请求主机为 IP 字面量的响应忽略；值为 JSON，report_to 须是同一响应里 Report-To 已声明的组，max_age 为 0 则删除该来源的策略；include_subdomains 命中子域请求时，报告里的 url 只保留 origin（本题规定）；success_fraction 与 failure_fraction 须在 0 到 1 之间，越界整条策略无效；来源按 origin 严格区分，子域的策略不影响父域；报告只投递到该策略自己 Report-To 声明的端点。测明文注入、子域继承、越界概率、未声明的 report_to。
