#include "netsec.hpp"

namespace netsec {

namespace {

uint32_t pgnOf(uint32_t canId) { return (canId >> 8) & 0x3FFFF; }

uint8_t sourceOf(uint32_t canId) { return static_cast<uint8_t>(canId & 0xFF); }

uint16_t readLe16(const uint8_t* p) {
    return static_cast<uint16_t>(p[0] | (static_cast<uint16_t>(p[1]) << 8));
}

uint32_t readLe24(const uint8_t* p) {
    return static_cast<uint32_t>(p[0]) | (static_cast<uint32_t>(p[1]) << 8) |
           (static_cast<uint32_t>(p[2]) << 16);
}

uint64_t readLe64(const uint8_t* p) {
    uint64_t value = 0;
    for (int i = 7; i >= 0; --i) {
        value = (value << 8) | p[i];
    }
    return value;
}

} // namespace

void TpReassembler::setMessageHandler(MessageHandler handler) { handler_ = std::move(handler); }

void TpReassembler::onCanFrame(uint32_t canId, const uint8_t* data, size_t length,
                               uint64_t nowMs) {
    const uint32_t pgn = pgnOf(canId);
    const uint8_t source = sourceOf(canId);

    if (pgn == kPgnAddressClaim) {
        handleAddressClaim(source, data, length);
        return;
    }

    if (pgn != kPgnTpCm && pgn != kPgnTpDt) {
        return;
    }

    // 源地址 254/255 为保留地址，其 TP 帧一律忽略。
    if (source == 254 || source == 255) {
        return;
    }

    // 包间隔超时：处理当前帧前先清算过期会话。
    auto it = sessions_.find(source);
    if (it != sessions_.end() && nowMs - it->second.lastPacketMs > kInterPacketTimeoutMs) {
        sessions_.erase(it);
    }

    if (pgn == kPgnTpCm) {
        handleTpCm(source, data, length, nowMs);
    } else {
        handleTpDt(source, data, length, nowMs);
    }
}

void TpReassembler::handleTpCm(uint8_t source, const uint8_t* data, size_t length,
                               uint64_t nowMs) {
    if (length < 8 || data[0] != kControlBam) {
        return; // 仅支持 BAM，其余控制字（如 RTS/CTS）不在本模块范围。
    }

    const uint16_t totalSize = readLe16(data + 1);
    const uint8_t totalPackets = data[3];
    const uint32_t pgn = readLe24(data + 5);

    // 总字节数合法范围与包数一致性校验，不符拒绝。
    const uint16_t expectedPackets = static_cast<uint16_t>(
        (totalSize + kDataBytesPerPacket - 1) / kDataBytesPerPacket);
    if (totalSize < kMinMessageSize || totalSize > kMaxMessageSize ||
        totalPackets != expectedPackets) {
        return;
    }

    // 同一源地址同时只允许 1 个 BAM 会话：新 BAM 到来即放弃旧会话。
    Session session;
    session.totalSize = totalSize;
    session.totalPackets = totalPackets;
    session.pgn = pgn;
    session.lastPacketMs = nowMs;
    session.buffer.reserve(totalSize);
    sessions_[source] = std::move(session);
}

void TpReassembler::handleTpDt(uint8_t source, const uint8_t* data, size_t length,
                               uint64_t nowMs) {
    if (length < 8) {
        return;
    }

    auto it = sessions_.find(source);
    if (it == sessions_.end()) {
        return;
    }
    Session& session = it->second;

    const uint8_t sequence = data[0];
    if (sequence != session.nextSequence) {
        // 乱序或重复序号：中止该会话。
        sessions_.erase(it);
        return;
    }

    session.buffer.insert(session.buffer.end(), data + 1, data + 8);
    session.lastPacketMs = nowMs;
    ++session.nextSequence;

    if (sequence == session.totalPackets) {
        session.buffer.resize(session.totalSize); // 去掉末包填充字节
        if (handler_) {
            handler_(source, session.pgn, session.buffer);
        }
        sessions_.erase(it);
    }
}

void TpReassembler::handleAddressClaim(uint8_t source, const uint8_t* data, size_t length) {
    if (length < 8) {
        return;
    }
    const uint64_t name = readLe64(data);

    auto it = claimedNames_.find(source);
    if (it == claimedNames_.end()) {
        claimedNames_[source] = name;
        return;
    }
    if (it->second == name) {
        return;
    }

    // 地址冲突：NAME 较小者保留地址，较大者放弃；该地址上的重组会话随之清除。
    sessions_.erase(source);
    if (name < it->second) {
        it->second = name;
    }
}

void TpReassembler::checkTimeouts(uint64_t nowMs) {
    for (auto it = sessions_.begin(); it != sessions_.end();) {
        if (nowMs - it->second.lastPacketMs > kInterPacketTimeoutMs) {
            it = sessions_.erase(it);
        } else {
            ++it;
        }
    }
}

size_t TpReassembler::activeSessionCount() const { return sessions_.size(); }

} // namespace netsec
