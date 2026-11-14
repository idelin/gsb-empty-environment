// 反回滚组件测试：旧镜像拒绝、熔丝跳过检测、授权降级、合法更新、回读验证。

#include "anti_rollback.hpp"
#include "sha256.hpp"

#include <cstdio>
#include <cstring>
#include <vector>

using namespace netsec;

namespace {

int g_failures = 0;

#define CHECK(cond)                                                                            \
    do {                                                                                       \
        if (!(cond)) {                                                                         \
            std::fprintf(stderr, "FAIL %s:%d: %s\n", __FILE__, __LINE__, #cond);               \
            ++g_failures;                                                                      \
        }                                                                                      \
    } while (0)

// ---------------------------------------------------------------------------
// 测试替身
// ---------------------------------------------------------------------------

class SimFuseBank final : public IFuseBank {
public:
    explicit SimFuseBank(unsigned bits) : words_((bits + 63) / 64, 0), bits_(bits) {}

    bool skip_blows = false; // 模拟“熔丝跳过”：上报成功但实际不熔断
    bool flaky_read = false; // 模拟回读不稳定（隔次翻转最低位）

    unsigned capacity_bits() const override { return bits_; }

    std::uint64_t read_word(unsigned index) override {
        std::uint64_t w = words_.at(index);
        if (flaky_read && (++read_count_ % 2 == 0)) w ^= 1ull;
        return w;
    }

    bool blow_bit(unsigned bit_index) override {
        if (skip_blows) return true; // 静默不生效，必须被写后回读捕获
        words_.at(bit_index / 64) |= (1ull << (bit_index % 64));
        return true;
    }

private:
    std::vector<std::uint64_t> words_;
    unsigned bits_;
    unsigned read_count_ = 0;
};

class SimNvStorage final : public INvStorage {
public:
    explicit SimNvStorage(std::size_t size) : data_(size, 0xFF) {} // 模拟擦除态 Flash

    bool fail_io = false;

    bool read(std::size_t offset, std::uint8_t* out, std::size_t len) override {
        if (fail_io || offset + len > data_.size()) return false;
        std::memcpy(out, data_.data() + offset, len);
        return true;
    }

    bool write(std::size_t offset, const std::uint8_t* in, std::size_t len) override {
        if (fail_io || offset + len > data_.size()) return false;
        std::memcpy(data_.data() + offset, in, len);
        return true;
    }

    void corrupt(std::size_t offset) { data_.at(offset) ^= 0xFF; }

private:
    std::vector<std::uint8_t> data_;
};

class VectorAuditLog final : public IAuditLog {
public:
    std::vector<AuditEvent> events;
    void record(const AuditEvent& e) override { events.push_back(e); }
    bool contains(AuditEventType t) const {
        for (const auto& e : events)
            if (e.type == t) return true;
        return false;
    }
};

const std::vector<std::uint8_t> kKey = {0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b,
                                        0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b};

DowngradeGrant make_grant(std::uint64_t target, std::uint64_t current, std::uint64_t serial,
                          const std::vector<std::uint8_t>& key = kKey) {
    DowngradeGrant g;
    g.target_version = target;
    g.current_version = current;
    g.serial = serial;
    g.signature = HmacAuthorizer::sign(key, target, current, serial);
    return g;
}

// ---------------------------------------------------------------------------
// 场景用例
// ---------------------------------------------------------------------------

// 合法更新：镜像版本高于计数器，放行并推进熔丝。
void test_legitimate_update() {
    SimFuseBank bank(64);
    OtpFuseCounter counter(bank);
    HmacAuthorizer auth(kKey);
    VectorAuditLog audit;
    AntiRollback arb(counter, auth, audit);

    CHECK(counter.advance(5));

    const auto d = arb.authorize_install(7);
    CHECK(d.decision == Decision::Allowed);
    CHECK(arb.commit(7));
    const auto cur = arb.current_version();
    CHECK(cur && cur.value == 7);
    CHECK(audit.contains(AuditEventType::UpdateAuthorized));
    CHECK(audit.contains(AuditEventType::CounterAdvanced));

    // 同版本重装允许，且不重复熔断。
    CHECK(arb.authorize_install(7).decision == Decision::Allowed);
    CHECK(arb.commit(7));
}

// 旧镜像：无授权降级必须拒绝，且计数器不变。
void test_old_image_rejected() {
    SimFuseBank bank(64);
    OtpFuseCounter counter(bank);
    HmacAuthorizer auth(kKey);
    VectorAuditLog audit;
    AntiRollback arb(counter, auth, audit);
    CHECK(counter.advance(5));

    const auto d = arb.authorize_install(3);
    CHECK(d.decision == Decision::Rejected);
    CHECK(audit.contains(AuditEventType::DowngradeRejected));
    const auto cur = arb.current_version();
    CHECK(cur && cur.value == 5);
}

// 伪造/重放授权：错误密钥、错误绑定字段、序号重放都必须拒绝。
void test_forged_and_replayed_grant_rejected() {
    SimFuseBank bank(64);
    OtpFuseCounter counter(bank);
    HmacAuthorizer auth(kKey);
    VectorAuditLog audit;
    AntiRollback arb(counter, auth, audit);
    CHECK(counter.advance(5));

    // 错误密钥签名
    const std::vector<std::uint8_t> wrong_key(16, 0xAA);
    auto forged = make_grant(3, 5, 1, wrong_key);
    CHECK(arb.authorize_install(3, &forged).decision == Decision::Rejected);

    // 授权绑定的计数器值与实际不符
    auto stale = make_grant(3, 4, 1);
    CHECK(arb.authorize_install(3, &stale).decision == Decision::Rejected);

    // 授权目标版本与镜像不符
    auto mismatched = make_grant(2, 5, 1);
    CHECK(arb.authorize_install(3, &mismatched).decision == Decision::Rejected);

    // 合法授权可用一次；同一序号重放必须拒绝
    auto grant = make_grant(3, 5, 7);
    CHECK(arb.authorize_install(3, &grant).decision == Decision::DowngradeAllowed);
    CHECK(arb.authorize_install(3, &grant).decision == Decision::Rejected);
}

// 授权降级：签名有效则显式放行并记录；熔丝计数器不降低。
void test_authorized_downgrade() {
    SimFuseBank bank(64);
    OtpFuseCounter counter(bank);
    HmacAuthorizer auth(kKey);
    VectorAuditLog audit;
    AntiRollback arb(counter, auth, audit);
    CHECK(counter.advance(5));

    auto grant = make_grant(3, 5, 1);
    const auto d = arb.authorize_install(3, &grant);
    CHECK(d.decision == Decision::DowngradeAllowed);
    CHECK(audit.contains(AuditEventType::DowngradeAuthorized));

    CHECK(arb.commit(3));
    // 熔丝物理不可回退：计数器保持 5，降级只是已记录的显式例外。
    const auto cur = arb.current_version();
    CHECK(cur && cur.value == 5);

    // 降级后再次安装 v4（仍低于计数器）依然需要授权。
    CHECK(arb.authorize_install(4).decision == Decision::Rejected);
    // 而 v5 及以上恢复正常流程。
    CHECK(arb.authorize_install(6).decision == Decision::Allowed);
}

// 熔丝跳过：熔断未生效时 commit 必须显式失败并记录，绝不静默。
void test_fuse_skip_detected() {
    SimFuseBank bank(64);
    OtpFuseCounter counter(bank);
    HmacAuthorizer auth(kKey);
    VectorAuditLog audit;
    AntiRollback arb(counter, auth, audit);
    CHECK(counter.advance(5));

    CHECK(arb.authorize_install(9).decision == Decision::Allowed);
    bank.skip_blows = true; // 攻击/故障：熔断操作被跳过
    const auto s = arb.commit(9);
    CHECK(!s);
    CHECK(s.error == CounterError::ProgramFault);
    CHECK(audit.contains(AuditEventType::CounterFault));
    // 计数器未被静默推进。
    const auto cur = arb.current_version();
    CHECK(cur && cur.value == 5);
}

// 回读验证：熔丝读数不稳定、冗余副本被篡改、IO 故障都必须 fail-closed。
void test_readback_verification() {
    // OTP 双重回读不一致
    {
        SimFuseBank bank(64);
        OtpFuseCounter counter(bank);
        HmacAuthorizer auth(kKey);
        VectorAuditLog audit;
        AntiRollback arb(counter, auth, audit);
        bank.flaky_read = true;
        const auto d = arb.authorize_install(1);
        CHECK(d.decision == Decision::Rejected);
        CHECK(d.counter_error == CounterError::ReadFault);
        CHECK(audit.contains(AuditEventType::CounterFault));
    }

    // 受信存储：正常工作 -> 篡改冗余副本 -> 拒绝
    {
        SimNvStorage nv(64);
        TrustedStorageCounter counter(nv);
        CHECK(counter.format());
        HmacAuthorizer auth(kKey);
        VectorAuditLog audit;
        AntiRollback arb(counter, auth, audit);

        CHECK(counter.advance(5));
        CHECK(arb.authorize_install(6).decision == Decision::Allowed);
        CHECK(arb.commit(6));

        nv.corrupt(16); // 篡改冗余副本（第二个槽起始处）
        const auto d = arb.authorize_install(7);
        CHECK(d.decision == Decision::Rejected);
        CHECK(d.counter_error == CounterError::RedundancyMismatch);
        CHECK(audit.contains(AuditEventType::CounterFault));
    }

    // 受信存储：主副本 CRC 损坏同样被检出
    {
        SimNvStorage nv(64);
        TrustedStorageCounter counter(nv);
        CHECK(counter.format());
        CHECK(counter.advance(2));
        nv.corrupt(8); // 主副本 CRC 字段
        const auto r = counter.read();
        CHECK(!r);
        CHECK(r.error == CounterError::RedundancyMismatch);
    }

    // 后端 IO 故障
    {
        SimNvStorage nv(64);
        TrustedStorageCounter counter(nv);
        CHECK(counter.format());
        CHECK(counter.advance(2));
        nv.fail_io = true;
        const auto r = counter.read();
        CHECK(!r);
        CHECK(r.error == CounterError::ReadFault);
    }
}

// 计数器单调性：任何后端都不允许回退。
void test_counter_monotonic() {
    SimFuseBank bank(64);
    OtpFuseCounter otp(bank);
    CHECK(otp.advance(5));
    CHECK(otp.advance(2).error == CounterError::RegressNotAllowed);

    SimNvStorage nv(64);
    TrustedStorageCounter ts(nv);
    CHECK(ts.format());
    CHECK(ts.advance(5));
    CHECK(ts.advance(2).error == CounterError::RegressNotAllowed);
}

// SHA-256/HMAC 标准测试向量，保证自包含实现正确。
void test_sha256_vectors() {
    const auto h = sha256(reinterpret_cast<const std::uint8_t*>("abc"), 3);
    const std::uint8_t expect[32] = {
        0xba, 0x78, 0x16, 0xbf, 0x8f, 0x01, 0xcf, 0xea, 0x41, 0x41, 0x40, 0xde, 0x5d, 0xae, 0x22,
        0x23, 0xb0, 0x03, 0x61, 0xa3, 0x96, 0x17, 0x7a, 0x9c, 0xb4, 0x10, 0xff, 0x61, 0xf2, 0x00,
        0x15, 0xad};
    CHECK(std::memcmp(h.data(), expect, 32) == 0);

    // RFC 4231 Test Case 1
    const std::uint8_t key[20] = {0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b,
                                  0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b};
    const char* msg = "Hi There";
    const auto mac = hmac_sha256(key, 20, reinterpret_cast<const std::uint8_t*>(msg), 8);
    const std::uint8_t expect_mac[32] = {
        0xb0, 0x34, 0x4c, 0x61, 0xd8, 0xdb, 0x38, 0x53, 0x5c, 0xa8, 0xaf, 0xce, 0xaf, 0x0b, 0xf1,
        0x2b, 0x88, 0x1d, 0xc2, 0x00, 0xc9, 0x83, 0x3d, 0xa7, 0x26, 0xe9, 0x37, 0x6c, 0x2e, 0x32,
        0xcf, 0xf7};
    CHECK(std::memcmp(mac.data(), expect_mac, 32) == 0);
}

} // namespace

int main() {
    test_sha256_vectors();
    test_legitimate_update();
    test_old_image_rejected();
    test_forged_and_replayed_grant_rejected();
    test_authorized_downgrade();
    test_fuse_skip_detected();
    test_readback_verification();
    test_counter_monotonic();

    if (g_failures == 0) {
        std::printf("all anti-rollback tests passed\n");
        return 0;
    }
    std::fprintf(stderr, "%d check(s) failed\n", g_failures);
    return 1;
}
