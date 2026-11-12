#include "netsec.hpp"

#include <cstdio>

namespace {

int failures = 0;

#define CHECK(cond)                                                          \
    do {                                                                   \
        if (!(cond)) {                                                     \
            std::fprintf(stderr, "FAIL %s:%d: %s\n", __FILE__, __LINE__, \
                         #cond);                                           \
            ++failures;                                                    \
        }                                                                  \
    } while (0)

using netsec::KeyClass;
using netsec::KeyDomainManager;
using netsec::Signature;
using netsec::TaskMode;

Signature bad_sig() { return {0xdeadbeefcafebabeULL}; }

// fork inherits the parent key while the family is below the failure
// threshold; reaching the threshold poisons the family so the next fork
// gets a fresh key, while the parent keeps its (possibly leaked) key.
void threshold_boundary() {
    KeyDomainManager mgr({3, 42});
    const auto parent = mgr.create_task();

    // Two failures: still below threshold, fork shares the key.
    CHECK(!mgr.authenticate(parent, 0x1000, bad_sig()));
    CHECK(!mgr.authenticate(parent, 0x1000, bad_sig()));
    CHECK(mgr.family_failures(parent) == 2);
    const auto child1 = mgr.fork_task(parent);
    CHECK(mgr.same_family(parent, child1));
    CHECK(mgr.user_key(child1) == mgr.user_key(parent));
    // A signature made by the parent validates in the shared-key child.
    const Signature s = mgr.sign(parent, 0x2000);
    CHECK(mgr.authenticate(child1, 0x2000, s));

    // Third failure hits the threshold: family is poisoned.
    CHECK(!mgr.authenticate(parent, 0x1000, bad_sig()));
    CHECK(mgr.family_failures(parent) == 3);
    const auto child2 = mgr.fork_task(parent);
    CHECK(!mgr.same_family(parent, child2));
    CHECK(mgr.user_key(child2) != mgr.user_key(parent));
    CHECK(mgr.family_failures(child2) == 0);
    // Old signatures no longer validate against the child's fresh key.
    CHECK(!mgr.authenticate(child2, 0x2000, s));
    // The parent keeps its key until it execs.
    CHECK(mgr.family_failures(parent) == 3);
    // The family stays poisoned: further forks also get fresh keys.
    const auto child3 = mgr.fork_task(parent);
    CHECK(!mgr.same_family(parent, child3));
    CHECK(mgr.user_key(child3) != mgr.user_key(parent));
}

// exec always rotates the key, even for a clean family, and resets the
// failure accounting so later forks inherit the fresh key.
void exec_rotates_key() {
    KeyDomainManager mgr({4, 7});
    const auto t = mgr.create_task();
    const auto before = mgr.user_key(t);
    const Signature old = mgr.sign(t, 0x3000);

    mgr.exec_task(t);
    CHECK(mgr.user_key(t) != before);
    CHECK(mgr.family_failures(t) == 0);
    // Signatures from the old image are useless after exec.
    CHECK(!mgr.authenticate(t, 0x3000, old));
    CHECK(mgr.family_failures(t) == 1);

    // exec of a poisoned family also resets rotation state.
    KeyDomainManager mgr2({1, 9});
    const auto p = mgr2.create_task();
    CHECK(!mgr2.authenticate(p, 0x1, bad_sig())); // threshold 1 -> poisoned
    mgr2.exec_task(p);
    const auto child = mgr2.fork_task(p);
    CHECK(mgr2.same_family(p, child));
    CHECK(mgr2.user_key(child) == mgr2.user_key(p));
}

// Threads share the key and the failure counter of their task's family.
void threads_share_key() {
    KeyDomainManager mgr({2, 11});
    const auto t = mgr.create_task();
    const auto th = mgr.clone_thread(t);
    CHECK(mgr.same_family(t, th));

    const Signature s = mgr.sign(t, 0x4000);
    CHECK(mgr.authenticate(th, 0x4000, s));

    // One failure per thread reaches the shared threshold of two.
    CHECK(!mgr.authenticate(t, 0x1, bad_sig()));
    CHECK(!mgr.authenticate(th, 0x1, bad_sig()));
    CHECK(mgr.family_failures(t) == 2);
    CHECK(mgr.family_failures(th) == 2);
    const auto child = mgr.fork_task(t);
    CHECK(!mgr.same_family(t, child));
}

// Signal frames are MACed with the kernel key: a genuine sigreturn
// restores the pre-signal key context, a tampered frame is rejected.
void signal_frame_restore() {
    KeyDomainManager mgr({8, 13});
    const auto t = mgr.create_task();
    const auto pre_signal_key = mgr.user_key(t);

    auto frame = mgr.save_signal_frame(t);
    // Handler runs, then the task execs (key rotates) before sigreturn.
    mgr.exec_task(t);
    CHECK(mgr.user_key(t) != pre_signal_key);
    CHECK(mgr.restore_signal_frame(t, frame));
    CHECK(mgr.user_key(t) == pre_signal_key);
    CHECK(mgr.mode(t) == TaskMode::User);

    // A frame saved in kernel mode restores kernel mode.
    mgr.enter_kernel(t);
    auto kframe = mgr.save_signal_frame(t);
    mgr.exit_kernel(t);
    CHECK(mgr.restore_signal_frame(t, kframe));
    CHECK(mgr.mode(t) == TaskMode::Kernel);
    mgr.exit_kernel(t);

    // Tampered frames are rejected and leave the task untouched.
    auto forged = mgr.save_signal_frame(t);
    forged.nonce ^= 1;
    CHECK(!mgr.restore_signal_frame(t, forged));
    forged = mgr.save_signal_frame(t);
    forged.mac.value ^= 1;
    CHECK(!mgr.restore_signal_frame(t, forged));
    // A frame belongs to its task and cannot be replayed elsewhere.
    const auto other = mgr.create_task();
    auto foreign = mgr.save_signal_frame(t);
    CHECK(!mgr.restore_signal_frame(other, foreign));
}

// Kernel entry switches to a dedicated kernel key; userspace (including
// privileged debuggers) can never read it, while the user key is
// readable only by a privileged requester.
void kernel_key_isolation_and_debug_read() {
    KeyDomainManager mgr({8, 17});
    const auto target = mgr.create_task();
    const auto debugger = mgr.create_task();

    // Unprivileged debug request is denied.
    CHECK(!mgr.debug_read_key(debugger, target, KeyClass::User).has_value());
    // Privileged debugger reads the user key.
    mgr.set_debug_privilege(debugger, true);
    const auto read = mgr.debug_read_key(debugger, target, KeyClass::User);
    CHECK(read.has_value());
    CHECK(*read == mgr.user_key(target));
    // The kernel key is never exposed, even to a privileged debugger.
    CHECK(!mgr.debug_read_key(debugger, target, KeyClass::Kernel).has_value());

    // Kernel entry uses a different key than user mode.
    const Signature user_sig = mgr.sign(target, 0x5000);
    mgr.enter_kernel(target);
    CHECK(mgr.mode(target) == TaskMode::Kernel);
    const Signature kernel_sig = mgr.sign(target, 0x5000);
    CHECK(kernel_sig.value != user_sig.value);
    // User signatures do not authenticate in kernel mode and vice versa.
    CHECK(!mgr.authenticate(target, 0x5000, user_sig));
    // Kernel-mode mismatches are not counted as user-key failures: the
    // kernel key is not an oracle for the user key family.
    CHECK(mgr.family_failures(target) == 0);
    mgr.exit_kernel(target);
    CHECK(!mgr.authenticate(target, 0x5000, kernel_sig));
    CHECK(mgr.family_failures(target) == 1);
}

} // namespace

int main() {
    threshold_boundary();
    exec_rotates_key();
    threads_share_key();
    signal_frame_restore();
    kernel_key_isolation_and_debug_read();
    if (failures != 0) {
        std::fprintf(stderr, "%d check(s) failed\n", failures);
        return 1;
    }
    std::puts("all key-domain tests passed");
    return 0;
}
