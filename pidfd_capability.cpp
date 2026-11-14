#include "pidfd_capability.hpp"

#include <algorithm>
#include <cerrno>
#include <fcntl.h>
#include <limits>
#include <unordered_map>
#include <unordered_set>
#include <vector>

#if defined(__linux__)
#include <sys/syscall.h>
#endif

namespace netsec::pidfd {
namespace {

constexpr OperationSet kReadOnlyInspection{Operation::Pread};

bool same_fd_binding(const FdRecord& left, const FdRecord& right) noexcept {
  return left == right;
}

class FreezeGuard {
 public:
  FreezeGuard(std::shared_ptr<Kernel> kernel, PidfdView pidfd)
      : kernel_(std::move(kernel)), pidfd_(pidfd) {}

  ~FreezeGuard() {
    if (frozen_) {
      (void)kernel_->thaw(pidfd_);
    }
  }

  Result<void> freeze() {
    auto result = kernel_->freeze(pidfd_);
    if (result) {
      frozen_ = true;
    }
    return result;
  }

  Result<void> thaw() {
    if (!frozen_) {
      return Status::InternalError;
    }
    auto result = kernel_->thaw(pidfd_);
    if (result) {
      frozen_ = false;
    }
    return result;
  }

 private:
  std::shared_ptr<Kernel> kernel_;
  PidfdView pidfd_;
  bool frozen_ = false;
};

}  // namespace

std::string_view to_string(Status status) noexcept {
  switch (status) {
    case Status::Ok:
      return "ok";
    case Status::InvalidArgument:
      return "invalid argument";
    case Status::Unsupported:
      return "unsupported";
    case Status::PermissionDenied:
      return "permission denied";
    case Status::NotInitialized:
      return "not initialized";
    case Status::StaleSnapshot:
      return "stale snapshot";
    case Status::IdentityMismatch:
      return "identity mismatch";
    case Status::TargetExited:
      return "target exited";
    case Status::DescriptorNotFound:
      return "descriptor not found";
    case Status::UnstableKernelInterface:
      return "kernel interface cannot provide an atomic identity check";
    case Status::CopyFailed:
      return "descriptor copy failed";
    case Status::OperationNotPermitted:
      return "operation not permitted";
    case Status::IoError:
      return "I/O error";
    case Status::InternalError:
      return "internal error";
  }
  return "unknown status";
}

Result<Duplicate> Kernel::duplicate_fd(PidfdView pidfd, int descriptor) {
#if defined(__linux__) && defined(SYS_pidfd_getfd)
  long duplicated;
  do {
    duplicated =
        ::syscall(SYS_pidfd_getfd, pidfd.native_fd(), descriptor, 0);
  } while (duplicated < 0 && errno == EINTR);

  if (duplicated < 0) {
    switch (errno) {
      case EACCES:
      case EPERM:
        return Status::PermissionDenied;
      case ESRCH:
        return Status::TargetExited;
      case EBADF:
        return Status::DescriptorNotFound;
      case EINVAL:
        return Status::InvalidArgument;
      case ENOSYS:
        return Status::Unsupported;
      default:
        return Status::CopyFailed;
    }
  }

  UniqueFd copy(static_cast<int>(duplicated));
  int descriptor_flags =
      ::fcntl(copy.native_handle_for_kernel(), F_GETFD);
  if (descriptor_flags < 0 ||
      ::fcntl(copy.native_handle_for_kernel(), F_SETFD,
              descriptor_flags | FD_CLOEXEC) < 0) {
    return Status::CopyFailed;
  }
  const int status_flags =
      ::fcntl(copy.native_handle_for_kernel(), F_GETFL);
  if (status_flags < 0 ||
      (status_flags & O_ACCMODE) != O_RDONLY) {
    return Status::OperationNotPermitted;
  }

  auto witness = verify_copied_fd(copy);
  if (!witness) {
    return witness.status();
  }
  return Duplicate{std::move(copy), std::move(witness.value())};
#else
  (void)pidfd;
  (void)descriptor;
  return Status::Unsupported;
#endif
}

namespace detail {

class HandleGate {
 public:
  HandleGate(UniqueFd fd, int source_descriptor) noexcept
      : fd_(std::move(fd)), source_descriptor_(source_descriptor) {}

  bool valid() const noexcept {
    if (revoked_.load(std::memory_order_acquire)) {
      return false;
    }
    std::lock_guard lock(mutex_);
    return !revoked_.load(std::memory_order_relaxed) && fd_.valid();
  }

