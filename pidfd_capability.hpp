#pragma once

#include <atomic>
#include <cstddef>
#include <cstdint>
#include <functional>
#include <future>
#include <initializer_list>
#include <memory>
#include <mutex>
#include <optional>
#include <span>
#include <stdexcept>
#include <string_view>
#include <utility>
#include <vector>

#include <unistd.h>

namespace netsec::pidfd {

enum class Status : std::uint8_t {
  Ok = 0,
  InvalidArgument,
  Unsupported,
  PermissionDenied,
  NotInitialized,
  StaleSnapshot,
  IdentityMismatch,
  TargetExited,
  DescriptorNotFound,
  UnstableKernelInterface,
  CopyFailed,
  OperationNotPermitted,
  IoError,
  InternalError,
};

std::string_view to_string(Status status) noexcept;

template <typename T>
class Result {
 public:
  Result(T value) : value_(std::move(value)) {}
  Result(Status status) : status_(status) {
    if (status == Status::Ok) {
      status_ = Status::InternalError;
    }
  }

  static Result ok(T value) { return Result(std::move(value)); }
  static Result fail(Status status) { return Result(status); }

  bool has_value() const noexcept { return value_.has_value(); }
  explicit operator bool() const noexcept { return has_value(); }
  Status status() const noexcept { return status_; }

  T& value() & {
    if (!value_) {
      throw std::logic_error("netsec::pidfd::Result has no value");
    }
    return *value_;
  }

  const T& value() const& {
    if (!value_) {
      throw std::logic_error("netsec::pidfd::Result has no value");
    }
    return *value_;
  }

  T&& value() && {
    if (!value_) {
      throw std::logic_error("netsec::pidfd::Result has no value");
    }
    return std::move(*value_);
  }

  T& operator*() & { return value(); }
  const T& operator*() const& { return value(); }
  T* operator->() { return &value(); }
  const T* operator->() const { return &value(); }

 private:
  std::optional<T> value_;
  Status status_ = Status::Ok;
};

template <>
class Result<void> {
 public:
  Result() noexcept = default;
  Result(Status status) noexcept : status_(status) {
    if (status == Status::Ok) {
      status_ = Status::InternalError;
    }
  }

  static Result ok() noexcept { return Result(); }
  static Result fail(Status status) noexcept { return Result(status); }

  bool has_value() const noexcept { return status_ == Status::Ok; }
  explicit operator bool() const noexcept { return has_value(); }
  Status status() const noexcept { return status_; }

  void value() const {
    if (!has_value()) {
      throw std::logic_error("netsec::pidfd::Result<void> has no value");
    }
  }

 private:
  Status status_ = Status::Ok;
};

class UniqueFd {
 public:
  UniqueFd() noexcept = default;
  explicit UniqueFd(int fd) noexcept : fd_(fd) {}
  ~UniqueFd() { reset(); }

  UniqueFd(const UniqueFd&) = delete;
  UniqueFd& operator=(const UniqueFd&) = delete;

  UniqueFd(UniqueFd&& other) noexcept : fd_(other.fd_) { other.fd_ = -1; }

  UniqueFd& operator=(UniqueFd&& other) noexcept {
    if (this != &other) {
      reset(other.fd_);
      other.fd_ = -1;
    }
    return *this;
  }

  bool valid() const noexcept { return fd_ >= 0; }

  // This accessor exists for the injected kernel backend. RestrictedHandle
  // never exposes a native descriptor to its caller.
  int native_handle_for_kernel() const noexcept { return fd_; }

  void reset(int fd = -1) noexcept {
    if (fd_ >= 0) {
      ::close(fd_);
    }
    fd_ = fd;
  }

 private:
  int fd_ = -1;
};

class PidfdView {
 public:
  explicit constexpr PidfdView(int native_fd) noexcept : native_fd_(native_fd) {}
  constexpr int native_fd() const noexcept { return native_fd_; }

 private:
  int native_fd_;
};

// All fields are compared. Tokens are issued by the injected kernel and must
// never be reused. A pidfd cookie makes a recycled pidfd number observable;
// start_time_ns and exec_generation prevent PID and exec reuse; the namespace
// and policy domain fields make setns and credential transitions observable.
struct PidfdIdentity {
  std::uint64_t pidfd_cookie = 0;
  std::uint64_t process_cookie = 0;

  bool operator==(const PidfdIdentity&) const = default;
};

struct NamespaceIdentity {
  std::uint64_t inode = 0;
  std::uint64_t generation = 0;

  bool operator==(const NamespaceIdentity&) const = default;
};

struct PolicyDomain {
  std::uint64_t id = 0;
  std::uint64_t generation = 0;

