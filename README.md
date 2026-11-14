# 智能家居配网绑定组件（`netsec.onboarding`）

供 C++20 配网服务调用的**可导入命名模块**（named module）。设备入网前校验一份
签名的配网凭证，确保设备只能加入被授权的家庭与控制器，抵御二维码替换、凭证复用
与跨家庭加入。

## 威胁模型与对策

| 攻击 / 约束 | 机制 | 拒绝原因 |
| --- | --- | --- |
| 二维码被替换成攻击者凭证 | 凭证对「家庭+控制器+设备类型+nonce+时间窗」整体签名，设备只信任本家庭锚点密钥 | `signature-invalid` |
| 二维码字段被篡改 | 长度前缀的规范化报文参与签名，任意字段改动都使签名失效 | `signature-invalid` |
| 凭证复用 / 重放 | nonce 在入网成功时**原子一次性消费**，再次提交即拒绝 | `nonce-reused` |
| 跨家庭加入 | 凭证家庭、设备授权家庭、控制器证书家庭三者必须一致 | `home-mismatch` |
| 伪造 / 过期控制器证书 | 入网前核对证书链、身份/家庭绑定与有效期 | `certificate-untrusted` |
| 配网窗口过长 | 凭证带短有效期（默认 120s），超时拒绝且不消费 nonce | `window-expired` |
| 无物理接触即可配网 | 要求人工物理确认（按键/灯态）后才继续 | `physical-confirmation-missing` |
| 设备类型不符 | 凭证绑定的设备类型必须与入网设备一致 | `device-type-mismatch` |
| 外来/畸形二维码 | 魔数 `ONBQ1`、字段数、受限字符集与十六进制签名结构校验 | `malformed-qr` |

## 模块结构

- `src/onboarding.cppm`：模块接口单元 `export module netsec.onboarding`，
  导出强类型标识、凭证/QR 编解码、`CredentialFactory`、`OnboardingService`
  以及内存测试替身。
- `src/onboarding.impl.cpp`：模块实现单元，承载全部 out-of-line 定义、
  签名/证书/nonce 校验链与测试替身实现。
- `tests/onboarding_test.cpp`：以 `import netsec.onboarding;` 消费模块的测试，
  覆盖合法配网、二维码替换（含字段篡改）、跨家庭、凭证复用、超时、物理确认、
  设备类型、畸形码与过期证书。

### 依赖注入而非虚基类

密码学、证书机构、nonce 存储与时钟通过 `std::function` 端口注入
（`Signer` / `SignatureVerifier` / `CertificateVerifier` /
`NonceIssuer` / `NonceConsumer` / `Clock`）。这样组件本身不绑定具体 PKI，
也不引入 vtable/RTTI——Apple Clang 21 在模块实现单元中不会为带 key function
的多态类生成虚表，类型擦除可在该工具链下稳定链接。生产环境应把端口接到
X.509 + Ed25519/ECDSA 与持久化 nonce 存储；当前 `InMemory*` 仅供测试，
其 FNV-128 密钥哈希是对称 MAC 替身，**不得用于生产**。

## 凭证与二维码格式

二维码负载：

```
ONBQ1|<home>|<controller>|<device-type>|<signer-key-id>|<nonce>|<issued-ms>|<window-ms>|<signature-hex>
```

签名不覆盖签名字段本身；参与签名的规范化报文对每个字段做
`<len>:<value>` 长度前缀拼接，杜绝分隔符注入。身份字段与 nonce 仅限
`[A-Za-z0-9_-]`，签名为小写十六进制。

## 入网校验顺序

`OnboardingService::Enroll` 严格按顺序短路，任何一步失败都不修改状态：

1. 物理确认
2. 二维码结构解析
3. 短时间窗
4. 凭证签名
5. 控制器证书（链/身份/家庭/有效期）
6. 家庭三方一致
7. 控制器身份一致
8. 设备类型一致
9. 原子一次性 nonce 消费（仅成功路径执行，失败不会浪费有效 nonce）

## 使用示例

```cpp
import netsec.onboarding;

using namespace netsec::onboarding;

// 配网服务侧：注入签名、nonce 登记与时钟端口，签发一次性凭证。
CredentialFactory factory(home_id, controller_id, device_type, signer_key,
                          signer_port, nonce_issuer_port, clock_port);
std::string qr = factory.IssueQr(/*nonce=*/"random-128-bit", kDefaultWindowMs);

// 设备侧：注入本家庭信任锚、签名校验、nonce 消费与时钟端口。
OnboardingService device(home_id, cert_verifier_port, signature_verifier_port,
                         nonce_consumer_port, clock_port);
EnrollmentRequest req{
    .qr_payload = qr,
    .device_serial = DeviceSerial{"sn-1"},
    .device_type = device_type,
    .physical_confirmation = true,        // 用户按下实体键
    .presented_certificate = controller_cert,
};
EnrollmentResult result = device.Enroll(req);
if (!result.accepted) {
  LogReject(ToString(*result.reason));    // 例如 "nonce-reused"
}
```

## 构建与测试

Apple Clang 21 需要 `-fcxx-modules` 才能接受命名模块语法。

使用 Makefile（已验证）：

```sh
make test
```

使用 CMake（Unix Makefiles 生成器，模块依赖通过自定义命令显式声明，
无需 Ninja 扫描器；采用 OBJECT 库以规避环境内 `ar` shim 限制）：

```sh
cmake -S . -B build/cmake -G "Unix Makefiles"
cmake --build build/cmake
./build/cmake/onboarding_test
```

测试输出 `33 checks, 0 failures`，覆盖：

- 合法配网（绑定到授权家庭与控制器）
- 二维码替换：攻击者签名、合法码字段篡改
- 跨家庭配网（外控制器证书 / 本控制器证书两种呈递）
- 凭证复用（同一 nonce 第二次被拒）
- 超时（过期拒绝且 nonce 不被消费，窗口内可重试）
- 物理确认缺失、设备类型不符、畸形二维码、控制器证书过期

## 生产落地要点

- 用非对称算法（Ed25519/ECDSA）替换测试 MAC，设备仅内置公钥/信任锚。
- nonce 存储需持久化并保证并发原子消费（接口已按线程安全设计）。
- nonce 由 CSPRNG 生成足够熵；时钟使用受信时间源并容忍合理时钟偏差。
- 物理确认应绑定实体按键/灯态交互，配网窗口保持分钟级。
