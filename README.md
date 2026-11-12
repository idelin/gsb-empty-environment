# Connect错误详情泄漏过滤

Connect 协议的错误体带 code、message 与 details（Any 类型），攻击者故意触发内部错误来读取数据库地址、堆栈或内网主机名。为TypeScript Connect 服务写可导入的错误出口拦截器：对外 code 为 unknown 或 internal 的错误，message 替换成固定文案并附随机关联 ID，原始信息只进内部日志；details 的 type URL 只允许调用方白名单里的类型，其余整条丢弃；details 编码总长超过 4096 字节时全部丢弃；响应头与 trailer 中含 authorization、cookie、set-cookie 或以 x-internal- 开头的键一律去掉；业务主动抛出的 invalid_argument、not_found 等可展示 code 保留 message，其中形如 IPv4 地址、`user:pass@`、文件绝对路径的片段打码。测内部错误、非白名单详情、超长详情、元数据泄漏。
