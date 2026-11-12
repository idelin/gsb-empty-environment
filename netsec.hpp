#pragma once

#include <cstddef>
#include <cstdint>
#include <deque>
#include <functional>
#include <map>
#include <memory>
#include <optional>
#include <ostream>
#include <stdexcept>
#include <string>
#include <string_view>
#include <type_traits>
#include <utility>
#include <vector>

namespace netsec {

using DeviceNumber = std::uint64_t;
using InodeNumber = std::uint64_t;

struct FileId {
    union {
        DeviceNumber device;
        DeviceNumber dev;
    };
    union {
        InodeNumber inode;
        InodeNumber ino;
    };

    constexpr FileId() : device(0), inode(0) {}

    constexpr FileId(DeviceNumber dev, InodeNumber ino)
        : device(dev), inode(ino) {}

    friend constexpr bool operator==(const FileId& lhs, const FileId& rhs) {
        return lhs.device == rhs.device && lhs.inode == rhs.inode;
    }

    friend constexpr bool operator!=(const FileId& lhs, const FileId& rhs) {
        return !(lhs == rhs);
    }

    friend constexpr bool operator<(const FileId& lhs, const FileId& rhs) {
        return lhs.device < rhs.device ||
               (lhs.device == rhs.device && lhs.inode < rhs.inode);
    }
};

using FileIdentity = FileId;
using FileKey = FileId;
using InodeKey = FileId;
using ProtectedFile = FileId;

enum class Decision : std::uint8_t {
    Allow = 0,
    Deny = 1,
    Allowed = 0,
    Denied = 1,
    kAllow = 0,
    kDeny = 1,
    ALLOW = 0,
    DENY = 1,
    allow = 0,
    deny = 1,
    Permit = 0,
    Reject = 1,
};

inline constexpr Decision Allow = Decision::Allow;
inline constexpr Decision Deny = Decision::Deny;
inline constexpr Decision ALLOW = Decision::ALLOW;
inline constexpr Decision DENY = Decision::DENY;

constexpr bool is_allow(Decision decision) {
    return decision == Decision::Allow;
}

constexpr bool is_deny(Decision decision) {
    return decision == Decision::Deny;
}

constexpr const char* to_string(Decision decision) {
    return decision == Decision::Allow ? "allow" : "deny";
}

struct HookContext {
    FileId file;
    std::string path;
    std::string operation = "open";

    HookContext() = default;

    explicit HookContext(FileId id, std::string path_hint = {},
                         std::string op = "open")
        : file(id), path(std::move(path_hint)), operation(std::move(op)) {}

    HookContext(DeviceNumber dev, InodeNumber ino, std::string path_hint = {},
                std::string op = "open")
        : file(dev, ino), path(std::move(path_hint)), operation(std::move(op)) {}

    DeviceNumber device() const { return file.device; }
    InodeNumber inode() const { return file.inode; }
    const std::string& path_hint() const { return path; }
    const FileId& id() const { return file; }
};

using HookEvent = HookContext;
using OpenEvent = HookContext;
using FileAccessEvent = HookContext;

namespace detail {

template <typename>
struct AlwaysFalse : std::false_type {};

inline Decision normalize_decision(Decision decision) {
    return decision;
}

inline Decision normalize_decision(bool denied) {
    return denied ? Decision::Deny : Decision::Allow;
}

template <typename T>
inline std::enable_if_t<std::is_integral_v<T> && !std::is_same_v<T, bool>,
                        Decision>
normalize_decision(T lsm_return_value) {
    return lsm_return_value == 0 ? Decision::Allow : Decision::Deny;
}

template <typename T>
inline Decision normalize_decision(const std::optional<T>& value) {
    return value ? normalize_decision(*value) : Decision::Allow;
}

template <typename F>
std::function<Decision(const HookContext&)> wrap_callback(F&& callback) {
    using Callback = std::decay_t<F>;
    return [fn = std::forward<F>(callback)](const HookContext& context) mutable
               -> Decision {
        if constexpr (std::is_invocable_v<Callback&, const HookContext&>) {
            return normalize_decision(fn(context));
        } else if constexpr (std::is_invocable_v<Callback&, const FileId&,
                                                 const std::string&>) {
            return normalize_decision(fn(context.file, context.path));
        } else if constexpr (std::is_invocable_v<Callback&, DeviceNumber,
                                                 InodeNumber,
                                                 const std::string&>) {
            return normalize_decision(
                fn(context.file.device, context.file.inode, context.path));
        } else if constexpr (std::is_invocable_v<Callback&, DeviceNumber,
                                                 InodeNumber>) {
            return normalize_decision(fn(context.file.device,
                                         context.file.inode));
        } else if constexpr (std::is_invocable_v<Callback&, const std::string&,
                                                 const FileId&>) {
            return normalize_decision(fn(context.path, context.file));
        } else if constexpr (std::is_invocable_v<Callback&, const std::string&,
                                                 DeviceNumber,
                                                 InodeNumber>) {
            return normalize_decision(
                fn(context.path, context.file.device, context.file.inode));
        } else if constexpr (std::is_invocable_v<Callback&, const FileId&>) {
            return normalize_decision(fn(context.file));
        } else if constexpr (std::is_invocable_v<Callback&,
                                                 const std::string&>) {
            return normalize_decision(fn(context.path));
        } else if constexpr (std::is_invocable_v<Callback&>) {
            return normalize_decision(fn());
        } else {
            static_assert(AlwaysFalse<Callback>::value,
                          "program callback has an unsupported signature");
        }
    };
}

}  // namespace detail

class Program {
public:
    using Callback = std::function<Decision(const HookContext&)>;

