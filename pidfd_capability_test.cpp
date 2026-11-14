#include "pidfd_capability.hpp"

#include <fcntl.h>
#include <sys/stat.h>
#include <unistd.h>

#include <atomic>
#include <chrono>
#include <condition_variable>
#include <cstdlib>
#include <cstring>
#include <filesystem>
#include <functional>
#include <iostream>
#include <map>
#include <mutex>
#include <optional>
#include <string>
#include <thread>
#include <unordered_map>
#include <vector>

namespace {

using namespace netsec::pidfd;
using namespace std::chrono_literals;

int failures = 0;

#define CHECK(expression)                                                   \
  do {                                                                      \
    if (!(expression)) {                                                    \
      std::cerr << __FILE__ << ":" << __LINE__ << ": CHECK failed: "       \
                << #expression << "\n";                                     \
      ++failures;                                                           \
    }                                                                       \
  } while (false)

#define REQUIRE(expression)                                                 \
  do {                                                                      \
    if (!(expression)) {                                                    \
      std::cerr << __FILE__ << ":" << __LINE__ << ": REQUIRE failed: "     \
                << #expression << "\n";                                     \
      ++failures;                                                           \
      return;                                                               \
    }                                                                       \
  } while (false)

class TempDir {
 public:
  TempDir() {
    const char* base = std::getenv("TMPDIR");
    std::string pattern =
        std::string(base ? base : ".") + "/netsec-pidfd-XXXXXX";
    std::vector<char> buffer(pattern.begin(), pattern.end());
    buffer.push_back('\0');
    char* created = ::mkdtemp(buffer.data());
    if (created) {
      path_ = created;
    }
  }

  ~TempDir() {
    std::error_code error;
    if (!path_.empty()) {
      std::filesystem::remove_all(path_, error);
    }
  }

  const std::string& path() const { return path_; }

  UniqueFd create_read_only(const std::string& name,
                            const std::string& contents) const {
    const std::string file = path_ + "/" + name;
    int writer = ::open(file.c_str(), O_CREAT | O_EXCL | O_WRONLY, 0600);
    if (writer < 0) {
      return UniqueFd();
    }
    const char* cursor = contents.data();
    std::size_t remaining = contents.size();
    while (remaining > 0) {
      ssize_t written = ::write(writer, cursor, remaining);
      if (written <= 0) {
        ::close(writer);
        return UniqueFd();
      }
      cursor += written;
      remaining -= static_cast<std::size_t>(written);
    }
    ::close(writer);
    return UniqueFd(::open(file.c_str(), O_RDONLY | O_CLOEXEC));
  }

  UniqueFd create_read_write(const std::string& name,
                             const std::string& contents) const {
    const std::string file = path_ + "/" + name;
    int fd = ::open(file.c_str(), O_CREAT | O_EXCL | O_RDWR, 0600);
    if (fd < 0) {
      return UniqueFd();
    }
    if (!contents.empty() &&
        ::write(fd, contents.data(), contents.size()) !=
            static_cast<ssize_t>(contents.size())) {
      ::close(fd);
      return UniqueFd();
    }
    return UniqueFd(fd);
  }

 private:
  std::string path_;
};

FileDescriptionIdentity identity_for(int fd, std::uint64_t mount_namespace,
                                     std::uint64_t token) {
  struct stat status {};
  if (::fstat(fd, &status) != 0) {
    return {};
  }
  FileDescriptionIdentity identity;
  identity.mount_namespace_inode = mount_namespace;
  identity.device = static_cast<std::uint64_t>(status.st_dev);
  identity.inode = static_cast<std::uint64_t>(status.st_ino);
  identity.inode_generation = 1;
  identity.description_token = token;
  return identity;
}

ProcessIdentity make_process(std::uint64_t start, std::uint64_t exec_generation,
                             std::uint64_t user_namespace,
                             std::uint64_t mount_namespace,
                             std::uint64_t policy_domain) {
  ProcessIdentity identity;
  identity.pidfd.pidfd_cookie = 0x1000 + start;
  identity.pidfd.process_cookie = 0x2000 + start;
  identity.start_time_ns = start;
  identity.exec_generation = exec_generation;
  identity.user_namespace = {user_namespace, 1};
  identity.mount_namespace = {mount_namespace, 1};
  identity.policy_domain = {policy_domain, 1};
  return identity;
}

int duplicate_cloexec(int fd) {
#ifdef F_DUPFD_CLOEXEC
  return ::fcntl(fd, F_DUPFD_CLOEXEC, 3);
#else
  int copy = ::dup(fd);
  if (copy >= 0) {
    (void)::fcntl(copy, F_SETFD, FD_CLOEXEC);
  }
  return copy;
#endif
}

class FakeKernel final : public Kernel {
 public:
  struct Entry {
    FdRecord record;
    UniqueFd fd;
  };

