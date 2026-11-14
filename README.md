# 服务配置完整性组件

面向 C++20 服务管理代理的可导入组件。它把服务配置的签名校验、注册表 ACL
审计、受信路径/文件 ACL 校验和启动计划生成放在同一个事务式流程里，避免出现
“注册表值已经改掉，但服务管理代理还按旧逻辑启动”的情况。

## 防护目标

- 注册表服务键以及全部子键的 owner 必须是特权 SID。
- 任何非特权 principal 不得持有 `KEY_SET_VALUE`、`KEY_CREATE_SUB_KEY`、
  `WRITE_DAC`、`WRITE_OWNER`、`DELETE`、`GENERIC_WRITE` 等写权限。
- 注册表键默认要求 protected DACL，防止父键后续继承普通用户可写 ACE。
- `ImagePath`、`ObjectName`、`DependOnService` 和恢复动作必须与已签名配置逐项一致。
- `ImagePath` 与 `run_program` 恢复命令必须是绝对路径、位于受信目录、指向
  普通 `.exe` 文件，并且文件及其全部祖先目录都不能被普通用户写入。
- 拒绝环境变量展开、设备命名空间、ADS、`..` 越界、8.3/大小写混淆、
  reparse point、canonical path 重定向和未加引号含空格的路径。
- 安装、更新、修复成功前都执行一次完整的写后审计。更新要求旧 ACL 已经安全，
  否则必须走显式 `repair()`。
- `prepare_launch()` 只从签名载荷生成 argv，不把注册表内容作为可执行路径来源。

## 构建与测试

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build
ctest --test-dir build --output-on-failure
```

库目标为 `netsec`，公开头文件为 `netsec.hpp`。默认构建一个静态库；调用方也可以
把它作为 DLL/共享库的一部分导入。

安装后可在 C++20 代理工程中使用：

```cmake
find_package(netsec CONFIG REQUIRED)
target_link_libraries(service_manager PRIVATE netsec::netsec)
```

## 核心 API

```cpp
#include "netsec.hpp"

auto verifier = std::make_shared<netsec::HmacSha256Verifier>();
verifier->add_key("release-2026", deployment_key);

auto registry = std::make_shared<WindowsServiceRegistry>();
auto files = std::make_shared<WindowsFileSystemInspector>();

netsec::SecurityPolicy policy = netsec::SecurityPolicy::windows_service_defaults();
policy.trusted_directories.push_back("C:\\Program Files\\Contoso");

netsec::ConfigurationIntegrity integrity(verifier, registry, files, policy);

// 安装：签名/路径校验 -> 创建或修复键 ACL -> 写声明值 -> 全量复核
auto install_status = integrity.install(signed_configuration);

// 更新：先复核旧 ACL，若已放宽则拒绝；合法更新后再次全量复核
auto update_status = integrity.update(next_signed_configuration);

// 修复：重新收紧整棵键树，并写回全部声明值
auto repair_status = integrity.repair(signed_configuration);

