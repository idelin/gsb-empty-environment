#include "netsec.hpp"

#include <cstdint>
#include <cstdio>
#include <vector>

namespace {

int g_failures = 0;

#define CHECK(cond)                                                                                \
    do {                                                                                           \
        if (!(cond)) {                                                                             \
            std::printf("FAIL %s:%d: %s\n", __FILE__, __LINE__, #cond);                            \
            ++g_failures;                                                                          \
        }                                                                                          \
    } while (0)

uint32_t makeId(uint32_t pgn, uint8_t sa) { return (pgn << 8) | sa; }

struct FrameSink {
    std::vector<std::vector<uint8_t>> messages;
    std::vector<uint32_t> pgns;
    std::vector<uint8_t> sources;

    netsec::TpReassembler::MessageHandler handler() {
        return [this](uint8_t src, uint32_t pgn, const std::vector<uint8_t>& payload) {
            sources.push_back(src);
            pgns.push_back(pgn);
            messages.push_back(payload);
        };
    }
};

void sendBam(netsec::TpReassembler& tp, uint8_t sa, uint16_t totalSize, uint8_t totalPackets,
             uint32_t pgn, uint64_t nowMs) {
    uint8_t d[8] = {32,
                    static_cast<uint8_t>(totalSize & 0xFF),
                    static_cast<uint8_t>(totalSize >> 8),
                    totalPackets,
                    0xFF,
                    static_cast<uint8_t>(pgn & 0xFF),
                    static_cast<uint8_t>((pgn >> 8) & 0xFF),
                    static_cast<uint8_t>((pgn >> 16) & 0xFF)};
    tp.onCanFrame(makeId(netsec::TpReassembler::kPgnTpCm, sa), d, 8, nowMs);
}

void sendDt(netsec::TpReassembler& tp, uint8_t sa, uint8_t seq, uint8_t fill, uint64_t nowMs) {
    uint8_t d[8];
    d[0] = seq;
    for (int i = 1; i < 8; ++i) {
        d[i] = static_cast<uint8_t>(fill + i);
    }
    tp.onCanFrame(makeId(netsec::TpReassembler::kPgnTpDt, sa), d, 8, nowMs);
}

void sendAddressClaim(netsec::TpReassembler& tp, uint8_t sa, uint64_t name, uint64_t nowMs) {
    uint8_t d[8];
    for (int i = 0; i < 8; ++i) {
        d[i] = static_cast<uint8_t>((name >> (8 * i)) & 0xFF);
    }
    tp.onCanFrame(makeId(netsec::TpReassembler::kPgnAddressClaim, sa), d, 8, nowMs);
}

// 正常流程：完整重组一条 9 字节消息（2 包）。
void testHappyPath() {
    netsec::TpReassembler tp;
    FrameSink sink;
    tp.setMessageHandler(sink.handler());

    sendBam(tp, 0x33, 9, 2, 0x00F004, 0);
    sendDt(tp, 0x33, 1, 0x10, 100);
    sendDt(tp, 0x33, 2, 0x20, 200);

    CHECK(sink.messages.size() == 1);
    CHECK(sink.pgns.at(0) == 0x00F004);
    CHECK(sink.sources.at(0) == 0x33);
    CHECK(sink.messages.at(0).size() == 9); // 末包 5 个填充字节被截掉
    CHECK(sink.messages.at(0)[0] == 0x11);
    CHECK(sink.messages.at(0)[8] == 0x22);
    CHECK(tp.activeSessionCount() == 0);
}

// 总包数与总字节数不符：拒绝建立会话。
void testPacketCountMismatch() {
    netsec::TpReassembler tp;
    FrameSink sink;
    tp.setMessageHandler(sink.handler());

    sendBam(tp, 0x33, 9, 3, 0x00F004, 0); // ceil(9/7)=2，声明 3 包
    CHECK(tp.activeSessionCount() == 0);
    sendDt(tp, 0x33, 1, 0x10, 10);
    sendDt(tp, 0x33, 2, 0x20, 20);
    CHECK(sink.messages.empty());

    sendBam(tp, 0x33, 8, 2, 0x00F004, 0);    // 总字节数 < 9
    CHECK(tp.activeSessionCount() == 0);
    sendBam(tp, 0x33, 1786, 255, 0x00F004, 0); // 总字节数 > 1785
    CHECK(tp.activeSessionCount() == 0);
    sendBam(tp, 0x33, 1785, 255, 0x00F004, 0); // 边界合法值
    CHECK(tp.activeSessionCount() == 1);
}

// 乱序与重复序号：中止会话。
void testOutOfOrderAndDuplicate() {
    netsec::TpReassembler tp;
    FrameSink sink;
    tp.setMessageHandler(sink.handler());

    sendBam(tp, 0x33, 16, 3, 0x00F004, 0);
    sendDt(tp, 0x33, 1, 0x10, 10);
    sendDt(tp, 0x33, 3, 0x30, 20); // 乱序：跳过 2
    CHECK(tp.activeSessionCount() == 0);
    sendDt(tp, 0x33, 2, 0x20, 30); // 会话已中止，后续 DT 被忽略
    sendDt(tp, 0x33, 3, 0x30, 40);
    CHECK(sink.messages.empty());

    sendBam(tp, 0x33, 16, 3, 0x00F004, 100);
    sendDt(tp, 0x33, 1, 0x10, 110);
    sendDt(tp, 0x33, 1, 0x10, 120); // 重复序号
    CHECK(tp.activeSessionCount() == 0);
    CHECK(sink.messages.empty());
}

// 同一源地址两个 BAM：新 BAM 放弃旧会话。
void testSecondBamReplacesSession() {
    netsec::TpReassembler tp;
    FrameSink sink;
    tp.setMessageHandler(sink.handler());

    sendBam(tp, 0x33, 16, 3, 0x00F004, 0);
    sendDt(tp, 0x33, 1, 0x10, 10);
    sendBam(tp, 0x33, 9, 2, 0x00FEEE, 20); // 新 BAM，旧会话作废
    CHECK(tp.activeSessionCount() == 1);
    sendDt(tp, 0x33, 2, 0x20, 30); // 旧会话的第 2 包：新会话期望序号 1，被中止
    CHECK(tp.activeSessionCount() == 0);
    CHECK(sink.messages.empty());

    sendBam(tp, 0x33, 9, 2, 0x00FEEE, 40);
    sendDt(tp, 0x33, 1, 0x50, 50);
    sendDt(tp, 0x33, 2, 0x60, 60);
    CHECK(sink.messages.size() == 1);
    CHECK(sink.pgns.at(0) == 0x00FEEE);
}

// 包间隔超过 750 ms：会话中止。
void testInterPacketTimeout() {
    netsec::TpReassembler tp;
    FrameSink sink;
    tp.setMessageHandler(sink.handler());

    sendBam(tp, 0x33, 16, 3, 0x00F004, 1000);
    sendDt(tp, 0x33, 1, 0x10, 1750); // 间隔恰好 750 ms，未超时
    CHECK(tp.activeSessionCount() == 1);
    sendDt(tp, 0x33, 2, 0x20, 2501); // 间隔 751 ms，超时
    CHECK(tp.activeSessionCount() == 0);
    CHECK(sink.messages.empty());

    // 无流量时由 checkTimeouts 清理。
    sendBam(tp, 0x33, 16, 3, 0x00F004, 3000);
    tp.checkTimeouts(3750);
    CHECK(tp.activeSessionCount() == 1);
    tp.checkTimeouts(3751);
    CHECK(tp.activeSessionCount() == 0);
}

// 地址声明冲突：NAME 较小者保留地址，较大者的会话被清除。
void testAddressClaimConflict() {
    netsec::TpReassembler tp;
    FrameSink sink;
    tp.setMessageHandler(sink.handler());

    const uint64_t smallName = 0x1000000000000001ULL;
    const uint64_t largeName = 0x2000000000000002ULL;

    // 后来者 NAME 较大：后来者放弃地址，但冲突导致该地址上的会话被清除。
    sendAddressClaim(tp, 0x44, smallName, 0);
    sendBam(tp, 0x44, 16, 3, 0x00F004, 10);
    sendDt(tp, 0x44, 1, 0x10, 20);
    sendAddressClaim(tp, 0x44, largeName, 30);
    CHECK(tp.activeSessionCount() == 0);
    sendDt(tp, 0x44, 2, 0x20, 40);
    sendDt(tp, 0x44, 3, 0x30, 50);
    CHECK(sink.messages.empty());

    // 后来者 NAME 更小：原持有者放弃地址，其会话同样被清除；地址归新 NAME。
    sendAddressClaim(tp, 0x55, largeName, 100);
    sendBam(tp, 0x55, 16, 3, 0x00F004, 110);
    sendDt(tp, 0x55, 1, 0x10, 120);
    sendAddressClaim(tp, 0x55, smallName, 130);
    CHECK(tp.activeSessionCount() == 0);
    CHECK(sink.messages.empty());

    // 冲突平息后，获胜方可以正常重建会话。
    sendBam(tp, 0x55, 9, 2, 0x00F004, 200);
    sendDt(tp, 0x55, 1, 0x10, 210);
    sendDt(tp, 0x55, 2, 0x20, 220);
    CHECK(sink.messages.size() == 1);
}

// 源地址 254/255 的 TP 帧忽略。
void testReservedSourceAddresses() {
    netsec::TpReassembler tp;
    FrameSink sink;
    tp.setMessageHandler(sink.handler());

    sendBam(tp, 254, 9, 2, 0x00F004, 0);
    sendBam(tp, 255, 9, 2, 0x00F004, 0);
    sendDt(tp, 254, 1, 0x10, 10);
    sendDt(tp, 255, 1, 0x10, 10);
    CHECK(tp.activeSessionCount() == 0);
    CHECK(sink.messages.empty());
}

} // namespace

int main() {
    testHappyPath();
    testPacketCountMismatch();
    testOutOfOrderAndDuplicate();
    testSecondBamReplacesSession();
    testInterPacketTimeout();
    testAddressClaimConflict();
    testReservedSourceAddresses();

    if (g_failures == 0) {
        std::printf("all tests passed\n");
        return 0;
    }
    std::printf("%d check(s) failed\n", g_failures);
    return 1;
}