  struct Process {
    ProcessIdentity identity;
    std::uint64_t fd_table_generation = 1;
    bool exited = false;
    bool frozen = false;
    std::map<int, Entry> fds;
  };

  Result<ProcessIdentity> inspect_process(PidfdView pidfd) override {
    std::lock_guard lock(mutex_);
    auto process = find_process_locked(pidfd);
    if (!process) {
      return Status::InvalidArgument;
    }
    if (process->exited) {
      return Status::TargetExited;
    }
    return process->identity;
  }

  Result<Enumeration> enumerate_fds(PidfdView pidfd) override {
    std::lock_guard lock(mutex_);
    auto process = find_process_locked(pidfd);
    if (!process) {
      return Status::InvalidArgument;
    }
    if (process->exited) {
      return Status::TargetExited;
    }
    Enumeration result;
    result.process = process->identity;
    result.fd_table_generation = process->fd_table_generation;
    for (const auto& [number, entry] : process->fds) {
      (void)number;
      result.fds.push_back(entry.record);
    }
    return result;
  }

  Result<FdProbe> probe_fd(PidfdView pidfd, int descriptor) override {
    std::lock_guard lock(mutex_);
    auto process = find_process_locked(pidfd);
    if (!process) {
      return Status::InvalidArgument;
    }
    if (process->exited) {
      return Status::TargetExited;
    }
    auto entry = process->fds.find(descriptor);
    if (entry == process->fds.end()) {
      return Status::DescriptorNotFound;
    }
    return FdProbe{process->identity, entry->second.record};
  }

  bool supports_atomic_checked_duplicate() const noexcept override {
    return atomic_;
  }

  void set_atomic(bool atomic) { atomic_ = atomic; }

  bool supports_freeze() const noexcept override { return freeze_; }

  void set_freeze_supported(bool supported) { freeze_ = supported; }

  Result<void> freeze(PidfdView pidfd) override {
    std::lock_guard lock(mutex_);
    auto process = find_process_locked(pidfd);
    if (!process) {
      return Status::InvalidArgument;
    }
    if (!freeze_) {
      return Status::Unsupported;
    }
    if (process->exited) {
      return Status::TargetExited;
    }
    process->frozen = true;
    return Result<void>::ok();
  }

  Result<void> thaw(PidfdView pidfd) override {
    std::lock_guard lock(mutex_);
    auto process = find_process_locked(pidfd);
    if (!process) {
      return Status::InvalidArgument;
    }
    process->frozen = false;
    return Result<void>::ok();
  }

  Result<Duplicate> checked_duplicate(
      PidfdView pidfd, int descriptor,
      const ExpectedFdBinding& expected) override {
    Result<Duplicate> result = Status::Unsupported;
    {
      std::lock_guard lock(mutex_);
      auto process = find_process_locked(pidfd);
      if (!process) {
        return Status::InvalidArgument;
      }
      if (process->exited) {
        return Status::TargetExited;
      }
      auto entry = process->fds.find(descriptor);
      if (entry == process->fds.end()) {
        return Status::DescriptorNotFound;
      }
      if (!(process->identity == expected.process) ||
          !(entry->second.record == expected.fd)) {
        return Status::IdentityMismatch;
      }
      result = copy_locked(process, entry->second);
    }
    run_after_copy_hook();
    return result;
  }