// 启动前最后一次审计；失败时不会返回 plan
auto decision = integrity.prepare_launch(signed_configuration);
if (decision.plan) {
  // 用 decision.plan->argv 启动；argv[0] 是规范化后的声明程序路径。
  launch(decision.plan->argv);
}
```

`SignedConfiguration` 的签名消息由
`signed_configuration_message()` 生成。它绑定 key id、算法和所有配置字段，
字段使用长度前缀编码，不存在 JSON 拼接造成的歧义。`HMAC-SHA-256` 实现是
完全自包含的参考适配器，适合离线部署和测试；生产环境应通过
`SignatureVerifier` 注入平台密钥库/TPM 保护的**非对称**验签器，私钥不要进入
服务管理进程。

## 适配器契约

### `ServiceRegistry`

`snapshot()` 必须返回服务键以及递归找到的全部子键。每个键返回 owner SID 和
ACE 列表；不要返回本地化账户名。`write_configuration()` 只写声明值，不得削弱
已有 ACL；新建键必须直接使用特权 owner 和 protected DACL。

`enforce_acl()` 需要递归设置 owner/DACL。Windows 适配器通常使用
`RegOpenKeyExW` + `RegSetKeySecurity`/`SetSecurityInfo`，owner 选
`SYSTEM`、`Administrators` 或 `TrustedInstaller`，并通过
`SE_DACL_PROTECTED` 关闭继承。注册表路径约定为：

```text
HKLM\SYSTEM\CurrentControlSet\Services\<service_name>
```

`snapshot().observed` 应把 Windows 的 `ImagePath`、`ObjectName`、
`DependOnService`、恢复动作解码为语义值。恢复动作在 Windows 中通常编码在
`FailureActions` 二进制值里，解码和写入由适配器负责；完整性引擎只比较结构化
结果。

### `FileSystemInspector`

`inspect()` 必须返回：

- `canonical_path`：通过已打开的句柄或等价的安全机制解析后的最终路径。
- `traverses_reparse_point`：路径中是否有符号链接、junction 或挂载点。
- `components`：目标文件在前，随后是每一个已存在的祖先目录，直到卷根。
- 每个组件的 owner SID 与 ACE。

引擎会自行检查 canonical path 与输入一致、所有祖先 ACL 均安全、路径位于受信
目录以及卷根链完整。Windows 适配器应使用 `GetFinalPathNameByHandleW`、
`GetFileInformationByHandleEx(FileAttributeTagInfo)` 和 `GetSecurityInfo`。

## 操作语义

- `validate_configuration()`：验签后才处理载荷；检查字段结构、命令解析、受信
  目录、reparse point 和文件/祖先 ACL。
- `audit()`：在上述检查之外，读取注册表快照，逐项比较签名值，并审计整棵键树
  ACL。
- `install()`：允许修复旧键 ACL；创建或覆盖声明值；写后再次收紧 ACL 并审计。
- `update()`：键必须已存在，且更新前旧 ACL 必须已经安全；之后写声明值并全量
  复核。检测到 ACL 放宽时返回错误且不写。
- `repair()`：收紧整棵键树，写回签名值，再全量复核。
- `prepare_launch()`：执行 `audit()`，只有完全通过才返回计划；`argv` 由签名
  `ImagePath` 解析，`argv[0]` 使用规范化路径。

## 覆盖的攻击场景

`tests/netsec_tests.cpp` 使用内存注册表、内存文件 ACL 和确定性签名器覆盖：

1. 安装后根键/子键被加入普通用户写 ACE：审计和启动计划失败，更新被拒绝。
2. 注册表 `ImagePath` 被替换为 `C:\Users\Public\evil.exe`：审计失败，不产生启动计划。
3. 受信目录内文件本身或祖先目录用户可写：安装/审计失败。
4. reparse point、canonical path 重定向、缺失文件、`..` 越界和环境变量路径：失败。
5. `ObjectName` 被替换为 `.\evil` 或环境变量形式：失败。
6. 依赖注入、恢复动作命令覆盖、非 `run_program` 动作携带命令：失败。
7. 签名被篡改、载荷在签名后被修改、未知 key id：失败。
8. 合法的签名更新（路径、账户、依赖和恢复动作同时变更）：成功，且启动计划只
  指向新版声明程序。
9. ACL 放宽、ImagePath/ObjectName/依赖/恢复动作同时被篡改、用户创建的恶意子键：
  `repair()` 全部恢复，随后启动计划只指向声明程序。
10. HMAC-SHA-256 已知向量、null DACL、非特权 owner、未保护 DACL：按预期处理。

## 生产注意事项

- `prepare_launch()` 是“最后一道闸门”，服务管理代理应立即使用返回的 `argv`
  启动，不要再次读取注册表并自行拼接命令行。
- `ServiceRegistry` 适配器应尽量在同一个已打开的键句柄/事务内完成写入和 ACL
  复核；组件会做写前/写后双重检查，但平台层仍应避免不必要的 TOCTOU 窗口。
- 受信目录配置应保持最小化。不要把 `C:\Windows\Temp`、用户目录或普通
  `ProgramData` 子目录加入白名单。
- 拒绝 `REG_EXPAND_SZ` 形式的 `ImagePath`（本组件直接拒绝未展开的 `%...%`
  路径）；安装器应写入明确的绝对 `REG_SZ` 值。
- 恢复动作的 `run_program` 在 Windows 上以服务账户运行，必须和 `ImagePath`
  使用同等严格的路径/ACL 校验。
