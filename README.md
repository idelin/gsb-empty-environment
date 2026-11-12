# PASETO页脚密钥选择约束

攻击者能改写PASETO页脚中的kid，诱使服务用另一用途的密钥验证。为Go令牌网关实现v4.public解析包，供请求鉴权调用：footer须为合法JSON且kid只映射到登记为v4.public的Ed25519公钥，PAE覆盖头、载荷、页脚和隐含断言；未知键、重复kid、指向v4.local密钥的kid拒绝。用替换页脚、隐含断言不同和合法令牌验证。
