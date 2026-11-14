#include "anti_rollback.hpp"

#include "sha256.hpp"

#include <array>
#include <cstring>

namespace netsec {

const char* to_string(CounterError e) {
    switch (e) {
    case CounterError::None: return "None";
    case CounterError::ReadFault: return "ReadFault";
    case CounterError::RedundancyMismatch: return "RedundancyMismatch";
    case CounterError::ProgramFault: return "ProgramFault";
    case CounterError::RegressNotAllowed: return "RegressNotAllowed";
    case CounterError::CapacityExceeded: return "CapacityExceeded";
    }
    return "Unknown";
}

namespace {

// CRC-64/XZ（反射多项式），用于受信存储副本的完整性校验。
std::uint64_t crc64(const std::uint8_t* data, std::size_t len) {
    std::uint64_t crc = 0xFFFFFFFFFFFFFFFFull;
    for (std::size_t i = 0; i < len; ++i) {
        crc ^= data[i];
        for (int bit = 0; bit < 8; ++bit) {
            const std::uint64_t mask = 0ull - (crc & 1ull);
            crc = (crc >> 1) ^ (0xC96C5795D7870F42ull & mask);
        }
    }
    return ~crc;
}

void store_le64(std::uint8_t* out, std::uint64_t v) {
    for (int i = 0; i < 8; ++i) out[i] = static_cast<std::uint8_t>(v >> (8 * i));
}

std::uint64_t load_le64(const std::uint8_t* in) {
    std::uint64_t v = 0;
    for (int i = 0; i < 8; ++i) v |= static_cast<std::uint64_t>(in[i]) << (8 * i);
    return v;
}

bool constant_time_equal(const std::uint8_t* a, const std::uint8_t* b, std::size_t n) {
    std::uint8_t diff = 0;
    for (std::size_t i = 0; i < n; ++i) diff |= a[i] ^ b[i];
    return diff == 0;
}

} // namespace

// ---------------------------------------------------------------------------
// OtpFuseCounter
// ---------------------------------------------------------------------------

std::uint64_t OtpFuseCounter::read_popcount_once() {
    std::uint64_t total = 0;
    const unsigned words = (bank_.capacity_bits() + 63) / 64;
    for (unsigned w = 0; w < words; ++w) {
        std::uint64_t word = bank_.read_word(w);
        total += static_cast<std::uint64_t>(__builtin_popcountll(word));
    }
    return total;
}

Expected<std::uint64_t> OtpFuseCounter::read() {
    // 双重回读：两次结果必须一致，防止不稳定/被干扰的读取被采信。
    const std::uint64_t first = read_popcount_once();
    const std::uint64_t second = read_popcount_once();
    if (first != second) return {0, CounterError::ReadFault};
    return {first, CounterError::None};
}

Status OtpFuseCounter::advance(std::uint64_t value) {
    const auto cur = read();
    if (!cur) return {cur.error};
    if (value < cur.value) return {CounterError::RegressNotAllowed};
    if (value > bank_.capacity_bits()) return {CounterError::CapacityExceeded};
    for (std::uint64_t bit = cur.value; bit < value; ++bit) {
        if (!bank_.blow_bit(static_cast<unsigned>(bit)))
            return {CounterError::ProgramFault};
    }
    // 写后回读：熔断未生效（被跳过/硬件故障）必须显式报错。
    const auto after = read();
    if (!after) return {after.error};
    if (after.value != value) return {CounterError::ProgramFault};
    return {CounterError::None};
}

// ---------------------------------------------------------------------------
// TrustedStorageCounter
// ---------------------------------------------------------------------------

Expected<std::uint64_t> TrustedStorageCounter::read_slot(std::size_t offset, bool& io_ok) {
    std::array<std::uint8_t, kSlotSize> buf{};
    io_ok = storage_.read(offset, buf.data(), buf.size());
    if (!io_ok) return {0, CounterError::ReadFault};
    const std::uint64_t value = load_le64(buf.data());
    const std::uint64_t crc = load_le64(buf.data() + 8);
    if (crc64(buf.data(), 8) != crc) return {0, CounterError::RedundancyMismatch};
    return {value, CounterError::None};
}

Status TrustedStorageCounter::write_slot(std::size_t offset, std::uint64_t value) {
    std::array<std::uint8_t, kSlotSize> buf{};
    store_le64(buf.data(), value);
    store_le64(buf.data() + 8, crc64(buf.data(), 8));
    if (!storage_.write(offset, buf.data(), buf.size())) return {CounterError::ProgramFault};
    return {CounterError::None};
}

Expected<std::uint64_t> TrustedStorageCounter::read() {
    bool ok_a = false, ok_b = false;
    const auto primary = read_slot(kPrimaryOffset, ok_a);
    const auto redundant = read_slot(kRedundantOffset, ok_b);
    if (!ok_a || !ok_b) return {0, CounterError::ReadFault};
    // 任一副本完整性失败或两副本不一致：篡改/故障显式上报，拒绝采信。
    if (!primary || !redundant) return {0, CounterError::RedundancyMismatch};
    if (primary.value != redundant.value) return {0, CounterError::RedundancyMismatch};
    return {primary.value, CounterError::None};
}

Status TrustedStorageCounter::format() {
    if (const auto s = write_slot(kPrimaryOffset, 0); !s) return s;
    if (const auto s = write_slot(kRedundantOffset, 0); !s) return s;
    const auto after = read();
    if (!after) return {after.error};
    if (after.value != 0) return {CounterError::ProgramFault};
    return {CounterError::None};
}

Status TrustedStorageCounter::advance(std::uint64_t value) {
    const auto cur = read();
    if (!cur) return {cur.error};
    if (value < cur.value) return {CounterError::RegressNotAllowed};
    if (const auto s = write_slot(kPrimaryOffset, value); !s) return s;
    if (const auto s = write_slot(kRedundantOffset, value); !s) return s;
    // 写后回读确认。
    const auto after = read();
    if (!after) return {after.error};
    if (after.value != value) return {CounterError::ProgramFault};
    return {CounterError::None};
}

// ---------------------------------------------------------------------------
// HmacAuthorizer
// ---------------------------------------------------------------------------

std::vector<std::uint8_t> HmacAuthorizer::sign(const std::vector<std::uint8_t>& key,
                                               std::uint64_t target_version,
                                               std::uint64_t current_version,
                                               std::uint64_t serial) {
    // 规范化编码：固定小端序，与平台无关。
    std::array<std::uint8_t, 24> msg{};
    store_le64(msg.data(), target_version);
    store_le64(msg.data() + 8, current_version);
    store_le64(msg.data() + 16, serial);
    const auto mac = hmac_sha256(key.data(), key.size(), msg.data(), msg.size());
    return {mac.begin(), mac.end()};
}

bool HmacAuthorizer::verify(const DowngradeGrant& grant) const {
    const auto expected = sign(key_, grant.target_version, grant.current_version, grant.serial);
    if (grant.signature.size() != expected.size()) return false;
    return constant_time_equal(expected.data(), grant.signature.data(), expected.size());
}

// ---------------------------------------------------------------------------
// AntiRollback
// ---------------------------------------------------------------------------

UpdateDecision AntiRollback::authorize_install(std::uint64_t image_version,
                                               const DowngradeGrant* grant) {
    const auto cur = counter_.read();
    if (!cur) {
        // 计数器读不出：fail-closed，绝不按“未知即放行”处理。
        audit_.record({AuditEventType::CounterFault, 0, image_version,
                       std::string("counter readback failed: ") + to_string(cur.error)});
        return {Decision::Rejected, cur.error, "version counter readback failed; failing closed"};
    }
    const std::uint64_t current = cur.value;

    if (image_version >= current) {
        audit_.record({AuditEventType::UpdateAuthorized, current, image_version, {}});
        return {Decision::Allowed, CounterError::None, {}};
    }

    // 降级路径：必须持有签名有效、字段绑定且未重放的授权。
    const bool grant_ok = grant != nullptr &&
                          grant->target_version == image_version &&
                          grant->current_version == current &&
                          grant->serial > last_grant_serial_ &&
                          authorizer_.verify(*grant);
    if (grant_ok) {
        last_grant_serial_ = grant->serial;
        audit_.record({AuditEventType::DowngradeAuthorized, current, image_version,
                       "grant serial=" + std::to_string(grant->serial)});
        return {Decision::DowngradeAllowed, CounterError::None, {}};
    }

    audit_.record({AuditEventType::DowngradeRejected, current, image_version,
                   grant ? "invalid or replayed downgrade grant" : "downgrade without grant"});
    return {Decision::Rejected, CounterError::None,
            "image version below anti-rollback counter; valid signed grant required"};
}

Status AntiRollback::commit(std::uint64_t installed_version) {
    const auto before = counter_.read();
    if (!before) {
        audit_.record({AuditEventType::CounterFault, 0, installed_version,
                       "pre-commit readback failed"});
        return {before.error};
    }

    // 计数器只增不减：正常更新推进到镜像版本；授权降级保持原值。
    const std::uint64_t expected =
        installed_version > before.value ? installed_version : before.value;

    if (installed_version > before.value) {
        if (const auto s = counter_.advance(installed_version); !s) {
            audit_.record({AuditEventType::CounterFault, before.value, installed_version,
                           std::string("counter advance failed: ") + to_string(s.error)});
            return s;
        }
        audit_.record({AuditEventType::CounterAdvanced, before.value, installed_version, {}});
    }

    // 最终回读：确认计数器状态与预期完全一致，不一致即显式失败。
    const auto fin = counter_.read();
    if (!fin) {
        audit_.record({AuditEventType::CounterFault, before.value, installed_version,
                       "post-commit readback failed"});
        return {fin.error};
    }
    if (fin.value != expected) {
        audit_.record({AuditEventType::CounterFault, before.value, installed_version,
                       "post-commit counter mismatch"});
        return {CounterError::ProgramFault};
    }
    return {CounterError::None};
}

} // namespace netsec