  Result<Duplicate> duplicate_fd(PidfdView pidfd, int descriptor) override {
    Result<Duplicate> result = Status::Unsupported;
    {
      std::lock_guard lock(mutex_);
      auto process = find_process_locked(pidfd);
      if (!process) {
        return Status::InvalidArgument;
      }
      if (process->exited) {
        return Status::TargetExited;
      }
      auto entry = process->fds.find(descriptor);
      if (entry == process->fds.end()) {
        return Status::DescriptorNotFound;
      }
      result = copy_locked(process, entry->second);
    }
    run_after_copy_hook();
    return result;
  }

  Result<CopyWitness> verify_copied_fd(const UniqueFd& fd) override {
    std::lock_guard lock(mutex_);
    auto found = copies_.find(fd.native_handle_for_kernel());
    if (found == copies_.end()) {
      return Status::CopyFailed;
    }
    ++verify_calls_;
    return found->second;
  }

  Result<std::uint64_t> subscribe_events(PidfdView pidfd,
                                          EventSink sink) override {
    std::lock_guard lock(mutex_);
    auto process = find_process_locked(pidfd);
    if (!process) {
      return Status::InvalidArgument;
    }
    const std::uint64_t id = next_subscription_++;
    sinks_[id] = std::move(sink);
    return id;
  }

  void unsubscribe_events(std::uint64_t subscription) noexcept override {
    std::lock_guard lock(mutex_);
    sinks_.erase(subscription);
  }

  void add_process(int pidfd, const ProcessIdentity& identity) {
    std::lock_guard lock(mutex_);
    Process process;
    process.identity = identity;
    processes_[pidfd] = std::move(process);
  }

  bool add_fd(int pidfd, int descriptor, UniqueFd fd, AccessMode access,
              OperationSet operations) {
    std::lock_guard lock(mutex_);
    auto process = find_process_locked(PidfdView(pidfd));
    if (!process || process->frozen || process->exited) {
      return false;
    }
    Entry entry;
    entry.fd = std::move(fd);
    entry.record.descriptor = descriptor;
    entry.record.slot_generation = ++next_slot_generation_;
    entry.record.description = identity_for(
        entry.fd.native_handle_for_kernel(),
        process->identity.mount_namespace.inode, ++next_description_token_);
    entry.record.access_mode = access;
    entry.record.allowed_operations = operations;
    process->fds[descriptor] = std::move(entry);
    ++process->fd_table_generation;
    return true;
  }

  bool replace_fd(int pidfd, int descriptor, UniqueFd fd, AccessMode access,
                  OperationSet operations, bool emit_event,
                  bool change_mount_namespace = false,
                  bool change_policy_domain = false) {
    std::lock_guard lock(mutex_);
    auto process = find_process_locked(PidfdView(pidfd));
    if (!process || process->frozen || process->exited) {
      return false;
    }
    Entry& entry = process->fds[descriptor];
    entry.fd = std::move(fd);
    entry.record.descriptor = descriptor;
    entry.record.slot_generation = ++next_slot_generation_;
    entry.record.description = identity_for(
        entry.fd.native_handle_for_kernel(),
        process->identity.mount_namespace.inode, ++next_description_token_);
    entry.record.access_mode = access;
    entry.record.allowed_operations = operations;
    ++process->fd_table_generation;
    if (change_mount_namespace) {
      ++process->identity.mount_namespace.inode;
      ++process->identity.mount_namespace.generation;
    }
    if (change_policy_domain) {
      ++process->identity.policy_domain.id;
      ++process->identity.policy_domain.generation;
    }
    if (emit_event) {
      pending_events_.push_back(
          KernelEvent{EventKind::FdReopened, 0, descriptor});
    }
    return true;
  }

  void flush_pending_events() {
    std::vector<KernelEvent> events;
    {
      std::lock_guard lock(mutex_);
      events.swap(pending_events_);
    }
    for (const KernelEvent& event : events) {
      emit(event);
    }
  }

