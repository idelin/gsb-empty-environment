# JWKS地址让验签器用了攻击者的密钥

API网关按JWT头部`jku`去下载密钥，攻击者可指定自己的JWKS并用私钥签发管理员令牌。请实现嵌入TypeScript网关的可导入令牌验签组件。密钥只从按issuer预配置的JWKS地址或固定公钥取得，头部中的jku、jwk、kid不得改变来源；kid只在已配置集合内查找，算法与密钥类型必须匹配，禁止`none`与非对称/对称混用。JWKS缓存按签名与有效期更新，issuer、audience、过期与nonce逐项核对。用自签jku、内嵌jwk、同名kid、算法混淆、缓存投毒和合法令牌验证验签密钥只来自配置。
