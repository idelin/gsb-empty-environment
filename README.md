# LwM2M引导账户替换防护

攻击者架设假 LwM2M 引导服务器，试图替换管理服务器地址/密钥或删除真实引导账户。本包为 Go 客户端提供引导会话期间的写入准入控制（`package netsec`）。

## 准入规则

- **仅信任已配置的引导服务器**：会话身份来自传输握手（DTLS/PSK、RawPK、证书），不接受载荷自报。URI、安全模式与认证密钥（PSK 模式还需身份标识）全部匹配预置 `TrustAnchor` 才标记为受信；NoSec 或无认证密钥永远不可信。未受信会话的 `Write`、`Delete`、`Finish` 一律拒绝。
- **根路径删除保留引导账户**：`Delete("/")` 与 `Delete("/0")` 清空管理侧状态（安全对象、服务器对象），但原样保留所有引导账户（`/0/x/1 == true`）及其密钥；直接删除某个引导账户实例返回 `ErrDeleteBootstrapAccount`。
- **Bootstrap-Finish 校验与回滚**：完成时必须至少存在一个非 NoSec 安全模式的管理服务器账户，否则返回 `ErrNoSecureManagementServer` 并把存储回滚到会话开始前的快照。
- **会话超时拒绝写入**：超过会话期限后，写入、删除与完成均返回 `ErrSessionTimeout`；失败的 `Finish` 不提交任何变更。

## 主要类型

- `Store`：对象/实例/资源存储，键材料以 `[]byte` 保存，读写均做防御性深拷贝。
- `Guard`：准入控制器，`NewGuard` 接收存储与预置信任锚点（也可用 `TrustAnchorsFromStore` 从已有引导账户派生）。
- `Session`：`BeginSession` 创建，方法 `Write(path, payload)`、`Delete(path)`、`Finish()`、`Discard()`。

路径支持 `"/{obj}"`、`"/{obj}/{inst}"`、`"/{obj}/{inst}/{res}"` 与根 `"/"`；写入载荷分别使用 `ObjectWrite`、`ResourceSet` 或标量/`[]byte`。

## 验证

`go test ./...`（`guard_test.go`）覆盖：

- 假服务器（伪造密钥 / 伪造 URI / NoSec）的写入、删除、完成全部被拒且存储不变。
- 删除根路径后引导账户与密钥保留、管理账户被清空，重新下发安全管理账户后才能完成。
- 仅下发 NoSec 管理账户时 `Finish` 失败并完整回滚。
- 超时后所有写入/删除/完成被拒；受信会话正常提交后关闭；快照深拷贝与 `Discard` 回滚。
