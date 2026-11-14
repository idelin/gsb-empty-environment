# netsec — 包安装内容一致性组件

面向 C++20 包安装器的可导入组件。它解决的核心问题是：验签器看到的是
“规范化文本”，安装器却从原始字节和本地编码重新读取文件，导致同一份签名
可以对应两种不同的安装内容。

本组件把规范化、摘要核对和安装绑定在一起：

- 签名覆盖的是**规范化字节**，不是调用方临时拼出来的文本。
- 安装接口只接受 `VerifiedPackage`，它拥有刚才被验签和逐项核对的规范字节。
- 文本在进入签名边界前固定为严格 UTF-8、无 BOM、LF 换行。
- 不进行隐式转码；UTF-16、Latin-1 等编码和非法 UTF-8 直接拒绝。
- 清单、每个脚本/配置文件、每个路径分别按 SHA-256 摘要核对。

## 规范化规则

### 文本（`Script` / `Config`）

- 只接受严格 UTF-8；拒绝 overlong、代理区、截断序列和超出 U+10FFFF 的码点。
- 允许并去除开头的一个 UTF-8 BOM；正文中再次出现 U+FEFF 视为错误。
- `CRLF` 和单独的 `CR` 统一为 `LF`。
- 默认拒绝 NUL、其他 C0/C1 控制字符、零宽字符和双向控制字符。
- 不做 Unicode 归一化。NFC 与 NFD 是不同码点序列，因此是不同内容。
- 只接受 UTF-8 这一种输入编码，不做 UTF-8/UTF-16/Latin-1 之间的转换。

### 路径

- 必须是相对路径，使用 `/` 分隔。
- 默认只允许 ASCII，避免 Unicode 归一化和同形字造成的文件系统别名。
- 折叠空组件和 `.`，例如 `./scripts//run.sh` 规范化为 `scripts/run.sh`。
- 拒绝 `..`、绝对路径、反斜杠、`%xx`、冒号、控制字符和结尾的 `.`/空格。
- 同一包内规范化后路径必须唯一；仅大小写不同的路径也会被拒绝。

### 签名载荷

签名覆盖一个确定的二进制载荷：

1. 固定魔数和版本；
2. 规范化后的完整清单字节；
3. 每个条目的规范路径、角色、路径摘要和内容摘要。

因此修改换行、BOM、清单字段、路径、角色或文件内容都会改变载荷或逐项摘要。

## 清单格式

`serialize_manifest()` 生成如下 UTF-8/LF 文本（条目按规范路径排序）：

```text
NETSEC-MANIFEST	1
package	demo-package
version	1.0.0
entry	script	<path-sha256>	<content-sha256>	scripts/install.sh
entry	config	<path-sha256>	<content-sha256>	share/config.txt
entry	data	<path-sha256>	<content-sha256>	share/payload.bin
```

`parse_manifest()` 会重新规范化并校验路径摘要；即使输入带 BOM 或 CRLF，
最终参与签名的仍是同一份规范清单。

## 快速使用

```cpp
#include "netsec.hpp"

using namespace netsec;

PackageDraft draft;
draft.package_id = "demo-package";
draft.version = "1.0.0";
draft.files.push_back({"scripts/install.sh", EntryRole::Script,
                       to_bytes("#!/bin/sh\r\necho ok\r\n")});
draft.files.push_back({"share/payload.bin", EntryRole::Data,
                       Bytes{0x00, 0x01, 0x02}});

const std::string key = "deployment-key";
auto package = build_hmac_package(draft, key);
if (!package) {
    // package.error().code / message
}

auto verified = verify_package(package.value(), hmac_verify_function(key));
if (!verified) {
    // 签名错误或任一清单/路径/内容摘要不匹配
}

// VerifiedPackage 内部是刚才验签和核对过的规范字节。
Status installed = install_package(verified.value(), "/opt/demo");
```

生产环境可把 `VerifyFunction` 换成公钥验签实现；`hmac_sha256()` 和
`hmac_verify_function()` 主要用于测试或已有对称密钥的部署。

## 主要 API

- `canonicalize_text()` / `canonicalize_path()`：规范化原语。
- `serialize_manifest()` / `parse_manifest()`：规范清单读写。
- `build_package()`：按规范形式构建包。
- `sign_package()`：校验包后对规范签名载荷签名。
- `build_hmac_package()`：构建并用 HMAC-SHA256 签名。
- `verify_package()`：先验签，再逐项核对路径和内容摘要。
- `verify_hmac_package()`：使用 HMAC-SHA256 的便捷验证入口。
- `VerifiedPackage`：只能由 `verify_package()` 产生；安装器应只接受该类型。
- `install_package()`：只写入已验证的规范字节；默认不写清单，
  可用 `InstallOptions::write_manifest` 打开。
- `verify_and_install()`：先验证再安装的组合入口，验证失败时不会写任何文件。
- `load_package()` / `write_package()`：`manifest.txt` + `signature.bin`
  的目录形式读写；目录中未列入清单的文件不会被安装。

## 构建与测试

需要支持 C++20 的编译器：

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build
(cd build && ctest --output-on-failure)
```

测试覆盖：

- 合法包：签名、规范清单、逐项摘要、安装后字节回读；
- CRLF 与 BOM 变体：验签通过，但安装的是签名覆盖的 LF/无 BOM 字节；
- Latin-1、UTF-16 和 NFC/NFD 差异：拒绝或摘要失配，不发生静默转码；
- `./`、`//`、`..`、绝对路径、反斜杠、百分号、大小写冲突；
- 签名篡改、清单篡改、脚本篡改、缺失文件和未列入清单的额外文件；
- 目录往返和安装目标符号链接拒绝。

## 安全边界

- 默认路径仅 ASCII；如业务必须使用 Unicode 路径，需要在上层先做统一的
  NFC/NFKC 处理并收紧文件系统别名策略，不能直接关闭本组件的检查。
- `Result`/`Status` 错误不会被静默忽略；安装器应把任何非成功结果视为拒绝安装。
- 文件读取和安装均有大小上限（`Limits`），目录读取拒绝符号链接。
- 组件保证“验签字节 == 安装字节”，不替代密钥管理、证书链或公钥基础设施。
