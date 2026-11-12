# App Attest断言计数器与挑战

iOS App Attest 的断言带单调递增的计数器，攻击者重放旧断言，或拿别的应用的断言来冒用。为Go后端写可导入的断言校验包，供请求鉴权调用，布局与验签输入按本题约定：authenticatorData 前 32 字节须为 SHA-256(teamID + "." + bundleID)；第 33 字节标志位须为 0x00，随后 4 字节大端计数器必须严格大于该密钥已记录的计数器，通过后才更新；clientDataHash 须等于 SHA-256(服务端一次性挑战 + 请求体哈希)，挑战只能用一次且 60 秒内有效；签名按调用方给出的该密钥公钥，对 authenticatorData 与 clientDataHash 拼接后的 SHA-256 摘要做 ECDSA-P256 验证；任一失败都不得更新计数器。用旧计数器、rpIdHash 不符、挑战重用、请求体被换和失败后计数器保持不变验证。
