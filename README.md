# IPNS记录序列回滚与字段一致

IPNS 记录里的值、有效期和序号既出现在 protobuf 字段里，又出现在被 V2 签名覆盖的 CBOR 数据里，攻击者可重放旧记录回滚指向，或让两处字段不一致来蒙混。给Go节点写供 DHT 记录存储调用的 IPNS 校验包，验签由调用方注入：只接受 V2 签名，V1 签名拒绝；签名覆盖的 CBOR 数据里的 Value、Validity、ValidityType、Sequence、TTL 必须与 protobuf 里的对应字段完全一致；ValidityType 只能为 EOL，EOL 是 RFC 3339 时间且须晚于当前时间；替换已有记录时序号必须更大，序号相同则 EOL 更晚者胜出，EOL 也相同则丢弃新记录。用旧序号重放、字段不一致、V1 签名、EOL 已过和同序号决胜验证。
