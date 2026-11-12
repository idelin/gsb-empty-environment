#pragma once

#include <cstdint>
#include <memory>
#include <optional>
#include <unordered_map>

namespace netsec {

// 128-bit PAC key, modeled after ARMv8.3 pointer-authentication keys.
struct PacKey {
    std::uint64_t lo = 0;
    std::uint64_t hi = 0;
};

bool operator==(const PacKey& a, const PacKey& b);
bool operator!=(const PacKey& a, const PacKey& b);

using TaskId = std::uint64_t;

enum class KeyClass { User, Kernel };
enum class TaskMode { User, Kernel };

struct Signature {
    std::uint64_t value = 0;
};

// Key-domain manager for a kernel task-manager model.
//
// Rules implemented:
//  - fork: child inherits the parent's key (shares the key family).
//  - exec: the task always receives a fresh key (new family).
//  - cumulative authentication failures within one key family are
//    counted; once the threshold is reached the family is poisoned and
//    the next fork hands the child a fresh key.
//  - kernel entry switches the task to a dedicated kernel key that is
//    never exposed to userspace (including debug read requests).
class KeyDomainManager {
public:
    struct KeyFamily; // opaque key-family state, defined in netsec.cpp

    struct Config {
        std::uint64_t failure_threshold = 8;
        std::uint64_t rng_seed = 0x9e3779b97f4a7c15ULL;
    };

    KeyDomainManager();
    explicit KeyDomainManager(Config cfg);

    // Task lifecycle.
    TaskId create_task();            // fresh key family
    TaskId fork_task(TaskId parent); // inherit, or rotate if family poisoned
    void exec_task(TaskId id);       // always rotate to a fresh key
    TaskId clone_thread(TaskId id);  // shares key and family with the task
    void exit_task(TaskId id);

    // Pointer signing / authentication with the task's active key.
    Signature sign(TaskId id, std::uint64_t ptr) const;
    bool authenticate(TaskId id, std::uint64_t ptr, Signature sig);

    // Kernel entry/exit: switches the active key domain.
    void enter_kernel(TaskId id);
    void exit_kernel(TaskId id);

    // Signal frame saved on delivery, restored on sigreturn. The frame
    // is MACed with the kernel key so userspace cannot forge it.
    struct SignalFrame {
        TaskId task = 0;
        TaskMode saved_mode = TaskMode::User;
        std::uint64_t nonce = 0;
        Signature mac;

    private:
        friend class KeyDomainManager;
        std::shared_ptr<KeyFamily> saved_family;
    };

    SignalFrame save_signal_frame(TaskId id) const;
    bool restore_signal_frame(TaskId id, const SignalFrame& frame);

    // Debug (ptrace-like) key read. The kernel key is never returned.
    std::optional<PacKey> debug_read_key(TaskId requester, TaskId target,
                                         KeyClass cls) const;
    void set_debug_privilege(TaskId id, bool privileged);

    // Kernel-internal introspection (not reachable from modeled userspace).
    const PacKey& user_key(TaskId id) const;
    std::uint64_t family_failures(TaskId id) const;
    bool same_family(TaskId a, TaskId b) const;
    TaskMode mode(TaskId id) const;

private:
    struct Task {
        std::shared_ptr<KeyFamily> family;
        TaskMode mode = TaskMode::User;
        bool debug_privileged = false;
    };

    Task& task_at(TaskId id);
    const Task& task_at(TaskId id) const;
    PacKey fresh_key();
    const PacKey& active_key(const Task& t) const;

    Config cfg_;
    std::uint64_t rng_state_;
    TaskId next_task_id_ = 1;
    PacKey kernel_key_;
    std::unordered_map<TaskId, Task> tasks_;
};

} // namespace netsec
