# ActivityPub收件箱对象归属校验

ActivityPub 收件箱收到的活动里 actor 与嵌入对象可以互不相干，攻击者持有自己服务器的合法签名，却在活动里冒用他人 actor 或嵌入他人服务器上的对象。给TypeScript联邦服务写供收件箱 POST 调用的授权包，签名与取回由调用方注入：HTTP 签名的 keyId 所属 actor 必须等于活动的 actor；Delete 与 Update 只在对象的 attributedTo 等于该 actor 时生效；Create 与 Announce 嵌入对象的 id 域名与 actor 域名不同，须从对象 id 重新取回并要求取回结果的 attributedTo 一致，否则忽略；对象 id 须是 https 且无 userinfo，取回只做一次且不跟随到其他域名。用冒用 actor、删除他人对象、嵌入外域对象和取回不一致验证。
