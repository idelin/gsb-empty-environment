# Cedar禁令求值出错放行

Cedar 风格的策略引擎里 forbid 优先于 permit，但某条 forbid 在求值时因属性缺失而出错会被跳过，攻击者故意省略上下文属性，让禁令失效。为Go授权服务写可导入的策略评估包，供请求授权调用，只处理本题子集：permit 与 forbid 的 (principal, action, resource) 加 when 条件，条件支持 ==、!=、<、>、&&、||、has、属性访问 a.b 与 in；默认拒绝；任一 forbid 满足即拒绝；求值出错（访问不存在属性、类型不符）的策略视为不适用并记入诊断，但出错的是 forbid 且调用方设置了 failClosed 时整个请求拒绝；用 has 保护的属性访问不出错；未设置 failClosed 时，出错的 forbid 被跳过，permit 仍须有求值不出错且满足的策略才放行；输出决定及每条策略的状态（满足、不满足、出错）。用缺属性的 forbid 被跳过、failClosed、has 保护、类型不符和 permit 出错验证。
