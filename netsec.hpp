#pragma once

#include <compare>
#include <cstddef>
#include <cstdint>
#include <memory>
#include <optional>
#include <span>
#include <string>
#include <string_view>
#include <utility>
#include <variant>
#include <vector>

namespace netsec {

using AccessMask = std::uint64_t;

// Landlock filesystem access rights, as defined by the Linux UAPI.  The numeric
// values are part of the kernel ABI and are intentionally hard-coded here so
// that an old userspace header cannot silently change their meaning.
namespace access {
inline constexpr AccessMask kExecute    = 1ULL << 0;
inline constexpr AccessMask kWriteFile  = 1ULL << 1;
inline constexpr AccessMask kReadFile   = 1ULL << 2;
inline constexpr AccessMask kReadDir    = 1ULL << 3;
inline constexpr AccessMask kRemoveDir  = 1ULL << 4;
inline constexpr AccessMask kRemoveFile = 1ULL << 5;
inline constexpr AccessMask kMakeChar   = 1ULL << 6;
inline constexpr AccessMask kMakeDir    = 1ULL << 7;
inline constexpr AccessMask kMakeReg    = 1ULL << 8;
inline constexpr AccessMask kMakeSock   = 1ULL << 9;
inline constexpr AccessMask kMakeFifo   = 1ULL << 10;
inline constexpr AccessMask kMakeBlock  = 1ULL << 11;
inline constexpr AccessMask kMakeSym    = 1ULL << 12;
inline constexpr AccessMask kRefer      = 1ULL << 13;  // ABI 2
inline constexpr AccessMask kTruncate   = 1ULL << 14;  // ABI 3
inline constexpr AccessMask kIoctlDev   = 1ULL << 15;  // ABI 5

inline constexpr AccessMask kAbi1 =
    kExecute | kWriteFile | kReadFile | kReadDir | kRemoveDir | kRemoveFile |
    kMakeChar | kMakeDir | kMakeReg | kMakeSock | kMakeFifo | kMakeBlock |
    kMakeSym;
inline constexpr AccessMask kAbi2 = kAbi1 | kRefer;
inline constexpr AccessMask kAbi3 = kAbi2 | kTruncate;
inline constexpr AccessMask kAbi5 = kAbi3 | kIoctlDev;
inline constexpr AccessMask kAllKnownFs = kAbi5;
}  // namespace access

// Returns the filesystem access mask enforceable by a Landlock ABI.  Only bits
// this library understands are ever returned.  ABI 0 means "unknown".
[[nodiscard]] AccessMask supported_fs_access(std::uint32_t abi) noexcept;

[[nodiscard]] std::string to_string(AccessMask mask);

enum class ErrorCode : std::uint8_t {
  InvalidArgument,
  InvalidFd,
  InvalidPlan,
  InvalidAccessMask,
  UnsupportedAccessBits,
  NoAccessBits,
  UnsupportedAbi,
  AbiUnavailable,
  UnstableObjectIdentity,
  CrossMountTraversal,
  SymlinkRejected,
  MagicLinkRejected,
  PathReplaced,
  IdentityChanged,
  StaleNamespaceGeneration,
  StaleRulesetGeneration,
  StaleLayoutGeneration,
  NotCoveredByAuthorizedRoot,
  ReadOnlyViolation,
  RulesetCreateFailed,
  RulesetRuleFailed,
  RestrictFailed,
  NoNewPrivsFailed,
  IoError,
  BackendMismatch,
  UnsupportedPlatform,
  InternalError,
};

[[nodiscard]] const char* to_string(ErrorCode code) noexcept;

struct Error {
  ErrorCode code = ErrorCode::InternalError;
  std::string message;
};

template <typename T>
class Result {
 public:
  using value_type = T;

  Result(T value) : storage_(std::in_place_index<0>, std::move(value)) {}
  Result(Error error) : storage_(std::in_place_index<1>, std::move(error)) {}

