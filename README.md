# FedCM账户选择请求来源

身份提供方在 FedCM 流程里以用户的 Cookie 返回账户列表，攻击者页面想让它直接返回账户信息，或在 id_assertion 端点冒用别的依赖方的来源。为TypeScript身份提供方写供端点处理器调用的 FedCM 请求校验包：accounts 端点须带 Sec-Fetch-Dest: webidentity，缺失或其他值拒绝，不得带 Origin 与 Referer，且不允许重定向；id_assertion 为 POST，Origin 须是该 client_id 在 client_metadata 中登记的来源之一，正文 account_id 必须属于 Cookie 会话的账户列表；client_metadata 不得带 Cookie；disclosure_text_shown 缺失或 false 时不得发放含邮箱的断言；同一 nonce 不得重放，响应须带 Vary 与禁缓存头。用页面直接 fetch、错误来源、他人 account_id、带 Cookie 的元数据请求、缺失披露与重放验证。
