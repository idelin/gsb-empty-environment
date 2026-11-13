#include "netsec.hpp"

#include <cstdint>
#include <cstdio>
#include <vector>

using namespace netsec;

namespace {

int g_failures = 0;
int g_checks = 0;

void check(bool condition, const char* expr, const char* test, int line) {
    ++g_checks;
    if (!condition) {
        ++g_failures;
        std::printf("  [FAIL] %s (line %d): %s\n", test, line, expr);
    }
}

#define CHECK(cond) check((cond), #cond, __func__, __LINE__)

FragmentInput makeFragment(uint16_t seq, uint8_t frag, bool more,
                           bool protectedFrame, uint8_t keyId, uint64_t pn,
                           std::vector<uint8_t> payload) {
    FragmentInput input;
    input.sequenceNumber = seq;
    input.fragmentNumber = frag;
    input.moreFragments = more;
    input.protectedFrame = protectedFrame;
    input.keyId = keyId;
    input.packetNumber = pn;
    input.payload = std::move(payload);
    return input;
}

std::vector<uint8_t> bytes(std::initializer_list<uint8_t> values) {
    return std::vector<uint8_t>(values);
}

// 正常重组：3 个加密分片，同一密钥，PN 100/101/102 逐片 +1。
void testNormalReassembly() {
    FragmentReassembler reassembler;

    auto r0 = reassembler.process(
        makeFragment(40, 0, true, true, 2, 100, bytes({0x01, 0x02})));
    CHECK(!r0.completed && !r0.rejected);
    CHECK(reassembler.cachedFragments() == 1);

    auto r1 = reassembler.process(
        makeFragment(40, 1, true, true, 2, 101, bytes({0x03})));
    CHECK(!r1.completed && !r1.rejected);

    auto r2 = reassembler.process(
        makeFragment(40, 2, false, true, 2, 102, bytes({0x04, 0x05})));
    CHECK(r2.completed && !r2.rejected);
    CHECK((r2.frame == bytes({0x01, 0x02, 0x03, 0x04, 0x05})));
    CHECK(reassembler.cachedFragments() == 0);
    CHECK(reassembler.cachedGroups() == 0);
    CHECK(reassembler.stats().framesCompleted == 1);
}

// 单片（非分片）帧直接透传，不占缓存。
void testSingleFragmentPassthrough() {
    FragmentReassembler reassembler;
    auto result = reassembler.process(
        makeFragment(7, 0, false, true, 0, 5, bytes({0xAA, 0xBB})));
    CHECK(result.completed && !result.rejected);
    CHECK((result.frame == bytes({0xAA, 0xBB})));
    CHECK(reassembler.cachedFragments() == 0);
}

// PN 跳变：期望 101 却收到 105，整组丢弃。
void testPacketNumberJump() {
    FragmentReassembler reassembler;
    reassembler.process(
        makeFragment(1, 0, true, true, 1, 100, bytes({0x10})));

    auto bad = reassembler.process(
        makeFragment(1, 1, true, true, 1, 105, bytes({0x11})));
    CHECK(bad.rejected);
    CHECK(bad.reason == RejectReason::PacketNumberGap);
    CHECK(reassembler.cachedGroups() == 0);
    CHECK(reassembler.cachedFragments() == 0);
    CHECK(reassembler.stats().groupsDropped == 1);

    // 旧组已清空，序号 1 的后续分片现在是孤儿，不能借尸还魂。
    auto orphan = reassembler.process(
        makeFragment(1, 1, false, true, 1, 106, bytes({0x12})));
    CHECK(orphan.rejected);
    CHECK(orphan.reason == RejectReason::OrphanFragment);
}

// PN 相等（未递增）同样拒绝。
void testPacketNumberEqual() {
    FragmentReassembler reassembler;
    reassembler.process(
        makeFragment(2, 0, true, true, 0, 9, bytes({0x20})));
    auto bad = reassembler.process(
        makeFragment(2, 1, false, true, 0, 9, bytes({0x21})));
    CHECK(bad.rejected);
    CHECK(bad.reason == RejectReason::PacketNumberGap);
    CHECK(reassembler.cachedGroups() == 0);
}

// 混合加密：加密首片后跟一个明文分片，整组丢弃。
void testMixedProtectedAndPlaintext() {
    FragmentReassembler reassembler;
    reassembler.process(
        makeFragment(3, 0, true, true, 0, 50, bytes({0x30})));

    auto plain = reassembler.process(
        makeFragment(3, 1, false, false, 0, 0, bytes({0x31})));
    CHECK(plain.rejected);
    CHECK(plain.reason == RejectReason::ProtectionMismatch);
    CHECK(reassembler.cachedGroups() == 0);
}

// 反向混合：明文首片后跟加密分片，同样整组丢弃。
void testPlaintextThenProtected() {
    FragmentReassembler reassembler;
    reassembler.process(
        makeFragment(4, 0, true, false, 0, 0, bytes({0x40})));

    auto encrypted = reassembler.process(
        makeFragment(4, 1, false, true, 0, 1, bytes({0x41})));
    CHECK(encrypted.rejected);
    CHECK(encrypted.reason == RejectReason::ProtectionMismatch);
}

// 换密钥后续片：同序号后续分片使用不同 keyId，整组丢弃。
void testKeyChangeMidFrame() {
    FragmentReassembler reassembler;
    reassembler.process(
        makeFragment(5, 0, true, true, 1, 70, bytes({0x50})));

    auto rekeyed = reassembler.process(
        makeFragment(5, 1, false, true, 2, 71, bytes({0x51})));
    CHECK(rekeyed.rejected);
    CHECK(rekeyed.reason == RejectReason::KeyMismatch);
    CHECK(reassembler.cachedGroups() == 0);
}

// 密钥重装：clear() 清空所有缓存分片，旧序号的续片成为孤儿。
void testClearOnKeyReinstall() {
    FragmentReassembler reassembler;
    reassembler.process(
        makeFragment(8, 0, true, true, 0, 1, bytes({0x80})));
    reassembler.process(
        makeFragment(9, 0, true, true, 0, 2, bytes({0x90})));
    CHECK(reassembler.cachedGroups() == 2);

    reassembler.clear();
    CHECK(reassembler.cachedGroups() == 0);
    CHECK(reassembler.cachedFragments() == 0);
    CHECK(reassembler.stats().groupsCleared == 2);

    auto orphan = reassembler.process(
        makeFragment(8, 1, false, true, 0, 2, bytes({0x81})));
    CHECK(orphan.rejected);
    CHECK(orphan.reason == RejectReason::OrphanFragment);
}

// 没有首片的续片直接拒绝。
void testOrphanFragment() {
    FragmentReassembler reassembler;
    auto orphan = reassembler.process(
        makeFragment(10, 2, false, true, 0, 3, bytes({0xA0})));
    CHECK(orphan.rejected);
    CHECK(orphan.reason == RejectReason::OrphanFragment);
    CHECK(reassembler.cachedFragments() == 0);
}

// 缓存上限 16：第 17 个分片触发淘汰最旧的整组。
void testCacheEviction() {
    FragmentReassembler reassembler(16);

    // 序号 0 放 2 个分片（最旧组），序号 1..14 各 1 个分片 => 共 16 个。
    reassembler.process(
        makeFragment(0, 0, true, true, 0, 0, bytes({0x00})));
    reassembler.process(
        makeFragment(0, 1, true, true, 0, 1, bytes({0x01})));
    for (uint16_t seq = 1; seq <= 14; ++seq) {
        reassembler.process(
            makeFragment(seq, 0, true, true, 0, seq, bytes({uint8_t(seq)})));
    }
    CHECK(reassembler.cachedFragments() == 16);
    CHECK(reassembler.cachedGroups() == 15);

    // 第 17 个分片：先淘汰最旧组（序号 0，含 2 个分片），再放入新分片。
    reassembler.process(
        makeFragment(15, 0, true, true, 0, 100, bytes({0x0F})));
    CHECK(reassembler.cachedFragments() == 15);
    CHECK(reassembler.cachedGroups() == 15);
    CHECK(reassembler.stats().groupsEvicted == 1);

    // 被淘汰组的续片无法重组：它已经不存在，续片变成孤儿。
    auto orphan = reassembler.process(
        makeFragment(0, 2, false, true, 0, 2, bytes({0x02})));
    CHECK(orphan.rejected);
    CHECK(orphan.reason == RejectReason::OrphanFragment);

    // 新组仍可正常重组。
    auto done = reassembler.process(
        makeFragment(15, 1, false, true, 0, 101, bytes({0x10})));
    CHECK(done.completed);
    CHECK((done.frame == bytes({0x0F, 0x10})));
}

// 分片号不连续也必须整组丢弃。
void testFragmentGap() {
    FragmentReassembler reassembler;
    reassembler.process(
        makeFragment(11, 0, true, true, 0, 0, bytes({0xB0})));
    auto gap = reassembler.process(
        makeFragment(11, 2, false, true, 0, 1, bytes({0xB2})));
    CHECK(gap.rejected);
    CHECK(gap.reason == RejectReason::FragmentGap);
    CHECK(reassembler.cachedGroups() == 0);
}

// 全明文多片允许重组（不做 PN/密钥约束），但仍要求分片连续。
void testPlaintextGroupReassembles() {
    FragmentReassembler reassembler;
    reassembler.process(
        makeFragment(12, 0, true, false, 0, 0, bytes({0xC0})));
    auto done = reassembler.process(
        makeFragment(12, 1, false, false, 0, 0, bytes({0xC1})));
    CHECK(done.completed);
    CHECK((done.frame == bytes({0xC0, 0xC1})));
}

// 首片重传：丢弃旧组后用新首片重建，随后正常重组。
void testFirstFragmentRetransmission() {
    FragmentReassembler reassembler;
    reassembler.process(
        makeFragment(13, 0, true, true, 0, 20, bytes({0xD0})));

    // 重传首片，PN 可能重置，旧组应被丢弃并重建。
    auto reset = reassembler.process(
        makeFragment(13, 0, true, true, 0, 40, bytes({0xE0})));
    CHECK(!reset.completed && !reset.rejected);
    CHECK(reassembler.cachedGroups() == 1);

    auto done = reassembler.process(
        makeFragment(13, 1, false, true, 0, 41, bytes({0xE1})));
    CHECK(done.completed);
    CHECK((done.frame == bytes({0xE0, 0xE1})));
}

#define RUN(test)                       \
    do {                                \
        std::printf("- %s\n", #test);   \
        test();                         \
    } while (0)

} // namespace

int main() {
    RUN(testNormalReassembly);
    RUN(testSingleFragmentPassthrough);
    RUN(testPacketNumberJump);
    RUN(testPacketNumberEqual);
    RUN(testMixedProtectedAndPlaintext);
    RUN(testPlaintextThenProtected);
    RUN(testKeyChangeMidFrame);
    RUN(testClearOnKeyReinstall);
    RUN(testOrphanFragment);
    RUN(testCacheEviction);
    RUN(testFragmentGap);
    RUN(testPlaintextGroupReassembles);
    RUN(testFirstFragmentRetransmission);

    std::printf("\n%d checks, %d failures\n", g_checks, g_failures);
    return g_failures == 0 ? 0 : 1;
}