  bool exec_and_reuse(int pidfd, int descriptor, UniqueFd fd,
                      AccessMode access, OperationSet operations,
                      bool emit_event) {
    std::lock_guard lock(mutex_);
    auto process = find_process_locked(PidfdView(pidfd));
    if (!process || process->frozen || process->exited) {
      return false;
    }
    Entry& entry = process->fds[descriptor];
    entry.fd = std::move(fd);
    entry.record.descriptor = descriptor;
    entry.record.slot_generation = ++next_slot_generation_;
    entry.record.description = identity_for(
        entry.fd.native_handle_for_kernel(),
        process->identity.mount_namespace.inode, ++next_description_token_);
    entry.record.access_mode = access;
    entry.record.allowed_operations = operations;
    ++process->fd_table_generation;
    ++process->identity.exec_generation;
    ++process->identity.pidfd.process_cookie;
    if (emit_event) {
      pending_events_.push_back(KernelEvent{EventKind::Exec, 0, -1});
    }
    return true;
  }

  bool setns(int pidfd, bool emit_event) {
    std::lock_guard lock(mutex_);
    auto process = find_process_locked(PidfdView(pidfd));
    if (!process || process->frozen || process->exited) {
      return false;
    }
    ++process->identity.mount_namespace.inode;
    ++process->identity.mount_namespace.generation;
    if (emit_event) {
      pending_events_.push_back(KernelEvent{EventKind::Setns, 0, -1});
    }
    return true;
  }

  bool credentials_changed(int pidfd, bool emit_event) {
    std::lock_guard lock(mutex_);
    auto process = find_process_locked(PidfdView(pidfd));
    if (!process || process->frozen || process->exited) {
      return false;
    }
    ++process->identity.user_namespace.generation;
    ++process->identity.policy_domain.id;
    if (emit_event) {
      pending_events_.push_back(
          KernelEvent{EventKind::CredentialsChanged, 0, -1});
    }
    return true;
  }

  bool reuse_pid(int pidfd, const ProcessIdentity& new_identity,
                 int descriptor, UniqueFd fd, AccessMode access,
                 OperationSet operations) {
    std::lock_guard lock(mutex_);
    auto found = processes_.find(pidfd);
    if (found == processes_.end() || found->second.frozen ||
        found->second.exited) {
      return false;
    }
    Process replacement;
    replacement.identity = new_identity;
    replacement.fd_table_generation = 1;
    Entry entry;
    entry.fd = std::move(fd);
    entry.record.descriptor = descriptor;
    entry.record.slot_generation = ++next_slot_generation_;
    entry.record.description = identity_for(
        entry.fd.native_handle_for_kernel(),
        new_identity.mount_namespace.inode, ++next_description_token_);
    entry.record.access_mode = access;
    entry.record.allowed_operations = operations;
    replacement.fds[descriptor] = std::move(entry);
    found->second = std::move(replacement);
    pending_events_.push_back(KernelEvent{EventKind::Exec, 0, -1});
    return true;
  }

  bool exit_process(int pidfd, bool emit_event) {
    std::lock_guard lock(mutex_);
    auto process = find_process_locked(PidfdView(pidfd));
    if (!process || process->exited) {
      return false;
    }
    process->exited = true;
    process->fds.clear();
    if (emit_event) {
      pending_events_.push_back(KernelEvent{EventKind::PidfdExited, 0, -1});
    }
    return true;
  }

  void emit(KernelEvent event) {
    std::vector<EventSink> sinks;
    {
      std::lock_guard lock(mutex_);
      event.sequence = ++next_event_sequence_;
      for (const auto& [id, sink] : sinks_) {
        (void)id;
        sinks.push_back(sink);
      }
    }
    for (const auto& sink : sinks) {
      sink(event);
    }
  }

  void set_after_copy_hook(std::function<void()> hook) {
    std::lock_guard lock(mutex_);
    after_copy_hook_ = std::move(hook);
  }

  std::uint64_t copy_attempts() const {
    std::lock_guard lock(mutex_);
    return copy_attempts_;
  }

  std::uint64_t verify_calls() const {
    std::lock_guard lock(mutex_);
    return verify_calls_;
  }

  std::optional<int> last_copy_fd() const {
    std::lock_guard lock(mutex_);
    return last_copy_fd_;
  }

 private:
  Process* find_process_locked(PidfdView pidfd) {
    auto found = processes_.find(pidfd.native_fd());
    return found == processes_.end() ? nullptr : &found->second;
  }

