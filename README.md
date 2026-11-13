# 无头浏览器渲染的本地协议与子资源拦截

文档与截图服务用无头浏览器渲染用户提供的页面，攻击者在页面里放 iframe 指向 file:///etc/passwd、用 meta refresh 跳向云元数据地址，或用 fetch 访问内网。为TypeScript渲染服务写可传给请求拦截回调的拦截判定包，输入每个请求的 URL、资源类型、发起者 frame 的 URL、重定向链，以及调用方的公网判定回调和主机白名单：协议只允许 http 与 https；file、chrome、view-source、ftp、非本页创建的 blob，以及类型不是图片或字体的 data 一律拒绝；主机经调用方回调解析出的全部地址必须是公网，IP 字面量（含 0x7f.1 与十进制整数写法）按字面判定；重定向链每一跳都重新判定，总跳数不超过 5；主页面请求须在白名单主机内，子资源可以是任意公网主机；WebSocket 与 EventSource 一律拒绝。用 file iframe、重定向到 169.254.169.254、十进制 IP、data:text/html 和合法的 CDN 图片验证。
