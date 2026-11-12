# containerd镜像站配置准入

containerd 通过 certs.d 下各注册中心的 hosts.toml 配置镜像加速，条目含 capabilities、skip_verify、override_path 与 header，攻击者能写这个目录或控制镜像引用时，可让拉取走明文镜像站，或把凭证头发给不该接收的主机。为 Go 节点配置服务写供配置落盘前调用的校验包：输入解析后的结构（server、host 表、capabilities、skip_verify、ca、client、header、override_path）与调用方策略；skip_verify 为真或使用 http 的 host 拒绝，回环地址除外；Authorization 头只允许出现在与 server 同域的 host 条目中；只有 resolve 能力的 host 不得提供 pull；按主机名规范化（大小写、端口、末尾点）判断同域；按摘要引用拉取时，内容摘要不符须在换下一 host 前丢弃已取内容。用明文镜像、凭证头外发、仅 resolve 提供 pull、同域写法变形和摘要不符验证。
