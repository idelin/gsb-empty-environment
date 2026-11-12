# PostgreSQL启动参数白名单

PostgreSQL 的启动报文能携带 options、replication 等参数，攻击者借此修改 search_path、打开复制连接或设置 session_authorization。为Go的连接代理写可导入的启动报文校验包（协议版本 3.0）：报文起始 4 字节为含自身的长度，随后 4 字节协议号 196608，参数是以 NUL 结尾的键值对，整体以空键结束；user 必填；参数名只允许 user、database、application_name、client_encoding、options，其余（含 replication、session_authorization）拒绝；options 按空白分词（反斜杠转义空白），其中 `-c` 项只允许 statement_timeout 与 lock_timeout，值不超过调用方上限，`--search_path` 与 `-c search_path` 拒绝；键重复拒绝；总长超过 10000 字节、不以 NUL 结束或含非 UTF-8 拒绝；SSLRequest 或 GSSENCRequest 之后缓冲里仍有未读字节则断开。测 replication、options 绕过、重复键、SSL 请求后残留字节。
