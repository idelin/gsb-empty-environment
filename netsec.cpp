#if defined(__linux__) && !defined(_GNU_SOURCE)
#define _GNU_SOURCE
#endif

#include "netsec.hpp"

#include <algorithm>
#include <atomic>
#include <cerrno>
#include <fcntl.h>
#include <functional>
#include <mutex>
#include <sstream>
#include <system_error>
#include <sys/stat.h>
#include <sys/types.h>
#include <unistd.h>

#if defined(__linux__)
#include <linux/stat.h>
#include <sys/prctl.h>
#include <sys/syscall.h>
#include <sys/sysmacros.h>
#if defined(__has_include)
#if __has_include(<linux/landlock.h>)
#include <linux/landlock.h>
#define NETSEC_HAVE_LANDLOCK_UAPI 1
#endif
#endif
#endif

#if defined(__linux__) && !defined(NETSEC_HAVE_LANDLOCK_UAPI)
struct landlock_ruleset_attr_compat {
  std::uint64_t handled_access_fs;
};
struct landlock_path_beneath_attr_compat {
  std::uint64_t allowed_access;
  std::int32_t parent_fd;
} __attribute__((packed));
#define NETSEC_LANDLOCK_RULESET_ATTR landlock_ruleset_attr_compat
#define NETSEC_LANDLOCK_PATH_ATTR landlock_path_beneath_attr_compat
#endif

#if defined(__linux__) && defined(NETSEC_HAVE_LANDLOCK_UAPI)
#define NETSEC_LANDLOCK_RULESET_ATTR landlock_ruleset_attr
#define NETSEC_LANDLOCK_PATH_ATTR landlock_path_beneath_attr
#endif

#if defined(__linux__)
#ifndef LANDLOCK_CREATE_RULESET_VERSION
#define LANDLOCK_CREATE_RULESET_VERSION 1U
#endif
#ifndef LANDLOCK_RULE_PATH_BENEATH
#define LANDLOCK_RULE_PATH_BENEATH 1
#endif
#endif

namespace netsec {

#if defined(LANDLOCK_ACCESS_FS_EXECUTE)
static_assert(access::kExecute == LANDLOCK_ACCESS_FS_EXECUTE);
static_assert(access::kWriteFile == LANDLOCK_ACCESS_FS_WRITE_FILE);
static_assert(access::kReadFile == LANDLOCK_ACCESS_FS_READ_FILE);
static_assert(access::kReadDir == LANDLOCK_ACCESS_FS_READ_DIR);
static_assert(access::kRemoveDir == LANDLOCK_ACCESS_FS_REMOVE_DIR);
static_assert(access::kRemoveFile == LANDLOCK_ACCESS_FS_REMOVE_FILE);
static_assert(access::kMakeChar == LANDLOCK_ACCESS_FS_MAKE_CHAR);
static_assert(access::kMakeDir == LANDLOCK_ACCESS_FS_MAKE_DIR);
static_assert(access::kMakeReg == LANDLOCK_ACCESS_FS_MAKE_REG);
static_assert(access::kMakeSock == LANDLOCK_ACCESS_FS_MAKE_SOCK);
static_assert(access::kMakeFifo == LANDLOCK_ACCESS_FS_MAKE_FIFO);
static_assert(access::kMakeBlock == LANDLOCK_ACCESS_FS_MAKE_BLOCK);
static_assert(access::kMakeSym == LANDLOCK_ACCESS_FS_MAKE_SYM);
#if defined(LANDLOCK_ACCESS_FS_REFER)
static_assert(access::kRefer == LANDLOCK_ACCESS_FS_REFER);
#endif
#if defined(LANDLOCK_ACCESS_FS_TRUNCATE)
static_assert(access::kTruncate == LANDLOCK_ACCESS_FS_TRUNCATE);
#endif
#if defined(LANDLOCK_ACCESS_FS_IOCTL_DEV)
static_assert(access::kIoctlDev == LANDLOCK_ACCESS_FS_IOCTL_DEV);
#endif
#endif

namespace {

Error error(ErrorCode code, std::string message) {
  return Error{code, std::move(message)};
}

std::string with_errno(std::string_view what, int value = errno) {
  std::ostringstream out;
  out << what << ": "
      << std::error_code(value, std::generic_category()).message()
      << " (errno " << value << ')';
  return out.str();
}

std::atomic<std::uint64_t> g_plan_generation{1};
std::atomic<std::uint64_t> g_ruleset_generation{1};

std::uint64_t next_plan_generation() noexcept {
  return g_plan_generation.fetch_add(1, std::memory_order_relaxed);
}

std::uint64_t next_ruleset_generation() noexcept {
  return g_ruleset_generation.fetch_add(1, std::memory_order_relaxed);
}

#if defined(O_PATH)
constexpr int kPathOpenFlags = O_PATH;
#else
constexpr int kPathOpenFlags = O_RDONLY;
#endif

Result<int> duplicate_fd(int fd) {
  if (fd < 0) {
    return error(ErrorCode::InvalidFd, "negative file descriptor");
  }
#if defined(F_DUPFD_CLOEXEC)
  const int copy = ::fcntl(fd, F_DUPFD_CLOEXEC, 3);
#else
  const int copy = ::dup(fd);
  if (copy >= 0) {
    (void)::fcntl(copy, F_SETFD, FD_CLOEXEC);
  }
#endif
  if (copy < 0) {
    return error(ErrorCode::IoError, with_errno("dup failed"));
  }
  return copy;
}

bool same_mount_and_device(const ObjectIdentity& a, const ObjectIdentity& b) {
  return a.mount_id == b.mount_id && a.device == b.device;
}

Status validate_ancestry(const Ancestry& ancestry, const ObjectIdentity& self) {
  if (ancestry.chain.empty()) {
    return error(ErrorCode::UnstableObjectIdentity,
                 "object resolver returned an empty ancestry");
  }
  if (!ancestry.complete) {
    return error(ErrorCode::UnstableObjectIdentity,
                 "ancestry walk did not reach a stable mount boundary");
  }
  if (!(ancestry.chain.front() == self)) {
    return error(ErrorCode::IdentityChanged,
                 "ancestry root does not match the resolved object identity");
  }

  for (const ObjectIdentity& id : ancestry.chain) {
    if (!id.stable) {
      return error(
          ErrorCode::UnstableObjectIdentity,
          "object has neither a unique mount ID nor a stable file handle");
    }
  }

  for (std::size_t i = 1; i < ancestry.chain.size(); ++i) {
    if (!same_mount_and_device(ancestry.chain[i - 1], ancestry.chain[i])) {
      return error(ErrorCode::CrossMountTraversal,
                   "ancestry walk crossed a mount boundary via '..'");
    }
  }
  return Status{};
}

bool contains_idmap(const PlanRule& rule, const IdMapIdentity& idmap) {
  return std::find(rule.idmaps.begin(), rule.idmaps.end(), idmap) !=
         rule.idmaps.end();
}

bool contains_label(const PlanRule& rule, std::string_view label) {
  const std::string value(label);
  return std::find(rule.labels.begin(), rule.labels.end(), value) !=
         rule.labels.end();
}

class RetainedFd {
 public:
  RetainedFd(std::shared_ptr<const ObjectResolver> resolver, int fd)
      : resolver_(std::move(resolver)), fd_(fd) {}

