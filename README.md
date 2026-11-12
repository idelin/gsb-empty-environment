# MySQL握手TLS剥离

中间人清除服务端握手包里的 CLIENT_SSL 能力位，或在 SSL 请求包后夹带明文字节，诱使客户端用明文认证。为Go MySQL 驱动写可导入的握手准入包：配置要求 TLS 时，服务端能力位缺 CLIENT_SSL 就中止；发出32字节 SSLRequest 后，TLS 握手完成前缓冲区里不得已有任何服务端字节；TLS 建立后才发送认证响应，auth_plugin_data 在明文阶段不得用于任何回复。测能力位剥离、提前字节、乱序包。
