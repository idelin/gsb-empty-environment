#pragma once

#include <cstddef>
#include <cstdint>
#include <functional>
#include <map>
#include <vector>

namespace netsec {

// J1939 传输协议（TP）BAM 重组器，供 CAN 接收回调直接调用。
// 线程安全由调用方保证（单线程 CAN 回调场景）。
class TpReassembler {
public:
    // 完整消息回调：源地址、PGN、按总字节数截断后的负载。
    using MessageHandler =
        std::function<void(uint8_t source, uint32_t pgn, const std::vector<uint8_t>& payload)>;

    static constexpr uint32_t kPgnTpCm = 0x00EC00;         // 60416
    static constexpr uint32_t kPgnTpDt = 0x00EB00;         // 60160
    static constexpr uint32_t kPgnAddressClaim = 0x00EE00; // 60928
    static constexpr uint8_t kControlBam = 32;
    static constexpr uint64_t kInterPacketTimeoutMs = 750;
    static constexpr uint16_t kMinMessageSize = 9;
    static constexpr uint16_t kMaxMessageSize = 1785;
    static constexpr size_t kDataBytesPerPacket = 7;

    void setMessageHandler(MessageHandler handler);

    // CAN 接收回调入口。canId 为 29 位扩展帧 ID，nowMs 为调用方时钟（毫秒）。
    void onCanFrame(uint32_t canId, const uint8_t* data, size_t length, uint64_t nowMs);

    // 主动超时扫描（可选，用于无流量时清理会话）。
    void checkTimeouts(uint64_t nowMs);

    size_t activeSessionCount() const;

private:
    struct Session {
        uint16_t totalSize = 0;
        uint8_t totalPackets = 0;
        uint32_t pgn = 0;
        uint8_t nextSequence = 1;
        uint64_t lastPacketMs = 0;
        std::vector<uint8_t> buffer;
    };

    void handleTpCm(uint8_t source, const uint8_t* data, size_t length, uint64_t nowMs);
    void handleTpDt(uint8_t source, const uint8_t* data, size_t length, uint64_t nowMs);
    void handleAddressClaim(uint8_t source, const uint8_t* data, size_t length);

    std::map<uint8_t, Session> sessions_;
    std::map<uint8_t, uint64_t> claimedNames_; // 地址 -> 当前获胜 NAME
    MessageHandler handler_;
};

} // namespace netsec
