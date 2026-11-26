PKI 证书验证器在解析 X.509 v3 证书链并核对 RFC 5280 命名约束（Name Constraints）与策略映射（Policy Mapping）时，若放过 BER 非最小长度编码、忽略排除子树优先于允许子树，或在交叉签名链中错算 `inhibitPolicyMapping`/`requireExplicitPolicy`/`inhibitAnyPolicy` 递减计数，受限中间 CA 即可越权为禁用域名或未授权策略签发有效证书。为 C++17 密码宿主实现零外部 ASN.1 库依赖、`-Wall -Wextra -Wconversion -Werror` 编译通过的原始字节级 DER 解码与 RFC 5280 §6.1 完整路径状态机验证库（签名核验通过抽象回调注入）。DER 解码器严格拒绝不定长编码、非最短长度字节、整数前导冗余 0x00/0xFF、布尔值非 `0x00`/`0xFF`、BIT STRING 未使用位非零及尾部冗余字节，并完整解析证书主体、`SubjectAltName`（DNS/IPv4/IPv6/Email/URI）、`BasicConstraints`（含 `cA` 与 `pathLenConstraint`）、`KeyUsage`、`NameConstraints`、`CertificatePolicies`、`PolicyMappings` 与 `PolicyConstraints`。路径验证严格执行 RFC 5280 §6.1：逐级交集收紧允许子树并集扩张排除子树（准确处理 DNS 前导点与子域匹配、IPv4/IPv6 CIDR 掩码越界拦截、Email 大小写与本地/域部规则及自签证书豁免边界），按规范维护有效策略树与三大抑制计数器（区分自颁发中间证书不消耗 `pathLen` 与计数器的特殊条款），以及识别未知 `critical` 扩展必须拒绝。用字节级构造的 BER 歧义样本、排除子树绕过、策略映射越权、自颁发路径计数与关键扩展样本实测全通过。

## 实现概览

- `include/pki/der.hpp` / `src/der.cpp`：无第三方依赖的字节级 DER/BER 解码器与最短 DER 构造器。
- `include/pki/x509.hpp` / `src/x509.cpp`：X.509 v3、DN、时间、SAN、基本约束、密钥用途、命名约束、策略扩展解析。
- `include/pki/names.hpp` / `src/names.cpp`：DNS、Email、URI、IPv4/IPv6 CIDR、DN 子树匹配与允许子树交集。
- `include/pki/validator.hpp` / `src/validator.cpp`：RFC 5280 §6.1 路径状态机、策略树、计数器、`pathLen`、关键扩展和签名回调。
- `tests/test_pki.cpp`：纯字节构造的严格 DER、命名绕过、策略越权、自颁发计数和关键扩展测试。

## 构建与测试

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build -j
ctest --test-dir build --output-on-failure
```

目标默认启用 `-Wall -Wextra -Wconversion -Wsign-conversion -Wpedantic -Werror`；需求中的 `-Wall -Wextra -Wconversion -Werror` 是其子集。

## 接入方式

实现 `pki::SignatureVerifier`，用宿主密码库核验证书中的 `tbsCertificate` DER、签名字节和算法 OID：

```cpp
class HostVerifier final : public pki::SignatureVerifier {
public:
    bool verify(const pki::SignatureInput& input) override {
        // 使用 input.issuer.subject_public_key_info 和
        // input.tbs / input.signature / input.algorithm_oid 验签。
        return host_crypto_verify(input);
    }
};

HostVerifier verifier;
pki::VerificationOptions options;
options.signature_verifier = &verifier;
options.verification_time = pki::current_unix_time();

auto result = pki::verify_der_path(leaf_and_intermediates_der,
                                   trust_anchor_der, options);
if (!result.ok()) {
    // pki::error_code_name(result.code), result.message
}
```

`leaf_first` / `leaf_first_der` 按叶证书到中间 CA 的顺序排列，不包含信任锚。也可以先调用 `pki::x509::parse_certificate`，再使用 `pki::verify_path` 复用解析结果。

## 已固化的安全边界

- 拒绝不定长、非最短长度、冗余整数符号字节、非规范布尔值、非零 BIT STRING 未使用位和任何 TLV 尾部字节。
- 排除子树先于允许子树判定；多层允许子树按交集收紧，排除子树按并集累积。
- DNS 不带前导点时允许本体及左侧追加标签；`.example.com` 只匹配子域，不匹配 `example.com`。
- Email 本地部分区分大小写，域部分不区分；无 `@` 前导点域规则匹配主机，`.domain` 只匹配子域。
- IPv4/IPv6 CIDR 前缀分别限制在 32/128 内，证书中的 IP 约束必须包含地址和等长掩码。
- 非最终自颁发证书豁免命名子树检查且不消耗 `pathLen`/策略计数器；最终自颁发证书不豁免命名检查。
- 未知 critical 扩展返回 `pki::ErrorCode::UnrecognizedCriticalExtension`。
