# 快照归属组件（C++20）

`netsec` 是供快照服务导入的归属校验库。它把快照的租户、VM、磁盘、生命周期、
序号和数据加密密钥封装进带认证的密文元数据；挂载时先解密验证完整性，再核对
认证主体与归属。存储侧只能看到随机盐、随机 nonce、密文和认证标签，看不到
任何租户/VM/磁盘标识，也无法通过猜测标识枚举其他租户的快照。

## 安全模型

假设攻击者可以读取快照服务持久化的元数据，并且可以构造挂载请求，但不能取得
服务的根密钥，也不能伪造认证层给出的 `Principal`（`subject_id` 与 `tenant_id`
必须来自已验证会话，不能取自请求体）。

元数据信封只包含：

```
magic "NSM1" | version | algorithm | reserved | salt[16] | nonce[12] | ciphertext | tag[16]
```

以下字段全部位于 ChaCha20-Poly1305 密文内：

- 随机 128 位快照标识；
- 租户、VM、磁盘、VM 所有者主体；
- VM 生命周期 epoch、快照序号、创建时间；
- 数据加密密钥的 SHA-256 指纹和密钥本体。

根密钥经 HKDF-SHA256（随机盐）派生每次写入的元数据密钥；每次封装都使用新的
随机盐和 nonce，因此同样的绑定连续封装两次也会得到不同密文，密文长度之外不
泄露可枚举信息。

## 挂载前校验

`mount()` 只有在以下检查全部通过后才返回数据密钥：

1. 元数据 AEAD 认证成功、字段完整且长度受上限保护；
2. `Principal.tenant_id` 等于密文内绑定的租户；
3. 该租户下 VM 存在且未被删除；
4. 密文内 VM 生命周期 epoch 等于当前 VM epoch（VM 重建后旧快照失效）；
5. 认证主体同时是密文内的所有者和当前 VM 的所有者；
6. 调用方指定的目标 VM/磁盘（如有）与绑定一致，磁盘仍属于该 VM；
7. 快照序号不低于防回滚下限且不高于已签发序号；
8. 快照未被显式撤销，且未超过 `max_snapshot_age_seconds`；
9. 若挂载路径提交了密钥，其指纹必须与绑定密钥一致。

默认 `Policy::latest_only = true`：签发新快照会把同一 VM 的旧快照标记为陈旧，
旧快照挂载返回 `stale_snapshot`。`revoke_snapshots_before()` 可推进防回滚下限；
`delete_vm()` 留下墓碑，旧快照不能复活已删除 VM；使用更高
`lifecycle_epoch` 重建 VM 后，旧 epoch 的快照一律返回 `vm_epoch_mismatch`。

## 使用

```cpp
#include "netsec.hpp"

netsec::Policy policy;
policy.latest_only = true;
policy.max_snapshot_age_seconds = 7 * 24 * 3600;

netsec::SnapshotOwnershipService ownership(
    netsec::MasterKey::from_secret(kms_secret), policy);

netsec::VmRecord vm;
vm.tenant_id = tenant_id;
vm.vm_id = vm_id;
vm.owner_subject_id = subject_id;
vm.lifecycle_epoch = 1;
vm.disk_ids = {disk_id};             // 可选；非空时限制可签发磁盘
ownership.register_vm(vm);

netsec::Principal owner{subject_id, tenant_id};
auto issued = ownership.take_snapshot(owner, vm_id, disk_id, data_key);
if (!issued) {
    return fail(issued.code);
}
snapshot_store.put(issued.snapshot_ref, issued.sealed_metadata);

// 挂载路径：principal 来自认证层。
netsec::MountRequest request;
request.principal = authenticated_principal;
request.vm_id = requested_vm_id;
request.disk_id = requested_disk_id;

auto grant = ownership.mount(snapshot_store.get(ref), request);
if (!grant) {
    return deny(grant.code);          // 不泄露数据密钥
}
mount_disk_with(grant.data_key);
```

`IssueResult::snapshot_ref` 是随机不透明引用，不由租户或 VM 标识派生，可用作
快照目录的键。`MountResult::data_key` 仅在 `allowed()` 为真时填充。

## 构建与验证

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build
ctest --test-dir build --output-on-failure
```

测试覆盖：合法挂载、跨租户挂载、同租户非所有者、标识枚举、重复封装随机性、
旧快照、年龄过期、显式撤销、VM 删除与重建、元数据篡改、错误密钥/磁盘/VM，
以及 SHA-256、HMAC、HKDF、ChaCha20、Poly1305、AEAD 的标准测试向量。

安装后可导入：

```sh
cmake --install build --prefix /opt/netsec
```

```cmake
find_package(netsec CONFIG REQUIRED)
target_link_libraries(snapshot_service PRIVATE netsec::netsec)
```

## 边界

- `register_vm`、`delete_vm`、`revoke_snapshots_before` 属于可信控制面接口；生产
  环境应把 VM 状态、墓碑和序号下限与快照目录放在同一事务存储中。
- 组件不负责根密钥轮换；持久化信封带版本号，后续可增加密钥版本/重封装流程。
- 元数据大小默认限制为 1 MiB，避免攻击者用超大输入消耗资源。
