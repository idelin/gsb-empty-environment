# vbmeta回滚索引与禁用标志准入

设备启动时校验 vbmeta 镜像，攻击者刷入旧版固件，或在已解锁的设备上留下禁用验证标志后又把设备重新上锁。为Go启动校验仿真组件写可嵌入设备启动模型的 vbmeta 准入包，输入 vbmeta 头（魔数 AVB0、flags、rollback_index、签名公钥摘要）、链式分区描述符（分区名、公钥摘要、rollback_index_location）、设备已存储的各 location 的回滚值和锁状态：魔数不符拒绝；公钥摘要不等于调用方给的信任根时，仅当设备已解锁才接受；flags 的 0x1（禁用哈希树）或 0x2（禁用验证）置位而设备已上锁时拒绝；rollback_index 小于该 location 的存储值拒绝，高于存储值则在整条链全部通过后才推进存储值；链式分区描述符的 rollback_index_location 不得重复，也不得为 0；启动失败时任何存储值都不得被推进。用旧版回滚、上锁带禁用标志、重复 location、链中后一项失败和已解锁设备验证。
