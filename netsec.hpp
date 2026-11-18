#pragma once

#include <atomic>
#include <cstdint>
#include <functional>
#include <memory>
#include <mutex>
#include <string>
#include <unordered_set>
#include <utility>
#include <vector>

namespace netsec {

enum class Decision { Allow, Deny };

const char* to_string(Decision d);

// Identity of a protected file. Decisions key on (dev, ino) only, so
// hardlinks, renames, bind mounts and mount-namespace path tricks cannot
// evade the policy.
struct FileKey {
  std::uint64_t dev = 0;
  std::uint64_t ino = 0;
  bool operator==(const FileKey& o) const { return dev == o.dev && ino == o.ino; }
};

struct FileKeyHash {
  std::size_t operator()(const FileKey& k) const {
    return std::hash<std::uint64_t>{}(k.dev * 0x9E3779B97F4A7C15ull ^ k.ino);
  }
};

// Immutable policy snapshot. Switching policy installs a new generation;
// in-flight calls keep evaluating against the snapshot they started with.
struct Policy {
  std::uint64_t generation = 0;
  std::unordered_set<FileKey, FileKeyHash> protected_files;
  bool protects(const FileKey& k) const {
    return protected_files.find(k) != protected_files.end();
  }
};

struct HookEvent {
  FileKey key;
  std::string path_hint; // audit log only, never used for the decision
};

struct AuditRecord {
  std::uint64_t sequence = 0;
  std::uint64_t generation = 0;   // policy generation that made the decision
  FileKey key;
  std::string path_hint;
  Decision decision = Decision::Allow;
  int first_deny_program = -1;    // index into the program list, -1 if allowed
  std::uint64_t lost_before = 0;  // records dropped since the previous stored record
};

// Fixed-capacity ring buffer for audit records. The lost counter and the
// store operation are updated under one lock, so a lost count can never be
// paired with the wrong record even under concurrent callers.
class AuditRing {
 public:
  explicit AuditRing(std::size_t capacity);
  AuditRing(const AuditRing&) = delete;
  AuditRing& operator=(const AuditRing&) = delete;

  // On success sets rec.lost_before to the number of records dropped since
  // the last stored record and returns true. On overflow returns false and
  // folds the drop into the lost counter.
  bool store(AuditRecord& rec);
  bool take(AuditRecord& out);
  std::uint64_t lost() const;
  std::size_t size() const;
  std::size_t capacity() const;

 private:
  mutable std::mutex mu_;
  std::vector<AuditRecord> buf_;
  std::size_t head_ = 0;
  std::size_t count_ = 0;
  std::uint64_t lost_ = 0;
};

// Executes the mounted eBPF LSM programs in mount order and emits exactly
// one audit record per hook call.
class LsmDispatcher {
 public:
  using Program = std::function<Decision(const HookEvent&, const Policy&)>;
  using ProgramList = std::vector<std::pair<std::string, Program>>;

  LsmDispatcher(ProgramList programs, std::shared_ptr<const Policy> policy,
                AuditRing& ring);

  void install_policy(std::shared_ptr<const Policy> policy);
  std::shared_ptr<const Policy> current_policy() const;

  // Two-phase hook call: begin() pins the current policy generation,
  // complete() evaluates programs in mount order against that snapshot
  // (stopping at the first deny) and emits exactly one audit record.
  struct Call {
    HookEvent event;
    std::shared_ptr<const Policy> policy;
    std::uint64_t sequence = 0;
  };

  Call begin(HookEvent event) const;
  AuditRecord complete(const Call& call);

  // Convenience for a call that does not span a policy switch.
  AuditRecord hook(HookEvent event) { return complete(begin(std::move(event))); }

  const ProgramList& programs() const { return programs_; }

 private:
  ProgramList programs_;
  mutable std::mutex policy_mu_;
  std::shared_ptr<const Policy> policy_;
  AuditRing& ring_;
  mutable std::atomic<std::uint64_t> seq_{0};
};

}  // namespace netsec
