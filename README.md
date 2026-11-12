# GNAP交互结束哈希校验

攻击者把自己的 interact_ref 塞进受害者的回调，想把授权结果绑到受害者会话。为TypeScript客户端写可导入的 GNAP 交互结束校验组件：回调后算 base64url(SHA-256(client_nonce+LF+as_nonce+LF+interact_ref+LF+授权请求URL))，与 hash 参数常量时间比较，一致才用 interact_ref 续期；每个事务只收一次回调，且须出现在登记的浏览器会话里。测伪造 ref、跨会话、重复回调。
