# ZMTP机制降级与CURVE计数

攻击者作为客户端声称 NULL 机制接入要求 CURVE 的服务端，或重放旧的 CURVE 消息。为Go消息库写可导入的 ZMTP 3.x 握手组件：greeting 首字节为 0xFF、第10字节为 0x7F，主版本不低于3；机制名20字节、尾部补零，与本端配置不同即断开；as-server 位须与角色相反；CURVE 的 MESSAGE 帧 nonce 须严格递增，重复或回退即断连。测机制降级、尾部非零、角色相同和 nonce 回退。