  Result<Duplicate> copy_locked(Process* process, const Entry& entry) {
    ++copy_attempts_;
    int copy = duplicate_cloexec(entry.fd.native_handle_for_kernel());
    if (copy < 0) {
      return Status::CopyFailed;
    }
    CopyWitness witness{process->identity, entry.record};
    copies_[copy] = witness;
    last_copy_fd_ = copy;
    return Duplicate{UniqueFd(copy), witness};
  }

  void run_after_copy_hook() {
    std::function<void()> hook;
    {
      std::lock_guard lock(mutex_);
      hook = after_copy_hook_;
    }
    if (hook) {
      hook();
    }
  }

  mutable std::mutex mutex_;
  std::map<int, Process> processes_;
  std::map<std::uint64_t, EventSink> sinks_;
  std::unordered_map<int, CopyWitness> copies_;
  std::vector<KernelEvent> pending_events_;
  std::function<void()> after_copy_hook_;
  bool atomic_ = false;
  bool freeze_ = false;
  std::uint64_t next_subscription_ = 1;
  std::uint64_t next_event_sequence_ = 0;
  std::uint64_t next_slot_generation_ = 0;
  std::uint64_t next_description_token_ = 0;
  std::uint64_t copy_attempts_ = 0;
  std::uint64_t verify_calls_ = 0;
  std::optional<int> last_copy_fd_;
};

struct Fixture {
  TempDir temp;
  std::shared_ptr<FakeKernel> kernel = std::make_shared<FakeKernel>();
  UniqueFd pidfd;
  int pidfd_number = -1;
  ProcessIdentity process =
      make_process(1000, 1, 100, 200, 300);

  bool initialize(bool atomic, bool freeze) {
    kernel->set_atomic(atomic);
    kernel->set_freeze_supported(freeze);
    pidfd = UniqueFd(::open("/dev/null", O_RDONLY | O_CLOEXEC));
    if (!pidfd.valid()) {
      return false;
    }
    pidfd_number = pidfd.native_handle_for_kernel();
    kernel->add_process(pidfd_number, process);
    return true;
  }

