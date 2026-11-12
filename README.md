# JMAP方法调用账户授权与引用

JMAP 把多个方法调用放进同一个请求，攻击者在其中混入对他人 accountId 的调用，或用结果引用把一个调用的输出接进另一个账户的调用。给TypeScript JMAP 服务写供请求分发器调用的授权包，按 RFC 8620：每个调用的 accountId 必须属于当前会话且持有该方法所需的读或写权限，否则只让该调用返回 forbidden 错误并继续后续调用；方法所属能力须在 using 中声明，未声明返回 unknownCapability；调用数超过 maxCallsInRequest 整个请求以 limit 错误拒绝；结果引用须指向同请求内更早且成功的调用，引用解出的 id 再按被调用方的 accountId 重新授权。用混入他人账户、未声明能力、引用失败调用和跨账户引用验证。
