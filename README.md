# Vault包装令牌一次性展开

凭据分发服务把机密包进一次性包装令牌交给接收方，攻击者在途中抢先展开，真正的接收方随后只看到“已用”而无法察觉泄露。为Go服务实现可导入的包装存储包：Wrap 返回令牌和 creation_path，Lookup 不消耗次数，Unwrap 原子消耗且并发时恰有一个成功，到期那一秒起失效，Rewrap 生成新令牌并让旧令牌立即失效；接收方先 Lookup 核对 creation_path 与预期一致再 Unwrap，若 Unwrap 失败要返回“疑似被截获”而不是普通未找到。用并发展开、Lookup 后被抢先展开和 creation_path 被替换验证。
