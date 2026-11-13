# 环签名挑战链与密钥映像绑定

匿名报告服务只验证签名外层字段，无法确认密钥映像与环中真实成员一致。给Go服务实现本题定义的可链接环签名生成、验证和链接判定包，使用P-256，环含1至64个互不相同的规范压缩公钥，按提交顺序参与转录。Hp(P)通过SHA256(固定域标签||作用域||P||大端计数器)所得32字节作为横坐标，取偶数纵坐标，直到成为非无穷远曲线点；计数器为大端4字节，从0至255，横坐标不规范则继续，全部失败返回错误。真实成员x的映像I=x*Hp(P)，验证从c0开始逐项计算L_i=s_iG+c_iP_i、R_i=s_iHp(P_i)+c_iI，再把作用域、完整环、消息、L_i、R_i的长度前缀编码哈希模群阶得到下一c，最终必须回到c0。中间L、R为单位元时以单字节00参与哈希，公钥与I的单位元拒绝。拒绝非法点、非规范标量和重复成员，生成时只知一个私钥，随机标量由宿主注入，签名用临时标量非零，其他响应随机标量可为零，均须小于群阶。相同作用域内按I链接，跨作用域不得直接链接。用真实曲线验证成员位置轮换、变更消息、替换I及更换环顺序，不能把验证成功解读为知道了哪个成员签名。

## 包 API

模块路径为 `example.org/netsec/q0047`，包名为 `netsec`。

- `GenerateRingSignature(ring, signerIndex, privateKey, message, scope, rng)` 生成签名；`Sign`、`Generate`、`GenerateSignature` 是等价别名。
- `VerifySignature(ring, message, scope, sig)` 返回 `(valid, error)`；格式错误返回 error，挑战链不闭合返回 `false, nil`。
- `LinkSignatures(sig1, sig2)` 比较密钥映像并返回 `(linked, error)`；`Link`/`AreLinked`/`Linked` 返回 bool。
- `KeyImage(privateKey, ringMember, scope)` 计算 `I = x * Hp(P, scope)`。
- `Hp`/`HpBytes` 暴露哈希到点逻辑；`CompressPublicKey`、`ValidateRing`、`GroupOrder`、`P256` 提供辅助能力。

`Signature` 的 `C0`、`S`、`I` 分别是 32 字节挑战、按环顺序排列的 32 字节响应和 33 字节压缩密钥映像；`KeyImage`/`Responses` 是描述性别名。环成员可使用 `[][]byte`、`[][33]byte`、`[]*ecdsa.PublicKey` 或 `[]ecdsa.PublicKey`。

挑战转录按提交顺序写入：作用域、完整环（所有成员压缩编码顺序拼接）、消息、`L_i`、`R_i`，每项均带 4 字节大端长度前缀。非单位元点使用 33 字节压缩编码，单位元使用单字节 `00`。`Hp` 的部分固定域标签为 `netsec/q0047/hp`。
