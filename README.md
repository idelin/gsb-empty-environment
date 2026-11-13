# TLS降级哨兵与FALLBACK_SCSV

中间人让客户端的高版本握手失败，诱使它以更低版本重试，再利用旧版本的弱点。为Go TLS 实现写可导入的降级防护包，服务端与客户端各有一个入口：服务端自身最高支持 TLS 1.3 却协商出 TLS 1.2 时，ServerHello.random 末 8 字节须为 44 4F 57 4E 47 52 44 01，协商出 TLS 1.1 或更低时为 44 4F 57 4E 47 52 44 00；服务端自身最高只到 TLS 1.2 时不写哨兵；客户端自己支持 TLS 1.3 却协商到 TLS 1.2 或更低时，必须检查 random 末 8 字节，等于上述任一哨兵即按 illegal_parameter 终止；ClientHello 的密码套件含 TLS_FALLBACK_SCSV（0x5600）而其 legacy_version 低于服务端最高支持版本时，服务端发 inappropriate_fallback 并终止；客户端只有在自行降级重试时才可带该套件，首次握手不得带。用 1.2 哨兵、1.1 哨兵、SCSV 加低版本、首次握手带 SCSV 和仅支持 1.2 的合法握手验证。
