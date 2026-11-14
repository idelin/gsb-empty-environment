#include "netsec.hpp"

namespace netsec {

namespace {

std::uint64_t fnv1a_mix(std::uint64_t h, std::uint64_t v) {
  constexpr std::uint64_t kPrime = 1099511628211ULL;
  for (int i = 0; i < 8; ++i) {
    h ^= (v >> (i * 8)) & 0xff;
    h *= kPrime;
  }
  return h;
}

}  // namespace

std::size_t ConntrackTable::FragKeyHash::operator()(const FragKey& k) const {
  std::uint64_t h = 1469598103934665603ULL;
  h = fnv1a_mix(h, k.zone);
  h = fnv1a_mix(h, k.tenant_id);
  h = fnv1a_mix(h, k.src_ip);
  h = fnv1a_mix(h, k.dst_ip);
  h = fnv1a_mix(h, static_cast<std::uint8_t>(k.proto));
  h = fnv1a_mix(h, k.ip_id);
  return static_cast<std::size_t>(h);
}

ConntrackTable::ConntrackTable(std::size_t bucket_count)
    : buckets_(bucket_count == 0 ? 1 : bucket_count) {}

std::uint64_t ConntrackTable::hash_flow(const FlowKey& flow) {
  std::uint64_t h = 1469598103934665603ULL;
  h = fnv1a_mix(h, flow.zone);
  h = fnv1a_mix(h, flow.tenant_id);
  h = fnv1a_mix(h, flow.tuple.src_ip);
  h = fnv1a_mix(h, flow.tuple.dst_ip);
  h = fnv1a_mix(h, flow.tuple.src_port);
  h = fnv1a_mix(h, flow.tuple.dst_port);
  h = fnv1a_mix(h, static_cast<std::uint8_t>(flow.tuple.proto));
  return h;
}

FlowKey ConntrackTable::reverse(const FlowKey& flow) {
  FlowKey r = flow;
  std::swap(r.tuple.src_ip, r.tuple.dst_ip);
  std::swap(r.tuple.src_port, r.tuple.dst_port);
  return r;
}

std::uint64_t ConntrackTable::timeout_for(ConnState state) {
  switch (state) {
    case ConnState::NEW:
      return kNewTimeout;
    case ConnState::ESTABLISHED:
      return kEstablishedTimeout;
    case ConnState::FIN_WAIT:
      return kFinWaitTimeout;
    case ConnState::TIME_WAIT:
      return kTimeWaitTimeout;
    case ConnState::DYING:
      return 0;
  }
  return 0;
}

ConntrackTable::FragKey ConntrackTable::frag_key_of(const FlowKey& flow,
                                                    const PacketView& pkt) {
  return FragKey{flow.zone,   flow.tenant_id, pkt.src_ip,
                 pkt.dst_ip,  pkt.proto,      pkt.ip_id};
}

std::size_t ConntrackTable::bucket_of(const FlowKey& flow) const {
  return static_cast<std::size_t>(hash_flow(flow)) % buckets_.size();
}

void ConntrackTable::transition(Entry& e, ConnState next, std::uint64_t now) {
  e.key_.state = next;
  e.expires_at_ = now + timeout_for(next);
}

ConntrackTable::Entry* ConntrackTable::find_live(const FlowKey& flow) {
  auto& bucket = buckets_[bucket_of(flow)];
  for (auto& e : bucket) {
    // 哈希只定位桶;命中与否由完整键(zone+租户+五元组+状态)决定。
    if (e.key_.flow == flow && e.key_.state != ConnState::DYING) {
      return &e;
    }
  }
  return nullptr;
}

ConntrackTable::Entry* ConntrackTable::find(const FlowKey& flow) {
  return find_live(flow);
}

ConntrackTable::Entry* ConntrackTable::insert(const FlowKey& flow,
                                              const NatMapping& mapping,
                                              std::uint64_t now) {
  if (Entry* live = find_live(flow)) {
    return live;
  }
  auto& bucket = buckets_[bucket_of(flow)];
  // 复用:仅当同流 DYING 条目引用计数为 0 时才重置槽位,
  // 引用未清零的旧映射绝不复用,避免串租户。
  for (auto& e : bucket) {
    if (e.key_.flow == flow && e.key_.state == ConnState::DYING &&
        e.refcount_ == 0) {
      e.mapping_ = mapping;
      transition(e, ConnState::NEW, now);
      return &e;
    }
  }
  Entry e;
  e.key_ = ConnKey{flow, ConnState::NEW};
  e.mapping_ = mapping;
  e.expires_at_ = now + kNewTimeout;
  bucket.push_back(e);
  ++size_;
  return &bucket.back();
}

ConntrackTable::Entry* ConntrackTable::process_packet(const FlowKey& flow,
                                                      const PacketView& pkt,
                                                      std::uint64_t now) {
  if (!pkt.has_ports) {
    // 非首片分片:按 (zone, tenant, src, dst, proto, ip_id) 关联。
    auto it = frag_assocs_.find(frag_key_of(flow, pkt));
    if (it == frag_assocs_.end() || it->second.expires_at <= now) {
      return nullptr;
    }
    return it->second.entry;
  }

  Entry* e = find_live(flow);
  FlowKey forward = flow;
  if (e == nullptr) {
    forward = reverse(flow);
    e = find_live(forward);
  }
  if (e == nullptr) {
    return nullptr;
  }

  if (pkt.tcp_fin) {
    if (e->key_.state == ConnState::ESTABLISHED) {
      transition(*e, ConnState::FIN_WAIT, now);
    } else if (e->key_.state == ConnState::FIN_WAIT) {
      transition(*e, ConnState::TIME_WAIT, now);
    }
  } else if (e->key_.state == ConnState::NEW && forward != flow) {
    // 收到反向报文,连接双向确认。
    transition(*e, ConnState::ESTABLISHED, now);
  } else {
    e->expires_at_ = now + timeout_for(e->key_.state);
  }

  if (pkt.is_fragment) {
    // 首片携带端口,登记分片关联供后续非首片命中。
    frag_assocs_[frag_key_of(forward, pkt)] =
        FragAssoc{e, now + kFragTimeout};
  }
  return e;
}

ConntrackTable::Entry* ConntrackTable::process_icmp_error(
    std::uint16_t zone, std::uint32_t tenant_id, const FiveTuple& embedded,
    std::uint64_t now) {
  FlowKey flow{zone, tenant_id, embedded};
  Entry* e = find_live(flow);
  if (e == nullptr) {
    e = find_live(reverse(flow));
  }
  if (e != nullptr) {
    e->expires_at_ = now + timeout_for(e->key_.state);
  }
  return e;
}

void ConntrackTable::acquire(Entry& e) { ++e.refcount_; }

void ConntrackTable::release(Entry& e) {
  if (e.refcount_ > 0) {
    --e.refcount_;
  }
}

void ConntrackTable::tick(std::uint64_t now) {
  for (auto& bucket : buckets_) {
    for (auto& e : bucket) {
      if (e.key_.state != ConnState::DYING && e.expires_at_ <= now) {
        transition(e, ConnState::DYING, now);
      }
    }
  }
  for (auto it = frag_assocs_.begin(); it != frag_assocs_.end();) {
    if (it->second.expires_at <= now) {
      it = frag_assocs_.erase(it);
    } else {
      ++it;
    }
  }
}

std::size_t ConntrackTable::reclaim() {
  std::size_t reclaimed = 0;
  for (auto& bucket : buckets_) {
    for (auto it = bucket.begin(); it != bucket.end();) {
      // 回收前核对引用计数:仍有数据面引用的 DYING 条目保留。
      if (it->key_.state == ConnState::DYING && it->refcount_ == 0) {
        it = bucket.erase(it);
        ++reclaimed;
        --size_;
      } else {
        ++it;
      }
    }
  }
  return reclaimed;
}

std::size_t ConntrackTable::size() const { return size_; }

}  // namespace netsec