  ~RetainedFd() {
    if (fd_ >= 0) {
      resolver_->release(fd_);
    }
  }

  RetainedFd(const RetainedFd&) = delete;
  RetainedFd& operator=(const RetainedFd&) = delete;

  [[nodiscard]] int get() const noexcept { return fd_; }

  int release() noexcept {
    const int fd = fd_;
    fd_ = -1;
    return fd;
  }

 private:
  std::shared_ptr<const ObjectResolver> resolver_;
  int fd_ = -1;
};

}  // namespace

AccessMask supported_fs_access(std::uint32_t abi) noexcept {
  switch (abi) {
    case 0:
      return 0;
    case 1:
      return access::kAbi1;
    case 2:
      return access::kAbi2;
    case 3:
    case 4:
      return access::kAbi3;
    default:
      return access::kAbi5;
  }
}

std::string to_string(AccessMask mask) {
  struct Name {
    AccessMask bit;
    const char* name;
  };
  static constexpr Name names[] = {
      {access::kExecute, "execute"},      {access::kWriteFile, "write_file"},
      {access::kReadFile, "read_file"},   {access::kReadDir, "read_dir"},
      {access::kRemoveDir, "remove_dir"}, {access::kRemoveFile, "remove_file"},
      {access::kMakeChar, "make_char"},   {access::kMakeDir, "make_dir"},
      {access::kMakeReg, "make_reg"},     {access::kMakeSock, "make_sock"},
      {access::kMakeFifo, "make_fifo"},   {access::kMakeBlock, "make_block"},
      {access::kMakeSym, "make_sym"},     {access::kRefer, "refer"},
      {access::kTruncate, "truncate"},    {access::kIoctlDev, "ioctl_dev"},
  };

  if (mask == 0) {
    return "none";
  }

  std::ostringstream out;
  bool first = true;
  for (const Name& entry : names) {
    if ((mask & entry.bit) == 0) {
      continue;
    }
    if (!first) {
      out << '|';
    }
    out << entry.name;
    first = false;
  }
  const AccessMask unknown = mask & ~access::kAllKnownFs;
  if (unknown != 0) {
    if (!first) {
      out << '|';
    }
    out << "unknown:0x" << std::hex << unknown;
  }
  return out.str();
}

const char* to_string(ErrorCode code) noexcept {
  switch (code) {
    case ErrorCode::InvalidArgument:
      return "InvalidArgument";
    case ErrorCode::InvalidFd:
      return "InvalidFd";
    case ErrorCode::InvalidPlan:
      return "InvalidPlan";
    case ErrorCode::InvalidAccessMask:
      return "InvalidAccessMask";
    case ErrorCode::UnsupportedAccessBits:
      return "UnsupportedAccessBits";
    case ErrorCode::NoAccessBits:
      return "NoAccessBits";
    case ErrorCode::UnsupportedAbi:
      return "UnsupportedAbi";
    case ErrorCode::AbiUnavailable:
      return "AbiUnavailable";
    case ErrorCode::UnstableObjectIdentity:
      return "UnstableObjectIdentity";
    case ErrorCode::CrossMountTraversal:
      return "CrossMountTraversal";
    case ErrorCode::SymlinkRejected:
      return "SymlinkRejected";
    case ErrorCode::MagicLinkRejected:
      return "MagicLinkRejected";
    case ErrorCode::PathReplaced:
      return "PathReplaced";
    case ErrorCode::IdentityChanged:
      return "IdentityChanged";
    case ErrorCode::StaleNamespaceGeneration:
      return "StaleNamespaceGeneration";
    case ErrorCode::StaleRulesetGeneration:
      return "StaleRulesetGeneration";
    case ErrorCode::StaleLayoutGeneration:
      return "StaleLayoutGeneration";
    case ErrorCode::NotCoveredByAuthorizedRoot:
      return "NotCoveredByAuthorizedRoot";
    case ErrorCode::ReadOnlyViolation:
      return "ReadOnlyViolation";
    case ErrorCode::RulesetCreateFailed:
      return "RulesetCreateFailed";
    case ErrorCode::RulesetRuleFailed:
      return "RulesetRuleFailed";
    case ErrorCode::RestrictFailed:
      return "RestrictFailed";
    case ErrorCode::NoNewPrivsFailed:
      return "NoNewPrivsFailed";
    case ErrorCode::IoError:
      return "IoError";
    case ErrorCode::BackendMismatch:
      return "BackendMismatch";
    case ErrorCode::UnsupportedPlatform:
      return "UnsupportedPlatform";
    case ErrorCode::InternalError:
      return "InternalError";
  }
  return "UnknownErrorCode";
}

std::size_t ObjectIdentityHash::operator()(const ObjectIdentity& id) const
    noexcept {
  std::size_t seed = std::hash<std::uint64_t>{}(id.mount_id);
  const auto mix = [&seed](std::uint64_t value) {
    seed ^= std::hash<std::uint64_t>{}(value) + 0x9e3779b97f4a7c15ULL +
            (seed << 6) + (seed >> 2);
  };
  mix(id.inode);
  mix(id.device);
  mix(id.inode_generation);
  mix(id.stable ? 1U : 0U);
  for (std::uint8_t byte : id.file_handle) {
    mix(byte);
  }
  return seed;
}

Result<int> ObjectResolver::retain(int fd) const { return duplicate_fd(fd); }

void ObjectResolver::release(int fd) const noexcept {
  if (fd >= 0) {
    (void)::close(fd);
  }
}

Result<int> ObjectResolver::open_beneath(int root_fd,
                                         std::string_view relative_path) const {
  if (root_fd < 0) {
    return error(ErrorCode::InvalidFd, "negative rule-root fd");
  }
  if (relative_path.empty() || relative_path.front() == '/') {
    return error(ErrorCode::InvalidArgument,
                 "open_beneath requires a non-empty relative path");
  }
  if (relative_path.find('\0') != std::string_view::npos) {
    return error(ErrorCode::InvalidArgument,
                 "open_beneath path contains an embedded NUL");
  }

  auto root_identity = identify(root_fd);
  if (!root_identity) {
    return root_identity.error();
  }
  if (root_identity.value().empty() || !root_identity.value().stable) {
    return error(ErrorCode::UnstableObjectIdentity,
                 "open_beneath root has no stable object identity");
  }

  int current = root_fd;
  bool owns_current = false;
  bool saw_component = false;
  bool in_proc = false;
  bool in_dev = false;
  bool previous_was_fd = false;

  std::size_t begin = 0;
  while (begin <= relative_path.size()) {
    const std::size_t end = relative_path.find('/', begin);
    const std::string_view component =
        relative_path.substr(begin, end == std::string_view::npos
                                        ? std::string_view::npos
                                        : end - begin);
    begin = (end == std::string_view::npos) ? relative_path.size() + 1
                                            : end + 1;
    if (component.empty() || component == ".") {
      continue;
    }
    if (component == "..") {
      if (owns_current) {
        (void)::close(current);
      }
      return error(ErrorCode::CrossMountTraversal,
                   "parent traversal is not permitted beneath a rule root");
    }
    saw_component = true;

    const bool is_proc = component == "proc";
    const bool is_dev = component == "dev";
    const bool fd_component = component == "fd";
    const bool magic_link_context =
        (in_proc || in_dev) && (fd_component || previous_was_fd);

    std::string name(component);
    const int next = ::openat(current, name.c_str(),
                              kPathOpenFlags | O_NOFOLLOW | O_CLOEXEC);
    if (next < 0) {
      const int saved = errno;
      if (owns_current) {
        (void)::close(current);
      }
      if (saved == ELOOP) {
        return error(magic_link_context ? ErrorCode::MagicLinkRejected
                                        : ErrorCode::SymlinkRejected,
                     with_errno("refusing to follow a link", saved));
      }
      return error(ErrorCode::IoError, with_errno("openat failed", saved));
    }

    struct stat st {};
    if (::fstat(next, &st) != 0) {
      const int saved = errno;
      (void)::close(next);
      if (owns_current) {
        (void)::close(current);
      }
      return error(ErrorCode::IoError, with_errno("fstat failed", saved));
    }
    if (S_ISLNK(st.st_mode)) {
      (void)::close(next);
      if (owns_current) {
        (void)::close(current);
      }
      return error(magic_link_context ? ErrorCode::MagicLinkRejected
                                      : ErrorCode::SymlinkRejected,
                   "path component is a symlink or magic link");
    }

    // A component that changes the mount is a mount traversal, even if it is
    // not the final component.
    auto parent_id = identify(current);
    auto child_id = identify(next);
    if (!parent_id || !child_id) {
      (void)::close(next);
      if (owns_current) {
        (void)::close(current);
      }
      return !parent_id ? parent_id.error() : child_id.error();
    }
    if (!parent_id->stable || !child_id->stable) {
      (void)::close(next);
      if (owns_current) {
        (void)::close(current);
      }
      return error(ErrorCode::UnstableObjectIdentity,
                   "open_beneath component has no stable object identity");
    }
    if (parent_id->mount_id != child_id->mount_id ||
        parent_id->device != child_id->device) {
      (void)::close(next);
      if (owns_current) {
        (void)::close(current);
      }
      return error(ErrorCode::CrossMountTraversal,
                   "path resolution crossed a mount boundary");
    }

    const bool final_component = (end == std::string_view::npos);
    if (!final_component && !S_ISDIR(st.st_mode)) {
      (void)::close(next);
      if (owns_current) {
        (void)::close(current);
      }
      return error(ErrorCode::InvalidArgument,
                   "non-directory used as an intermediate path component");
    }

    if (owns_current) {
      (void)::close(current);
    }
    current = next;
    owns_current = true;
    in_proc = in_proc || is_proc;
    in_dev = in_dev || is_dev;
    previous_was_fd = fd_component;
  }

  if (!saw_component) {
    return error(ErrorCode::InvalidArgument,
                 "open_beneath path has no components");
  }
  return current;
}

struct RulePlan::Impl {
  std::shared_ptr<const ObjectResolver> resolver;
  std::shared_ptr<LandlockBackend> kernel;
  std::uint32_t abi = 0;
  AccessMask requested = 0;
  AccessMask handled = 0;
  AccessMask trimmed = 0;
  MountNamespace mount_namespace{};
  std::uint64_t plan_generation = 0;
  std::vector<PlanRule> rules;
  std::vector<int> held_fds;

