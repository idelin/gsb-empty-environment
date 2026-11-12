# 准入Webhook失败即拒绝

校验型准入 webhook 一旦评估出错就放行，攻击者提交会让规则引擎 panic、超时或让请求超大的对象来绕过策略。为Go服务实现可挂到 net/http 的 AdmissionReview 处理包装：请求体超过 1 MiB、未知 apiVersion 或 kind、评估返回错误、panic、超出给定截止时间，都返回 allowed=false 和 403 状态，响应 uid 必须原样回显请求 uid；评估协程超时后继续运行也不得改写已发出的响应。用 panic、超时、未知 kind 与超大请求体验证。
