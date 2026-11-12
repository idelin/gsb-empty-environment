# Traefik中间件顺序与逐跳头

反向代理先认证再注入身份头，攻击者在 Connection 头里点名该身份头，让代理在注入之后把它当逐跳头删掉，或在注入之前带上伪造的值。为Go的反向代理写可导入的中间件链包，顺序固定为：清洗、认证、注入、转发。清洗阶段删除外部传入的内部身份头 X-Auth-User、X-Auth-Groups，并处理 Connection 点名的逐跳头及 Keep-Alive、Proxy-Authenticate、Proxy-Authorization、TE、Trailer、Transfer-Encoding、Upgrade；Connection 里点名内部身份头的请求在清洗阶段直接 400；链创建后顺序不可调整，调用方给的自定义中间件只能插在注入之后；认证失败的响应不得带注入头。测 Connection 点名、伪造身份头、自定义中间件越位、认证失败的响应。
