# STUN消息完整性与属性顺序

STUN 消息里 MESSAGE-INTEGRITY 之后的属性不受完整性保护，攻击者截获合法请求，在其后追加改写地址的属性，服务端若照单全收，就会把流量中继到攻击者指定的对端。为Go ICE 与 TURN 服务写可导入的 STUN 消息校验包，输入消息字节与长期凭据密钥：头部 20 字节，长度字段须等于属性总长度且是 4 的倍数，魔数 0x2112A442 不符拒绝；属性按 TLV 解析，值按 4 字节对齐补零，补位字节须为 0；校验 MESSAGE-INTEGRITY（0x0008）时，HMAC-SHA1 的输入是头部到该属性之前的字节，其中头部长度字段按截止到该属性末尾重算；该属性之后只允许 MESSAGE-INTEGRITY-SHA256（0x001C）与 FINGERPRINT（0x8028），其他属性一律忽略，不得进入业务处理；FINGERPRINT 为 CRC-32 异或 0x5354554E，且须是最后一个属性；MESSAGE-INTEGRITY 出现两次、长度不是 20 或属性长度越界时拒绝。用完整性之后追加 XOR-PEER-ADDRESS、长度字段不符、补位非零、FINGERPRINT 不在末尾和合法请求验证。