  Result<std::size_t> read_at(std::uint64_t offset,
                              std::span<std::byte> destination) noexcept {
    std::lock_guard lock(mutex_);
    if (revoked_.load(std::memory_order_relaxed) || !fd_.valid()) {
      return Status::StaleSnapshot;
    }
    if (destination.empty()) {
      return std::size_t{0};
    }
    constexpr auto kMaxOffset = static_cast<std::uint64_t>(
        std::numeric_limits<off_t>::max());
    if (offset > kMaxOffset) {
      return Status::InvalidArgument;
    }
    const std::size_t max_io =
        static_cast<std::size_t>(std::numeric_limits<ssize_t>::max());
    const std::size_t count = std::min(destination.size(), max_io);
    ssize_t result;
    do {
      result = ::pread(fd_.native_handle_for_kernel(), destination.data(),
                       count, static_cast<off_t>(offset));
    } while (result < 0 && errno == EINTR);
    if (result < 0) {
      return Status::IoError;
    }
    return static_cast<std::size_t>(result);
  }

  void revoke_and_close() noexcept {
    revoked_.store(true, std::memory_order_release);
    std::lock_guard lock(mutex_);
    fd_.reset();
  }

  int source_descriptor() const noexcept { return source_descriptor_; }

  bool revoked() const noexcept {
    return revoked_.load(std::memory_order_acquire);
  }

 private:
  std::atomic<bool> revoked_{false};
  mutable std::mutex mutex_;
  UniqueFd fd_;
  int source_descriptor_ = -1;
};

}  // namespace detail

RestrictedHandle::RestrictedHandle(
    std::shared_ptr<detail::HandleGate> gate,
    HandleMetadata metadata) noexcept
    : gate_(std::move(gate)), metadata_(std::move(metadata)) {}

RestrictedHandle::~RestrictedHandle() { close(); }

RestrictedHandle::RestrictedHandle(RestrictedHandle&& other) noexcept
    : gate_(std::move(other.gate_)), metadata_(std::move(other.metadata_)) {}

RestrictedHandle& RestrictedHandle::operator=(
    RestrictedHandle&& other) noexcept {
  if (this != &other) {
    close();
    gate_ = std::move(other.gate_);
    metadata_ = std::move(other.metadata_);
  }
  return *this;
}

bool RestrictedHandle::valid() const noexcept {
  return gate_ && gate_->valid();
}

Result<std::size_t> RestrictedHandle::read_at(
    std::uint64_t offset, std::span<std::byte> destination) noexcept {
  if (!gate_) {
    return Status::NotInitialized;
  }
  if (!metadata_.granted_operations.contains(Operation::Pread)) {
    return Status::OperationNotPermitted;
  }
  return gate_->read_at(offset, destination);
}

void RestrictedHandle::close() noexcept {
  if (gate_) {
    gate_->revoke_and_close();
    gate_.reset();
  }
}

struct CapabilityAgent::State {
  State(std::shared_ptr<Kernel> kernel_in, UniqueFd pidfd_in,
        AgentOptions options_in, ProcessIdentity bound_in)
      : kernel(std::move(kernel_in)),
        pidfd(std::move(pidfd_in)),
        options(options_in),
        bound(std::move(bound_in)) {}

  ~State() {
    if (subscribed) {
      kernel->unsubscribe_events(subscription);
    }

    std::vector<std::shared_ptr<detail::HandleGate>> gates;
    {
      std::lock_guard lock(mutex);
      if (snapshot) {
        ++snapshot_generation;
        snapshot.reset();
      }
      for (auto& [id, weak_gate] : handles) {
        (void)id;
        if (auto gate = weak_gate.lock()) {
          gates.push_back(std::move(gate));
        }
      }
      handles.clear();
    }
    for (const auto& gate : gates) {
      gate->revoke_and_close();
    }
  }

  PidfdView view() const noexcept { return PidfdView(pidfd.native_handle_for_kernel()); }

  ProcessIdentity bound_identity() const {
    std::lock_guard lock(mutex);
    return bound;
  }

  void invalidate_snapshot_locked() {
    ++snapshot_generation;
    snapshot.reset();
  }

  std::vector<std::shared_ptr<detail::HandleGate>> collect_handles_locked(
      std::optional<int> source_descriptor) {
    std::vector<std::shared_ptr<detail::HandleGate>> result;
    for (auto iterator = handles.begin(); iterator != handles.end();) {
      auto gate = iterator->second.lock();
      if (!gate) {
        iterator = handles.erase(iterator);
        continue;
      }
      if (!source_descriptor || gate->source_descriptor() == *source_descriptor) {
        result.push_back(std::move(gate));
        iterator = handles.erase(iterator);
      } else {
        ++iterator;
      }
    }
    return result;
  }