  bool operator==(const PolicyDomain&) const = default;
};

struct ProcessIdentity {
  PidfdIdentity pidfd;
  std::uint64_t start_time_ns = 0;
  std::uint64_t exec_generation = 0;
  NamespaceIdentity user_namespace;
  NamespaceIdentity mount_namespace;
  PolicyDomain policy_domain;

  bool operator==(const ProcessIdentity&) const = default;
};

// description_token identifies the file description (the object shared by
// dup/pidfd_getfd), not merely the inode. Close/reopen of the same path must
// produce a different token.
struct FileDescriptionIdentity {
  std::uint64_t mount_namespace_inode = 0;
  std::uint64_t device = 0;
  std::uint64_t inode = 0;
  std::uint64_t inode_generation = 0;
  std::uint64_t description_token = 0;

  bool operator==(const FileDescriptionIdentity&) const = default;
};

enum class AccessMode : std::uint8_t {
  None = 0,
  Read,
  Write,
  ReadWrite,
};

constexpr bool is_readable(AccessMode mode) noexcept {
  return mode == AccessMode::Read || mode == AccessMode::ReadWrite;
}

constexpr bool is_writable(AccessMode mode) noexcept {
  return mode == AccessMode::Write || mode == AccessMode::ReadWrite;
}

enum class Operation : std::uint32_t {
  Read = 1U << 0,
  Pread = 1U << 1,
  Write = 1U << 2,
  Pwrite = 1U << 3,
  Seek = 1U << 4,
  Fstat = 1U << 5,
  MmapRead = 1U << 6,
  MmapWrite = 1U << 7,
  Ioctl = 1U << 8,
  Fcntl = 1U << 9,
  Sync = 1U << 10,
  Truncate = 1U << 11,
};

class OperationSet {
 public:
  constexpr OperationSet() noexcept = default;
  constexpr OperationSet(Operation operation) noexcept
      : bits_(static_cast<std::uint32_t>(operation)) {}
  constexpr OperationSet(std::initializer_list<Operation> operations) noexcept {
    for (Operation operation : operations) {
      bits_ |= static_cast<std::uint32_t>(operation);
    }
  }

  static constexpr OperationSet from_mask(std::uint32_t mask) noexcept {
    OperationSet result;
    result.bits_ = mask;
    return result;
  }

  constexpr bool contains(Operation operation) const noexcept {
    return (bits_ & static_cast<std::uint32_t>(operation)) != 0;
  }

  constexpr std::uint32_t mask() const noexcept { return bits_; }

  constexpr OperationSet operator|(OperationSet other) const noexcept {
    return from_mask(bits_ | other.bits_);
  }

  constexpr OperationSet operator&(OperationSet other) const noexcept {
    return from_mask(bits_ & other.bits_);
  }

  constexpr OperationSet& operator|=(OperationSet other) noexcept {
    bits_ |= other.bits_;
    return *this;
  }

  bool operator==(const OperationSet&) const = default;

 private:
  std::uint32_t bits_ = 0;
};

struct FdRecord {
  int descriptor = -1;
  std::uint64_t slot_generation = 0;
  FileDescriptionIdentity description;
  AccessMode access_mode = AccessMode::None;
  OperationSet allowed_operations;

  bool operator==(const FdRecord&) const = default;
};

struct FdProbe {
  ProcessIdentity process;
  FdRecord fd;
};

struct Enumeration {
  ProcessIdentity process;
  std::uint64_t fd_table_generation = 0;
  std::vector<FdRecord> fds;
};

struct Snapshot {
  std::uint64_t epoch = 0;
  std::uint64_t fd_table_generation = 0;
  ProcessIdentity target;
  std::vector<FdRecord> fds;

  const FdRecord* find(int descriptor) const noexcept {
    for (const FdRecord& fd : fds) {
      if (fd.descriptor == descriptor) {
        return &fd;
      }
    }
    return nullptr;
  }
};

enum class EventKind : std::uint8_t {
  Exec,
  Setns,
  CredentialsChanged,
  FdClosed,
  FdReopened,
  FdTableChanged,
  PidfdExited,
};

struct KernelEvent {
  EventKind kind = EventKind::FdTableChanged;
  std::uint64_t sequence = 0;
  int descriptor = -1;
};

struct ExpectedFdBinding {
  ProcessIdentity process;
  FdRecord fd;
};

struct CopyWitness {
  ProcessIdentity process;
  FdRecord fd;
};

struct Duplicate {
  UniqueFd fd;
  CopyWitness witness;
};

// The injected kernel interface is deliberately transactional. A backend that
// reports supports_atomic_checked_duplicate() must compare expected.process and
// expected.fd and duplicate the same file description in one kernel operation.
// Backends without that guarantee may offer freeze/thaw, in which case the
// user-space component revalidates around duplicate_fd().
class Kernel {
 public:
  using EventSink = std::function<void(const KernelEvent&)>;

