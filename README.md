# LUKS2元数据与keyslot绑定

攻击者改写 LUKS2 头部 JSON，让 keyslot 区域重叠、digest 绑到别的 keyslot，或塞超大 KDF 参数拖垮解锁。为Go磁盘库写可导入的元数据校验包：JSON 同名键即拒；keyslot 的 area 须在 keyslots 区内且互不重叠；digest 引用的 keyslot 与 segment 须存在且双向一致；PBKDF 的 time、memory、cpus 超调用方上限拒绝；头部 SHA-256 校验和按校验和字段置零后重算。测重复键、区域重叠、悬空引用、超限 KDF。
