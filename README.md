# OP_MSG认证前命令门禁

未认证连接想借 OP_MSG 的多段文档或未知标志位运行受限命令。为Go代理写可导入的 OP_MSG 认证前门禁：flagBits 低16位出现未识别位即拒，checksumPresent 时校验 CRC-32C；恰有一个 kind 0 段，命令名取其文档第一个键；未认证只放行 hello、isMaster、saslStart、saslContinue，且 $db 须是字符串 admin；kind 1 段长度不得越界。测多段夹带、错长度、坏校验和、命令大小写。
