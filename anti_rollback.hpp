#pragma once

// 反回滚（Anti-Rollback）组件：供固件更新组件调用。
//
// 安全不变式：
//   1. 版本计数器由一次性熔丝（OTP）或受信存储维护，只增不减；
//   2. 待安装镜像版本必须 >= 当前计数器值；
//   3. 降级必须持有签名授权（DowngradeGrant），且全程记录审计日志；
//   4. 计数器每次读取都做冗余回读校验，任何失败一律“拒绝并上报”，
//      绝不静默放行或静默降级。

#include <cstdint>
#include <string>
#include <vector>

namespace netsec {

// 计数器错误：所有失败路径显式上报，调用方必须检查。
enum class CounterError {
    None = 0,
    ReadFault,          // 回读失败（两次读取不一致 / 后端故障）
    RedundancyMismatch, // 冗余副本不一致或完整性校验失败
    ProgramFault,       // 熔断/写入未生效（写后回读不符，例如熔丝被跳过）
    RegressNotAllowed,  // 计数器只允许单调递增
    CapacityExceeded,   // 超出熔丝位容量
};

const char* to_string(CounterError e);

template <typename T>
struct Expected {
    T value{};
    CounterError error = CounterError::None;
    explicit operator bool() const { return error == CounterError::None; }
};

struct Status {
    CounterError error = CounterError::None;
    explicit operator bool() const { return error == CounterError::None; }
};

// ---------------------------------------------------------------------------
// 审计日志
// ---------------------------------------------------------------------------

enum class AuditEventType {
    UpdateAuthorized,    // 正常更新/同版本重装被允许
    DowngradeAuthorized, // 带有效签名的降级被允许（显式例外）
    DowngradeRejected,   // 无有效授权的降级被拒绝
    CounterFault,        // 计数器回读/冗余校验/熔断失败
    CounterAdvanced,     // 计数器成功推进
};

struct AuditEvent {
    AuditEventType type;
    std::uint64_t counter_before; // 事件发生时读到的计数器值（读失败为 0）
    std::uint64_t image_version;  // 涉及的镜像版本
    std::string detail;
};

class IAuditLog {
public:
    virtual ~IAuditLog() = default;
    virtual void record(const AuditEvent& event) = 0;
};

// ---------------------------------------------------------------------------
// 版本计数器抽象
// ---------------------------------------------------------------------------

class IVersionCounter {
public:
    virtual ~IVersionCounter() = default;
    // 读取当前计数器值，实现必须做冗余回读校验。
    virtual Expected<std::uint64_t> read() = 0;
    // 将计数器推进到 value（必须 >= 当前值），写后必须回读确认。
    virtual Status advance(std::uint64_t value) = 0;
};

// 一次性熔丝阵列硬件抽象：每一位只能 0 -> 1，物理不可回退。
class IFuseBank {
public:
    virtual ~IFuseBank() = default;
    virtual unsigned capacity_bits() const = 0;
    virtual std::uint64_t read_word(unsigned index) = 0; // 回读熔丝字
    virtual bool blow_bit(unsigned bit_index) = 0;       // 熔断指定位（幂等）
};

// OTP 熔丝计数器：版本 = 已熔断位数（温度计编码）。
// read() 连续回读两次，结果不一致即报 ReadFault；
// advance() 逐位熔断后回读，位数不符即报 ProgramFault（捕获“熔丝跳过”）。
class OtpFuseCounter final : public IVersionCounter {
public:
    explicit OtpFuseCounter(IFuseBank& bank) : bank_(bank) {}
    Expected<std::uint64_t> read() override;
    Status advance(std::uint64_t value) override;

private:
    std::uint64_t read_popcount_once();
    IFuseBank& bank_;
};

// 受信存储后端抽象（如 RPMB / 安全 enclave 内的 NV 区）。
class INvStorage {
public:
    virtual ~INvStorage() = default;
    virtual bool read(std::size_t offset, std::uint8_t* out, std::size_t len) = 0;
    virtual bool write(std::size_t offset, const std::uint8_t* data, std::size_t len) = 0;
};

// 受信存储计数器：主副本 + 冗余副本，各自带 CRC-64 完整性校验。
// read() 要求两副本 CRC 均有效且值一致，否则报 RedundancyMismatch。
class TrustedStorageCounter final : public IVersionCounter {
public:
    explicit TrustedStorageCounter(INvStorage& storage) : storage_(storage) {}
    // 出厂初始化：将两个副本写为 0 并回读确认。
    // 未格式化的存储 read() 一律报 RedundancyMismatch（fail-closed）。
    Status format();
    Expected<std::uint64_t> read() override;
    Status advance(std::uint64_t value) override;

private:
    static constexpr std::size_t kSlotSize = 16; // {value:8, crc64:8}
    static constexpr std::size_t kPrimaryOffset = 0;
    static constexpr std::size_t kRedundantOffset = kSlotSize;

