# EVM属性HMAC与inode绑定评估

EVM 用 security.evm 扩展属性保护文件的安全相关属性（security.ima、security.selinux、uid、gid、mode），攻击者离线改属性，或把另一个文件的合法 HMAC 拷过来。为 Go 完整性引擎写供文件访问前调用的评估包：本机 HMAC 形式（类型字节 2）按固定顺序对各属性值、inode 号、i_generation、uid、gid、mode 做 HMAC，可移植签名形式（类型字节 5）不计入 inode 与 generation；HMAC 形式用主机密钥重算比对，签名形式用受信公钥验证；属性被改、文件被复制到另一 inode、硬链接到另一路径但 inode 相同分别给出不同结论；密钥未加载时一律拒绝，不退回只检查 IMA；类型字节未知拒绝。用改 uid、复制 HMAC 到另一文件、硬链接、缺密钥和可移植签名验证。
