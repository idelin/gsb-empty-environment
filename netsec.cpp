#include "netsec.hpp"

#include <stdexcept>

namespace netsec {

namespace {

std::uint64_t mix64(std::uint64_t x) {
    x += 0x9e3779b97f4a7c15ULL;
    x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9ULL;
    x = (x ^ (x >> 27)) * 0x94d049bb133111ebULL;
    return x ^ (x >> 31);
}

Signature compute_signature(const PacKey& key, std::uint64_t ptr) {
    return {mix64(key.lo ^ mix64(ptr ^ key.hi))};
}

} // namespace

struct KeyDomainManager::KeyFamily {
    PacKey key;
    std::uint64_t failures = 0;
    bool rotation_pending = false;
};

bool operator==(const PacKey& a, const PacKey& b) {
    return a.lo == b.lo && a.hi == b.hi;
}

bool operator!=(const PacKey& a, const PacKey& b) { return !(a == b); }

KeyDomainManager::KeyDomainManager() : KeyDomainManager(Config{}) {}

KeyDomainManager::KeyDomainManager(Config cfg)
    : cfg_(cfg), rng_state_(cfg.rng_seed) {
    if (cfg_.failure_threshold == 0) {
        cfg_.failure_threshold = 1;
    }
    kernel_key_ = fresh_key();
}

PacKey KeyDomainManager::fresh_key() {
    PacKey key;
    rng_state_ += 0x9e3779b97f4a7c15ULL;
    key.lo = mix64(rng_state_);
    rng_state_ += 0x9e3779b97f4a7c15ULL;
    key.hi = mix64(rng_state_);
    return key;
}

KeyDomainManager::Task& KeyDomainManager::task_at(TaskId id) {
    auto it = tasks_.find(id);
    if (it == tasks_.end()) {
        throw std::out_of_range("unknown task id");
    }
    return it->second;
}

const KeyDomainManager::Task& KeyDomainManager::task_at(TaskId id) const {
    auto it = tasks_.find(id);
    if (it == tasks_.end()) {
        throw std::out_of_range("unknown task id");
    }
    return it->second;
}

const PacKey& KeyDomainManager::active_key(const Task& t) const {
    return t.mode == TaskMode::Kernel ? kernel_key_ : t.family->key;
}

TaskId KeyDomainManager::create_task() {
    Task t;
    t.family = std::make_shared<KeyFamily>();
    t.family->key = fresh_key();
    const TaskId id = next_task_id_++;
    tasks_.emplace(id, std::move(t));
    return id;
}

TaskId KeyDomainManager::fork_task(TaskId parent) {
    const Task& p = task_at(parent);
    Task child;
    if (p.family->rotation_pending) {
        // Family accumulated too many auth failures: the forked child
        // starts over with a fresh key and a clean failure counter.
        child.family = std::make_shared<KeyFamily>();
        child.family->key = fresh_key();
    } else {
        child.family = p.family;
    }
    const TaskId id = next_task_id_++;
    tasks_.emplace(id, std::move(child));
    return id;
}

void KeyDomainManager::exec_task(TaskId id) {
    Task& t = task_at(id);
    // exec always rotates: the old image's key must not sign new code.
    t.family = std::make_shared<KeyFamily>();
    t.family->key = fresh_key();
    t.mode = TaskMode::User;
}

TaskId KeyDomainManager::clone_thread(TaskId id) {
    const Task& t = task_at(id);
    Task thread;
    thread.family = t.family; // threads share the address space and key
    thread.mode = t.mode;
    const TaskId tid = next_task_id_++;
    tasks_.emplace(tid, std::move(thread));
    return tid;
}

void KeyDomainManager::exit_task(TaskId id) { tasks_.erase(id); }

Signature KeyDomainManager::sign(TaskId id, std::uint64_t ptr) const {
    return compute_signature(active_key(task_at(id)), ptr);
}

bool KeyDomainManager::authenticate(TaskId id, std::uint64_t ptr,
                                    Signature sig) {
    Task& t = task_at(id);
    const bool ok = compute_signature(active_key(t), ptr).value == sig.value;
    if (!ok && t.mode == TaskMode::User) {
        // Failed user-mode authentications accumulate per key family.
        if (++t.family->failures >= cfg_.failure_threshold) {
            t.family->rotation_pending = true;
        }
    }
    return ok;
}

void KeyDomainManager::enter_kernel(TaskId id) {
    task_at(id).mode = TaskMode::Kernel;
}

void KeyDomainManager::exit_kernel(TaskId id) {
    task_at(id).mode = TaskMode::User;
}

KeyDomainManager::SignalFrame
KeyDomainManager::save_signal_frame(TaskId id) const {
    const Task& t = task_at(id);
    SignalFrame frame;
    frame.task = id;
    frame.saved_mode = t.mode;
    frame.saved_family = t.family;
    frame.nonce = mix64(rng_state_ ^ 0xa5a5a5a55a5a5a5aULL);
    std::uint64_t h = kernel_key_.lo;
    h = mix64(h ^ frame.task);
    h = mix64(h ^ static_cast<std::uint64_t>(frame.saved_mode));
    h = mix64(h ^ t.family->key.lo);
    h = mix64(h ^ t.family->key.hi);
    h = mix64(h ^ frame.nonce ^ kernel_key_.hi);
    frame.mac = {h};
    return frame;
}

bool KeyDomainManager::restore_signal_frame(TaskId id,
                                            const SignalFrame& frame) {
    Task& t = task_at(id);
    if (frame.task != id || !frame.saved_family) {
        return false;
    }
    std::uint64_t h = kernel_key_.lo;
    h = mix64(h ^ frame.task);
    h = mix64(h ^ static_cast<std::uint64_t>(frame.saved_mode));
    h = mix64(h ^ frame.saved_family->key.lo);
    h = mix64(h ^ frame.saved_family->key.hi);
    h = mix64(h ^ frame.nonce ^ kernel_key_.hi);
    if (h != frame.mac.value) {
        return false; // forged or corrupted frame
    }
    t.family = frame.saved_family;
    t.mode = frame.saved_mode;
    return true;
}

std::optional<PacKey>
KeyDomainManager::debug_read_key(TaskId requester, TaskId target,
                                 KeyClass cls) const {
    if (cls == KeyClass::Kernel) {
        return std::nullopt; // kernel key never leaves the kernel
    }
    const Task& req = task_at(requester);
    if (!req.debug_privileged) {
        return std::nullopt;
    }
    return task_at(target).family->key;
}

void KeyDomainManager::set_debug_privilege(TaskId id, bool privileged) {
    task_at(id).debug_privileged = privileged;
}

const PacKey& KeyDomainManager::user_key(TaskId id) const {
    return task_at(id).family->key;
}

std::uint64_t KeyDomainManager::family_failures(TaskId id) const {
    return task_at(id).family->failures;
}

bool KeyDomainManager::same_family(TaskId a, TaskId b) const {
    return task_at(a).family == task_at(b).family;
}

TaskMode KeyDomainManager::mode(TaskId id) const {
    return task_at(id).mode;
}

} // namespace netsec