    Expected<std::uint64_t> read_slot(std::size_t offset, bool& io_ok);
    Status write_slot(std::size_t offset, std::uint64_t value);

    INvStorage& storage_;
};

// ---------------------------------------------------------------------------
// 降级授权
// ---------------------------------------------------------------------------

// 降级授权凭证：由厂外密钥对 (target_version, current_version, serial) 签名。
// current_version 将授权绑定到签发时的计数器值，serial 单调递增防重放。
struct DowngradeGrant {
    std::uint64_t target_version = 0;  // 允许降级到的镜像版本
    std::uint64_t current_version = 0; // 签发时的计数器值
    std::uint64_t serial = 0;          // 授权序号（必须大于已用序号）
    std::vector<std::uint8_t> signature;
};

class IDowngradeAuthorizer {
public:
    virtual ~IDowngradeAuthorizer() = default;
    virtual bool verify(const DowngradeGrant& grant) const = 0;
};

// 基于 HMAC-SHA-256 的授权验证器（演示/对称密钥场景）。
// 生产环境可替换为 Ed25519/ECDSA 验签，接口不变。
class HmacAuthorizer final : public IDowngradeAuthorizer {
public:
    explicit HmacAuthorizer(std::vector<std::uint8_t> key) : key_(std::move(key)) {}
    bool verify(const DowngradeGrant& grant) const override;

    // 厂外签发工具：对授权字段做规范化编码后计算 HMAC。
    static std::vector<std::uint8_t> sign(const std::vector<std::uint8_t>& key,
                                          std::uint64_t target_version,
                                          std::uint64_t current_version,
                                          std::uint64_t serial);

private:
    std::vector<std::uint8_t> key_;
};

// ---------------------------------------------------------------------------
// 反回滚策略
// ---------------------------------------------------------------------------

enum class Decision {
    Allowed,          // 正常更新或同版本重装
    DowngradeAllowed, // 带有效签名授权的降级（显式例外，已记录）
    Rejected,        // 拒绝安装
};

struct UpdateDecision {
    Decision decision = Decision::Rejected;
    CounterError counter_error = CounterError::None; // 计数器故障时非 None
    std::string reason;
    bool allowed() const { return decision != Decision::Rejected; }
};

class AntiRollback {
public:
    // last_grant_serial 应来自受信存储中持久化的已用授权序号。
    AntiRollback(IVersionCounter& counter, const IDowngradeAuthorizer& authorizer,
                 IAuditLog& audit, std::uint64_t last_grant_serial = 0)
        : counter_(counter), authorizer_(authorizer), audit_(audit),
          last_grant_serial_(last_grant_serial) {}

    // 安装前校验：镜像版本 >= 计数器值才放行；低于则要求有效签名授权。
    // 计数器读不出时一律拒绝（fail-closed）。
    UpdateDecision authorize_install(std::uint64_t image_version,
                                     const DowngradeGrant* grant = nullptr);

    // 安装后提交：按需推进计数器并回读确认。
    // 授权降级不降低计数器（熔丝物理不可回退），仅作为已记录的例外。
    // 任何失败都显式返回错误并记录审计，绝不静默。
    Status commit(std::uint64_t installed_version);

    // 当前计数器值（含冗余回读校验）。
    Expected<std::uint64_t> current_version() { return counter_.read(); }

    std::uint64_t last_grant_serial() const { return last_grant_serial_; }

private:
    IVersionCounter& counter_;
    const IDowngradeAuthorizer& authorizer_;
    IAuditLog& audit_;
    std::uint64_t last_grant_serial_;
};

} // namespace netsec