    Program() = default;

    Program(std::string name, Callback callback)
        : name_(std::move(name)), callback_(std::move(callback)) {}

    template <typename F,
              typename = std::enable_if_t<
                  !std::is_same_v<std::decay_t<F>, Program>>>
    Program(std::string name, F&& callback)
        : name_(std::move(name)),
          callback_(detail::wrap_callback(std::forward<F>(callback))) {}

    const std::string& name() const { return name_; }

    Decision evaluate(const HookContext& context) const {
        return callback_ ? callback_(context) : Decision::Allow;
    }

    explicit operator bool() const {
        return static_cast<bool>(callback_);
    }

private:
    std::string name_;
    Callback callback_;
};

using LsmProgram = Program;
using HookProgram = Program;

struct AuditRecord {
    Decision decision = Decision::Allow;
    std::string denying_program;
    std::uint64_t inode = 0;
    std::uint64_t dropped = 0;
    std::uint64_t device = 0;
    std::uint64_t sequence = 0;
    std::string path;
    std::string policy_path;
    FileId file;
    std::uint64_t lost = 0;
    std::uint64_t dropped_events = 0;
    std::string first_denying_program;
    std::string first_deny_program;
    std::string denied_by;
    std::string program;
    std::uint64_t dev = 0;
    std::uint64_t ino = 0;

    bool allowed() const { return decision == Decision::Allow; }
    bool denied() const { return decision == Decision::Deny; }
    const std::string& denier() const { return denying_program; }

    friend bool operator==(const AuditRecord& lhs, const AuditRecord& rhs) {
        return lhs.decision == rhs.decision &&
               lhs.denying_program == rhs.denying_program &&
               lhs.inode == rhs.inode && lhs.device == rhs.device &&
               lhs.dropped == rhs.dropped && lhs.sequence == rhs.sequence &&
               lhs.path == rhs.path && lhs.policy_path == rhs.policy_path;
    }

    friend bool operator!=(const AuditRecord& lhs, const AuditRecord& rhs) {
        return !(lhs == rhs);
    }
};

using AuditEntry = AuditRecord;

namespace detail {

template <typename T>
struct RingDropStamp {
    static void apply(T&, std::uint64_t) {}
};

template <>
struct RingDropStamp<AuditRecord> {
    static void apply(AuditRecord& record, std::uint64_t lost) {
        record.dropped = lost;
        record.lost = lost;
        record.dropped_events = lost;
    }
};

}  // namespace detail

template <typename T = AuditRecord>
class RingBuffer {
public:
    explicit RingBuffer(std::size_t capacity = 64)
        : capacity_(capacity) {}

    bool push(T value) {
        ++total_attempts_;
        if (capacity_ == 0 || queue_.size() >= capacity_) {
            ++dropped_;
            ++total_dropped_;
            return false;
        }
        detail::RingDropStamp<T>::apply(value, dropped_);
        dropped_ = 0;
        queue_.push_back(std::move(value));
        ++stored_;
        return true;
    }