  [[nodiscard]] bool has_value() const noexcept {
    return storage_.index() == 0;
  }
  [[nodiscard]] explicit operator bool() const noexcept { return has_value(); }

  [[nodiscard]] T& value() & { return std::get<0>(storage_); }
  [[nodiscard]] const T& value() const& { return std::get<0>(storage_); }
  [[nodiscard]] T&& value() && { return std::get<0>(std::move(storage_)); }

  [[nodiscard]] Error& error() & { return std::get<1>(storage_); }
  [[nodiscard]] const Error& error() const& { return std::get<1>(storage_); }
  [[nodiscard]] Error&& error() && { return std::get<1>(std::move(storage_)); }

  [[nodiscard]] T& operator*() & { return value(); }
  [[nodiscard]] const T& operator*() const& { return value(); }
  [[nodiscard]] T* operator->() { return &value(); }
  [[nodiscard]] const T* operator->() const { return &value(); }

 private:
  std::variant<T, Error> storage_;
};

template <>
class Result<void> {
 public:
  Result() = default;
  Result(Error error) : error_(std::move(error)) {}

  [[nodiscard]] bool has_value() const noexcept { return !error_.has_value(); }
  [[nodiscard]] explicit operator bool() const noexcept { return has_value(); }

  [[nodiscard]] Error& error() & { return *error_; }
  [[nodiscard]] const Error& error() const& { return *error_; }

 private:
  std::optional<Error> error_;
};

using Status = Result<void>;

// An object identity is deliberately independent of paths, uids and gids.
// mount_id + inode + device, with the inode generation and file handle when
// available, distinguish two objects that happen to share the same path text.
struct ObjectIdentity {
  std::uint64_t mount_id = 0;
  std::uint64_t inode = 0;
  std::uint64_t device = 0;
  std::uint64_t inode_generation = 0;
  std::vector<std::uint8_t> file_handle;
  // True only when the mount identifier is unique for the object's lifetime
  // (STATX_MNT_ID_UNIQUE) or a stable file handle is available.
  bool stable = false;

  [[nodiscard]] bool empty() const noexcept {
    return inode == 0 && device == 0 && mount_id == 0;
  }

  friend bool operator==(const ObjectIdentity&, const ObjectIdentity&) = default;
  friend auto operator<=>(const ObjectIdentity&, const ObjectIdentity&) = default;
};

struct ObjectIdentityHash {
  [[nodiscard]] std::size_t operator()(const ObjectIdentity& id) const noexcept;
};

// Recorded for auditability only.  It must never be used as an authorization
// key: uid/gid mappings change presentation, not the inode/mount object.
struct IdMapIdentity {
  std::uint32_t uid_inside = 0;
  std::uint32_t gid_inside = 0;
  std::uint32_t uid_outside = 0;
  std::uint32_t gid_outside = 0;
  std::uint64_t generation = 0;

  friend bool operator==(const IdMapIdentity&, const IdMapIdentity&) = default;
};

struct MountNamespace {
  std::uint64_t generation = 0;

  friend bool operator==(const MountNamespace&, const MountNamespace&) = default;
};

struct RuleRoot {
  // Borrowed during planning; the planner duplicates it before creating the
  // ruleset.  The caller may close it once plan() returns.
  int fd = -1;
  AccessMask access = access::kAllKnownFs;
  std::optional<IdMapIdentity> idmap;
  std::string label;
};

enum class UnsupportedAccessPolicy {
  // Requesting a right that the running kernel cannot enforce is an error.
  // This is the default and prevents an ABI downgrade from silently weakening
  // a restriction.
  FailClosed,
  // Explicitly permit clipping to the kernel ABI.  The dropped bits are
  // reported in RulePlan::trimmed_access(); this is never silent.
  TrimAndReport,
};

struct PlannerConfig {
  MountNamespace mount_namespace{};
  UnsupportedAccessPolicy unsupported_access =
      UnsupportedAccessPolicy::FailClosed;
};

struct Ancestry {
  // The object itself followed by its parent chain in the same mount.  The
  // chain stops before crossing a mount boundary.
  std::vector<ObjectIdentity> chain;
  bool reached_mount_root = false;
  bool complete = true;

