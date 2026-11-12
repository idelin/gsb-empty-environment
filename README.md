# SSH证书登录限制执行

跳板机用SSH用户证书发放受限登录，攻击者会改选项、冒用主体名或换网段。为Go会话网关实现证书授权包，供认证通过后回调调用：只信给定CA，principals含映射登录名，有效期按半开区间，未知critical option拒绝，source-address匹配真实对端CIDR，force-command固定执行内容并忽略客户端请求。用过期、网段外、未知选项和合法证书验证。
