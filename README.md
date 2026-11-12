# SPDM GET_MEASUREMENTS 响应校验包

`netsec` 是一个不依赖 OpenSSL 的 C++17 校验库，供主机固件处理 SPDM
`GET_MEASUREMENTS` 响应时调用。库负责绑定请求/响应 nonce、重建消息记录摘要、
校验证书链根信任、签名长度和测量块集合；具体签名算法由调用方以回调接入已有
密码模块。

## 校验内容

- 请求 nonce 必须由调用方随机源生成；响应 nonce 是独立的 32 字节值，且不得
  与本次请求 nonce 相同。
- 记录摘要为 `SHA-384(VCA || 证书链记录 || 请求字节 || 响应除签名外字节)`，
  顺序固定、不插入分隔符，必须传入真实 wire bytes。
- 证书链根的 `SHA-384`（可直接给哈希，或给根证书 DER 由此库计算）必须在调用
  方 `trusted_root_hashes` 中。
- 请求了签名时响应必须带签名；签名长度必须与算法匹配。签名回调收到的是库
  重新计算出的记录摘要，而不是响应中声明的摘要。
- 测量块索引必须唯一且严格升序；重复索引单独报错。
- 测量块与期望集合逐索引比对，缺块、多块、值不同均失败。

## 接入示例

```cpp
#include "netsec.hpp"

netsec::VerificationPolicy policy;
policy.trusted_root_hashes = load_trusted_root_hashes();
policy.expected_measurements = load_expected_measurements();
policy.signature_verifier =
    [](const netsec::Sha384Digest& digest,
       netsec::SignatureAlgorithm algorithm,
       const netsec::Bytes& signature,
       const netsec::CertificateChainRecord& chain) {
        // 用已经校验证书链的叶子公钥，按 algorithm 验证 signature。
        // 签名输入是库计算的 digest，不能使用响应自带的摘要字段。
        return verify_with_device_public_key(digest, algorithm, signature,
                                             chain);
    };

netsec::MeasurementRequest request;
request.nonce = netsec::generate_request_nonce(
    [](std::uint8_t* out, std::size_t size) { fill_random(out, size); });
request.bytes = build_get_measurements_bytes(request.nonce);
request.signature_requested = true;

netsec::MeasurementResponse response;
response.nonce = parsed_response_nonce;
response.bytes_without_signature = raw_response_bytes_without_signature;
response.signature_present = signature_present;
response.signature_algorithm = parsed_signature_algorithm;
response.signature = parsed_signature;
response.measurements = parse_measurement_blocks(raw_response_bytes);

const auto result = netsec::verify_measurements_response(
    session, request, response, policy);
if (!result) {
    reject(result.error, result.detail);
}
```

`MeasurementResponse::measurements` 与 `bytes_without_signature` 必须由同一份
响应报文解析得到。真实固件中不要接收调用方单独填写、未经报文绑定校验的测量值。

## 构建与测试

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```

测试覆盖：合法响应、旧响应重放、响应 nonce 冲突/被改、VCA/证书链/请求/响应
记录被改、重复与乱序索引、缺签名、签名长度不符、根不受信、缺块/多块/值不符。
