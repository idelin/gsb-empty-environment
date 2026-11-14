# 设备注册事务组件

供 C++20 设备证书注册服务调用的可导入事务组件。它用事务标识、设备身份、nonce、请求摘要和有效期构成不可变绑定，并在组件内维护待审、签发、完成、拒绝、超时、撤销六个状态。

## 安全语义

- 同一事务标识只允许一个完整绑定。绑定任一项被替换时返回 `Conflict`，原记录不变。
- nonce 在组件内全局单次使用；跨事务、跨设备复用均返回 `NonceReused`，已消费的 nonce 在事务进入终态后仍不可再次使用。
- 重复 `begin`、`issue`、`complete` 只返回已保存的证书、状态和原因，不再次迁移状态。
- `issue` 和 `complete` 同时校验事务记录中的密钥代次、模板代次与当前活动代次。代次不匹配返回 `GenerationMismatch`，原状态不变。
- `revoke` 同时绑定事务、设备、请求摘要、原代次和证书指纹；旧撤销请求只能命中旧事务，不能撤销同设备的新证书。
- 待审事务超过 `not_after` 后进入 `Expired`。已签发事务不会被超时改写，仍可通过 `complete` 完成或通过 `revoke` 撤销。
- 终态不会被旧请求复活。`Rejected`、`Expired`、`Revoked` 是失败终态；`Completed` 是成功终态，仅允许显式撤销转为 `Revoked`。

## 状态机

| 当前状态 | 事件 | 下一状态 |
| --- | --- | --- |
| `Pending` | `issue` | `Issued` |
| `Pending` | `reject` | `Rejected` |
| `Pending` | `not_after` 到期 | `Expired` |
| `Issued` | `complete` | `Completed` |
| `Issued` | `revoke` | `Revoked` |
| `Completed` | `revoke` | `Revoked` |

`Rejected`、`Expired`、`Revoked` 不接受新的迁移。`Completed` 除显式撤销外不再迁移，重复请求只返回原结果。

## 使用示例

```cpp
#include "netsec.hpp"

using namespace netsec;

RegistrationTransactionStore store(/*key_generation=*/1,
                                   /*template_generation=*/1);
const auto now = Clock::now();

RegistrationRequest request{
    .transaction_id = "tx-123",
    .device_id = "device-a",
    .nonce = "device-nonce-123",
    .request_digest = compute_request_digest("device-a",
                                             "device-nonce-123",
                                             csr_der),
    .not_before = now,
    .not_after = now + std::chrono::minutes(5),
    .key_generation = store.key_generation(),
    .template_generation = store.template_generation(),
};

auto begun = store.begin(request, now);
auto issued = store.issue(IssueRequest{request, certificate_fingerprint}, now);
auto completed = store.complete(CompleteRequest{request, certificate_fingerprint}, now);
```

服务也可以注入自己的时钟，并定期清理超时事务：

```cpp
RegistrationTransactionStore store(1, 1, [] { return Clock::now(); });
auto expired = store.expire_due();
```

`find(transaction_id)` 只读取当前快照；`find(transaction_id, now)` 会先结算待审事务的超时。所有会改变状态的请求在进入状态机前也会自动结算超时。

## 绑定与代次

`TransactionBinding` 是 `begin`、`issue`、`complete`、`reject`、`revoke` 的共同输入。事务标识缺失、设备身份缺失、nonce 缺失、摘要缺失或时间窗非法时返回 `InvalidRequest`。

服务应使用收到的原始 CSR/请求体调用 `compute_request_digest`（或等价的安全摘要算法），不要直接信任客户端自报的摘要；组件负责把摘要与事务的其余字段一起做不可变绑定。

密钥代次和模板代次只能单调前进：

```cpp
store.advance_key_generation(2);
store.advance_template_generation(2);
```

旧代次事务不能再签发或完成，但撤销仍按记录中的旧代次校验，因此轮换后仍可撤销历史证书。

## 构建与测试

在设备注册服务的 CMake 工程中导入：

```cmake
add_subdirectory(path/to/netsec)
target_link_libraries(device_registration PRIVATE netsec)
```

```bash
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```

测试覆盖合法注册、重复请求、重放旧请求、事务替换、nonce 复用、超时、密钥/模板代次、拒绝终态和撤销终态。