  UniqueFd read_only_file(const std::string& name,
                          const std::string& contents) const {
    return temp.create_read_only(name, contents);
  }
};

OperationSet readable_operations() {
  return OperationSet{Operation::Read, Operation::Pread, Operation::Fstat};
}

void test_legal_read_only_copy() {
  Fixture fixture;
  REQUIRE(fixture.initialize(true, false));
  REQUIRE(fixture.kernel->add_fd(fixture.pidfd_number, 7,
                                 fixture.read_only_file("public", "alpha"),
                                 AccessMode::Read, readable_operations()));

  auto agent = CapabilityAgent::open(fixture.kernel, std::move(fixture.pidfd));
  REQUIRE(agent);

  auto snapshot = (*agent)->enumerate();
  REQUIRE(snapshot);
  REQUIRE(snapshot.value().fds.size() == 1);
  CHECK(snapshot.value().fds[0].descriptor == 7);
  CHECK(snapshot.value().fds[0].access_mode == AccessMode::Read);
  CHECK(snapshot.value().fds[0].allowed_operations.contains(Operation::Pread));
  CHECK(snapshot.value().fds[0].description.description_token != 0);
  CHECK(snapshot.value().target == fixture.process);

  auto handle = (*agent)->duplicate_read_only(7);
  REQUIRE(handle);
  CHECK(handle.value().valid());
  CHECK(handle.value().metadata().source.descriptor == 7);
  CHECK(handle.value().metadata().source.description ==
        snapshot.value().fds[0].description);
  CHECK(handle.value().metadata().granted_operations ==
        OperationSet{Operation::Pread});

  std::byte buffer[16]{};
  auto bytes = handle.value().read_at(0, buffer);
  REQUIRE(bytes);
  CHECK(bytes.value() == 5);
  CHECK(std::string(reinterpret_cast<char*>(buffer), bytes.value()) == "alpha");
  CHECK(fixture.kernel->verify_calls() == 1);

  handle.value().close();
  CHECK(!handle.value().valid());
}

void test_exec_reuses_same_fd_number() {
  Fixture fixture;
  REQUIRE(fixture.initialize(true, false));
  REQUIRE(fixture.kernel->add_fd(fixture.pidfd_number, 7,
                                 fixture.read_only_file("public", "alpha"),
                                 AccessMode::Read, readable_operations()));

  auto agent = CapabilityAgent::open(fixture.kernel, std::move(fixture.pidfd));
  REQUIRE(agent);
  REQUIRE((*agent)->enumerate());

  REQUIRE(fixture.kernel->exec_and_reuse(
      fixture.pidfd_number, 7, fixture.read_only_file("secret", "TOPSECRET"),
      AccessMode::Read, readable_operations(), false));

  auto copied = (*agent)->duplicate_read_only(7);
  CHECK(!copied);
  CHECK(copied.status() == Status::IdentityMismatch);
  CHECK(fixture.kernel->copy_attempts() == 0);
}

void test_exec_event_revokes_snapshot() {
  Fixture fixture;
  REQUIRE(fixture.initialize(true, false));
  REQUIRE(fixture.kernel->add_fd(fixture.pidfd_number, 7,
                                 fixture.read_only_file("public", "alpha"),
                                 AccessMode::Read, readable_operations()));
  auto agent = CapabilityAgent::open(fixture.kernel, std::move(fixture.pidfd));
  REQUIRE(agent);
  REQUIRE((*agent)->enumerate());

  REQUIRE(fixture.kernel->exec_and_reuse(
      fixture.pidfd_number, 7, fixture.read_only_file("secret", "TOPSECRET"),
      AccessMode::Read, readable_operations(), true));
  fixture.kernel->flush_pending_events();

  auto copied = (*agent)->duplicate_read_only(7);
  CHECK(!copied);
  CHECK(copied.status() == Status::StaleSnapshot);
  CHECK(fixture.kernel->copy_attempts() == 0);
}

void test_pid_reuse() {
  Fixture fixture;
  REQUIRE(fixture.initialize(true, false));
  REQUIRE(fixture.kernel->add_fd(fixture.pidfd_number, 7,
                                 fixture.read_only_file("public", "alpha"),
                                 AccessMode::Read, readable_operations()));
  auto agent = CapabilityAgent::open(fixture.kernel, std::move(fixture.pidfd));
  REQUIRE(agent);
  REQUIRE((*agent)->enumerate());

  auto replacement = make_process(9000, 1, 900, 901, 902);
  REQUIRE(fixture.kernel->reuse_pid(
      fixture.pidfd_number, replacement, 7,
      fixture.read_only_file("other", "different"), AccessMode::Read,
      readable_operations()));
  fixture.kernel->flush_pending_events();

  auto copied = (*agent)->duplicate_read_only(7);
  CHECK(!copied);
  CHECK(copied.status() == Status::IdentityMismatch ||
        copied.status() == Status::StaleSnapshot);
  CHECK(fixture.kernel->copy_attempts() == 0);
}

void test_cross_mount_namespace() {
  Fixture fixture;
  REQUIRE(fixture.initialize(true, false));
  REQUIRE(fixture.kernel->add_fd(fixture.pidfd_number, 7,
                                 fixture.read_only_file("public", "alpha"),
                                 AccessMode::Read, readable_operations()));
  auto agent = CapabilityAgent::open(fixture.kernel, std::move(fixture.pidfd));
  REQUIRE(agent);
  REQUIRE((*agent)->enumerate());

  REQUIRE(fixture.kernel->setns(fixture.pidfd_number, false));
  auto copied = (*agent)->duplicate_read_only(7);
  CHECK(!copied);
  CHECK(copied.status() == Status::IdentityMismatch);
  CHECK(fixture.kernel->copy_attempts() == 0);
}

void test_concurrent_close_reopen_with_freeze() {
  Fixture fixture;
  REQUIRE(fixture.initialize(false, true));
  REQUIRE(fixture.kernel->add_fd(fixture.pidfd_number, 7,
                                 fixture.read_only_file("public", "alpha"),
                                 AccessMode::Read, readable_operations()));
  auto agent = CapabilityAgent::open(fixture.kernel, std::move(fixture.pidfd));
  REQUIRE(agent);
  REQUIRE((*agent)->enumerate());

  fixture.kernel->set_after_copy_hook([&] {
    // The target is frozen by the agent for this non-atomic backend, so this
    // close/reopen attempt must fail and the copied description must stay old.
    CHECK(!fixture.kernel->replace_fd(
        fixture.pidfd_number, 7,
        fixture.read_only_file("secret", "TOPSECRET"), AccessMode::Read,
        readable_operations(), false));
  });

  auto copied = (*agent)->duplicate_read_only(7);
  REQUIRE(copied);
  std::byte buffer[16]{};
  auto bytes = copied.value().read_at(0, buffer);
  REQUIRE(bytes);
  CHECK(std::string(reinterpret_cast<char*>(buffer), bytes.value()) == "alpha");
}

void test_non_atomic_without_freeze_is_denied() {
  Fixture fixture;
  REQUIRE(fixture.initialize(false, false));
  REQUIRE(fixture.kernel->add_fd(fixture.pidfd_number, 7,
                                 fixture.read_only_file("public", "alpha"),
                                 AccessMode::Read, readable_operations()));
  auto agent = CapabilityAgent::open(fixture.kernel, std::move(fixture.pidfd));
  REQUIRE(agent);
  REQUIRE((*agent)->enumerate());

  fixture.kernel->set_after_copy_hook(
      [&] { CHECK(false); });
  auto copied = (*agent)->duplicate_read_only(7);
  CHECK(!copied);
  CHECK(copied.status() == Status::UnstableKernelInterface);
  CHECK(fixture.kernel->copy_attempts() == 0);
}

void test_late_copy_is_closed() {
  Fixture fixture;
  REQUIRE(fixture.initialize(true, false));
  REQUIRE(fixture.kernel->add_fd(fixture.pidfd_number, 7,
                                 fixture.read_only_file("public", "alpha"),
                                 AccessMode::Read, readable_operations()));
  auto agent = CapabilityAgent::open(fixture.kernel, std::move(fixture.pidfd));
  REQUIRE(agent);
  REQUIRE((*agent)->enumerate());

  std::mutex hook_mutex;
  std::condition_variable hook_cv;
  bool copied = false;
  bool release = false;
  fixture.kernel->set_after_copy_hook([&] {
    std::unique_lock lock(hook_mutex);
    copied = true;
    hook_cv.notify_all();
    hook_cv.wait(lock, [&] { return release; });
  });

  auto pending = (*agent)->duplicate_read_only_async(7);
  {
    std::unique_lock lock(hook_mutex);
    REQUIRE(hook_cv.wait_for(lock, 2s, [&] { return copied; }));
  }

  REQUIRE(fixture.kernel->exec_and_reuse(
      fixture.pidfd_number, 7, fixture.read_only_file("secret", "TOPSECRET"),
      AccessMode::Read, readable_operations(), true));
  fixture.kernel->flush_pending_events();

  {
    std::lock_guard lock(hook_mutex);
    release = true;
  }
  hook_cv.notify_all();

  auto result = pending.get();
  CHECK(!result);
  CHECK(result.status() == Status::StaleSnapshot ||
        result.status() == Status::IdentityMismatch);
  auto copy_fd = fixture.kernel->last_copy_fd();
  REQUIRE(copy_fd.has_value());
  CHECK(::fcntl(*copy_fd, F_GETFD) == -1);
}

void test_target_exit_during_copy() {
  Fixture fixture;
  REQUIRE(fixture.initialize(true, false));
  REQUIRE(fixture.kernel->add_fd(fixture.pidfd_number, 7,
                                 fixture.read_only_file("public", "alpha"),
                                 AccessMode::Read, readable_operations()));
  auto agent = CapabilityAgent::open(fixture.kernel, std::move(fixture.pidfd));
  REQUIRE(agent);
  REQUIRE((*agent)->enumerate());

  fixture.kernel->set_after_copy_hook(
      [&] {
        CHECK(fixture.kernel->exit_process(fixture.pidfd_number, true));
        fixture.kernel->flush_pending_events();
      });

  auto copied = (*agent)->duplicate_read_only(7);
  CHECK(!copied);
  CHECK(copied.status() == Status::TargetExited ||
        copied.status() == Status::StaleSnapshot);
  auto copy_fd = fixture.kernel->last_copy_fd();
  REQUIRE(copy_fd.has_value());
  CHECK(::fcntl(*copy_fd, F_GETFD) == -1);
}

void test_fd_event_revokes_existing_handle() {
  Fixture fixture;
  REQUIRE(fixture.initialize(true, false));
  REQUIRE(fixture.kernel->add_fd(fixture.pidfd_number, 7,
                                 fixture.read_only_file("public", "alpha"),
                                 AccessMode::Read, readable_operations()));
  auto agent = CapabilityAgent::open(fixture.kernel, std::move(fixture.pidfd));
  REQUIRE(agent);
  REQUIRE((*agent)->enumerate());

  auto handle = (*agent)->duplicate_read_only(7);
  REQUIRE(handle);
  REQUIRE(handle.value().valid());
  auto copy_fd = fixture.kernel->last_copy_fd();
  REQUIRE(copy_fd.has_value());

  fixture.kernel->emit(KernelEvent{EventKind::FdClosed, 0, 7});
  CHECK(!handle.value().valid());
  CHECK(::fcntl(*copy_fd, F_GETFD) == -1);

  auto after = (*agent)->duplicate_read_only(7);
  CHECK(!after);
  CHECK(after.status() == Status::StaleSnapshot);
}

void test_credentials_event_revokes_existing_handle() {
  Fixture fixture;
  REQUIRE(fixture.initialize(true, false));
  REQUIRE(fixture.kernel->add_fd(fixture.pidfd_number, 7,
                                 fixture.read_only_file("public", "alpha"),
                                 AccessMode::Read, readable_operations()));
  auto agent = CapabilityAgent::open(fixture.kernel, std::move(fixture.pidfd));
  REQUIRE(agent);
  REQUIRE((*agent)->enumerate());

  auto handle = (*agent)->duplicate_read_only(7);
  REQUIRE(handle);
  auto copy_fd = fixture.kernel->last_copy_fd();
  REQUIRE(copy_fd.has_value());

  REQUIRE(fixture.kernel->credentials_changed(fixture.pidfd_number, true));
  fixture.kernel->flush_pending_events();
  CHECK(!handle.value().valid());
  CHECK(::fcntl(*copy_fd, F_GETFD) == -1);

  auto after = (*agent)->duplicate_read_only(7);
  CHECK(!after);
  CHECK(after.status() == Status::StaleSnapshot);
}

void test_writable_source_is_rejected() {
  Fixture fixture;
  REQUIRE(fixture.initialize(true, false));
  REQUIRE(fixture.kernel->add_fd(
      fixture.pidfd_number, 7,
      fixture.temp.create_read_write("writable", "alpha"), AccessMode::ReadWrite,
      OperationSet{Operation::Read, Operation::Pread, Operation::Write,
                   Operation::Fstat}));
  auto agent = CapabilityAgent::open(fixture.kernel, std::move(fixture.pidfd));
  REQUIRE(agent);
  REQUIRE((*agent)->enumerate());

  auto copied = (*agent)->duplicate_read_only(7);
  CHECK(!copied);
  CHECK(copied.status() == Status::OperationNotPermitted);
  CHECK(fixture.kernel->copy_attempts() == 0);
}

}  // namespace

int main() {
  test_legal_read_only_copy();
  test_exec_reuses_same_fd_number();
  test_exec_event_revokes_snapshot();
  test_pid_reuse();
  test_cross_mount_namespace();
  test_concurrent_close_reopen_with_freeze();
  test_non_atomic_without_freeze_is_denied();
  test_late_copy_is_closed();
  test_target_exit_during_copy();
  test_fd_event_revokes_existing_handle();
  test_credentials_event_revokes_existing_handle();
  test_writable_source_is_rejected();

  if (failures != 0) {
    std::cerr << failures << " test assertion(s) failed\n";
    return 1;
  }
  std::cout << "all pidfd capability tests passed\n";
  return 0;
}
