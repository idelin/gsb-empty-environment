# 嵌入式 SIM 配置文件下载事务

`netsec` 是一个无外部依赖的 C++20 下载事务组件，供配置服务直接链接。它解决事务标识被另一台设备复用的问题：只有同时持有目标 EID、服务端有效证书链签名和未消费 nonce 的合法事务，才能在该 EID 上完成下载与安装。

## 安全模型

- 事务签名覆盖事务标识、EID、服务器身份、nonce、配置文件标识、版本、完整配置内容和校验时间字段。
- 证书链从叶子证书开始，按签发者向上验证到显式配置的根信任锚，同时检查 CA 约束、有效期和每级 Ed25519 签名。
- 叶子证书主题必须等于请求中的服务器身份。
- nonce 和事务标识在首次成功下载后永久消费，重复使用会被拒绝。
- 下载、下载确认、安装确认是三个严格有序的阶段；每一步只能成功一次。
- 安装使用组件内部保存的已验签配置，不使用调用方在安装时重新传入的内容。
- 已安装版本形成按配置文件标识单调递增的版本下限，下载和安装两个阶段都会拒绝回退或同版本重装。
- `snapshot()` / `restore_state()` 可用于持久化重放记录和版本下限；服务应原子保存该状态，否则重启会丢失一次性语义。

组件自带 Ed25519、SHA-256 和 SHA-512 实现，没有 OpenSSL 等运行时依赖。

## 使用方式

```cpp
#include "netsec.hpp"

netsec::DownloadTransactionManager device("89049032000000000000000000000001");
device.add_trust_anchor(root_ca_certificate);

netsec::DownloadRequest request;
request.transaction_id = "txn-2026-0001";
request.eid = device.eid();
request.server_identity = "profile-server.example";
request.nonce = device.issue_nonce();
request.profile = {"profile-A", 7, profile_bytes};
request.certificate_chain = {server_certificate, intermediate_certificate};
request.signature = netsec::sign_download_request(server_private_key, request);

auto download = device.download(request);
// 将 download.profile_data 送到设备，并校验传输完整性。
device.confirm_download(download);
auto installed = device.install(download);
```

`download()`、`confirm_download()` 和 `install()` 通过 `netsec::TransactionError` 报告拒绝原因；需要非异常接口时可使用对应的 `try_download()`、`try_confirm_download()` 和 `try_install()`。

服务端签发时使用：

```cpp
auto root = netsec::make_root_certificate("device-root", root_key);
auto intermediate = netsec::issue_certificate(
    "device-intermediate", intermediate_key.public_key(), root, root_key,
    2, 0, expiry, true);
auto server = netsec::issue_certificate(
    "profile-server.example", server_key.public_key(), intermediate,
    intermediate_key, 3, 0, expiry, false);
```

`validation_time` 为 `0` 时使用系统时间；测试或离线校验可显式设置 Unix 秒。非零值会一并纳入签名，服务不得把该字段直接取自不可信网络输入。

## 构建与测试

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build
ctest --test-dir build --output-on-failure
```

在其他 CMake 工程中可使用 `add_subdirectory(path/to/netsec)` 后链接 `netsec::netsec`。

测试覆盖合法下载和事务复用、EID 替换、跨设备 receipt、nonce 重放、未确认安装、证书过期/不可信/签名损坏、安装前版本回退、另一事务先升级造成的安装时回退，以及重启后的重放与降级拒绝。
