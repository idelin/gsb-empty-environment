# netsec：C++20 电磁侧信道防护组件

面向“设备附近采集电磁辐射、通过模板分析恢复 AES 中间值”的威胁模型，实现一个可由 C++20 安全模块直接链接/安装导入的防护库。

## 防护设计

- **掩码密钥与状态**：AES-128/AES-256 的用户密钥导入后立即拆成两路布尔份额；扩展轮密钥和加密状态始终以两份额形式保存，公开接口不会返回密钥中间值。
- **掩码 S 盒**：`SubBytes`/`InvSubBytes` 不按秘密值索引表，而是用 ISW 掩码等值判断扫描 256 个候选，再以掩码选择结果。每次非线性门使用新随机位。
- **随机化顺序**：每轮刷新全部状态份额；16 个 S 盒、4 个 MixColumns 列以及 KeySchedule 的 SubWord 字节均使用新随机排列访问。表扫描地址序列固定，与密钥/明文无关。
- **物理防护区域**：`PhysicalProtectionArea` 签发短时租约；每次密钥导入、轮换和使用都校验区域 ID、能力证明、有效期和吊销状态。新租约会使旧租约失效，跨区域租约直接拒绝。
- **EM 监测**：`EmMonitor` 同时检查绝对辐射强度、基线强度 z-score、每个频谱 bin 的异常 z-score、无效传感器数据；异常后锁存并阻止后续密钥运算，必须由可信校准路径重新布防。
- **采集与评估**：`EmSensor`/`EmCollector` 提供真实采集适配接口；`TemplateAnalyzer` 提供对角高斯模板训练、分类和恢复率评估，用于实验室验证。

## 快速使用

```cpp
#include "netsec.hpp"

netsec::SystemRandomSource rng;

// 可信启动/飞地代码先完成环境基线校准。
netsec::EmMonitor monitor;
std::vector<netsec::EmTrace> ambient = acquire_ambient_traces();
monitor.calibrate(ambient);

// 区域对象应由受信任的物理边界建立；这里演示其 API。
netsec::PhysicalProtectionArea area("attested-secure-enclave", rng);
netsec::ProtectionLease lease = area.lease(std::chrono::seconds(5));

std::array<std::uint8_t, 16> key = load_key_from_secure_storage();
netsec::ProtectedAes aes(area, lease, key, rng, &monitor);
netsec::secure_erase(key.data(), key.size()); // 调用方也应立即擦除导入副本

std::array<std::uint8_t, 16> plaintext{};
std::array<std::uint8_t, 16> ciphertext{};
netsec::OperationReport report;
const netsec::Status status =
    aes.encrypt_block(plaintext, ciphertext, lease, &report);

if (status != netsec::Status::ok) {
    // fail closed：不要使用未成功输出的密文，也不要继续密钥运算。
}
```

`ProtectedAes` 是块原语。业务层应在其上实现带认证的 AEAD 模式；不要直接用 ECB 处理消息。

## 监测与采集

`EmMonitor::ingest()` 接受一次采集的 RMS、峰值和频谱。监测器在 `warmup_samples` 个样本或显式 `calibrate()` 前保持 `calibrating` 并拒绝密钥运算。观察到强度/频谱异常后进入锁定状态：

```cpp
netsec::EmTrace trace = sensor.read_one();
netsec::EmObservation observation = monitor.ingest(trace);
if (!observation.key_operations_allowed) {
    // 停止密钥使用并触发安全事件。
}
```

真实硬件只需实现：

```cpp
class BoardEmSensor final : public netsec::EmSensor {
public:
    bool capture(netsec::EmTrace& trace) noexcept override;
};
```

然后交给 `EmCollector` 采集，或直接送入 `EmMonitor`。

## 泄露验证

测试覆盖以下链路：

1. AES-128/AES-256 标准向量、加密/解密往返、不同随机流下结果一致。
2. 每次合法运算的轮密钥/状态重掩码次数、随机排列次数和随机字节约量可观测。
3. 旧租约、跨区域租约、吊销后的租约全部被拒绝。
4. 强度超限、频谱 bin 突增、NaN 传感器数据触发监测锁定，并阻止密钥运算。
5. 合成 EM 采集 + 模板分类：未掩码一阶单点泄露可恢复率接近 100%，掩码后同一模型回落到类别先验（测试中为 25%）。

运行：

```bash
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build -j
ctest --test-dir build --output-on-failure
```

## 安装导入

```bash
cmake --install build --prefix /opt/netsec
```

其他 C++20 工程：

```cmake
find_package(netsec 1 CONFIG REQUIRED)
target_link_libraries(your_security_module PRIVATE netsec::netsec)
```

也可用 `-DBUILD_SHARED_LIBS=ON` 生成动态库。

## 安全边界

- 本库不自行证明进程位于真实物理防护区。`PhysicalProtectionArea` 是能力接口，生产环境必须由可信启动、TEE/安全飞地、TPM 或经认证的板载安全芯片签发/校验区域身份和租约。
- “操作顺序/地址去相关”指不按秘密值分支或索引敏感表；最终机器码仍可能因编译器优化、缓存、寄存器复用和微架构效应产生泄露。高保证场景应固定编译器/优化级别，检查汇编，并结合硬件屏蔽、双轨逻辑或专用安全核。
- 监测阈值必须按板卡和环境噪声标定；阈值、校准和重新布防接口应仅由受信任代码调用。攻击者若能任意调用 `reset()`，即可清除锁定状态。
- 两份额一阶掩码针对单点一阶模板/DPA 模型。若威胁包含高阶、多探针、故障注入、模板与功耗组合或微架构泄露，需要更高阶掩码、冗余/故障检测和专用硬件评估。
- 本库是软件组件，不能替代真实电磁实验室测试（如 ISO/IEC 17825、TVLA 或芯片级模板评估）。模板测试使用合成泄露模型验证防护设计逻辑，不构成硬件认证。