    bool record(T value) { return push(std::move(value)); }
    bool append(T value) { return push(std::move(value)); }
    bool write(T value) { return push(std::move(value)); }

    std::optional<T> pop() {
        if (queue_.empty()) {
            return std::nullopt;
        }
        T value = std::move(queue_.front());
        queue_.pop_front();
        return value;
    }

    std::optional<T> pop_front() { return pop(); }

    bool pop(T& value) {
        if (queue_.empty()) {
            return false;
        }
        value = std::move(queue_.front());
        queue_.pop_front();
        return true;
    }

    const T& front() const { return queue_.front(); }
    T& front() { return queue_.front(); }
    const T& back() const { return queue_.back(); }
    T& back() { return queue_.back(); }

    const T& at(std::size_t index) const { return queue_.at(index); }
    T& at(std::size_t index) { return queue_.at(index); }
    const T& operator[](std::size_t index) const { return queue_[index]; }
    T& operator[](std::size_t index) { return queue_[index]; }

    std::vector<T> records() const {
        return std::vector<T>(queue_.begin(), queue_.end());
    }

    std::vector<T> snapshot() const { return records(); }

    std::vector<T> drain() {
        std::vector<T> values;
        values.reserve(queue_.size());
        while (!queue_.empty()) {
            values.push_back(std::move(queue_.front()));
            queue_.pop_front();
        }
        return values;
    }

    std::size_t capacity() const { return capacity_; }
    std::size_t size() const { return queue_.size(); }
    bool empty() const { return queue_.empty(); }
    bool full() const {
        return capacity_ == 0 || queue_.size() >= capacity_;
    }

    std::size_t dropped() const { return dropped_; }
    std::size_t dropped_count() const { return total_dropped_; }
    std::size_t pending_dropped() const { return dropped_; }
    std::size_t lost() const { return dropped_; }
    std::size_t total_dropped() const { return total_dropped_; }
    std::size_t dropped_total() const { return total_dropped_; }
    std::size_t lost_total() const { return total_dropped_; }
    std::size_t total_attempts() const { return total_attempts_; }
    std::size_t total() const { return total_attempts_; }
    std::size_t stored() const { return stored_; }

    void clear() {
        queue_.clear();
        dropped_ = 0;
        total_dropped_ = 0;
        total_attempts_ = 0;
        stored_ = 0;
    }

    void reset() { clear(); }

private:
    std::size_t capacity_;
    std::deque<T> queue_;
    std::size_t dropped_ = 0;
    std::size_t total_dropped_ = 0;
    std::size_t total_attempts_ = 0;
    std::size_t stored_ = 0;
};

using AuditRing = RingBuffer<AuditRecord>;
using AuditBuffer = RingBuffer<AuditRecord>;
using AuditLog = RingBuffer<AuditRecord>;
using Ring = RingBuffer<AuditRecord>;
using AuditEvent = AuditRecord;

class Policy {
public:
    void protect(FileId file, std::string path_hint = {});
    void protect(DeviceNumber device, InodeNumber inode,
                 std::string path_hint = {});
    void protect(std::string path_hint, DeviceNumber device,
                 InodeNumber inode);
    void protect_file(FileId file, std::string path_hint = {});
    void protect_inode(DeviceNumber device, InodeNumber inode,
                       std::string path_hint = {});
    void add_protected(FileId file, std::string path_hint = {});
    void add_rule(FileId file, std::string path_hint = {});
    void deny(FileId file, std::string path_hint = {});
    void deny(DeviceNumber device, InodeNumber inode,
              std::string path_hint = {});

    bool unprotect(FileId file);
    bool unprotect(DeviceNumber device, InodeNumber inode);
    bool remove_protection(FileId file);

    bool is_protected(const FileId& file) const;
    bool is_protected(DeviceNumber device, InodeNumber inode) const;
    bool protects(const FileId& file) const { return is_protected(file); }
    bool protects(DeviceNumber device, InodeNumber inode) const {
        return is_protected(device, inode);
    }

