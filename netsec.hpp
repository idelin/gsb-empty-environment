#pragma once

#include <cstdint>
#include <map>
#include <vector>

namespace netsec {

// 一个 802.11 分片在经过 CCMP/GCMP 解密与头部解析后交给重组器的输入。
// 调用方负责真正的密码学校验（MIC、重放检查），重组器只负责把同一帧的
// 多个分片安全地拼回去，并执行密钥/PN/明文一致性策略。
struct FragmentInput {
    uint16_t sequenceNumber = 0;   // 802.11 序列控制字段中的 12 bit 序号
    uint8_t  fragmentNumber = 0;   // 分片号 0..15
    bool     moreFragments = false;// 分片位（More Fragments）
    bool     protectedFrame = false;// 数据帧受保护位（Protected）
    uint8_t  keyId = 0;            // 密钥索引 0..3，仅对加密分片有效
    uint64_t packetNumber = 0;     // 该分片的 PN（CCMP 48bit / WEP 24bit 都可装下）
    std::vector<uint8_t> payload;  // 已解密（或明文）的 MSDU 分片负载
};

// 分片被拒绝的原因，便于驱动统计与日志。
enum class RejectReason {
    None,
    InvalidFragment,     // 分片号 > 15 等非法输入
    OrphanFragment,      // 首片(分片号 0)尚未到达就收到后续分片
    ProtectionMismatch,  // 同一序号下明文分片与加密分片混合
    KeyMismatch,         // 同一序号的加密分片使用了不同密钥
    PacketNumberGap,     // PN 没有逐片 +1（跳变/回退/相等）
    FragmentGap,         // 分片号不连续
    FirstFragmentReset,  // 已有缓存时又收到一个首片（重传），整组丢弃重来
    Evicted,             // 缓存达到上限，整组被淘汰
};

// 重组结果。completed 为 true 时 frame 为拼好的完整帧；
// rejected 为 true 时 reason 给出原因，frame 为空。
struct ReassemblyResult {
    bool completed = false;
    bool rejected = false;
    RejectReason reason = RejectReason::None;
    uint16_t sequenceNumber = 0;
    std::vector<uint8_t> frame;
};

struct ReassemblerStats {
    uint64_t fragmentsAccepted = 0;  // 进入缓存的分片数
    uint64_t framesCompleted = 0;    // 成功重组（含单片直通）的帧数
    uint64_t groupsDropped = 0;      // 因策略违例被整体丢弃的组数
    uint64_t groupsEvicted = 0;      // 因缓存上限被淘汰的组数
    uint64_t groupsCleared = 0;      // 密钥重装/重新关联时清空的组数
};

// 802.11 MPDU 分片重组器。
//
// 安全策略（每组按 802.11 序号区分）：
//   1. 同序号的所有分片必须同为加密或同为明文，混合即整组丢弃；
//   2. 同序号的加密分片必须使用同一密钥索引，否则整组丢弃；
//   3. 同序号加密分片的 PN 必须逐片恰好 +1，跳变/回退/相等均整组丢弃；
//   4. 分片号必须连续，且首片必须先到；
//   5. 密钥重装或重新关联时必须调用 clear() 清空全部缓存；
//   6. 缓存的分片总数不超过 maxCachedFragments（默认 16），超限淘汰最旧一组。
//
// 本类本身不加锁；驱动应在 RX 串行化上下文中使用，或自行加锁。
class FragmentReassembler {
public:
    static constexpr size_t kDefaultCacheLimit = 16;
    static constexpr uint8_t  kMaxFragmentNumber = 15;

    explicit FragmentReassembler(size_t maxCachedFragments = kDefaultCacheLimit);

    // 处理一个分片。无论是否成功，返回对象都描述了处置结果。
    ReassemblyResult process(const FragmentInput& input);

    // 密钥重装（PTK/GTK/IGTK 安装）或站点重新关联时调用，清空所有缓存分片。
    void clear();

    size_t cachedGroups() const { return groups_.size(); }
    size_t cachedFragments() const { return cachedFragments_; }
    const ReassemblerStats& stats() const { return stats_; }

private:
    struct Group {
        uint16_t sequenceNumber = 0;
        uint8_t  expectedFragment = 0;
        bool     protectedFrame = false;
        uint8_t  keyId = 0;
        uint64_t lastPacketNumber = 0;
        uint64_t bornAt = 0;       // 单调插入序号，用于淘汰“最旧”一组
        std::vector<uint8_t> reassembled;
    };

    ReassemblyResult reject(RejectReason reason, uint16_t sequenceNumber);
    void dropGroup(std::map<uint16_t, Group>::iterator it);
    void evictOldestGroup();

    size_t maxCachedFragments_;
    size_t cachedFragments_ = 0;
    uint64_t insertionCounter_ = 0;
    std::map<uint16_t, Group> groups_;
    ReassemblerStats stats_;
};

} // namespace netsec
