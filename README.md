# 私网访问预检与地址空间

攻击者页面借 DNS 重绑定或 IPv4 映射的 IPv6 地址，让公网页面访问内网设备。为TypeScript浏览器网络栈写可导入的私网访问预检组件：按连接实际对端 IP 而非主机名划分 loopback、private、public，::ffff:a.b.c.d 先还原再判定；请求方比目标更公开须先预检，响应带 Access-Control-Allow-Private-Network: true，缓存键含目标 IP；实际连接 IP 与预检不同则中止。测重绑定、映射地址、缓存键。
