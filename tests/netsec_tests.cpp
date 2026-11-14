#include "netsec.hpp"

#include <atomic>
#include <chrono>
#include <cstdlib>
#include <exception>
#include <filesystem>
#include <fstream>
#include <iostream>
#include <memory>
#include <mutex>
#include <optional>
#include <string>
#include <thread>
#include <unordered_map>
#include <utility>
#include <vector>

#include <fcntl.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <unistd.h>

namespace {

struct TestFailure : std::exception {
  std::string message;
  explicit TestFailure(std::string value) : message(std::move(value)) {}
  const char* what() const noexcept override { return message.c_str(); }
};

void fail_at(const char* file, int line, std::string message) {
  throw TestFailure(std::string(file) + ":" + std::to_string(line) + ": " +
                    message);
}

#define CHECK(condition)                                                    \
  do {                                                                      \
    if (!(condition)) {                                                     \
      fail_at(__FILE__, __LINE__, "check failed: " #condition);             \
    }                                                                       \
  } while (0)

#define CHECK_OK(expression)                                                \
  do {                                                                      \
    const auto& netsec_status = (expression);                               \
    if (!netsec_status) {                                                   \
      fail_at(__FILE__, __LINE__, netsec_status.error().message);           \
    }                                                                       \
  } while (0)

#define CHECK_ERR(expression, expected_code)                                \
  do {                                                                      \
    const auto& netsec_result = (expression);                               \
    if (netsec_result) {                                                    \
      fail_at(__FILE__, __LINE__, "expected failure: " #expression);        \
    }                                                                       \
    if (netsec_result.error().code != (expected_code)) {                    \
      fail_at(__FILE__, __LINE__,                                           \
              std::string("wrong error code: got ") +                       \
                  netsec::to_string(netsec_result.error().code) +            \
                  ", expected " + netsec::to_string(expected_code));        \
    }                                                                       \
  } while (0)

template <typename T>
T take(netsec::Result<T> result, const char* file, int line) {
  if (!result) {
    fail_at(file, line, result.error().message);
  }
  return std::move(result.value());
}

#define TAKE(expression) take((expression), __FILE__, __LINE__)

netsec::ObjectIdentity identity(std::uint64_t mount_id,
                                std::uint64_t inode,
                                std::uint64_t device = 7) {
  netsec::ObjectIdentity result;
  result.mount_id = mount_id;
  result.inode = inode;
  result.device = device;
  result.stable = true;
  return result;
}

class FakeResolver final : public netsec::ObjectResolver {
 public:
  bool follow_cross_mount_parents = false;

  int add(netsec::ObjectIdentity id, int parent_fd,
          std::string path_text = {}) {
    const int fd = next_fd_++;
    auto node = std::make_shared<Node>();
    node->identity = std::move(id);
    node->parent_fd = parent_fd;
    node->path_text = std::move(path_text);
    nodes_.emplace(fd, std::move(node));
    return fd;
  }

  void mutate_identity(int fd, netsec::ObjectIdentity id) {
    auto it = nodes_.find(fd);
    if (it == nodes_.end()) {
      throw TestFailure("fake resolver: unknown fd");
    }
    it->second->identity = std::move(id);
  }

  netsec::Result<netsec::ObjectIdentity> identify(int fd) const override {
    auto it = nodes_.find(fd);
    if (it == nodes_.end()) {
      return netsec::Error{netsec::ErrorCode::InvalidFd,
                           "fake resolver: unknown fd"};
    }
    return it->second->identity;
  }

  netsec::Result<netsec::Ancestry> ancestry(int fd) const override {
    netsec::Ancestry result;
    std::optional<int> current = fd;
    std::size_t depth = 0;
    while (current && depth++ < 64) {
      auto it = nodes_.find(*current);
      if (it == nodes_.end()) {
        result.complete = false;
        break;
      }
      result.chain.push_back(it->second->identity);
      if (it->second->parent_fd < 0) {
        result.reached_mount_root = true;
        current.reset();
        break;
      }
      const auto parent = nodes_.find(it->second->parent_fd);
      if (parent == nodes_.end()) {
        result.complete = false;
        current.reset();
        break;
      }
      if (!follow_cross_mount_parents &&
          (parent->second->identity.mount_id !=
               it->second->identity.mount_id ||
           parent->second->identity.device != it->second->identity.device)) {
        result.reached_mount_root = true;
        current.reset();
        break;
      }
      current = it->second->parent_fd;
    }
    if (depth >= 64) {
      result.complete = false;
    }
    return result;
  }

  netsec::Result<int> retain(int fd) const override {
    auto it = nodes_.find(fd);
    if (it == nodes_.end()) {
      return netsec::Error{netsec::ErrorCode::InvalidFd,
                           "fake resolver: unknown fd"};
    }
    const int alias = next_fd_++;
    nodes_.emplace(alias, it->second);
    return alias;
  }

  void release(int fd) const noexcept override { nodes_.erase(fd); }

 private:
  struct Node {
    netsec::ObjectIdentity identity;
    int parent_fd = -1;
    std::string path_text;
  };

  mutable std::unordered_map<int, std::shared_ptr<Node>> nodes_;
  mutable int next_fd_ = 100;
};

class FakeBackend final : public netsec::LandlockBackend {
 public:
  std::uint32_t abi = 3;
  bool abi_fails = false;
  bool create_fails = false;
  bool restrict_fails = false;

  mutable int abi_calls = 0;
  int create_calls = 0;
  int prepare_calls = 0;
  int restrict_calls = 0;
  std::vector<netsec::AccessMask> created_rulesets;
  std::vector<std::pair<int, netsec::AccessMask>> added_rules;
  std::vector<int> closed_rulesets;

  netsec::Result<std::uint32_t> abi_version() const override {
    ++abi_calls;
    if (abi_fails) {
      return netsec::Error{netsec::ErrorCode::AbiUnavailable,
                           "fake backend: ABI unavailable"};
    }
    return abi;
  }

  netsec::Result<int> create_ruleset(
      netsec::AccessMask handled_access) override {
    ++create_calls;
    if (create_fails) {
      return netsec::Error{netsec::ErrorCode::RulesetCreateFailed,
                           "fake backend: create failed"};
    }
    if ((handled_access & ~netsec::access::kAllKnownFs) != 0) {
      return netsec::Error{netsec::ErrorCode::InvalidAccessMask,
                           "fake backend: unknown bits"};
    }
    if ((handled_access & ~netsec::supported_fs_access(abi)) != 0) {
      return netsec::Error{netsec::ErrorCode::UnsupportedAccessBits,
                           "fake backend: ABI cannot enforce bits"};
    }
    const int fd = next_fd_++;
    rulesets_.emplace(fd, handled_access);
    created_rulesets.push_back(handled_access);
    return fd;
  }

  netsec::Status add_path_beneath(int ruleset_fd, int root_fd,
                                  netsec::AccessMask allowed_access) override {
    auto it = rulesets_.find(ruleset_fd);
    if (it == rulesets_.end()) {
      return netsec::Error{netsec::ErrorCode::InvalidFd,
                           "fake backend: unknown ruleset"};
    }
    if ((allowed_access & ~it->second) != 0) {
      return netsec::Error{netsec::ErrorCode::RulesetRuleFailed,
                           "fake backend: rule exceeds handled access"};
    }
    added_rules.emplace_back(root_fd, allowed_access);
    return netsec::Status{};
  }

  netsec::Status prepare_restrict() override {
    ++prepare_calls;
    return netsec::Status{};
  }

  netsec::Status restrict_self(int ruleset_fd) override {
    ++restrict_calls;
    if (restrict_fails) {
      return netsec::Error{netsec::ErrorCode::RestrictFailed,
                           "fake backend: restrict failed"};
    }
    if (rulesets_.find(ruleset_fd) == rulesets_.end()) {
      return netsec::Error{netsec::ErrorCode::InvalidFd,
                           "fake backend: unknown ruleset"};
    }
    restricted_.push_back(ruleset_fd);
    return netsec::Status{};
  }

  void close_fd(int fd) noexcept override {
    closed_rulesets.push_back(fd);
    rulesets_.erase(fd);
  }

 private:
  std::unordered_map<int, netsec::AccessMask> rulesets_;
  std::vector<int> restricted_;
  int next_fd_ = 500;
};

struct Fixture {
  std::shared_ptr<FakeResolver> resolver =
      std::make_shared<FakeResolver>();
  std::shared_ptr<FakeBackend> backend = std::make_shared<FakeBackend>();

  netsec::Planner planner{resolver, backend};

  netsec::ActiveRuleset apply_root(int root_fd,
                                   netsec::AccessMask access,
                                   std::uint64_t namespace_generation) {
    netsec::PlannerConfig config;
    config.mount_namespace.generation = namespace_generation;
    netsec::RuleRoot root;
    root.fd = root_fd;
    root.access = access;
    auto plan = TAKE(planner.plan(root, config));
    return TAKE(netsec::apply(std::move(plan)));
  }
};

void test_abi_and_access_trimming() {
  Fixture fixture;
  fixture.backend->abi = 1;
  const int root = fixture.resolver->add(identity(100, 1), -1, "/same");

  netsec::RuleRoot rule_root;
  rule_root.fd = root;
  rule_root.access = netsec::access::kAbi1 | netsec::access::kRefer |
                     netsec::access::kTruncate | netsec::access::kIoctlDev;
  rule_root.label = "old-kernel";

  netsec::PlannerConfig strict;
  strict.mount_namespace.generation = 11;
  CHECK_ERR(fixture.planner.plan(rule_root, strict),
            netsec::ErrorCode::UnsupportedAccessBits);

  netsec::PlannerConfig trim;
  trim.mount_namespace.generation = 11;
  trim.unsupported_access = netsec::UnsupportedAccessPolicy::TrimAndReport;
  auto plan = TAKE(fixture.planner.plan(rule_root, trim));
  CHECK(plan.handled_access() == netsec::access::kAbi1);
  const auto trimmed = plan.trimmed_access();
  CHECK(trimmed ==
        (netsec::access::kRefer | netsec::access::kTruncate |
         netsec::access::kIoctlDev));
  CHECK(fixture.backend->create_calls == 0);

  auto active = TAKE(netsec::apply(std::move(plan)));
  CHECK(fixture.backend->create_calls == 1);
  CHECK(fixture.backend->created_rulesets.size() == 1);
  CHECK(fixture.backend->created_rulesets.front() == netsec::access::kAbi1);
  CHECK(active.trimmed_access == trimmed);
  CHECK(fixture.backend->prepare_calls == 1);
  CHECK(fixture.backend->restrict_calls == 1);
}

void test_abi_failure_is_fail_closed() {
  Fixture fixture;
  fixture.backend->abi_fails = true;
  const int root = fixture.resolver->add(identity(100, 1), -1);
  netsec::RuleRoot rule_root;
  rule_root.fd = root;
  rule_root.access = netsec::access::kReadFile;
  CHECK_ERR(fixture.planner.plan(rule_root), netsec::ErrorCode::AbiUnavailable);
  CHECK(fixture.backend->create_calls == 0);

  fixture.backend->abi_fails = false;
  fixture.backend->abi = 0;
  CHECK_ERR(fixture.planner.plan(rule_root), netsec::ErrorCode::UnsupportedAbi);
  CHECK(fixture.backend->create_calls == 0);

  fixture.backend->abi = 3;
  rule_root.access = 1ULL << 40;
  CHECK_ERR(fixture.planner.plan(rule_root),
            netsec::ErrorCode::InvalidAccessMask);
  rule_root.access = 0;
  CHECK_ERR(fixture.planner.plan(rule_root), netsec::ErrorCode::NoAccessBits);
}

void test_supported_masks_are_abi_exact() {
  CHECK(netsec::supported_fs_access(0) == 0);
  CHECK(netsec::supported_fs_access(1) == netsec::access::kAbi1);
  CHECK(netsec::supported_fs_access(2) == netsec::access::kAbi2);
  CHECK(netsec::supported_fs_access(3) == netsec::access::kAbi3);
  CHECK(netsec::supported_fs_access(4) == netsec::access::kAbi3);
  CHECK(netsec::supported_fs_access(5) == netsec::access::kAbi5);
  CHECK(netsec::supported_fs_access(99) == netsec::access::kAbi5);
  CHECK((netsec::access::kAbi1 & netsec::access::kRefer) == 0);
  CHECK((netsec::access::kAbi2 & netsec::access::kRefer) ==
        netsec::access::kRefer);
  CHECK((netsec::access::kAbi3 & netsec::access::kTruncate) ==
        netsec::access::kTruncate);
  CHECK((netsec::access::kAbi5 & netsec::access::kIoctlDev) ==
        netsec::access::kIoctlDev);
}

void test_same_text_different_mount_does_not_inherit() {
  Fixture fixture;
  const int root_a = fixture.resolver->add(identity(100, 7), -1, "/same/path");
  const int root_b = fixture.resolver->add(identity(200, 7), -1, "/same/path");

  netsec::RuleRoot a;
  a.fd = root_a;
  a.access = netsec::access::kReadFile;
  a.label = "a";
  netsec::RuleRoot b;
  b.fd = root_b;
  b.access = netsec::access::kWriteFile;
  b.label = "b";

  auto plan_a = TAKE(fixture.planner.plan(a));
  auto plan_b = TAKE(fixture.planner.plan(b));
  CHECK(plan_a.rules().size() == 1);
  CHECK(plan_b.rules().size() == 1);
  CHECK(plan_a.rules().front().identity.mount_id == 100);
  CHECK(plan_b.rules().front().identity.mount_id == 200);
  CHECK(plan_a.rules().front().access == netsec::access::kReadFile);
  CHECK(plan_b.rules().front().access == netsec::access::kWriteFile);

  // The identities differ even though the path text, inode and uid would be
  // identical in a path/uid-based cache.
  CHECK(!(plan_a.rules().front().identity == plan_b.rules().front().identity));
}

void test_idmap_is_audit_only() {
  Fixture fixture;
  const int root = fixture.resolver->add(identity(100, 7), -1, "/mapped");

  netsec::IdMapIdentity before;
  before.uid_inside = 1000;
  before.gid_inside = 1000;
  before.uid_outside = 0;
  before.gid_outside = 0;
  before.generation = 1;

  netsec::IdMapIdentity after = before;
  after.uid_inside = 0;
  after.gid_inside = 0;
  after.uid_outside = 1000;
  after.gid_outside = 1000;
  after.generation = 2;

  netsec::RuleRoot first;
  first.fd = root;
  first.access = netsec::access::kReadFile | netsec::access::kWriteFile;
  first.idmap = before;
  first.label = "before";
  netsec::RuleRoot second = first;
  second.idmap = after;
  second.label = "after";

  std::vector<netsec::RuleRoot> roots{first, second};
  auto plan = TAKE(fixture.planner.plan(roots));
  CHECK(plan.rules().size() == 1);
  CHECK(plan.rules().front().idmaps.size() == 2);
  CHECK(plan.rules().front().identity.mount_id == 100);

  // Same uid/gid presentation and same inode text on another mount must not
  // collapse into the authorized object.
  const int other_mount =
      fixture.resolver->add(identity(201, 7), -1, "/mapped");
  netsec::RuleRoot other;
  other.fd = other_mount;
  other.access = netsec::access::kReadFile;
  other.idmap = before;
  auto plan_other = TAKE(fixture.planner.plan(other));
  CHECK(plan_other.rules().size() == 1);
  CHECK(plan_other.rules().front().identity.mount_id == 201);
}

void test_cross_mount_ancestry_is_rejected() {
  Fixture fixture;
  const int parent = fixture.resolver->add(identity(100, 1), -1);
  const int child = fixture.resolver->add(identity(200, 2), parent);
  fixture.resolver->follow_cross_mount_parents = true;

  netsec::RuleRoot root;
  root.fd = child;
  root.access = netsec::access::kReadFile;
  CHECK_ERR(fixture.planner.plan(root),
            netsec::ErrorCode::CrossMountTraversal);
  CHECK(fixture.backend->create_calls == 0);
}

void test_apply_revalidates_replaced_object() {
  Fixture fixture;
  const int root = fixture.resolver->add(identity(100, 7), -1, "/replace");
  netsec::RuleRoot rule_root;
  rule_root.fd = root;
  rule_root.access = netsec::access::kReadFile;
  auto plan = TAKE(fixture.planner.plan(rule_root));

  fixture.resolver->mutate_identity(root, identity(200, 7));
  CHECK_ERR(netsec::apply(std::move(plan)), netsec::ErrorCode::PathReplaced);
  CHECK(fixture.backend->create_calls == 0);
}

void test_bind_subtree_and_pivot_root_coverage() {
  Fixture fixture;
  const int root = fixture.resolver->add(identity(100, 1), -1, "/root");
  const int child = fixture.resolver->add(identity(100, 2), root, "/root/sub");
  const int sibling =
      fixture.resolver->add(identity(100, 3), root, "/root/sibling");
  const int outside = fixture.resolver->add(identity(300, 9), -1, "/outside");
  const int same_path_other_mount =
      fixture.resolver->add(identity(200, 1), -1, "/root");
  const int bind_root =
      fixture.resolver->add(identity(101, 2), root, "/root/sub");
  const int inside_bind =
      fixture.resolver->add(identity(101, 4), bind_root, "/root/sub/file");

  const std::uint64_t ns = 7;
  auto active = fixture.apply_root(
      root, netsec::access::kReadFile | netsec::access::kWriteFile, ns);
  auto& hooks = *active.hooks;
  CHECK(active.mount_namespace.generation == ns);
  CHECK(active.ruleset_generation > 0);
  CHECK(active.plan_generation > 0);

  CHECK_OK(hooks.verify_covered(child, ns, active.ruleset_generation, 0));
  CHECK_ERR(hooks.verify_covered(child, ns + 1, active.ruleset_generation, 0),
            netsec::ErrorCode::StaleNamespaceGeneration);
  CHECK_ERR(hooks.verify_covered(child, ns, active.ruleset_generation + 1, 0),
            netsec::ErrorCode::StaleRulesetGeneration);
  CHECK_ERR(hooks.verify_covered(same_path_other_mount, ns,
                                 active.ruleset_generation, 0),
            netsec::ErrorCode::NotCoveredByAuthorizedRoot);
  CHECK_ERR(hooks.verify_covered(outside, ns, active.ruleset_generation, 0),
            netsec::ErrorCode::NotCoveredByAuthorizedRoot);

  netsec::MountRequest stale_namespace;
  stale_namespace.operation = netsec::MountOperation::Mount;
  stale_namespace.source_fd = child;
  stale_namespace.namespace_generation = ns + 1;
  stale_namespace.ruleset_generation = active.ruleset_generation;
  stale_namespace.layout_generation = 0;
  CHECK_ERR(hooks.on_mount(stale_namespace),
            netsec::ErrorCode::StaleNamespaceGeneration);

  netsec::MountRequest stale_ruleset;
  stale_ruleset.operation = netsec::MountOperation::Mount;
  stale_ruleset.source_fd = child;
  stale_ruleset.namespace_generation = ns;
  stale_ruleset.ruleset_generation = active.ruleset_generation + 1;
  stale_ruleset.layout_generation = 0;
  CHECK_ERR(hooks.on_mount(stale_ruleset),
            netsec::ErrorCode::StaleRulesetGeneration);

  netsec::MountRequest swapped_mount;
  swapped_mount.operation = netsec::MountOperation::MoveMount;
  swapped_mount.source_fd = same_path_other_mount;
  swapped_mount.namespace_generation = ns;
  swapped_mount.ruleset_generation = active.ruleset_generation;
  swapped_mount.layout_generation = 0;
  CHECK_ERR(hooks.on_move_mount(swapped_mount),
            netsec::ErrorCode::NotCoveredByAuthorizedRoot);

  netsec::MountRequest mount_outside;
  mount_outside.operation = netsec::MountOperation::MoveMount;
  mount_outside.source_fd = outside;
  mount_outside.namespace_generation = ns;
  mount_outside.ruleset_generation = active.ruleset_generation;
  mount_outside.layout_generation = 0;
  CHECK_ERR(hooks.on_move_mount(mount_outside),
            netsec::ErrorCode::NotCoveredByAuthorizedRoot);
  CHECK(hooks.layout_generation() == 0);

  const int unrelated_destination =
      fixture.resolver->add(identity(900, 99), -1, "/unrelated");
  netsec::MountRequest swapped_destination;
  swapped_destination.operation = netsec::MountOperation::Mount;
  swapped_destination.source_fd = child;
  swapped_destination.destination_fd = unrelated_destination;
  swapped_destination.namespace_generation = ns;
  swapped_destination.ruleset_generation = active.ruleset_generation;
  swapped_destination.layout_generation = 0;
  CHECK_ERR(hooks.on_mount(swapped_destination),
            netsec::ErrorCode::IdentityChanged);
  CHECK(hooks.layout_generation() == 0);

  netsec::MountRequest bind_subtree;
  bind_subtree.operation = netsec::MountOperation::Mount;
  bind_subtree.source_fd = child;
  bind_subtree.destination_fd = bind_root;
  bind_subtree.namespace_generation = ns;
  bind_subtree.ruleset_generation = active.ruleset_generation;
  bind_subtree.layout_generation = 0;
  auto receipt = TAKE(hooks.on_mount(bind_subtree));
  CHECK(receipt.layout_generation == 1);
  CHECK(receipt.destination && receipt.destination->mount_id == 101);
  CHECK_OK(hooks.verify_covered(inside_bind, ns, active.ruleset_generation, 1));

  // A stale observer must not inherit authority from the new layout.
  CHECK_ERR(hooks.verify_covered(inside_bind, ns, active.ruleset_generation, 0),
            netsec::ErrorCode::StaleLayoutGeneration);

  netsec::MountRequest pivot;
  pivot.operation = netsec::MountOperation::PivotRoot;
  pivot.source_fd = child;
  pivot.namespace_generation = ns;
  pivot.ruleset_generation = active.ruleset_generation;
  pivot.layout_generation = 1;
  auto pivot_receipt = TAKE(hooks.on_pivot_root(pivot));
  CHECK(pivot_receipt.layout_generation == 2);
  CHECK(hooks.authorized_roots().size() == 1);
  CHECK(hooks.authorized_roots().front().identity ==
        fixture.resolver->identify(child).value());

  // The old sibling is no longer covered after the pivot.
  CHECK_ERR(hooks.verify_covered(sibling, ns, active.ruleset_generation, 2),
            netsec::ErrorCode::NotCoveredByAuthorizedRoot);

  netsec::MountRequest reattach_old;
  reattach_old.operation = netsec::MountOperation::MoveMount;
  reattach_old.source_fd = sibling;
  reattach_old.namespace_generation = ns;
  reattach_old.ruleset_generation = active.ruleset_generation;
  reattach_old.layout_generation = 2;
  CHECK_ERR(hooks.on_move_mount(reattach_old),
            netsec::ErrorCode::NotCoveredByAuthorizedRoot);
}

void test_concurrent_pivot_root() {
  Fixture fixture;
  const int root = fixture.resolver->add(identity(100, 1), -1, "/root");
  const int pivot_a =
      fixture.resolver->add(identity(100, 2), root, "/root/a");
  const int pivot_b =
      fixture.resolver->add(identity(100, 3), root, "/root/b");

  const std::uint64_t ns = 8;
  auto active = fixture.apply_root(root, netsec::access::kReadFile, ns);
  auto& hooks = *active.hooks;
  CHECK(hooks.layout_generation() == 0);

  std::atomic<int> ready{0};
  std::atomic<int> successes{0};
  std::atomic<int> stale{0};
  std::atomic<bool> go{false};
  std::mutex error_mutex;
  std::string unexpected;

  auto worker = [&](int source_fd) {
    ready.fetch_add(1, std::memory_order_release);
    while (!go.load(std::memory_order_acquire)) {
      std::this_thread::yield();
    }
    netsec::MountRequest request;
    request.operation = netsec::MountOperation::PivotRoot;
    request.source_fd = source_fd;
    request.namespace_generation = ns;
    request.ruleset_generation = active.ruleset_generation;
    request.layout_generation = 0;
    auto result = hooks.on_pivot_root(request);
    if (result) {
      successes.fetch_add(1, std::memory_order_relaxed);
    } else if (result.error().code ==
               netsec::ErrorCode::StaleLayoutGeneration) {
      stale.fetch_add(1, std::memory_order_relaxed);
    } else {
      std::lock_guard<std::mutex> lock(error_mutex);
      unexpected = result.error().message;
    }
  };

  std::thread a(worker, pivot_a);
  std::thread b(worker, pivot_b);
  while (ready.load(std::memory_order_acquire) != 2) {
    std::this_thread::yield();
  }
  go.store(true, std::memory_order_release);
  a.join();
  b.join();

  CHECK(unexpected.empty());
  CHECK(successes.load() == 1);
  CHECK(stale.load() == 1);
  CHECK(hooks.layout_generation() == 1);

  netsec::MountRequest unauthorized;
  unauthorized.operation = netsec::MountOperation::PivotRoot;
  unauthorized.source_fd = fixture.resolver->add(identity(300, 9), -1);
  unauthorized.namespace_generation = ns;
  unauthorized.ruleset_generation = active.ruleset_generation;
  unauthorized.layout_generation = 1;
  CHECK_ERR(hooks.on_pivot_root(unauthorized),
            netsec::ErrorCode::NotCoveredByAuthorizedRoot);
  CHECK(hooks.layout_generation() == 1);
}

void test_read_only_subtree() {
  Fixture fixture;
  const int root = fixture.resolver->add(identity(100, 1), -1);
  const int sub = fixture.resolver->add(identity(100, 2), root);
  const int ro_mount = fixture.resolver->add(identity(102, 2), root);
  const int under_ro = fixture.resolver->add(identity(102, 3), ro_mount);

  const std::uint64_t ns = 9;
  auto active = fixture.apply_root(root, netsec::access::kReadFile, ns);
  auto& hooks = *active.hooks;

  netsec::MountRequest ro_event;
  ro_event.operation = netsec::MountOperation::Mount;
  ro_event.source_fd = sub;
  ro_event.destination_fd = ro_mount;
  ro_event.namespace_generation = ns;
  ro_event.ruleset_generation = active.ruleset_generation;
  ro_event.layout_generation = 0;
  ro_event.read_only = true;
  auto receipt = TAKE(hooks.on_mount(ro_event));
  CHECK(receipt.layout_generation == 1);
  CHECK_OK(hooks.verify_covered(under_ro, ns, active.ruleset_generation, 1));

  // Re-mounting the read-only subtree writable would broaden the mount's
  // exposure, so it is rejected even though the inode is otherwise authorized.
  netsec::MountRequest writable_remount;
  writable_remount.operation = netsec::MountOperation::Mount;
  writable_remount.source_fd = under_ro;
  writable_remount.namespace_generation = ns;
  writable_remount.ruleset_generation = active.ruleset_generation;
  writable_remount.layout_generation = 1;
  writable_remount.read_only = false;
  CHECK_ERR(hooks.on_mount(writable_remount),
            netsec::ErrorCode::ReadOnlyViolation);
}

class RealResolver final : public netsec::ObjectResolver {
 public:
  netsec::Result<netsec::ObjectIdentity> identify(int fd) const override {
    struct stat st {};
    if (fd < 0 || ::fstat(fd, &st) != 0) {
      return netsec::Error{netsec::ErrorCode::InvalidFd,
                           "real resolver: fstat failed"};
    }
    netsec::ObjectIdentity id;
    id.mount_id = static_cast<std::uint64_t>(st.st_dev);
    id.device = static_cast<std::uint64_t>(st.st_dev);
    id.inode = static_cast<std::uint64_t>(st.st_ino);
    id.stable = true;
    return id;
  }

  netsec::Result<netsec::Ancestry> ancestry(int fd) const override {
    auto first = retain(fd);
    if (!first) {
      return first.error();
    }
    int current = first.value();
    netsec::Ancestry result;
    for (int depth = 0; depth < 256; ++depth) {
      auto current_id = identify(current);
      if (!current_id) {
        (void)::close(current);
        return current_id.error();
      }
      result.chain.push_back(current_id.value());
      const int parent =
          ::openat(current, "..", O_RDONLY | O_NOFOLLOW | O_CLOEXEC);
      if (parent < 0) {
        result.complete = false;
        break;
      }
      auto parent_id = identify(parent);
      if (!parent_id) {
        (void)::close(parent);
        (void)::close(current);
        return parent_id.error();
      }
      if (parent_id.value().inode == current_id.value().inode &&
          parent_id.value().device == current_id.value().device) {
        (void)::close(parent);
        result.reached_mount_root = true;
        break;
      }
      (void)::close(current);
      current = parent;
    }
    (void)::close(current);
    return result;
  }

  netsec::Result<int> retain(int fd) const override {
    const int copy = ::fcntl(fd, F_DUPFD_CLOEXEC, 3);
    if (copy < 0) {
      return netsec::Error{netsec::ErrorCode::IoError,
                           "real resolver: dup failed"};
    }
    return copy;
  }

  void release(int fd) const noexcept override {
    if (fd >= 0) {
      (void)::close(fd);
    }
  }
};

struct TempTree {
  std::filesystem::path root;

  TempTree() {
    const auto base = std::filesystem::temp_directory_path();
    root = base / ("netsec-tests-" + std::to_string(::getpid()) + "-" +
                   std::to_string(
                       std::chrono::steady_clock::now().time_since_epoch().count()));
    std::filesystem::create_directories(root / "sub");
    {
      std::ofstream(root / "sub" / "file") << "data";
    }
  }

  ~TempTree() {
    std::error_code ignored;
    std::filesystem::remove_all(root, ignored);
  }
};

void test_path_resolution_rejects_links_and_parent_traversal() {
  RealResolver resolver;
  TempTree tree;
  std::filesystem::create_symlink(tree.root / "sub",
                                  tree.root / "symlink");
  std::filesystem::create_directories(tree.root / "dev" / "fd");
  std::filesystem::create_symlink(tree.root / "sub",
                                  tree.root / "dev" / "fd" / "0");

  const int root_fd = ::open(tree.root.c_str(), O_RDONLY | O_DIRECTORY |
                                                  O_CLOEXEC);
  CHECK(root_fd >= 0);
  struct CloseFd {
    int fd;
    ~CloseFd() {
      if (fd >= 0) {
        (void)::close(fd);
      }
    }
  } closer{root_fd};

  CHECK_ERR(resolver.open_beneath(root_fd, ".."),
            netsec::ErrorCode::CrossMountTraversal);
  CHECK_ERR(resolver.open_beneath(root_fd, "/etc/passwd"),
            netsec::ErrorCode::InvalidArgument);
  CHECK_ERR(resolver.open_beneath(root_fd, "symlink"),
            netsec::ErrorCode::SymlinkRejected);
  CHECK_ERR(resolver.open_beneath(root_fd, "dev/fd/0"),
            netsec::ErrorCode::MagicLinkRejected);

  auto file = TAKE(resolver.open_beneath(root_fd, "sub/file"));
  (void)::close(file);
}

void test_unavailable_native_backend() {
#if !defined(__linux__)
  CHECK(!netsec::native_landlock_available());
  auto backend = netsec::make_native_landlock_backend();
  CHECK_ERR(backend->abi_version(), netsec::ErrorCode::UnsupportedPlatform);
#endif
}

struct TestCase {
  const char* name;
  void (*run)();
};

}  // namespace

int main() {
  const TestCase tests[] = {
      {"abi_and_access_trimming", test_abi_and_access_trimming},
      {"abi_failure_is_fail_closed", test_abi_failure_is_fail_closed},
      {"supported_masks_are_abi_exact", test_supported_masks_are_abi_exact},
      {"same_text_different_mount", test_same_text_different_mount_does_not_inherit},
      {"idmap_is_audit_only", test_idmap_is_audit_only},
      {"cross_mount_ancestry", test_cross_mount_ancestry_is_rejected},
      {"apply_revalidates_replaced_object",
       test_apply_revalidates_replaced_object},
      {"bind_subtree_and_pivot", test_bind_subtree_and_pivot_root_coverage},
      {"concurrent_pivot_root", test_concurrent_pivot_root},
      {"read_only_subtree", test_read_only_subtree},
      {"path_resolution", test_path_resolution_rejects_links_and_parent_traversal},
      {"unavailable_native_backend", test_unavailable_native_backend},
  };

  int failures = 0;
  for (const TestCase& test : tests) {
    try {
      test.run();
      std::cout << "PASS " << test.name << '\n';
    } catch (const TestFailure& failure) {
      ++failures;
      std::cerr << "FAIL " << test.name << ": " << failure.what() << '\n';
    } catch (const std::exception& failure) {
      ++failures;
      std::cerr << "FAIL " << test.name << ": unexpected exception: "
                << failure.what() << '\n';
    }
  }
  std::cout << (failures == 0 ? "all tests passed" : "tests failed") << '\n';
  return failures == 0 ? 0 : 1;
}
