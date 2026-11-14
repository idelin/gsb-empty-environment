# SNMP authoritative engine 的身份与重放保护

该库为 C++20 SNMP 代理提供可导入的 authoritative engine 状态机。它维护
`engineID`、`boots`、`engineTime`，在独立持久化存储中保证 `boots` 单调，
并对每个 USM 用户维护时间窗与已接收 PDU 指纹窗口。

## 恢复规则

- `FileEngineStateStore` 应指向不随配置备份恢复的本地路径，例如
  `/var/lib/snmp/engine-state`。写入使用同目录临时文件、`fsync` 和原子替换，
  文件权限为 `0600`。
- 启动时持久化的 `(engineID, boots, engineTime)` 优先于配置中的值。配置里的
  较低 `boots` 不会覆盖持久状态。
- 运行时恢复较低的 `boots` 返回 `RequiresResynchronization`，不修改状态；在
  调用 `resynchronize()` 或 `engineBooted()` 前，所有认证通过的请求仍会被拒绝。
- 恢复较高的 `boots` 会先持久化再生效。`engineID` 不一致返回
  `EngineIdMismatch` 并要求重同步，不会回退持久身份。
- `resynchronize()` 取持久 `boots + 1`（或指定的更高最小值），把
  `engineTime` 重置为 0，并清空所有用户窗口。

## 请求校验

每个 `UsmRequest` 依次核对：

1. `msgAuthoritativeEngineID` 与当前身份完全相同；
2. 用户名、密钥代次、auth/privacy 算法与当前用户记录一致；
3. `msgAuthenticationParameters` 是完整认证消息的 USM HMAC；
4. `msgAuthoritativeEngineBoots` 与当前 boot 相等；
5. `msgAuthoritativeEngineTime` 在当前 `engineTime` 的 150 秒窗口内（可配置）；
6. 该用户窗口内没有相同 PDU 指纹。窗口内不同时间到达的合法 PDU按乱序接受。

认证消息必须是认证参数字段置零后的完整 BER 消息。应用层可用
`makeAuthenticator()` 生成或验证测试消息；支持 HMAC-SHA-1（截断 96 bit）、
HMAC-SHA-256（截断 192 bit），以及用于旧设备的 HMAC-MD5（截断 96 bit，仅建议
兼容迁移期启用）。privacy 算法与密钥代次一起绑定校验，但载荷加解密仍由代理现有
的 privacy 层完成。

拒绝结果只区分粗粒度类别，诊断字符串固定为 `"request rejected"`。未知用户、
坏认证参数和错误 `engineID` 不会返回可枚举用户的差异信息；细粒度计数仅通过
本地管理接口 `statistics()` 暴露。

## 使用

```cpp
#include "netsec.hpp"

auto store = std::make_shared<netsec::FileEngineStateStore>(
    "/var/lib/snmp/engine-state");

netsec::EngineConfig config;
config.engine_id = netsec::Octets{0x80, 0x00, 0x1f, 0x88, 0x01};
config.boots = 7;             // 配置备份中的值可以较低
config.time_window_seconds = 150;

netsec::ReplayEngine engine(config, store);
engine.engineBooted();        // 进程真实启动时递增并持久化 boots

netsec::UsmUser user;
user.name = "operator";
user.key_generation = 2;
user.auth_protocol = netsec::AuthProtocol::HmacSha256;
user.auth_key = netsec::localizeKey(
    user.auth_protocol, "auth-password", engine.snapshot().engine_id);
engine.addUser(user);

netsec::UsmRequest request;
const netsec::EngineSnapshot state = engine.snapshot();
request.authoritative_engine_id = state.engine_id;
request.boots = state.boots;
request.engine_time = state.engine_time;
request.user_name = "operator";
request.key_generation = 2;
request.auth_protocol = netsec::AuthProtocol::HmacSha256;
request.message = authenticated_message_with_zeroed_auth_field;
request.authentication_parameters = netsec::makeAuthenticator(
    request.auth_protocol, user.auth_key, request.message);

if (!engine.verify(request)) {
    // 丢弃 PDU；统计已由组件记录。
}
```

重密钥时使用严格递增的代次，旧代次消息会被拒绝：

```cpp
engine.rotateUserKeys(
    "operator", 3,
    netsec::AuthProtocol::HmacSha256,
    netsec::PrivProtocol::Aes256,
    new_auth_key, new_privacy_key);
```

`ReplayEngine` 的请求校验、用户管理和状态迁移都由同一个互斥量串行化，可由多个
代理工作线程调用。

## 构建与测试

```sh
cmake -S . -B build -DBUILD_TESTING=ON
cmake --build build
ctest --test-dir build --output-on-failure
```

测试覆盖 boots 回滚、单调恢复、持久化重载、时钟前后偏移、窗口边界与乱序、
同一 PDU 重放、密钥代次/算法绑定、合法重同步、窗口容量以及认证错误不泄露用户。
