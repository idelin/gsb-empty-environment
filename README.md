# 密钥证明授权列表校验

后端依据 Android 密钥证明证书里的授权列表决定是否信任设备密钥，攻击者可提供软件实现的密钥，并把声明放在 teeEnforced 之外。为Go后端实现供注册接口调用的证明扩展校验包：证书链验证由调用方完成，本包解析叶证书扩展 1.3.6.1.4.1.11129.2.1.17 的 DER 编码 KeyDescription，attestationChallenge 须与服务端挑战逐字节相等；attestationSecurityLevel 与 keymasterSecurityLevel 须为 TrustedEnvironment 或 StrongBox；purpose、algorithm 与 origin 须出现在 teeEnforced 且 origin 为 GENERATED，出现在 softwareEnforced 则拒绝；rootOfTrust 的 verifiedBootState 须为 Verified 且 deviceLocked 为真。用软件级别、标签位置错置、挑战不符和合法列表验证。
