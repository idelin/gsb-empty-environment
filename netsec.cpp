#include "netsec.hpp"

#include <functional>
#include <limits>
#include <queue>
#include <utility>

namespace netsec {

std::uint16_t fletcher16(std::span<const std::uint8_t> data) noexcept {
  std::uint32_t c0 = 0;
  std::uint32_t c1 = 0;
  for (std::uint8_t byte : data) {
    c0 = (c0 + byte) % 255;
    c1 = (c1 + c0) % 255;
  }
  return static_cast<std::uint16_t>((c1 << 8) | c0);
}

std::vector<std::uint8_t> serialize_lsa(const Lsa& lsa, bool zero_age,
                                        bool zero_checksum) {
  std::vector<std::uint8_t> out;
  out.reserve(20 + lsa.links.size() * 8);
  auto put16 = [&out](std::uint16_t value) {
    out.push_back(static_cast<std::uint8_t>(value >> 8));
    out.push_back(static_cast<std::uint8_t>(value & 0xff));
  };
  auto put32 = [&out](std::uint32_t value) {
    out.push_back(static_cast<std::uint8_t>(value >> 24));
    out.push_back(static_cast<std::uint8_t>((value >> 16) & 0xff));
    out.push_back(static_cast<std::uint8_t>((value >> 8) & 0xff));
    out.push_back(static_cast<std::uint8_t>(value & 0xff));
  };

  put16(zero_age ? 0 : lsa.header.age);
  out.push_back(static_cast<std::uint8_t>(lsa.header.type));
  out.push_back(0); // options
  put32(lsa.header.link_state_id);
  put32(lsa.header.advertising_router);
  put32(lsa.header.sequence);
  put16(zero_checksum ? 0 : lsa.header.checksum);
  put16(lsa.header.length);
  for (const RouterLink& link : lsa.links) {
    put32(link.neighbor);
    put16(link.metric);
    put16(0); // padding: link type + t0s count
  }
  return out;
}

void finalize_lsa(Lsa& lsa) {
  lsa.header.length =
      static_cast<std::uint16_t>(20 + lsa.links.size() * 8);
  lsa.header.checksum = 0;
  const std::vector<std::uint8_t> bytes =
      serialize_lsa(lsa, /*zero_age=*/true, /*zero_checksum=*/true);
  lsa.header.checksum = fletcher16(bytes);
}

bool verify_lsa_checksum(const Lsa& lsa) noexcept {
  if (lsa.header.length != 20 + lsa.links.size() * 8) {
    return false;
  }
  const std::vector<std::uint8_t> bytes =
      serialize_lsa(lsa, /*zero_age=*/true, /*zero_checksum=*/true);
  return fletcher16(bytes) == lsa.header.checksum;
}

bool sequence_more_recent(std::uint32_t a, std::uint32_t b) noexcept {
  return static_cast<std::int32_t>(a - b) > 0;
}

bool sequence_valid(std::uint32_t seq) noexcept {
  return seq != kReservedSequence;
}

bool age_valid(std::uint16_t age) noexcept {
  return age >= 1 && age <= kMaxAge;
}

HelloVerdict validate_hello(const VirtualLinkConfig& config,
                            const HelloPacket& hello) noexcept {
  if (hello.router_id != config.peer_router_id) {
    return HelloVerdict::kBadEndpoint;
  }
  if (hello.area_id != config.transit_area) {
    return HelloVerdict::kBadArea;
  }
  if (hello.source != config.peer_source) {
    return HelloVerdict::kBadSource;
  }
  return HelloVerdict::kAccept;
}

LsaVerdict validate_lsa_wire(const Lsa& lsa) noexcept {
  if (!age_valid(lsa.header.age)) {
    return LsaVerdict::kBadAge;
  }
  if (!sequence_valid(lsa.header.sequence)) {
    return LsaVerdict::kBadSequence;
  }
  if (!verify_lsa_checksum(lsa)) {
    return LsaVerdict::kBadChecksum;
  }
  return LsaVerdict::kAccept;
}

bool LsaKey::operator<(const LsaKey& other) const noexcept {
  if (type != other.type) {
    return type < other.type;
  }
  if (link_state_id != other.link_state_id) {
    return link_state_id < other.link_state_id;
  }
  return advertising_router < other.advertising_router;
}

LsaKey key_of(const Lsa& lsa) noexcept {
  return LsaKey{lsa.header.type, lsa.header.link_state_id,
                lsa.header.advertising_router};
}

InstallResult LinkStateDatabase::install(const Lsa& lsa,
                                         LsaVerdict* wire_verdict) {
  const LsaVerdict verdict = validate_lsa_wire(lsa);
  if (wire_verdict != nullptr) {
    *wire_verdict = verdict;
  }
  if (verdict != LsaVerdict::kAccept) {
    return InstallResult::kRejected;
  }

  const LsaKey key = key_of(lsa);
  const auto it = db_.find(key);
  if (it == db_.end()) {
    db_.emplace(key, lsa);
    return InstallResult::kInstalled;
  }
  const std::uint32_t stored = it->second.header.sequence;
  if (lsa.header.sequence == stored) {
    return InstallResult::kDuplicate;
  }
  if (!sequence_more_recent(lsa.header.sequence, stored)) {
    return InstallResult::kRollback;
  }
  it->second = lsa;
  return InstallResult::kUpdated;
}

const Lsa* LinkStateDatabase::lookup(const LsaKey& key) const {
  const auto it = db_.find(key);
  return it == db_.end() ? nullptr : &it->second;
}

SpfVerdict spf_filter(const AreaPolicy& policy, AreaId arrival_area,
                      const Lsa& lsa) noexcept {
  if (lsa.header.type == LsaType::kAsExternal) {
    return policy.trusted_external_advertisers.count(
               lsa.header.advertising_router) > 0
               ? SpfVerdict::kAccept
               : SpfVerdict::kUntrustedExternal;
  }
  if (lsa.header.type == LsaType::kSummaryNet ||
      lsa.header.type == LsaType::kSummaryAsbr) {
    return SpfVerdict::kUnsupportedType;
  }
  if (arrival_area != policy.local_area) {
    return SpfVerdict::kWrongArea;
  }
  return SpfVerdict::kAccept;
}

std::map<RouterId, std::uint32_t> run_spf(const AreaPolicy& policy,
                                          AreaId arrival_area,
                                          const LinkStateDatabase& db,
                                          RouterId self) {
  std::map<RouterId, std::vector<RouterLink>> adjacency;
  for (const auto& [key, lsa] : db) {
    if (lsa.header.type != LsaType::kRouter) {
      continue;
    }
    if (spf_filter(policy, arrival_area, lsa) != SpfVerdict::kAccept) {
      continue;
    }
    adjacency[lsa.header.advertising_router] = lsa.links;
  }

  using Entry = std::pair<std::uint32_t, RouterId>; // (metric, router)
  std::priority_queue<Entry, std::vector<Entry>, std::greater<>> frontier;
  std::map<RouterId, std::uint32_t> distance;
  distance[self] = 0;
  frontier.emplace(0, self);

  while (!frontier.empty()) {
    const auto [metric, router] = frontier.top();
    frontier.pop();
    if (metric > distance[router]) {
      continue;
    }
    const auto links_it = adjacency.find(router);
    if (links_it == adjacency.end()) {
      continue;
    }
    for (const RouterLink& link : links_it->second) {
      if (metric >
          std::numeric_limits<std::uint32_t>::max() - link.metric) {
        continue;
      }
      const std::uint32_t next = metric + link.metric;
      const auto dist_it = distance.find(link.neighbor);
      if (dist_it == distance.end() || next < dist_it->second) {
        distance[link.neighbor] = next;
        frontier.emplace(next, link.neighbor);
      }
    }
  }

  distance.erase(self);
  return distance;
}

} // namespace netsec
