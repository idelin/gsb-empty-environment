# knownhosts

运维平台的Go SSH客户端要按OpenSSH的known_hosts核对主机密钥。实现可供连接回调调用的匹配包：支持哈希主机名、通配与否定模式、[host]:port形式、@cert-authority和@revoked标记；吊销记录优先于任何匹配，同一主机出现不同密钥时报告冲突，不静默接受。用混合多种格式的文件与多组主机、端口、密钥组合测试每个决策。
