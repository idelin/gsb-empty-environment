#include "netsec.hpp"

#include <utility>

namespace netsec {

FragmentReassembler::FragmentReassembler(size_t maxCachedFragments)
    : maxCachedFragments_(maxCachedFragments == 0 ? kDefaultCacheLimit
                                                  : maxCachedFragments) {}

ReassemblyResult FragmentReassembler::reject(RejectReason reason,
                                             uint16_t sequenceNumber) {
    ReassemblyResult result;
    result.rejected = true;
    result.reason = reason;
    result.sequenceNumber = sequenceNumber;
    return result;
}

void FragmentReassembler::dropGroup(std::map<uint16_t, Group>::iterator it) {
    cachedFragments_ -= it->second.expectedFragment;
    groups_.erase(it);
    ++stats_.groupsDropped;
}

void FragmentReassembler::evictOldestGroup() {
    auto oldest = groups_.end();
    for (auto it = groups_.begin(); it != groups_.end(); ++it) {
        if (oldest == groups_.end() || it->second.bornAt < oldest->second.bornAt) {
            oldest = it;
        }
    }
    if (oldest != groups_.end()) {
        cachedFragments_ -= oldest->second.expectedFragment;
        groups_.erase(oldest);
        ++stats_.groupsEvicted;
    }
}

ReassemblyResult FragmentReassembler::process(const FragmentInput& input) {
    if (input.fragmentNumber > kMaxFragmentNumber) {
        return reject(RejectReason::InvalidFragment, input.sequenceNumber);
    }

    const bool isFirst = (input.fragmentNumber == 0);

    // 单片帧（非分片）：直通，不占用缓存，也不参与 PN/密钥分组策略。
    if (isFirst && !input.moreFragments) {
        ReassemblyResult result;
        result.completed = true;
        result.sequenceNumber = input.sequenceNumber;
        result.frame = input.payload;
        ++stats_.framesCompleted;
        return result;
    }

    auto existing = groups_.find(input.sequenceNumber);

    // 首片先到才能建组；没有首片的后续分片一律拒绝，防止攻击者凭空插入。
    if (!isFirst && existing == groups_.end()) {
        return reject(RejectReason::OrphanFragment, input.sequenceNumber);
    }

    // 该序号已有缓存却又来一个首片，视为重传/重建：旧组整体丢弃后重新建组。
    if (isFirst && existing != groups_.end()) {
        dropGroup(existing);
        existing = groups_.end();
    }

    if (isFirst) {
        // 入缓存前先保证不超过上限（分片以组为单位，淘汰最旧的整组）。
        if (cachedFragments_ >= maxCachedFragments_) {
            evictOldestGroup();
        }

        Group group;
        group.sequenceNumber = input.sequenceNumber;
        group.expectedFragment = 1;
        group.protectedFrame = input.protectedFrame;
        group.keyId = input.keyId;
        group.lastPacketNumber = input.packetNumber;
        group.bornAt = ++insertionCounter_;
        group.reassembled = input.payload;

        groups_.emplace(input.sequenceNumber, std::move(group));
        ++cachedFragments_;
        ++stats_.fragmentsAccepted;

        // 首片即末片的情况在上面已作为单片帧直通，这里一定还有后续分片。
        ReassemblyResult pending;
        pending.sequenceNumber = input.sequenceNumber;
        return pending;
    }

    Group& group = existing->second;

    // 1) 明文/加密必须一致。
    if (input.protectedFrame != group.protectedFrame) {
        dropGroup(existing);
        return reject(RejectReason::ProtectionMismatch, input.sequenceNumber);
    }

    // 2) 加密分片必须同密钥；3) PN 必须逐片恰好 +1。
    //    纯明文组无密钥/PN 概念，跳过这两项检查。
    if (group.protectedFrame) {
        if (input.keyId != group.keyId) {
            dropGroup(existing);
            return reject(RejectReason::KeyMismatch, input.sequenceNumber);
        }
        if (input.packetNumber != group.lastPacketNumber + 1) {
            dropGroup(existing);
            return reject(RejectReason::PacketNumberGap, input.sequenceNumber);
        }
    }

    // 4) 分片号必须连续。
    if (input.fragmentNumber != group.expectedFragment) {
        dropGroup(existing);
        return reject(RejectReason::FragmentGap, input.sequenceNumber);
    }

    // 6) 缓存上限：入缓存前保证不超过 maxCachedFragments_，淘汰最旧的整组。
    if (cachedFragments_ >= maxCachedFragments_) {
        evictOldestGroup();
    }

    group.reassembled.insert(group.reassembled.end(),
                             input.payload.begin(), input.payload.end());
    ++group.expectedFragment;
    ++cachedFragments_;
    ++stats_.fragmentsAccepted;
    group.lastPacketNumber = input.packetNumber;

    // 末片（More Fragments 清零）：交出完整帧并回收缓存。
    if (!input.moreFragments) {
        ReassemblyResult result;
        result.completed = true;
        result.sequenceNumber = input.sequenceNumber;
        result.frame = std::move(group.reassembled);
        cachedFragments_ -= group.expectedFragment;
        groups_.erase(existing);
        ++stats_.framesCompleted;
        return result;
    }

    ReassemblyResult pending;
    pending.sequenceNumber = input.sequenceNumber;
    return pending;
}

void FragmentReassembler::clear() {
    if (!groups_.empty()) {
        stats_.groupsCleared += groups_.size();
    }
    groups_.clear();
    cachedFragments_ = 0;
}

} // namespace netsec
