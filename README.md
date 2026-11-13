# EIP-712域分隔与跨链重放

链下签名授权的合约调用，签名里如果没有绑定链标识、合约地址和每个签名者的计数，就能被搬到另一条链、另一个合约，或被重复提交。为TypeScript中继服务写可导入的类型化数据签名校验包，keccak256 与签名恢复由调用方注入：域分隔符为 hashStruct(EIP712Domain)，字段只含声明出现的 name、version、chainId、verifyingContract，类型字符串按声明顺序拼接；消息哈希为 keccak256(0x19 0x01 ‖ 域分隔符 ‖ hashStruct(message))，bytes 与 string 取其哈希，数组为元素编码拼接后的哈希，嵌套结构取其 hashStruct；中继要求域里的 chainId 等于配置链、verifyingContract 等于目标合约，缺失即拒绝；消息里的 nonce 必须等于链上当前值，提交成功后递增，deadline 已过拒绝；恢复出的地址须等于消息里声明的签名者；签名的 s 在高半区或 v 不是 27 与 28 时拒绝。用换链重放、换合约重放、nonce 复用、过期 deadline 和高 s 值验证。
