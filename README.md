# DNS Cookie时间戳与源地址

攻击者伪造受害者源地址发 UDP 查询，借大应答放大。为Go DNS服务写可导入的 Cookie 门禁：客户端 Cookie 须8字节；服务端 Cookie 16字节：版本1、3字节保留、4字节时间戳、8字节 HMAC-SHA256(密钥, 客户端Cookie‖前8字节‖客户端IP) 截断；时间戳早不超1小时、晚不超5分钟，按32位序列号回绕。缺服务端 Cookie 只回不大于请求的 TC 应答，无效回 BADCOOKIE。测伪造 IP、过期、回绕。