  ~Impl() { release_fds(); }

  void release_fds() noexcept {
    if (!resolver) {
      return;
    }
    for (int fd : held_fds) {
      resolver->release(fd);
    }
    held_fds.clear();
  }
};

RulePlan::RulePlan() noexcept = default;
RulePlan::RulePlan(RulePlan&&) noexcept = default;
RulePlan& RulePlan::operator=(RulePlan&&) noexcept = default;
RulePlan::~RulePlan() = default;

bool RulePlan::valid() const noexcept { return impl_ != nullptr; }

std::uint32_t RulePlan::abi() const noexcept { return impl_ ? impl_->abi : 0; }

AccessMask RulePlan::requested_access() const noexcept {
  return impl_ ? impl_->requested : 0;
}

AccessMask RulePlan::handled_access() const noexcept {
  return impl_ ? impl_->handled : 0;
}

AccessMask RulePlan::trimmed_access() const noexcept {
  return impl_ ? impl_->trimmed : 0;
}

MountNamespace RulePlan::mount_namespace() const noexcept {
  return impl_ ? impl_->mount_namespace : MountNamespace{};
}

std::uint64_t RulePlan::plan_generation() const noexcept {
  return impl_ ? impl_->plan_generation : 0;
}

const std::vector<PlanRule>& RulePlan::rules() const noexcept {
  static const std::vector<PlanRule> empty;
  return impl_ ? impl_->rules : empty;
}

Planner::Planner(std::shared_ptr<const ObjectResolver> resolver,
                 std::shared_ptr<LandlockBackend> kernel)
    : resolver_(std::move(resolver)), kernel_(std::move(kernel)) {}

Result<RulePlan> Planner::plan(const RuleRoot& root,
                               const PlannerConfig& config) const {
  return plan(std::span<const RuleRoot>(&root, 1), config);
}

Result<RulePlan> Planner::plan(std::span<const RuleRoot> roots,
                               const PlannerConfig& config) const {
  if (!resolver_ || !kernel_) {
    return error(ErrorCode::InvalidArgument,
                 "planner requires both an object resolver and a backend");
  }
  if (roots.empty()) {
    return error(ErrorCode::InvalidArgument, "no rule roots were supplied");
  }

  const auto abi_result = kernel_->abi_version();
  if (!abi_result) {
    return error(abi_result.error().code,
                 "cannot determine the Landlock ABI; refusing to guess: " +
                     abi_result.error().message);
  }
  const std::uint32_t abi = abi_result.value();
  if (abi < 1) {
    return error(ErrorCode::UnsupportedAbi,
                 "the kernel does not advertise a usable Landlock ABI");
  }
  const AccessMask supported = supported_fs_access(abi);

  auto impl = std::make_unique<RulePlan::Impl>();
  impl->resolver = resolver_;
  impl->kernel = kernel_;
  impl->abi = abi;
  impl->mount_namespace = config.mount_namespace;
  impl->plan_generation = next_plan_generation();

  for (const RuleRoot& root : roots) {
    if (root.fd < 0) {
      return error(ErrorCode::InvalidFd,
                   "rule root '" + root.label + "' has a negative fd");
    }
    if (root.access == 0) {
      return error(ErrorCode::NoAccessBits,
                   "rule root '" + root.label + "' requests no access");
    }

    const AccessMask unknown = root.access & ~access::kAllKnownFs;
    if (unknown != 0) {
      return error(ErrorCode::InvalidAccessMask,
                   "rule root '" + root.label +
                       "' requests access bits unknown to this library: " +
                       to_string(unknown));
    }

    const AccessMask unsupported = root.access & ~supported;
    if (unsupported != 0 &&
        config.unsupported_access == UnsupportedAccessPolicy::FailClosed) {
      return error(
          ErrorCode::UnsupportedAccessBits,
          "Landlock ABI " + std::to_string(abi) +
              " cannot enforce requested access (" + to_string(unsupported) +
              "); failing closed instead of silently broadening access");
    }
    const AccessMask effective = root.access & supported;
    if (effective == 0) {
      return error(ErrorCode::NoAccessBits,
                   "rule root '" + root.label +
                       "' has no access bits enforceable by ABI " +
                       std::to_string(abi));
    }

    // Pin the object before resolving it.  If the caller's fd were closed and
    // reused concurrently, resolving first and retaining later could pair one
    // object's identity with another object's descriptor.
    auto retained = resolver_->retain(root.fd);
    if (!retained) {
      return error(static_cast<ErrorCode>(retained.error().code),
                   "cannot retain rule root '" + root.label + "': " +
                       retained.error().message);
    }
    RetainedFd held(resolver_, retained.value());

    auto identity_result = resolver_->identify(held.get());
    if (!identity_result) {
      return error(static_cast<ErrorCode>(identity_result.error().code),
                   "cannot identify rule root '" + root.label + "': " +
                       identity_result.error().message);
    }
    const ObjectIdentity identity = identity_result.value();
    if (identity.empty()) {
      return error(ErrorCode::UnstableObjectIdentity,
                   "rule root '" + root.label + "' has an empty identity");
    }
    if (!identity.stable) {
      return error(ErrorCode::UnstableObjectIdentity,
                   "rule root '" + root.label +
                       "' has no stable mount ID or file handle");
    }

    auto ancestry_result = resolver_->ancestry(held.get());
    if (!ancestry_result) {
      return error(static_cast<ErrorCode>(ancestry_result.error().code),
                   "cannot resolve rule root '" + root.label +
                       "' ancestry: " + ancestry_result.error().message);
    }
    const Status ancestry_status =
        validate_ancestry(ancestry_result.value(), identity);
    if (!ancestry_status) {
      return ancestry_status.error();
    }

    // The fd pins the object, but re-resolve it once more after walking the
    // ancestry.  This rejects a resolver whose object identity changed while
    // the hierarchy was being inspected.
    auto recheck = resolver_->identify(held.get());
    if (!recheck) {
      return recheck.error();
    }
    if (!(recheck.value() == identity)) {
      return error(ErrorCode::PathReplaced,
                   "rule root '" + root.label +
                       "' changed identity during planning");
    }

    auto existing = std::find_if(
        impl->rules.begin(), impl->rules.end(),
        [&](const PlanRule& rule) { return rule.identity == identity; });
    if (existing != impl->rules.end()) {
      existing->access |= effective;
      if (root.idmap && !contains_idmap(*existing, *root.idmap)) {
        existing->idmaps.push_back(*root.idmap);
      }
      if (!root.label.empty() && !contains_label(*existing, root.label)) {
        existing->labels.push_back(root.label);
      }
      // `held` releases the duplicate at the end of this iteration.
    } else {
      PlanRule rule;
      rule.identity = identity;
      rule.access = effective;
      if (root.idmap) {
        rule.idmaps.push_back(*root.idmap);
      }
      if (!root.label.empty()) {
        rule.labels.push_back(root.label);
      }
      impl->rules.push_back(std::move(rule));
      impl->held_fds.push_back(held.release());
    }

    impl->requested |= root.access;
    impl->handled |= effective;
    impl->trimmed |= unsupported;
  }

  if (impl->handled == 0 || impl->rules.empty()) {
    return error(ErrorCode::NoAccessBits,
                 "the plan contains no enforceable rule");
  }

  RulePlan plan;
  plan.impl_ = std::move(impl);
  return plan;
}

struct HookRootRecord {
  PlanRule rule;
  bool read_only = false;
};

struct RuntimeHooks::Impl {
  std::shared_ptr<const ObjectResolver> resolver;
  mutable std::mutex mutex;
  MountNamespace mount_namespace{};
  std::uint64_t ruleset_generation = 0;
  std::uint64_t layout_generation = 0;
  std::vector<HookRootRecord> roots;
};

RuntimeHooks::RuntimeHooks(std::shared_ptr<const ObjectResolver> resolver,
                           MountNamespace mount_namespace,
                           std::uint64_t ruleset_generation,
                           std::vector<PlanRule> roots)
    : impl_(std::make_shared<Impl>()) {
  impl_->resolver = std::move(resolver);
  impl_->mount_namespace = mount_namespace;
  impl_->ruleset_generation = ruleset_generation;
  impl_->roots.reserve(roots.size());
  for (PlanRule& rule : roots) {
    impl_->roots.push_back(HookRootRecord{std::move(rule), false});
  }
}

namespace {

struct Coverage {
  AccessMask access = 0;
  bool any_writable = false;
  ObjectIdentity source;
};

Result<Coverage> find_coverage_locked(
    const std::shared_ptr<const ObjectResolver>& resolver,
    const std::vector<HookRootRecord>& roots, int fd) {
  auto identity_result = resolver->identify(fd);
  if (!identity_result) {
    return identity_result.error();
  }
  const ObjectIdentity identity = identity_result.value();
  if (identity.empty()) {
    return error(ErrorCode::UnstableObjectIdentity,
                 "mount event source has an empty object identity");
  }
  if (!identity.stable) {
    return error(ErrorCode::UnstableObjectIdentity,
                 "mount event source has no stable mount ID or file handle");
  }

  auto ancestry_result = resolver->ancestry(fd);
  if (!ancestry_result) {
    return ancestry_result.error();
  }
  const Ancestry& ancestry = ancestry_result.value();
  if (ancestry.chain.empty() || !(ancestry.chain.front() == identity)) {
    return error(ErrorCode::IdentityChanged,
                 "mount event ancestry does not start at the resolved object");
  }
  if (!ancestry.complete) {
    return error(ErrorCode::UnstableObjectIdentity,
                 "mount event ancestry walk was incomplete");
  }
  for (std::size_t i = 1; i < ancestry.chain.size(); ++i) {
    if (!same_mount_and_device(ancestry.chain[i - 1], ancestry.chain[i])) {
      return error(ErrorCode::CrossMountTraversal,
                   "mount event ancestry crossed a mount boundary");
    }
  }

  Coverage coverage;
  coverage.source = identity;
  for (const HookRootRecord& root : roots) {
    const bool covers =
        root.rule.identity == identity ||
        std::find(ancestry.chain.begin(), ancestry.chain.end(),
                  root.rule.identity) != ancestry.chain.end();
    if (!covers) {
      continue;
    }
    coverage.access |= root.rule.access;
    if (!root.read_only) {
      coverage.any_writable = true;
    }
  }
  if (coverage.access == 0) {
    return error(ErrorCode::NotCoveredByAuthorizedRoot,
                 "object is not covered by any authorized rule root");
  }
  return coverage;
}

}  // namespace

Result<MountReceipt> RuntimeHooks::on_mount(const MountRequest& request) {
  if (request.operation != MountOperation::Mount) {
    return error(ErrorCode::InvalidArgument,
                 "on_mount received a non-mount operation");
  }
  std::lock_guard<std::mutex> lock(impl_->mutex);
  if (request.namespace_generation != impl_->mount_namespace.generation) {
    return error(ErrorCode::StaleNamespaceGeneration,
                 "mount event observed a different mount namespace generation");
  }
  if (request.ruleset_generation != impl_->ruleset_generation) {
    return error(ErrorCode::StaleRulesetGeneration,
                 "mount event observed a different ruleset generation");
  }
  if (request.layout_generation != impl_->layout_generation) {
    return error(ErrorCode::StaleLayoutGeneration,
                 "mount event observed a stale layout generation");
  }

  auto coverage = find_coverage_locked(
      impl_->resolver, impl_->roots, request.source_fd);
  if (!coverage) {
    return coverage.error();
  }
  if (!request.read_only && !coverage.value().any_writable) {
    return error(ErrorCode::ReadOnlyViolation,
                 "writable mount would expose a read-only authorized subtree");
  }

  std::optional<ObjectIdentity> destination;
  if (request.destination_fd >= 0) {
    auto destination_result = impl_->resolver->identify(request.destination_fd);
    if (!destination_result) {
      return destination_result.error();
    }
    if (!destination_result.value().stable) {
      return error(ErrorCode::UnstableObjectIdentity,
                   "destination mount has no stable identity");
    }
    destination = destination_result.value();

    // A bind or move of an authorized source must expose the same
    // inode/device at the new mount root.  Without this check a caller could
    // pass a covered source fd plus an unrelated destination fd and trick the
    // hook into authorizing the unrelated object.
    if (!(destination->inode == coverage.value().source.inode &&
          destination->device == coverage.value().source.device)) {
      return error(ErrorCode::IdentityChanged,
                   "destination mount root is not the proven source object");
    }
  }

  const std::uint64_t next_layout = impl_->layout_generation + 1;
  if (destination) {
    auto existing = std::find_if(
        impl_->roots.begin(), impl_->roots.end(),
        [&](const HookRootRecord& root) {
          return root.rule.identity == *destination;
        });
    if (existing == impl_->roots.end()) {
      PlanRule rule;
      rule.identity = *destination;
      rule.access = coverage.value().access;
      rule.labels.push_back("<mount-event>");
      impl_->roots.push_back(HookRootRecord{std::move(rule), request.read_only});
    } else {
      existing->rule.access |= coverage.value().access;
      if (!request.read_only) {
        existing->read_only = false;
      }
    }
  }
  impl_->layout_generation = next_layout;

  MountReceipt receipt;
  receipt.layout_generation = next_layout;
  receipt.source = coverage.value().source;
  receipt.destination = std::move(destination);
  receipt.access = coverage.value().access;
  return receipt;
}

Result<MountReceipt> RuntimeHooks::on_move_mount(const MountRequest& request) {
  if (request.operation != MountOperation::MoveMount) {
    return error(ErrorCode::InvalidArgument,
                 "on_move_mount received a non-move operation");
  }
  MountRequest copy = request;
  copy.operation = MountOperation::Mount;
  return on_mount(copy);
}

Result<MountReceipt> RuntimeHooks::on_pivot_root(const MountRequest& request) {
  if (request.operation != MountOperation::PivotRoot) {
    return error(ErrorCode::InvalidArgument,
                 "on_pivot_root received a non-pivot operation");
  }
  std::lock_guard<std::mutex> lock(impl_->mutex);
  if (request.namespace_generation != impl_->mount_namespace.generation) {
    return error(ErrorCode::StaleNamespaceGeneration,
                 "pivot_root observed a different mount namespace generation");
  }
  if (request.ruleset_generation != impl_->ruleset_generation) {
    return error(ErrorCode::StaleRulesetGeneration,
                 "pivot_root observed a different ruleset generation");
  }
  if (request.layout_generation != impl_->layout_generation) {
    return error(ErrorCode::StaleLayoutGeneration,
                 "pivot_root observed a stale layout generation");
  }

  auto coverage = find_coverage_locked(
      impl_->resolver, impl_->roots, request.source_fd);
  if (!coverage) {
    return coverage.error();
  }
  if (!request.read_only && !coverage.value().any_writable) {
    return error(ErrorCode::ReadOnlyViolation,
                 "writable pivot would expose a read-only authorized subtree");
  }

  std::optional<ObjectIdentity> destination;
  if (request.destination_fd >= 0) {
    auto destination_result = impl_->resolver->identify(request.destination_fd);
    if (!destination_result) {
      return destination_result.error();
    }
    if (!(destination_result.value() == coverage.value().source)) {
      return error(ErrorCode::IdentityChanged,
                   "pivot_root destination is not the proven new root");
    }
    destination = destination_result.value();
  }

  // After pivot_root only the new root remains reachable.  Keeping authority
  // for old siblings would let a later mount reintroduce them by object
  // identity, so replace the authorization set rather than extending it.
  PlanRule pivot_root;
  pivot_root.identity = coverage.value().source;
  pivot_root.access = coverage.value().access;
  pivot_root.labels.push_back("<pivot-root>");
  impl_->roots.clear();
  impl_->roots.push_back(HookRootRecord{std::move(pivot_root), request.read_only});
  impl_->layout_generation += 1;

  MountReceipt receipt;
  receipt.layout_generation = impl_->layout_generation;
  receipt.source = coverage.value().source;
  receipt.destination = std::move(destination);
  receipt.access = coverage.value().access;
  return receipt;
}

Status RuntimeHooks::verify_covered(int fd, std::uint64_t namespace_generation,
                                    std::uint64_t ruleset_generation,
                                    std::uint64_t layout_generation) const {
  std::lock_guard<std::mutex> lock(impl_->mutex);
  if (namespace_generation != impl_->mount_namespace.generation) {
    return error(ErrorCode::StaleNamespaceGeneration,
                 "coverage check used a stale namespace generation");
  }
  if (ruleset_generation != impl_->ruleset_generation) {
    return error(ErrorCode::StaleRulesetGeneration,
                 "coverage check used a stale ruleset generation");
  }
  if (layout_generation != impl_->layout_generation) {
    return error(ErrorCode::StaleLayoutGeneration,
                 "coverage check used a stale layout generation");
  }
  auto coverage = find_coverage_locked(
      impl_->resolver, impl_->roots, fd);
  if (!coverage) {
    return coverage.error();
  }
  return Status{};
}

std::uint64_t RuntimeHooks::layout_generation() const noexcept {
  std::lock_guard<std::mutex> lock(impl_->mutex);
  return impl_->layout_generation;
}

MountNamespace RuntimeHooks::mount_namespace() const noexcept {
  return impl_->mount_namespace;
}

std::uint64_t RuntimeHooks::ruleset_generation() const noexcept {
  return impl_->ruleset_generation;
}

std::vector<PlanRule> RuntimeHooks::authorized_roots() const {
  std::lock_guard<std::mutex> lock(impl_->mutex);
  std::vector<PlanRule> result;
  result.reserve(impl_->roots.size());
  for (const HookRootRecord& root : impl_->roots) {
    result.push_back(root.rule);
  }
  return result;
}

Result<ActiveRuleset> apply(RulePlan&& plan, const ApplyOptions& options) {
  (void)options;
  if (!plan.impl_) {
    return error(ErrorCode::InvalidPlan, "apply called with an empty plan");
  }
  auto impl = std::move(plan.impl_);
  if (!impl->resolver || !impl->kernel) {
    return error(ErrorCode::InvalidPlan,
                 "plan is missing its resolver or kernel backend");
  }
  if (impl->handled == 0 || impl->rules.empty() ||
      impl->held_fds.size() != impl->rules.size()) {
    return error(ErrorCode::InvalidPlan, "plan is internally inconsistent");
  }

  // Re-resolve every retained object before creating a ruleset.  The retained
  // fd should pin the object, but an injected resolver may use an equivalent
  // authority token which can change; never create rules from a stale token.
  for (std::size_t i = 0; i < impl->rules.size(); ++i) {
    auto identity = impl->resolver->identify(impl->held_fds[i]);
    if (!identity) {
      return error(static_cast<ErrorCode>(identity.error().code),
                   "cannot revalidate planned rule: " +
                       identity.error().message);
    }
    if (!(identity.value() == impl->rules[i].identity)) {
      return error(ErrorCode::PathReplaced,
                   "planned rule object was replaced between plan and apply");
    }
    if (!identity.value().stable) {
      return error(ErrorCode::UnstableObjectIdentity,
                   "planned rule object lost its stable identity");
    }
    auto ancestry = impl->resolver->ancestry(impl->held_fds[i]);
    if (!ancestry) {
      return error(static_cast<ErrorCode>(ancestry.error().code),
                   "cannot revalidate planned ancestry: " +
                       ancestry.error().message);
    }
    const Status valid =
        validate_ancestry(ancestry.value(), identity.value());
    if (!valid) {
      return valid.error();
    }
  }

  const auto abi = impl->kernel->abi_version();
  if (!abi) {
    return error(ErrorCode::AbiUnavailable,
                 "Landlock ABI disappeared before ruleset creation: " +
                     abi.error().message);
  }
  if (abi.value() != impl->abi) {
    return error(ErrorCode::BackendMismatch,
                 "Landlock ABI changed between plan and apply");
  }

  const std::uint64_t ruleset_generation = next_ruleset_generation();
  auto hooks = std::shared_ptr<RuntimeHooks>(new RuntimeHooks(
      impl->resolver, impl->mount_namespace, ruleset_generation, impl->rules));

  auto ruleset_result = impl->kernel->create_ruleset(impl->handled);
  if (!ruleset_result) {
    return error(ErrorCode::RulesetCreateFailed,
                 "landlock_create_ruleset failed: " +
                     ruleset_result.error().message);
  }
  const int ruleset_fd = ruleset_result.value();
  const auto close_ruleset = [&]() { impl->kernel->close_fd(ruleset_fd); };

  for (std::size_t i = 0; i < impl->rules.size(); ++i) {
    const Status added = impl->kernel->add_path_beneath(
        ruleset_fd, impl->held_fds[i], impl->rules[i].access);
    if (!added) {
      close_ruleset();
      return error(ErrorCode::RulesetRuleFailed,
                   "landlock_add_rule failed: " + added.error().message);
    }
  }

  const Status prepared = impl->kernel->prepare_restrict();
  if (!prepared) {
    close_ruleset();
    return error(ErrorCode::NoNewPrivsFailed,
                 "could not set no_new_privs: " + prepared.error().message);
  }

  const Status restricted = impl->kernel->restrict_self(ruleset_fd);
  if (!restricted) {
    close_ruleset();
    return error(ErrorCode::RestrictFailed,
                 "landlock_restrict_self failed: " +
                     restricted.error().message);
  }
  close_ruleset();

  // The ruleset is now attached to this thread.  Drop the plan's retained fds
  // only after the kernel has accepted every rule.
  impl->release_fds();

  ActiveRuleset active;
  active.abi = impl->abi;
  active.requested_access = impl->requested;
  active.handled_access = impl->handled;
  active.trimmed_access = impl->trimmed;
  active.mount_namespace = impl->mount_namespace;
  active.plan_generation = impl->plan_generation;
  active.ruleset_generation = ruleset_generation;
  active.hooks = std::move(hooks);
  return active;
}

#if defined(__linux__)
namespace {

std::uint64_t combine_device(std::uint32_t major, std::uint32_t minor) {
  return (static_cast<std::uint64_t>(major) << 32) | minor;
}

std::uint64_t stat_device(dev_t dev) {
  return combine_device(static_cast<std::uint32_t>(major(dev)),
                        static_cast<std::uint32_t>(minor(dev)));
}

class LinuxObjectResolver final : public ObjectResolver {
 public:
  Result<ObjectIdentity> identify(int fd) const override {
    if (fd < 0) {
      return error(ErrorCode::InvalidFd, "negative fd");
    }

    ObjectIdentity identity;
    bool have_statx = false;
#if defined(SYS_statx)
    struct statx stx {};
    unsigned int mask = STATX_BASIC_STATS | STATX_MNT_ID;
#if defined(STATX_MNT_ID_UNIQUE)
    mask |= STATX_MNT_ID_UNIQUE;
#endif
    long result = ::syscall(SYS_statx, fd, "",
                            AT_EMPTY_PATH | AT_SYMLINK_NOFOLLOW, mask, &stx);
    if (result != 0 && errno == EINVAL) {
      mask = STATX_BASIC_STATS | STATX_MNT_ID;
      result = ::syscall(SYS_statx, fd, "", AT_EMPTY_PATH | AT_SYMLINK_NOFOLLOW,
                         mask, &stx);
    }
    have_statx = (result == 0);
    if (have_statx) {
      identity.mount_id = stx.stx_mnt_id;
      identity.inode = stx.stx_ino;
      identity.device = combine_device(stx.stx_dev_major, stx.stx_dev_minor);
#if defined(STATX_MNT_ID_UNIQUE)
      if ((stx.stx_mask & STATX_MNT_ID_UNIQUE) != 0) {
        identity.stable = true;
      }
#endif
    }
#endif
    if (!have_statx) {
      struct stat st {};
      if (::fstat(fd, &st) != 0) {
        return error(ErrorCode::IoError, with_errno("fstat failed"));
      }
      identity.inode = static_cast<std::uint64_t>(st.st_ino);
      identity.device = stat_device(st.st_dev);
    }

    // A handle pins both inode generation and the containing filesystem.  It
    // is optional: some filesystems do not support name_to_handle_at().
    struct HandleBuffer {
      struct file_handle handle;
      unsigned char bytes[128];
    } buffer {};
    buffer.handle.handle_bytes = sizeof(buffer.bytes);
    int handle_mount_id = 0;
    if (::name_to_handle_at(fd, "", &buffer.handle, &handle_mount_id,
                            AT_EMPTY_PATH) == 0) {
      identity.file_handle.assign(
          buffer.handle.f_handle,
          buffer.handle.f_handle + buffer.handle.handle_bytes);
      if (identity.mount_id == 0) {
        identity.mount_id = static_cast<std::uint64_t>(handle_mount_id);
      }
      identity.stable = true;
    }

    if (identity.mount_id == 0) {
      // Without a mount ID the object cannot be distinguished from an object
      // in another mount.  Keep it unstable unless a file handle was found.
      identity.stable = !identity.file_handle.empty();
    }
    return identity;
  }

