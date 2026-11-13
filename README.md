# BLE隐私地址解析换绑设备身份

网关用可解析私有地址识别已配对设备，若解析表更新与连接建立不同步，攻击者可让同一地址被绑定到错误身份。给Go无线宿主实现题内地址解析包：设备记录含deviceID、128位IRK句柄、记录代次及撤销状态；私有地址为按本题约定为网络字节序的3字节hash||3字节prand，prand最高两位须为01，hash由宿主AES回调按IRK和补零prand计算后取3字节。解析时固定一版IRK表，对所有Active记录计算，恰好一个匹配才返回(deviceID,recordGeneration,address,tableVersion)票据，零匹配为匿名，多匹配拒绝并报告冲突，不能按首个取值。连接完成前重验票据对应记录仍Active、代次和表版本未变，且链路层身份挑战由该设备长期公钥签名覆盖随机数、私有地址和连接ID；通过后才发布身份。IRK轮换原子替换记录并递增表版本，旧IRK可按策略保留发现宽限，但只能产生需重新认证票据，不能直接恢复权限。用故意碰撞的AES替身、表更新、设备撤销、旧IRK地址、prand类型错误和两个并发连接验证，地址匹配本身不等于认证。

## 实现说明

包名为 `netsec`，代码位于模块根目录。核心不变量如下：

- `Address` 固定为网络字节序的 `hash[3] || prand[3]`；`prand[0]&0xc0 == 0x40` 才可解析。
- 地址哈希由 `AESFunc(IRK, prand || 0^13)` 的前三个输出字节得到，宿主完全控制 AES 实现和密钥存储。
- `Resolve` 只加载一次不可变表快照。它对快照内所有 `RecordActive` 记录计算，零匹配返回匿名，唯一匹配签发 `Ticket`，多匹配返回 `ConflictError` 并列出全部冲突设备，绝不取首个。
- `Ticket` 字段私有并用 resolver 本地 HMAC 密封，绑定 `deviceID`、记录代次、地址、表版本和命中的 IRK 凭据。
- `Complete` 在表读锁内重验记录仍 Active、代次和表版本未变、凭据仍有效，并重新计算地址哈希；随后验证长期公钥对 `nonce || address || connectionID` 的域分隔签名。全部通过后才调用发布回调/返回 `Identity`。
- 任何成功的表变更都会递增 `TableVersion`，因此跨版本票据不能完成。IRK 轮换同时递增记录代次；保留的旧 IRK 只产生 `RequiresReauthentication() == true` 的票据，完成后最高只有 `PrivilegeDiscovery`，不会恢复完整权限。

## 主要 API

- `NewResolver(Options)`：配置 AES 回调、验签回调、发布回调、旧 IRK 宽限和时钟。
- `AddDevice` / `RotateIRK` / `RevokeDevice`：原子更新表快照并递增版本。
- `Resolve(Address)`：返回 `Resolution`；检查 `Status`、`Ticket` 和 `Errors.Is(err, ErrAddressConflict)`。
- `Complete(Ticket, Challenge, IdentityProof)`：连接完成前重验并验证长期公钥签名。
- `GenerateNonce` / `ChallengeMessage`：生成随机数和构造设备端应签名的精确字节串。

`PublishIdentity` 回调在 `Complete` 的表读锁内执行，不得回调 resolver 的写操作（`AddDevice`、`RotateIRK`、`RevokeDevice`）。

## 验证

测试覆盖唯一解析、故意 AES 碰撞、解析中的表更新、撤销、IRK 轮换与旧 IRK 宽限、过期旧票据、错误 prand 类型、票据篡改、挑战字段绑定、错误长期公钥、发布前并发写阻塞以及两个并发连接：

```sh
go test ./...
go test -race ./...
go vet ./...
```
