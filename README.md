终端安全网关与包审查服务在离线校验 macOS Mach-O 64 位二进制及 Universal Fat 归档的代码签名完整性时，若仅检查单个切片或忽略 `LC_CODE_SIGNATURE` 中 `CodeDirectory` 页哈希与 `Entitlements` XML/DER 双槽位一致性，攻击者可利用 Fat 架构切片差异、尾段未签名代码注入或 XML 与 DER 权限声明分歧绕过代码签名审查。为 C++17 二进制安全宿主实现可嵌入、零外部依赖、`-Wall -Wextra -Wconversion -Werror` 编译通过的原始字节级 Mach-O 64 与 `LC_CODE_SIGNATURE` 完整验证库（提供内置 SHA-256 与抽象 PKCS#7 验签回调）。解析器支持标准 64 位 Mach-O（`0xFEEDFACF`）及 Fat Binary（`0xCAFEBABE`/`0xCAFEBABF`），严格校验所有切片不重叠、不越界且每个切片均须独立通过签名验证；解析全部 `load_command`（`LC_SEGMENT_64`/`LC_CODE_SIGNATURE`/`LC_LOAD_DYLIB`/`LC_RPATH`），拒绝段虚拟地址重叠、`fileoff + filesize` 回绕、以及存在同时可写可执行（`VM_PROT_WRITE | VM_PROT_EXECUTE`）的段。对 `LC_CODE_SIGNATURE` 指向的 `CSMAGIC_EMBEDDED_SIGNATURE` SuperBlob，完整解析 `CodeDirectory`（校验 `codeLimit` 精确覆盖至签名段起点、按 `pageSize` 逐页重算并核对全部代码页 SHA-256 哈希、拒绝缺失 `CS_RUNTIME` 硬运行时标志）、特殊槽位（`-2` Requirements、`-5` XML Entitlements、`-7` DER Entitlements）：强制验证 XML 与 DER 权限槽位哈希均匹配 `CodeDirectory` 且两者声明的敏感权限集合语义一致，同时拦截未签名的尾部冗余字节与含相对路径的危险 `LC_RPATH`。用字节级构造的合法 Mach-O/Fat 样本与篡改代码页、XML/DER 权限分歧、未签名切片样本实测全拦截。

## 实现

- `include/machosec/machosec.h`：公共 API、报告结构、SHA-256 与 PKCS#7 抽象回调。
- `src/sha256.cpp`：零依赖内置 SHA-256。
- `src/verifier.cpp`：Mach-O 64、Fat/Fat64、load command、SuperBlob、CodeDirectory、页哈希和槽位校验。
- `src/entitlements.cpp`：最小 XML plist 与 Apple DER entitlement 解析器、敏感权限语义比较。
- `tests/sample_builder.*`：直接按磁盘字节布局构造合法/异常 Mach-O 与 Fat 样本。
- `tests/test_verifier.cpp`：代码页篡改、XML/DER 分歧、未签名 Fat 切片、尾部注入、重叠/越界、RWX、rpath、PKCS#7 回调和全截断测试。
- `tools/machosec_verify.cpp`：离线 CLI。

## 构建与实测

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build -j
ctest --test-dir build --output-on-failure
./build/machosec_verify /path/to/binary
./build/machosec_samples "$TMPDIR/machosec-samples"
```

目标默认使用 `-Wall -Wextra -Wconversion -Werror -Wpedantic`。当前字节级测试为 63 项检查，全部通过；并用本机 `codesign -o runtime` 生成的 arm64 与 Universal 二进制交叉验证通过。

## 嵌入示例

```cpp
#include "machosec/machosec.h"

class HostPkcs7Verifier final : public machosec::IPkcs7Verifier {
public:
    bool verify_pkcs7(const machosec::Pkcs7Context& ctx,
                      std::string& error) override {
        // 宿主在此接入受信任根、证书链、CMS/PKCS#7 签名验证和 CD hash 绑定。
        return host_cms_verify(ctx.cms_bytes, ctx.code_directory_hash, error);
    }
};

HostPkcs7Verifier pkcs7;
machosec::VerifyOptions options;
options.pkcs7_verifier = &pkcs7;
options.require_pkcs7_verifier = true;
options.require_pkcs7_signature_slot = true;

machosec::Report report;
std::string error;
if (!machosec::verify_file("/path/to/binary", options, &report, &error)) {
    // 拒绝加载并记录 error。
}
```

未设置 PKCS#7 回调时，库仍完成所有原始字节、SHA-256、槽位和权限一致性检查，但不会把 CMS 证书链验证标记为已完成。安全宿主应注入 `IPkcs7Verifier` 并按需打开两个 `require_pkcs7_*` 选项。

## 严格策略

- Fat/Fat64 的每个切片都独立执行完整 Mach-O 校验；切片重叠、越界或尾部追加均失败。
- `codeLimit` 必须恰好等于 `LC_CODE_SIGNATURE` 的 `dataoff`，每个非完整末页也按实际长度计算 SHA-256。
- `-2`、`-5`、`-7` 槽的整个 child blob 都参与 SHA-256 核对；缺失槽或非零无主槽均拒绝。
- XML/DER 默认比较敏感权限的有效语义：布尔 `false` 与缺省视为均未授予，其余值按类型比较；可切换为全量权限比较。
- 默认拒绝非绝对 `LC_RPATH` 和任何 `..` 路径分量，因此普通相对路径与 `@executable_path`/`@loader_path` 形式均需宿主显式放宽策略后才能接受。
- `LC_CODE_SIGNATURE` SuperBlob 后的非零 slack 拒绝；全零对齐 padding 可由选项控制。文件末尾超出 `dataoff + datasize` 的字节一律拒绝。