  Result<Ancestry> ancestry(int fd) const override {
    auto first = retain(fd);
    if (!first) {
      return first.error();
    }
    int current = first.value();
    Ancestry result;

    for (std::size_t depth = 0; depth < 4096; ++depth) {
      auto current_id = identify(current);
      if (!current_id) {
        (void)::close(current);
        return current_id.error();
      }
      result.chain.push_back(current_id.value());

      struct stat st {};
      if (::fstat(current, &st) != 0) {
        const int saved = errno;
        (void)::close(current);
        return error(ErrorCode::IoError,
                     with_errno("fstat failed during ancestry walk", saved));
      }
      if (!S_ISDIR(st.st_mode)) {
        // A file rule root has no useful ancestor chain within its mount.
        result.reached_mount_root = true;
        break;
      }

      const int parent = ::openat(
          current, "..",
          kPathOpenFlags | O_DIRECTORY | O_NOFOLLOW | O_CLOEXEC);
      if (parent < 0) {
        (void)::close(current);
        result.complete = false;
        return result;
      }
      auto parent_id = identify(parent);
      if (!parent_id) {
        (void)::close(parent);
        (void)::close(current);
        return parent_id.error();
      }
      if (parent_id.value() == current_id.value()) {
        (void)::close(parent);
        result.reached_mount_root = true;
        break;
      }
      if (!same_mount_and_device(parent_id.value(), current_id.value())) {
        // Do not follow '..' across a mount.  The chain ends at this mount's
        // root and the cross-mount parent is deliberately not included.
        (void)::close(parent);
        result.reached_mount_root = true;
        break;
      }
      (void)::close(current);
      current = parent;
    }

    if (result.chain.size() >= 4096 && !result.reached_mount_root) {
      (void)::close(current);
      return error(ErrorCode::UnstableObjectIdentity,
                   "ancestry walk exceeded its depth limit");
    }
    (void)::close(current);
    return result;
  }
};

class LinuxLandlockBackend final : public LandlockBackend {
 public:
  Result<std::uint32_t> abi_version() const override {
#if defined(__NR_landlock_create_ruleset)
    const long result = ::syscall(__NR_landlock_create_ruleset, nullptr, 0,
                                  LANDLOCK_CREATE_RULESET_VERSION);
#else
    const long result = -1;
    errno = ENOSYS;
#endif
    if (result < 0) {
      if (errno == ENOSYS || errno == EOPNOTSUPP || errno == EINVAL) {
        return error(ErrorCode::AbiUnavailable,
                     "Landlock is not available in this kernel");
      }
      return error(ErrorCode::IoError,
                   with_errno("landlock_create_ruleset(VERSION) failed"));
    }
    if (result < 1) {
      return error(ErrorCode::UnsupportedAbi,
                   "Landlock ABI version is not usable");
    }
    return static_cast<std::uint32_t>(result);
  }