  virtual ~Kernel() = default;

  virtual Result<ProcessIdentity> inspect_process(PidfdView pidfd) = 0;
  virtual Result<Enumeration> enumerate_fds(PidfdView pidfd) = 0;
  virtual Result<FdProbe> probe_fd(PidfdView pidfd, int descriptor) = 0;

  virtual bool supports_atomic_checked_duplicate() const noexcept {
    return false;
  }

  virtual Result<Duplicate> checked_duplicate(
      PidfdView pidfd, int descriptor, const ExpectedFdBinding& expected) {
    (void)pidfd;
    (void)descriptor;
    (void)expected;
    return Status::Unsupported;
  }

  virtual bool supports_freeze() const noexcept { return false; }

  virtual Result<void> freeze(PidfdView pidfd) {
    (void)pidfd;
    return Status::Unsupported;
  }

  virtual Result<void> thaw(PidfdView pidfd) {
    (void)pidfd;
    return Status::Unsupported;
  }

  // Used only when checked_duplicate() is not atomic. The returned witness must
  // describe the file description that was actually duplicated.
  // The default implementation calls pidfd_getfd(2), applies FD_CLOEXEC, and
  // asks verify_copied_fd() for the witness. It never falls back to procfs.
  virtual Result<Duplicate> duplicate_fd(PidfdView pidfd, int descriptor);

  // Defense-in-depth check on the descriptor copied into this process. The
  // default duplicate_fd() requires it to succeed, so a backend that relies on
  // the default pidfd_getfd(2) path must implement this method.
  virtual Result<CopyWitness> verify_copied_fd(const UniqueFd& fd) {
    (void)fd;
    return Status::Unsupported;
  }

  virtual Result<std::uint64_t> subscribe_events(PidfdView pidfd,
                                                  EventSink sink) {
    (void)pidfd;
    (void)sink;
    return Status::Unsupported;
  }

  virtual void unsubscribe_events(std::uint64_t subscription) noexcept {
    (void)subscription;
  }
};

struct AgentOptions {
  // Without an atomic checked dedupe, freezing is the only allowed fallback.
  bool allow_freeze_fallback = true;
};

struct HandleMetadata {
  ProcessIdentity process;
  FdRecord source;
  std::uint64_t snapshot_epoch = 0;
  OperationSet granted_operations;
};

namespace detail {
class HandleGate;
}

class RestrictedHandle {
 public:
  RestrictedHandle() noexcept = default;
  ~RestrictedHandle();

  RestrictedHandle(const RestrictedHandle&) = delete;
  RestrictedHandle& operator=(const RestrictedHandle&) = delete;
  RestrictedHandle(RestrictedHandle&& other) noexcept;
  RestrictedHandle& operator=(RestrictedHandle&& other) noexcept;

  bool valid() const noexcept;
  const HandleMetadata& metadata() const noexcept { return metadata_; }

  // Only positional, read-only I/O is exposed. It neither exposes a raw fd nor
  // permits changing the shared file offset or descriptor flags.
  Result<std::size_t> read_at(std::uint64_t offset,
                              std::span<std::byte> destination) noexcept;

  void close() noexcept;

 private:
  friend class CapabilityAgent;

  RestrictedHandle(std::shared_ptr<detail::HandleGate> gate,
                   HandleMetadata metadata) noexcept;

  std::shared_ptr<detail::HandleGate> gate_;
  HandleMetadata metadata_;
};

class CapabilityAgent {
 public:
  ~CapabilityAgent();

  CapabilityAgent(const CapabilityAgent&) = delete;
  CapabilityAgent& operator=(const CapabilityAgent&) = delete;

  // Takes ownership of pidfd. The pidfd is never replaced by a procfs path.
  static Result<std::shared_ptr<CapabilityAgent>> open(
      std::shared_ptr<Kernel> kernel, UniqueFd pidfd,
      AgentOptions options = {});

  ProcessIdentity bound_identity() const;

  Result<Snapshot> enumerate();
  Result<Snapshot> snapshot() { return enumerate(); }
  Result<RestrictedHandle> duplicate_read_only(int descriptor);
  Result<RestrictedHandle> copy_read_only(int descriptor) {
    return duplicate_read_only(descriptor);
  }
  std::future<Result<RestrictedHandle>> duplicate_read_only_async(
      int descriptor);

 private:
  struct State;

  explicit CapabilityAgent(std::shared_ptr<State> state);

  static Result<RestrictedHandle> duplicate_impl(
      const std::shared_ptr<State>& state, int descriptor);

  std::shared_ptr<State> state_;
};

}  // namespace netsec::pidfd