  [[nodiscard]] bool empty() const noexcept { return chain.empty(); }
};

// Resolves live kernel objects.  Implementations must use fd-relative,
// no-follow operations and must not cache by path string or user-visible uid.
class ObjectResolver {
 public:
  virtual ~ObjectResolver() = default;

  [[nodiscard]] virtual Result<ObjectIdentity> identify(int fd) const = 0;
  [[nodiscard]] virtual Result<Ancestry> ancestry(int fd) const = 0;

  // Retains an object for the lifetime of a plan.  The default implementation
  // uses F_DUPFD_CLOEXEC; injected resolvers may return an equivalent handle.
  [[nodiscard]] virtual Result<int> retain(int fd) const;
  virtual void release(int fd) const noexcept;

  // Resolves a path beneath root_fd without following symlinks or magic links,
  // without accepting "..", and without crossing a mount.  The returned fd is
  // owned by the caller.
  [[nodiscard]] virtual Result<int> open_beneath(
      int root_fd, std::string_view relative_path) const;
};

class LandlockBackend {
 public:
  virtual ~LandlockBackend() = default;

  // Returns the kernel's Landlock ABI version.  Failure, including an
  // unavailable Landlock, must be reported as an error rather than guessed.
  [[nodiscard]] virtual Result<std::uint32_t> abi_version() const = 0;
  [[nodiscard]] virtual Result<int> create_ruleset(
      AccessMask handled_access) = 0;
  [[nodiscard]] virtual Status add_path_beneath(int ruleset_fd, int root_fd,
                                                AccessMask allowed_access) = 0;
  [[nodiscard]] virtual Status prepare_restrict() = 0;
  [[nodiscard]] virtual Status restrict_self(int ruleset_fd) = 0;
  virtual void close_fd(int fd) noexcept = 0;
};

struct PlanRule {
  ObjectIdentity identity;
  AccessMask access = 0;
  // Audit data only.  Multiple idmap views of one object deliberately collapse
  // into a single rule with the same identity.
  std::vector<IdMapIdentity> idmaps;
  std::vector<std::string> labels;
};

class RulePlan {
 public:
  RulePlan() noexcept;
  RulePlan(RulePlan&&) noexcept;
  RulePlan& operator=(RulePlan&&) noexcept;
  ~RulePlan();

  RulePlan(const RulePlan&) = delete;
  RulePlan& operator=(const RulePlan&) = delete;

  [[nodiscard]] bool valid() const noexcept;
  [[nodiscard]] std::uint32_t abi() const noexcept;
  [[nodiscard]] AccessMask requested_access() const noexcept;
  [[nodiscard]] AccessMask handled_access() const noexcept;
  [[nodiscard]] AccessMask trimmed_access() const noexcept;
  [[nodiscard]] MountNamespace mount_namespace() const noexcept;
  [[nodiscard]] std::uint64_t plan_generation() const noexcept;
  [[nodiscard]] const std::vector<PlanRule>& rules() const noexcept;

 private:
  struct Impl;
  std::unique_ptr<Impl> impl_;

  friend class Planner;
  friend Result<struct ActiveRuleset> apply(RulePlan&& plan,
                                            const struct ApplyOptions& options);
};

class Planner {
 public:
  Planner(std::shared_ptr<const ObjectResolver> resolver,
          std::shared_ptr<LandlockBackend> kernel);

  [[nodiscard]] Result<RulePlan> plan(
      std::span<const RuleRoot> roots,
      const PlannerConfig& config = PlannerConfig{}) const;
  [[nodiscard]] Result<RulePlan> plan(
      const RuleRoot& root,
      const PlannerConfig& config = PlannerConfig{}) const;