  Result<int> create_ruleset(AccessMask handled_access) override {
    if ((handled_access & ~access::kAllKnownFs) != 0) {
      return error(ErrorCode::InvalidAccessMask,
                   "refusing to pass unknown access bits to the kernel");
    }
    if (handled_access == 0) {
      return error(ErrorCode::NoAccessBits,
                   "refusing to create a ruleset that handles no access");
    }
#if defined(__NR_landlock_create_ruleset)
    NETSEC_LANDLOCK_RULESET_ATTR attr {};
    attr.handled_access_fs = handled_access;
    // Pass only the filesystem-field prefix so a newer userspace UAPI struct
    // remains compatible with an older running kernel; missing fields are
    // treated as zero by Landlock.
    const long fd = ::syscall(__NR_landlock_create_ruleset, &attr,
                              sizeof(attr.handled_access_fs), 0);
    if (fd < 0) {
      return error(ErrorCode::RulesetCreateFailed,
                   with_errno("landlock_create_ruleset failed"));
    }
    return static_cast<int>(fd);
#else
    (void)handled_access;
    return error(ErrorCode::AbiUnavailable,
                 "Landlock syscalls are unavailable");
#endif
  }

  Status add_path_beneath(int ruleset_fd, int root_fd,
                          AccessMask allowed_access) override {
#if defined(__NR_landlock_add_rule)
    NETSEC_LANDLOCK_PATH_ATTR attr {};
    attr.allowed_access = allowed_access;
    attr.parent_fd = root_fd;
    const long result = ::syscall(__NR_landlock_add_rule, ruleset_fd,
                                  LANDLOCK_RULE_PATH_BENEATH, &attr, 0);
    if (result != 0) {
      return error(ErrorCode::RulesetRuleFailed,
                   with_errno("landlock_add_rule failed"));
    }
    return Status{};
#else
    (void)ruleset_fd;
    (void)root_fd;
    (void)allowed_access;
    return error(ErrorCode::AbiUnavailable,
                 "Landlock syscalls are unavailable");
#endif
  }

