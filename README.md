# 凭据助手协议换行注入

Git 托管平台的凭据助手按 key=value 逐行读写，攻击者在仓库地址的用户名或路径里塞 %0a，借换行注入 host= 等字段，把令牌发给自己的主机。为Go凭据助手封装写可导入的请求构造与解析包：URL 拆出的 protocol、host、path、username 解码后含 LF、CR 或 NUL 即整体拒绝；请求里的 host 须等于 URL 的真实主机；助手响应里未被请求的 protocol 与 host 字段忽略。
