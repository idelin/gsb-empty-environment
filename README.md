# cert-manager签发请求策略

cert-manager 为证书请求向颁发者申请证书，由批准控制器决定放行，攻击者创建请求时把用途改成服务端与客户端并存、把 isCA 置真，或在 SAN 里加入其他命名空间的服务名。为 Go 策略组件写供批准控制器调用的签发策略包：输入请求的 CSR 内容（CN、DNS、IP、URI、邮箱 SAN、密钥用途、isCA、密钥算法与长度）、请求者身份（用户名、组、命名空间）与颁发者策略（命名空间到域名后缀的映射、禁止 isCA、最大有效期、RSA 至少 2048 位、允许的 ECDSA 曲线）；DNS 通配只允许最左一级，域名后缀按标签边界比较（example.com 不匹配 notexample.com）；CSR 内 SAN 与证书资源声明不一致拒绝；URI SAN 中的 SPIFFE ID 单独匹配信任域。用后缀冒充、isCA、跨命名空间 SAN、RSA 1024 和合规请求验证。