  Status prepare_restrict() override {
#if defined(PR_SET_NO_NEW_PRIVS)
    if (::prctl(PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0) != 0) {
      return error(ErrorCode::NoNewPrivsFailed,
                   with_errno("prctl(PR_SET_NO_NEW_PRIVS) failed"));
    }
    return Status{};
#else
    return error(ErrorCode::UnsupportedPlatform,
                 "PR_SET_NO_NEW_PRIVS is unavailable");
#endif
  }

  Status restrict_self(int ruleset_fd) override {
#if defined(__NR_landlock_restrict_self)
    if (::syscall(__NR_landlock_restrict_self, ruleset_fd, 0) != 0) {
      return error(ErrorCode::RestrictFailed,
                   with_errno("landlock_restrict_self failed"));
    }
    return Status{};
#else
    (void)ruleset_fd;
    return error(ErrorCode::AbiUnavailable,
                 "Landlock syscalls are unavailable");
#endif
  }

  void close_fd(int fd) noexcept override {
    if (fd >= 0) {
      (void)::close(fd);
    }
  }
};

}  // namespace
#else
namespace {

class UnavailableObjectResolver final : public ObjectResolver {
 public:
  Result<ObjectIdentity> identify(int) const override {
    return error(ErrorCode::UnsupportedPlatform,
                 "object identity resolution is only implemented on Linux");
  }
  Result<Ancestry> ancestry(int) const override {
    return error(ErrorCode::UnsupportedPlatform,
                 "object hierarchy resolution is only implemented on Linux");
  }
};

class UnavailableLandlockBackend final : public LandlockBackend {
 public:
  Result<std::uint32_t> abi_version() const override {
    return error(ErrorCode::UnsupportedPlatform,
                 "Landlock is only available on Linux");
  }
  Result<int> create_ruleset(AccessMask) override {
    return error(ErrorCode::UnsupportedPlatform,
                 "Landlock is only available on Linux");
  }
  Status add_path_beneath(int, int, AccessMask) override {
    return error(ErrorCode::UnsupportedPlatform,
                 "Landlock is only available on Linux");
  }
  Status prepare_restrict() override {
    return error(ErrorCode::UnsupportedPlatform,
                 "Landlock is only available on Linux");
  }
  Status restrict_self(int) override {
    return error(ErrorCode::UnsupportedPlatform,
                 "Landlock is only available on Linux");
  }
  void close_fd(int fd) noexcept override {
    if (fd >= 0) {
      (void)::close(fd);
    }
  }
};

}  // namespace
#endif

std::shared_ptr<const ObjectResolver> make_native_object_resolver() {
#if defined(__linux__)
  return std::make_shared<LinuxObjectResolver>();
#else
  return std::make_shared<UnavailableObjectResolver>();
#endif
}

std::shared_ptr<LandlockBackend> make_native_landlock_backend() {
#if defined(__linux__)
  return std::make_shared<LinuxLandlockBackend>();
#else
  return std::make_shared<UnavailableLandlockBackend>();
#endif
}

bool native_landlock_available() noexcept {
  const auto backend = make_native_landlock_backend();
  return backend && backend->abi_version().has_value();
}

}  // namespace netsec
