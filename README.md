# 硬件钱包签名时地址显示被替换

硬件钱包在签名时把交易发到主机解析，攻击者篡改主机显示让用户确认的收款地址与实际签名不同。请实现供C++20硬件钱包固件调用的可导入交易确认组件。设备独立解析交易并显示金额、地址与手续费，显示内容与签名摘要绑定；主机提交的解析结果不参与确认。地址编码、链标识与合约数据逐项校验。用显示替换、地址混淆、链替换、合法签名和设备回读验证所见即所签。

本仓库实现了一个无第三方依赖、可直接嵌入 C++20 固件的 Ethereum
交易确认组件。设备只读取原始交易字节，所有显示字段和签名摘要都在设备侧
重新计算；主机提供的 `HostMetadata` 仅用于传输层兼容，确认流程从不读取。

## 安全属性

- **设备独立解析**：严格、规范化的 RLP 解析器支持 Legacy、EIP-2930 和
  EIP-1559 交易。拒绝尾随字节、非最短 RLP、前导零整数、截断字段和未知交易
  类型。
- **链绑定**：Typed 交易校验显式 `chainId`；Legacy EIP-155 从 `v` 推导链并
  校验，未受保护的 `v=27/28` 默认拒绝。6 字段 Legacy 交易由设备策略注入
  链 ID 后计算 EIP-155 摘要，主机无法指定链。
- **地址逐项校验**：交易 `to` 必须恰好 20 字节；ERC-20 ABI 地址字要求前
  12 字节为零。屏幕始终绘制完整 42 字符 EIP-55 校验和地址，不截断、不使用
  省略号。
- **合约数据逐项校验**：受信 token 只接受精确的
  `transfer(address,uint256)` 选择器、68 字节长度、规范地址字和策略额度；
  未知合约调用、合约创建默认拒绝，只有策略显式放行后才显示醒目警告。
- **摘要与显示绑定**：
  `display_commitment = Keccak256("netsec.display.v1" || signing_digest ||
  uint32be(len) || display_bytes)`。显示文本包含完整地址、金额、最高手续费、
  链 ID 和签名摘要。
- **签名流程绑定**：签名回调只收到 `signing_digest` 和
  `display_commitment`，不收到主机解析结果或可写显示缓冲区。返回的 ECDSA
  签名会从摘要恢复地址，并必须等于固件预期的设备地址。
- **设备回读**：`readback()` 返回实际应绘制的字节；`verify_readback()` 对
  回读帧重新计算承诺。任一字符、地址、金额、手续费、链或摘要被替换都会失败。

## 快速使用

```cpp
#include "netsec.hpp"

netsec::TransactionPolicy policy;
policy.chain_id = 1;
policy.chain_name = "Ethereum";
policy.native_symbol = "ETH";
policy.expected_signer = *netsec::Address::from_hex(
    "0x9d8A62f656a8d1615C1294fd71e9CFb3E4855A4F");

netsec::TransactionConfirmer confirmer(policy);

// raw 来自主机；host_metadata 中的 recipient/amount/display 等都是不可信输入。
netsec::HostRequest request{raw_from_host, host_metadata_from_host};
auto confirmation = confirmer.confirm(request);
if (!confirmation) {
    // 拒绝并显示 confirmation.error()
    return;
}

// 固件把 confirmation->display().text 绘制到可信屏幕。
auto readback = confirmation->readback();
auto signed_tx = confirmation->sign(
    [&](const netsec::Hash256& digest, const netsec::Hash256& display) {
        return secure_element_sign(digest, display);  // 只签名摘要
    },
    policy.expected_signer.value());

// 安全元件返回后再从显示器读取实际帧并再次校验。
if (!signed_tx ||
    !confirmation->verify_readback(actual_framebuffer_bytes)) {
    // 放弃签名结果
}
```

`Result<T>` 不抛异常表示业务失败；`error().code` 给出稳定错误类别，
`error().byte_offset` 指向失败字段（适用时）。

## 默认策略

- 未知合约调用：拒绝（`allow_unknown_contract_calls = false`）。
- 合约创建：拒绝（`allow_contract_creation = false`）。
- Pre-EIP-155 签名：拒绝（`allow_unprotected_legacy = false`）。
- 高 `s` 签名：拒绝（`require_low_s_signature = true`）。
- Token 必须在 `TransactionPolicy::tokens` 中显式登记。
- 可选 `max_fee`、`max_transfer`、`max_transaction_bytes` 和
  `max_calldata_bytes` 限制资源使用与风险。

## 攻击验证

`tests/netsec_tests.cpp` 覆盖：

1. **显示替换**：HostMetadata 中伪造收款地址、金额、手续费、链和摘要后，
   设备显示仍只来自原始交易与固件策略。
2. **地址混淆**：EIP-55 官方向量、错误大小写校验和、ABI 地址非零填充、
   完整 42 字符地址和主机伪地址隔离。
3. **链替换**：Typed `chainId` 与 Legacy `v` 的跨链替换均被拒绝；主机声称
   的链不影响设备显示。
4. **合法签名**：EIP-155 官方向量恢复出正确发送方；签名只对准确的
   `signing_digest` 生效，高 `s`、篡改 `r`、错误设备地址和不匹配摘要均失败。
5. **设备回读**：回读字节逐字符校验；修改一个字符、摘要或承诺均导致
   `verify_readback()` 失败。
6. **合约数据**：错误选择器、错误长度、非规范地址字、未登记 token、默认
   拒绝的未知调用/合约创建、畸形 access list 和畸形 RLP。

## 构建

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build
ctest --test-dir build --output-on-failure
```

目标 `netsec` / `netsec::netsec` 可被其他 CMake 工程 `add_subdirectory` 或
安装后 `find_package` 导入。公共头文件只有 `netsec.hpp`；`netsec_crypto.*`
是内部实现。

## 范围与限制

- 支持 Ethereum Legacy、EIP-2930、EIP-1559；EIP-4844/7702 等类型显式拒绝。
- 受信 token 默认只理解 ERC-20 `transfer(address,uint256)`；其他函数需要
  固件扩展白名单，不能依赖主机解码。
- ECDSA 恢复只处理公开数据（签名和摘要），不包含私钥操作；实际签名由
  安全元件/签名回调完成。组件不做网络访问、不持久化交易、不信任主机时间或
  解析结果。
