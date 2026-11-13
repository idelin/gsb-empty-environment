# Cosmos Amino签名模式弃用与未知字段

Cosmos 交易的 SIGN_MODE_LEGACY_AMINO_JSON 把消息转成 JSON 供硬件钱包显示，攻击者可借非关键扩展字段或未注册消息，让显示的内容与实际执行的不同。给Go交易解码与准入包写供 ante 处理调用的校验：交易体中的 extension_options 非空一律拒绝；未知字段按编号区分，编号小于 1024 的为关键字段，出现即拒绝，编号至少 1024 的为非关键，只在 SIGN_MODE_DIRECT 下忽略，在 LEGACY_AMINO_JSON 下同样拒绝；LEGACY_AMINO_JSON 只允许调用方注册表中有 amino 名称的消息类型；高度达到调用方给定的弃用高度后，LEGACY_AMINO_JSON 只对调用方白名单地址有效。用隐藏非关键字段、未注册消息、弃用高度前后一块与白名单地址验证。
