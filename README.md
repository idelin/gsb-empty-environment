# UPnP端口映射请求的内部客户端与来源绑定

家用网关的 UPnP 服务允许内网设备申请端口映射，攻击者控制的内网设备在 AddPortMapping 里把 InternalClient 写成另一台主机，让公网流量打到别的设备，或映射出管理端口。为Go网关固件写可嵌入 SOAP 动作处理器的映射准入包，输入动作参数（RemoteHost、ExternalPort、Protocol、InternalPort、InternalClient、租约时间）、连接的真实源地址和调用方的已映射表：InternalClient 必须等于连接源地址（文本规范化后比较，IPv4 映射写法先还原）；ExternalPort 不得小于 1024，也不得落入调用方给的保留端口集合；InternalPort 为 0 或大于 65535 拒绝；RemoteHost 非空时只允许该远端，不得被改写成通配；同一 (ExternalPort, Protocol) 已被另一个内部客户端占用时拒绝，不得覆盖；租约时间为 0（永久）时拒绝，上限由调用方给出；每个内部客户端最多 16 条映射；请求头 Host 不是网关自身地址时拒绝，防止浏览器经域名重绑定发起请求。用 InternalClient 指向他机、ExternalPort 为 22、占用覆盖、永久租约、Host 为外部域名和 ::ffff 写法验证。