    bool allows(const FileId& file) const { return !is_protected(file); }
    bool allows(DeviceNumber device, InodeNumber inode) const {
        return !is_protected(device, inode);
    }
    bool denies(const FileId& file) const { return is_protected(file); }
    bool denies(DeviceNumber device, InodeNumber inode) const {
        return is_protected(device, inode);
    }
    bool is_allowed(const FileId& file) const { return allows(file); }
    bool is_allowed(DeviceNumber device, InodeNumber inode) const {
        return allows(device, inode);
    }
    bool should_deny(const FileId& file) const { return denies(file); }
    bool should_deny(DeviceNumber device, InodeNumber inode) const {
        return denies(device, inode);
    }

    Decision decide(const FileId& file) const;
    Decision decide(FileId file, std::string_view path_hint) const {
        (void)path_hint;
        return decide(file);
    }
    Decision decide(DeviceNumber device, InodeNumber inode,
                    std::string_view path_hint = {}) const;
    Decision decide(const HookContext& context) const {
        return decide(context.file);
    }
    Decision evaluate(const HookContext& context) const {
        return decide(context.file);
    }
    Decision check(const FileId& file) const { return decide(file); }
    Decision check(DeviceNumber device, InodeNumber inode) const {
        return decide(device, inode);
    }
    Decision operator()(const HookContext& context) const {
        return decide(context.file);
    }

    std::optional<std::string> path_hint(const FileId& file) const;
    std::optional<std::string> path_hint(DeviceNumber device,
                                         InodeNumber inode) const;

    std::size_t size() const { return rules_.size(); }
    std::size_t protected_count() const { return rules_.size(); }
    bool empty() const { return rules_.empty(); }

    void clear() { rules_.clear(); }

private:
    std::map<FileId, std::string> rules_;
};

using LsmPolicy = Policy;
using AccessPolicy = Policy;

struct HookResult {
    Decision decision = Decision::Allow;
    std::string denying_program;
    std::uint64_t inode = 0;
    std::uint64_t device = 0;
    FileId file;
    bool audit_stored = false;
    bool stored = false;
    bool policy_denied = false;
    bool callback_error = false;
    std::uint64_t dropped = 0;
    std::uint64_t audit_dropped = 0;
    std::uint64_t sequence = 0;
    std::vector<std::string> executed_programs;
    AuditRecord record;
    AuditRecord audit_record;
    AuditRecord audit;
    std::string first_denying_program;
    std::string first_deny_program;
    std::string denied_by;

    bool allowed() const { return decision == Decision::Allow; }
    bool denied() const { return decision == Decision::Deny; }
    bool is_allowed() const { return allowed(); }
    bool is_denied() const { return denied(); }
    const std::string& denier() const { return denying_program; }
    const AuditRecord& audit_record_ref() const { return audit_record; }

    operator Decision() const { return decision; }

    friend bool operator==(const HookResult& lhs, Decision rhs) {
        return lhs.decision == rhs;
    }

    friend bool operator==(Decision lhs, const HookResult& rhs) {
        return rhs == lhs;
    }

    friend bool operator!=(const HookResult& lhs, Decision rhs) {
        return !(lhs == rhs);
    }

    friend bool operator!=(Decision lhs, const HookResult& rhs) {
        return !(rhs == lhs);
    }
};

using HookOutcome = HookResult;
using OpenResult = HookResult;
using Result = HookResult;
using AccessResult = HookResult;

class HookEngine {
public:
    using AuditLogger = std::function<void(const AuditRecord&)>;

    explicit HookEngine(std::size_t audit_capacity = 64);
    explicit HookEngine(Policy policy, std::size_t audit_capacity = 64);
    HookEngine(Policy& policy, AuditRing& audit);
    HookEngine(Policy& policy, std::size_t audit_capacity);

    void add_program(Program program);
    void add_program(const std::string& name, Program::Callback callback);

    template <typename F,
              typename = std::enable_if_t<
                  !std::is_same_v<std::decay_t<F>, Program> &&
                  !std::is_same_v<std::decay_t<F>, Program::Callback>>>
    void add_program(const std::string& name, F&& callback) {
        programs_.emplace_back(
            name, detail::wrap_callback(std::forward<F>(callback)));
    }

    void mount(Program program) { add_program(std::move(program)); }
    void attach(Program program) { add_program(std::move(program)); }

    template <typename F>
    void mount(const std::string& name, F&& callback) {
        add_program(name, std::forward<F>(callback));
    }

