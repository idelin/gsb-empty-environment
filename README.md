# JAR请求对象嵌套与覆盖

授权请求用 JWT 封装（RFC 9101）后，攻击者可以把 request 参数嵌进请求对象自身、让 URL 查询参数覆盖已签名内容，或套一层未签名的外壳。为Go授权服务器写供授权端点调用的请求对象解析包：request 的值须为 JWS，或外层 JWE 内层 JWS（先解密再验签），JWE 嵌套超过一层拒绝；请求对象的声明里出现 request 或 request_uri 一律拒绝；请求对象有效时，URL 查询里除 client_id 与 request（或 request_uri）外的授权参数全部忽略，不得覆盖对象内容；client_id 在查询与对象里必须相同，iss 等于该 client_id，aud 包含本服务器颁发者标识；alg 为 none 或不在该客户端登记的算法内拒绝。用嵌套 request、查询参数覆盖、client_id 不一致、alg=none 和双层 JWE 验证。