  void revoke_all() {
    std::vector<std::shared_ptr<detail::HandleGate>> gates;
    {
      std::lock_guard lock(mutex);
      invalidate_snapshot_locked();
      gates = collect_handles_locked(std::nullopt);
    }
    for (const auto& gate : gates) {
      gate->revoke_and_close();
    }
  }

  void on_event(const KernelEvent& event) {
    // Publish the event before taking the state lock. This closes the race in
    // which a copy finishes while an event callback is waiting for the lock.
    const std::uint64_t event_number =
        event_epoch.fetch_add(1, std::memory_order_acq_rel) + 1;

    const bool fd_specific =
        (event.kind == EventKind::FdClosed ||
         event.kind == EventKind::FdReopened) &&
        event.descriptor >= 0;
    std::vector<std::shared_ptr<detail::HandleGate>> gates;
    {
      std::lock_guard lock(mutex);
      // A snapshot committed after this event is already based on the new
      // state. A snapshot that predates the event must not survive.
      if (!snapshot || snapshot_event_epoch < event_number) {
        invalidate_snapshot_locked();
      }
      gates = collect_handles_locked(
          fd_specific ? std::optional<int>(event.descriptor) : std::nullopt);
    }
    for (const auto& gate : gates) {
      gate->revoke_and_close();
    }
  }

  std::shared_ptr<Kernel> kernel;
  UniqueFd pidfd;
  AgentOptions options;