    HookResult on_open(const HookContext& context);
    HookResult on_open(FileId file, std::string path_hint = {});
    HookResult on_open(DeviceNumber device, InodeNumber inode,
                       std::string path_hint = {});
    HookResult on_open(const std::string& path_hint, DeviceNumber device,
                       InodeNumber inode);

    HookResult open(const HookContext& context) { return on_open(context); }
    HookResult open(FileId file, std::string path_hint = {}) {
        return on_open(file, std::move(path_hint));
    }
    HookResult open(DeviceNumber device, InodeNumber inode,
                    std::string path_hint = {}) {
        return on_open(device, inode, std::move(path_hint));
    }
    HookResult open(const std::string& path_hint, DeviceNumber device,
                    InodeNumber inode) {
        return on_open(path_hint, device, inode);
    }

    HookResult handle(const HookContext& context) { return on_open(context); }
    HookResult handle(FileId file, std::string path_hint = {}) {
        return on_open(file, std::move(path_hint));
    }
    HookResult handle(DeviceNumber device, InodeNumber inode,
                      std::string path_hint = {}) {
        return on_open(device, inode, std::move(path_hint));
    }

    HookResult invoke(const HookContext& context) { return on_open(context); }
    HookResult open_file(const HookContext& context) {
        return on_open(context);
    }
    HookResult check(const HookContext& context) { return on_open(context); }

    Decision decide(const HookContext& context) {
        return on_open(context).decision;
    }
    Decision decide(FileId file, std::string path_hint = {}) {
        return on_open(file, std::move(path_hint)).decision;
    }
    Decision decide(DeviceNumber device, InodeNumber inode,
                    std::string path_hint = {}) {
        return on_open(device, inode, std::move(path_hint)).decision;
    }
    Decision evaluate(const HookContext& context) {
        return decide(context);
    }
    Decision evaluate(FileId file, std::string path_hint = {}) {
        return decide(file, std::move(path_hint));
    }
    Decision evaluate(DeviceNumber device, InodeNumber inode,
                      std::string path_hint = {}) {
        return decide(device, inode, std::move(path_hint));
    }

    void protect(FileId file, std::string path_hint = {}) {
        policy_->protect(file, std::move(path_hint));
    }
    void protect(DeviceNumber device, InodeNumber inode,
                 std::string path_hint = {}) {
        policy_->protect(device, inode, std::move(path_hint));
    }
    bool is_protected(const FileId& file) const {
        return policy_->is_protected(file);
    }

    Policy& policy() { return *policy_; }
    const Policy& policy() const { return *policy_; }

    AuditRing& audit_ring() { return *audit_; }
    const AuditRing& audit_ring() const { return *audit_; }
    AuditRing& audit_log() { return *audit_; }
    const AuditRing& audit_log() const { return *audit_; }
    AuditRing& audit_buffer() { return *audit_; }
    const AuditRing& audit_buffer() const { return *audit_; }
    AuditRing& ring() { return *audit_; }
    const AuditRing& ring() const { return *audit_; }
    AuditRing& audit() { return *audit_; }
    const AuditRing& audit() const { return *audit_; }

    std::vector<AuditRecord> audit_records() const {
        return audit_->records();
    }

    const std::vector<Program>& programs() const { return programs_; }
    std::size_t program_count() const { return programs_.size(); }
    std::vector<std::string> program_names() const;

    std::size_t audit_dropped() const { return audit_->dropped(); }
    std::size_t audit_dropped_total() const {
        return audit_->total_dropped();
    }
    std::size_t dropped() const { return audit_->dropped(); }
    std::size_t dropped_total() const { return audit_->total_dropped(); }
    std::size_t total_audits() const { return audit_->total_attempts(); }
    std::size_t calls() const { return calls_; }

    void set_audit_logger(AuditLogger logger);
    void clear_audit_logger();

private:
    std::shared_ptr<Policy> policy_;
    std::shared_ptr<AuditRing> audit_;
    std::vector<Program> programs_;
    std::uint64_t calls_ = 0;
    AuditLogger logger_;
};

using Guard = HookEngine;
using LsmHook = HookEngine;
using Hook = HookEngine;
using PolicyEngine = HookEngine;
using Engine = HookEngine;
using LsmDecisionEngine = HookEngine;

inline constexpr const char* kPolicyProgramName = "policy";

}  // namespace netsec
