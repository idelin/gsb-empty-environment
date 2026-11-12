# WASI出站HTTP授权与地址固定

组件经 wasi:http 的 outgoing-handler 发请求，攻击者想借它访问云元数据地址、内网服务，或借解析结果变化绕过白名单。为 Go 宿主写供 outgoing-handler 实现调用的出站授权包：按 scheme 与 authority 匹配调用方登记的允许项（主机名不分大小写，端口缺省按 scheme 补 80 或 443，IPv6 字面量带方括号）；允许域名解析出的地址若属回环、链路本地、私网或 169.254.169.254 即拒绝，连接必须使用校验过的地址而不是再解析一次；请求头 Host 须与 authority 一致，Connection、Transfer-Encoding 与 Upgrade 被拒绝；宿主不跟随重定向。用大写主机名、解析到 169.254.169.254、两次解析结果不同、Host 不一致和合法 443 请求验证。