  mutable std::mutex mutex;
  ProcessIdentity bound;
  std::atomic<std::uint64_t> event_epoch{0};
  std::uint64_t snapshot_generation = 0;
  std::uint64_t snapshot_event_epoch = 0;
  std::optional<Snapshot> snapshot;
  std::uint64_t subscription = 0;
  bool subscribed = false;
  std::uint64_t next_handle_id = 1;
  std::unordered_map<std::uint64_t,
                     std::weak_ptr<detail::HandleGate>>
      handles;
};

CapabilityAgent::CapabilityAgent(std::shared_ptr<State> state)
    : state_(std::move(state)) {}

CapabilityAgent::~CapabilityAgent() = default;

Result<std::shared_ptr<CapabilityAgent>> CapabilityAgent::open(
    std::shared_ptr<Kernel> kernel, UniqueFd pidfd, AgentOptions options) {
  if (!kernel || !pidfd.valid()) {
    return Status::InvalidArgument;
  }

  auto initial = kernel->inspect_process(PidfdView(pidfd.native_handle_for_kernel()));
  if (!initial) {
    return initial.status();
  }

  auto state = std::make_shared<State>(std::move(kernel), std::move(pidfd),
                                       options, std::move(initial.value()));
  std::weak_ptr<State> weak_state = state;
  auto subscribed = state->kernel->subscribe_events(
      state->view(), [weak_state](const KernelEvent& event) {
        if (auto live = weak_state.lock()) {
          live->on_event(event);
        }
      });
  if (!subscribed) {
    return subscribed.status();
  }
  state->subscription = subscribed.value();
  state->subscribed = true;

  // Close the subscribe/inspect race: an exec, setns or credential transition
  // can happen while the callback is being installed.
  auto confirmed =
      state->kernel->inspect_process(state->view());
  if (!confirmed) {
    return confirmed.status();
  }
  if (!(confirmed.value() == state->bound_identity())) {
    state->revoke_all();
    return Status::IdentityMismatch;
  }

  return std::shared_ptr<CapabilityAgent>(
      new CapabilityAgent(std::move(state)));
}

ProcessIdentity CapabilityAgent::bound_identity() const {
  return state_->bound_identity();
}

Result<Snapshot> CapabilityAgent::enumerate() {
  if (!state_) {
    return Status::NotInitialized;
  }

  const std::uint64_t start_epoch =
      state_->event_epoch.load(std::memory_order_acquire);
  const ProcessIdentity bound = state_->bound_identity();

  auto before = state_->kernel->inspect_process(state_->view());
  if (!before) {
    if (before.status() == Status::TargetExited) {
      state_->revoke_all();
    }
    return before.status();
  }
  if (!(before.value() == bound)) {
    state_->revoke_all();
    return Status::IdentityMismatch;
  }

  auto enumerated = state_->kernel->enumerate_fds(state_->view());
  if (!enumerated) {
    if (enumerated.status() == Status::TargetExited) {
      state_->revoke_all();
    }
    return enumerated.status();
  }
  if (!(enumerated.value().process == bound)) {
    state_->revoke_all();
    return Status::IdentityMismatch;
  }

  std::unordered_set<int> descriptors;
  for (const FdRecord& fd : enumerated.value().fds) {
    if (fd.descriptor < 0) {
      return Status::InvalidArgument;
    }
    if (!descriptors.insert(fd.descriptor).second) {
      return Status::InvalidArgument;
    }
  }
  std::sort(enumerated.value().fds.begin(), enumerated.value().fds.end(),
            [](const FdRecord& left, const FdRecord& right) {
              return left.descriptor < right.descriptor;
            });

  auto after = state_->kernel->inspect_process(state_->view());
  if (!after) {
    if (after.status() == Status::TargetExited) {
      state_->revoke_all();
    }
    return after.status();
  }
  if (!(after.value() == bound)) {
    state_->revoke_all();
    return Status::IdentityMismatch;
  }
  if (state_->event_epoch.load(std::memory_order_acquire) != start_epoch) {
    state_->revoke_all();
    return Status::StaleSnapshot;
  }

  std::lock_guard lock(state_->mutex);
  if (state_->event_epoch.load(std::memory_order_acquire) != start_epoch) {
    return Status::StaleSnapshot;
  }

  Snapshot snapshot;
  const std::uint64_t commit_event_epoch =
      state_->event_epoch.load(std::memory_order_acquire);
  if (commit_event_epoch != start_epoch) {
    return Status::StaleSnapshot;
  }
  snapshot.epoch = ++state_->snapshot_generation;
  snapshot.fd_table_generation = enumerated.value().fd_table_generation;
  snapshot.target = bound;
  snapshot.fds = std::move(enumerated.value().fds);
  state_->snapshot_event_epoch = commit_event_epoch;
  state_->snapshot = snapshot;
  if (state_->event_epoch.load(std::memory_order_acquire) != start_epoch) {
    state_->snapshot.reset();
    ++state_->snapshot_generation;
    return Status::StaleSnapshot;
  }
  return snapshot;
}

Result<RestrictedHandle> CapabilityAgent::duplicate_read_only(
    int descriptor) {
  if (!state_) {
    return Status::NotInitialized;
  }
  return duplicate_impl(state_, descriptor);
}

std::future<Result<RestrictedHandle>>
CapabilityAgent::duplicate_read_only_async(int descriptor) {
  auto state = state_;
  return std::async(std::launch::async, [state = std::move(state), descriptor] {
    if (!state) {
      return Result<RestrictedHandle>::fail(Status::NotInitialized);
    }
    return duplicate_impl(state, descriptor);
  });
}

Result<RestrictedHandle> CapabilityAgent::duplicate_impl(
    const std::shared_ptr<State>& state, int descriptor) {
  if (descriptor < 0) {
    return Status::InvalidArgument;
  }

  Snapshot snapshot;
  FdRecord expected;
  OperationSet granted;
  std::uint64_t start_epoch = 0;
  {
    std::lock_guard lock(state->mutex);
    if (!state->snapshot) {
      return Status::StaleSnapshot;
    }
    if (state->event_epoch.load(std::memory_order_acquire) !=
        state->snapshot_event_epoch) {
      return Status::StaleSnapshot;
    }
    start_epoch = state->event_epoch.load(std::memory_order_acquire);
    snapshot = *state->snapshot;
    const FdRecord* found = snapshot.find(descriptor);
    if (!found) {
      return Status::DescriptorNotFound;
    }
    expected = *found;
    if (expected.access_mode != AccessMode::Read) {
      return Status::OperationNotPermitted;
    }
    if (!is_readable(expected.access_mode)) {
      return Status::OperationNotPermitted;
    }
    granted = expected.allowed_operations & kReadOnlyInspection;
    if (!granted.contains(Operation::Pread)) {
      return Status::OperationNotPermitted;
    }
  }

  const ExpectedFdBinding binding{snapshot.target, expected};

  auto preflight = state->kernel->probe_fd(state->view(), descriptor);
  if (!preflight) {
    if (preflight.status() == Status::TargetExited ||
        preflight.status() == Status::DescriptorNotFound) {
      state->revoke_all();
    }
    return preflight.status();
  }
  if (!(preflight.value().process == snapshot.target) ||
      !same_fd_binding(preflight.value().fd, expected)) {
    state->revoke_all();
    return Status::IdentityMismatch;
  }

  UniqueFd copied;
  CopyWitness witness;

  if (state->kernel->supports_atomic_checked_duplicate()) {
    auto result =
        state->kernel->checked_duplicate(state->view(), descriptor, binding);
    if (!result) {
      if (result.status() == Status::IdentityMismatch ||
          result.status() == Status::TargetExited ||
          result.status() == Status::DescriptorNotFound) {
        state->revoke_all();
      }
      return result.status();
    }
    copied = std::move(result.value().fd);
    witness = std::move(result.value().witness);
  } else {
    if (!state->options.allow_freeze_fallback ||
        !state->kernel->supports_freeze()) {
      return Status::UnstableKernelInterface;
    }

    FreezeGuard frozen(state->kernel, state->view());
    auto freeze_result = frozen.freeze();
    if (!freeze_result) {
      return freeze_result.status() == Status::Ok
                 ? Status::UnstableKernelInterface
                 : freeze_result.status();
    }

    auto frozen_before =
        state->kernel->probe_fd(state->view(), descriptor);
    if (!frozen_before) {
      return frozen_before.status();
    }
    if (!(frozen_before.value().process == snapshot.target) ||
        !same_fd_binding(frozen_before.value().fd, expected)) {
      state->revoke_all();
      return Status::IdentityMismatch;
    }

    auto result = state->kernel->duplicate_fd(state->view(), descriptor);
    if (!result) {
      if (result.status() == Status::IdentityMismatch ||
          result.status() == Status::TargetExited ||
          result.status() == Status::DescriptorNotFound) {
        state->revoke_all();
      }
      return result.status();
    }
    copied = std::move(result.value().fd);
    witness = std::move(result.value().witness);

    auto frozen_after = state->kernel->probe_fd(state->view(), descriptor);
    if (!frozen_after) {
      return frozen_after.status();
    }
    if (!(frozen_after.value().process == snapshot.target) ||
        !same_fd_binding(frozen_after.value().fd, expected)) {
      state->revoke_all();
      return Status::IdentityMismatch;
    }

    auto thaw_result = frozen.thaw();
    if (!thaw_result) {
      return Status::InternalError;
    }
  }

  // Recheck after the copy even for a backend that advertises atomic identity
  // matching. This catches a broken or malicious backend and post-copy exit.
  auto postflight = state->kernel->probe_fd(state->view(), descriptor);
  if (!postflight) {
    if (postflight.status() == Status::TargetExited ||
        postflight.status() == Status::DescriptorNotFound) {
      state->revoke_all();
    }
    return postflight.status();
  }
  if (!(postflight.value().process == snapshot.target) ||
      !same_fd_binding(postflight.value().fd, expected)) {
    state->revoke_all();
    return Status::IdentityMismatch;
  }

  if (!(witness.process == snapshot.target) ||
      !same_fd_binding(witness.fd, expected)) {
    state->revoke_all();
    return Status::IdentityMismatch;
  }

  auto verified = state->kernel->verify_copied_fd(copied);
  if (verified) {
    if (!(verified.value().process == snapshot.target) ||
        !same_fd_binding(verified.value().fd, expected)) {
      state->revoke_all();
      return Status::IdentityMismatch;
    }
  } else if (verified.status() != Status::Unsupported) {
    state->revoke_all();
    return verified.status();
  }

  if (state->event_epoch.load(std::memory_order_acquire) != start_epoch) {
    state->revoke_all();
    return Status::StaleSnapshot;
  }

  std::lock_guard lock(state->mutex);
  if (state->event_epoch.load(std::memory_order_acquire) != start_epoch ||
      state->snapshot_generation != snapshot.epoch || !state->snapshot) {
    return Status::StaleSnapshot;
  }
  if (!(state->bound == snapshot.target)) {
    return Status::IdentityMismatch;
  }

  auto gate = std::make_shared<detail::HandleGate>(
      std::move(copied), expected.descriptor);
  HandleMetadata metadata;
  metadata.process = snapshot.target;
  metadata.source = expected;
  metadata.snapshot_epoch = snapshot.epoch;
  metadata.granted_operations = granted;

  const std::uint64_t handle_id = state->next_handle_id++;
  state->handles.emplace(handle_id, gate);
  if (state->event_epoch.load(std::memory_order_acquire) != start_epoch) {
    state->handles.erase(handle_id);
    gate->revoke_and_close();
    return Status::StaleSnapshot;
  }
  return RestrictedHandle(std::move(gate), std::move(metadata));
}

}  // namespace netsec::pidfd
