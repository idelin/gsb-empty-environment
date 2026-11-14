#pragma once

#include <cstdint>
#include <cstddef>
#include <list>
#include <unordered_map>
#include <vector>

namespace netsec {

enum class Protocol : std::uint8_t { ICMP = 1, TCP = 6, UDP = 17 };

// 协议状态机:NEW -> ESTABLISHED -> FIN_WAIT -> TIME_WAIT -> DYING
// 任意活跃状态超时后进入 DYING;DYING 且引用计数为 0 才可回收或复用。
enum class ConnState : std::uint8_t { NEW, ESTABLISHED, FIN_WAIT, TIME_WAIT, DYING };

struct FiveTuple {
  std::uint32_t src_ip;
  std::uint32_t dst_ip;
  std::uint16_t src_port;
  std::uint16_t dst_port;
  Protocol proto;
  friend bool operator==(const FiveTuple&, const FiveTuple&) = default;
};

// 流标识:zone + 租户 + 五元组。哈希只覆盖 FlowKey;
// 桶内按完整 ConnKey(含协议状态)逐一比较,哈希碰撞不会误命中。
struct FlowKey {
  std::uint16_t zone;
  std::uint32_t tenant_id;
  FiveTuple tuple;
  friend bool operator==(const FlowKey&, const FlowKey&) = default;
};

// 条目完整键:FlowKey + 协议状态。同一条流的 DYING 旧条目与
// 复用后的 NEW 条目可共存,由状态字段区分,互不串扰。
struct ConnKey {
  FlowKey flow;
  ConnState state;
  friend bool operator==(const ConnKey&, const ConnKey&) = default;
};

struct NatMapping {
  std::uint32_t xlated_ip;
  std::uint16_t xlated_port;
  friend bool operator==(const NatMapping&, const NatMapping&) = default;
};

// 数据面报文视图。非首片分片没有端口(has_ports=false),
// 通过 (zone, tenant, src, dst, proto, ip_id) 关联到首片建立的条目。
struct PacketView {
  std::uint32_t src_ip;
  std::uint32_t dst_ip;
  std::uint16_t src_port;
  std::uint16_t dst_port;
  Protocol proto;
  std::uint16_t ip_id;
  bool has_ports;
  bool is_fragment;
  bool tcp_fin;
};

class ConntrackTable {
 public:
  static constexpr std::uint64_t kNewTimeout = 30;
  static constexpr std::uint64_t kEstablishedTimeout = 300;
  static constexpr std::uint64_t kFinWaitTimeout = 30;
  static constexpr std::uint64_t kTimeWaitTimeout = 60;
  static constexpr std::uint64_t kFragTimeout = 30;

  class Entry {
   public:
    const ConnKey& key() const { return key_; }
    ConnState state() const { return key_.state; }
    const NatMapping& mapping() const { return mapping_; }
    std::uint32_t refcount() const { return refcount_; }
    std::uint64_t expires_at() const { return expires_at_; }

   private:
    friend class ConntrackTable;
    ConnKey key_;
    NatMapping mapping_;
    std::uint64_t expires_at_;
    std::uint32_t refcount_ = 0;
  };

  explicit ConntrackTable(std::size_t bucket_count = 1024);

  ConntrackTable(const ConntrackTable&) = delete;
  ConntrackTable& operator=(const ConntrackTable&) = delete;

  // 快路径:按 FlowKey 精确查找活跃(非 DYING)条目,找不到返回 nullptr。
  Entry* find(const FlowKey& flow);

  // 慢路径:新建条目;若同流存在可回收的 DYING 条目(refcount==0)
  // 则按状态机复用该槽位;已存在活跃条目时直接返回该条目。
  Entry* insert(const FlowKey& flow, const NatMapping& mapping, std::uint64_t now);

  // 数据面报文处理:正向/反向查找、状态迁移、刷新超时、分片关联。
  // 非首片分片(has_ports=false)通过分片缓存关联,未关联返回 nullptr。
  Entry* process_packet(const FlowKey& flow, const PacketView& pkt, std::uint64_t now);

  // ICMP 差错报文:按内嵌原始五元组(自动尝试正/反两个方向)
  // 关联到所属租户的连接条目,找不到返回 nullptr。
  Entry* process_icmp_error(std::uint16_t zone, std::uint32_t tenant_id,
                            const FiveTuple& embedded, std::uint64_t now);

  // 引用计数:数据面持有映射期间必须 acquire,用完 release。
  void acquire(Entry& e);
  void release(Entry& e);

  // 时钟推进:到期活跃条目迁移到 DYING,并清理过期分片缓存。
  void tick(std::uint64_t now);

  // 回收:仅删除 DYING 且 refcount==0 的条目,返回回收数量。
  std::size_t reclaim();

  std::size_t size() const;

 private:
  struct FragKey {
    std::uint16_t zone;
    std::uint32_t tenant_id;
    std::uint32_t src_ip;
    std::uint32_t dst_ip;
    Protocol proto;
    std::uint16_t ip_id;
    friend bool operator==(const FragKey&, const FragKey&) = default;
  };
  struct FragKeyHash {
    std::size_t operator()(const FragKey& k) const;
  };
  struct FragAssoc {
    Entry* entry;
    std::uint64_t expires_at;
  };

  static std::uint64_t hash_flow(const FlowKey& flow);
  static FlowKey reverse(const FlowKey& flow);
  static std::uint64_t timeout_for(ConnState state);
  static FragKey frag_key_of(const FlowKey& flow, const PacketView& pkt);

  std::size_t bucket_of(const FlowKey& flow) const;
  Entry* find_live(const FlowKey& flow);
  void transition(Entry& e, ConnState next, std::uint64_t now);

  std::vector<std::list<Entry>> buckets_;
  std::unordered_map<FragKey, FragAssoc, FragKeyHash> frag_assocs_;
  std::size_t size_ = 0;
};

}  // namespace netsec
