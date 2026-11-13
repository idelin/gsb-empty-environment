# 时间戳令牌印记与签发证书用途

签名制品的时间戳令牌可能出自攻击者的假时间戳服务，或借别的文件的令牌证明签名早于证书过期。为Go验签库写可导入的 RFC 3161 令牌核对包：messageImprint 的算法与摘要须等于待证数据摘要，nonce 等于请求 nonce，policy OID 在白名单；签发证书的 EKU 须为唯一的 critical id-kp-timeStamping；genTime 在证书有效期内，加上 accuracy 不晚于验证时刻。
