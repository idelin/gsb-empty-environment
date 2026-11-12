# OAuth资源指示符受众绑定

OAuth 授权服务器签发令牌时，攻击者在 token 请求里改 resource 参数，想让一个令牌被多个资源服务器接受。为Go授权服务器写可导入的 RFC 8707 resource 参数处理包：resource 须是不含 fragment 的绝对 URI，可重复；授权请求里批准的 resource 集合固定，token 请求的 resource 只能是其子集，缺省时取批准集合中唯一的一个，多于一个而未指定则返回 invalid_target；签出令牌的 aud 恰为所请求的 resource（多个时为数组），scope 按调用方给出的各 resource 批准范围取交集；刷新令牌换新令牌时同样受限于原批准集合且不能新增；资源服务器按自身标识逐字节比较 aud，不做前缀匹配。测超集、fragment、未指定多个、刷新扩增。
