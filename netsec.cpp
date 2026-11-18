#include "netsec.hpp"

namespace netsec {

const char* to_string(Decision d) { return d == Decision::Deny ? "DENY" : "ALLOW"; }

AuditRing::AuditRing(std::size_t capacity) : buf_(capacity) {}

bool AuditRing::store(AuditRecord& rec) {
  std::lock_guard<std::mutex> lk(mu_);
  if (count_ == buf_.size()) {
    ++lost_;
    return false;
  }
  rec.lost_before = lost_;
  lost_ = 0;
  buf_[(head_ + count_) % buf_.size()] = rec;
  ++count_;
  return true;
}

bool AuditRing::take(AuditRecord& out) {
  std::lock_guard<std::mutex> lk(mu_);
  if (count_ == 0) return false;
  out = buf_[head_];
  head_ = (head_ + 1) % buf_.size();
  --count_;
  return true;
}

std::uint64_t AuditRing::lost() const {
  std::lock_guard<std::mutex> lk(mu_);
  return lost_;
}

std::size_t AuditRing::size() const {
  std::lock_guard<std::mutex> lk(mu_);
  return count_;
}

std::size_t AuditRing::capacity() const {
  std::lock_guard<std::mutex> lk(mu_);
  return buf_.size();
}

LsmDispatcher::LsmDispatcher(ProgramList programs,
                             std::shared_ptr<const Policy> policy,
                             AuditRing& ring)
    : programs_(std::move(programs)), policy_(std::move(policy)), ring_(ring) {}

void LsmDispatcher::install_policy(std::shared_ptr<const Policy> policy) {
  std::lock_guard<std::mutex> lk(policy_mu_);
  policy_ = std::move(policy);
}

std::shared_ptr<const Policy> LsmDispatcher::current_policy() const {
  std::lock_guard<std::mutex> lk(policy_mu_);
  return policy_;
}

LsmDispatcher::Call LsmDispatcher::begin(HookEvent event) const {
  Call call;
  call.event = std::move(event);
  call.policy = current_policy();
  call.sequence = seq_.fetch_add(1, std::memory_order_relaxed);
  return call;
}

AuditRecord LsmDispatcher::complete(const Call& call) {
  AuditRecord rec;
  rec.sequence = call.sequence;
  rec.generation = call.policy->generation;
  rec.key = call.event.key;
  rec.path_hint = call.event.path_hint;
  for (std::size_t i = 0; i < programs_.size(); ++i) {
    if (programs_[i].second(call.event, *call.policy) == Decision::Deny) {
      rec.decision = Decision::Deny;
      rec.first_deny_program = static_cast<int>(i);
      break;  // final deny: later programs are not executed
    }
  }
  ring_.store(rec);  // exactly one audit record per hook call
  return rec;
}

}  // namespace netsec