 private:
  std::shared_ptr<const ObjectResolver> resolver_;
  std::shared_ptr<LandlockBackend> kernel_;
};

enum class MountOperation : std::uint8_t {
  Mount,
  MoveMount,
  PivotRoot,
};

struct MountRequest {
  MountOperation operation = MountOperation::Mount;
  // Opened root of the object being introduced.  The hook resolves this fd; it
  // does not trust a path or uid supplied by the caller.
  int source_fd = -1;
  // Optional opened root of the resulting mount.  When present it is recorded
  // as an authorized root so later operations through the new mount can be
  // proven.
  int destination_fd = -1;
  std::uint64_t namespace_generation = 0;
  std::uint64_t ruleset_generation = 0;
  // Observed value from RuntimeHooks::layout_generation().  Concurrent mount
  // operations must observe the latest value or be rejected as stale.
  std::uint64_t layout_generation = 0;
  bool read_only = false;
};

struct MountReceipt {
  std::uint64_t layout_generation = 0;
  ObjectIdentity source;
  std::optional<ObjectIdentity> destination;
  AccessMask access = 0;
};

// Serializes and authorizes mount-namespace changes after restrict_self().
// Every new mount, move_mount and pivot_root must be passed through this
// object.  The hook proves that the source object is still under an authorized
// root before committing the layout change.
class RuntimeHooks {
 public:
  [[nodiscard]] Result<MountReceipt> on_mount(const MountRequest& request);
  [[nodiscard]] Result<MountReceipt> on_move_mount(
      const MountRequest& request);
  [[nodiscard]] Result<MountReceipt> on_pivot_root(
      const MountRequest& request);

  // Checks coverage without changing the layout generation.
  [[nodiscard]] Status verify_covered(int fd,
                                      std::uint64_t namespace_generation,
                                      std::uint64_t ruleset_generation,
                                      std::uint64_t layout_generation) const;

  [[nodiscard]] std::uint64_t layout_generation() const noexcept;
  [[nodiscard]] MountNamespace mount_namespace() const noexcept;
  [[nodiscard]] std::uint64_t ruleset_generation() const noexcept;
  [[nodiscard]] std::vector<PlanRule> authorized_roots() const;

 private:
  struct Impl;
  explicit RuntimeHooks(std::shared_ptr<const ObjectResolver> resolver,
                        MountNamespace mount_namespace,
                        std::uint64_t ruleset_generation,
                        std::vector<PlanRule> roots);
  std::shared_ptr<Impl> impl_;

  friend Result<struct ActiveRuleset> apply(RulePlan&& plan,
                                            const struct ApplyOptions& options);
};

// Object identities and mount ancestry are always required to be stable;
// there is deliberately no option to relax that invariant.
struct ApplyOptions {};

struct ActiveRuleset {
  std::uint32_t abi = 0;
  AccessMask requested_access = 0;
  AccessMask handled_access = 0;
  AccessMask trimmed_access = 0;
  MountNamespace mount_namespace{};
  std::uint64_t plan_generation = 0;
  std::uint64_t ruleset_generation = 0;
  std::shared_ptr<RuntimeHooks> hooks;
};

// Consumes the plan, revalidates every retained object, creates the ruleset,
// sets no_new_privs, calls restrict_self(), and records the namespace/ruleset
// generations.  Any failure before restrict_self leaves the process
// unconfined; an inability to build the runtime state after that point is
// treated as fatal (the state is prepared before restricting).
[[nodiscard]] Result<ActiveRuleset> apply(RulePlan&& plan,
                                          const ApplyOptions& options = {});

// Native backends.  On non-Linux platforms these report
// ErrorCode::UnsupportedPlatform; the portable planner can still be tested
// with injected equivalent object identities.
[[nodiscard]] std::shared_ptr<const ObjectResolver>
make_native_object_resolver();
[[nodiscard]] std::shared_ptr<LandlockBackend> make_native_landlock_backend();

[[nodiscard]] bool native_landlock_available() noexcept;

}  // namespace netsec
