#include "netsec.hpp"

#include <exception>

namespace netsec {

void Policy::protect(FileId file, std::string path_hint) {
    auto position = rules_.find(file);
    if (position == rules_.end()) {
        rules_.emplace(file, std::move(path_hint));
        return;
    }
    if (!path_hint.empty()) {
        position->second = std::move(path_hint);
    }
}

void Policy::protect(DeviceNumber device, InodeNumber inode,
                     std::string path_hint) {
    protect(FileId(device, inode), std::move(path_hint));
}

void Policy::protect(std::string path_hint, DeviceNumber device,
                     InodeNumber inode) {
    protect(FileId(device, inode), std::move(path_hint));
}

void Policy::protect_file(FileId file, std::string path_hint) {
    protect(file, std::move(path_hint));
}

void Policy::protect_inode(DeviceNumber device, InodeNumber inode,
                           std::string path_hint) {
    protect(device, inode, std::move(path_hint));
}

void Policy::add_protected(FileId file, std::string path_hint) {
    protect(file, std::move(path_hint));
}

void Policy::add_rule(FileId file, std::string path_hint) {
    protect(file, std::move(path_hint));
}

void Policy::deny(FileId file, std::string path_hint) {
    protect(file, std::move(path_hint));
}

void Policy::deny(DeviceNumber device, InodeNumber inode,
                  std::string path_hint) {
    protect(device, inode, std::move(path_hint));
}

bool Policy::unprotect(FileId file) {
    return rules_.erase(file) != 0;
}

bool Policy::unprotect(DeviceNumber device, InodeNumber inode) {
    return unprotect(FileId(device, inode));
}

bool Policy::remove_protection(FileId file) {
    return unprotect(file);
}

bool Policy::is_protected(const FileId& file) const {
    return rules_.find(file) != rules_.end();
}

bool Policy::is_protected(DeviceNumber device, InodeNumber inode) const {
    return is_protected(FileId(device, inode));
}

Decision Policy::decide(const FileId& file) const {
    return is_protected(file) ? Decision::Deny : Decision::Allow;
}

Decision Policy::decide(DeviceNumber device, InodeNumber inode,
                        std::string_view) const {
    return decide(FileId(device, inode));
}

std::optional<std::string> Policy::path_hint(const FileId& file) const {
    auto position = rules_.find(file);
    if (position == rules_.end() || position->second.empty()) {
        return std::nullopt;
    }
    return position->second;
}

std::optional<std::string> Policy::path_hint(DeviceNumber device,
                                             InodeNumber inode) const {
    return path_hint(FileId(device, inode));
}

HookEngine::HookEngine(std::size_t audit_capacity)
    : policy_(std::make_shared<Policy>()),
      audit_(std::make_shared<AuditRing>(audit_capacity)) {}

HookEngine::HookEngine(Policy policy, std::size_t audit_capacity)
    : policy_(std::make_shared<Policy>(std::move(policy))),
      audit_(std::make_shared<AuditRing>(audit_capacity)) {}

HookEngine::HookEngine(Policy& policy, AuditRing& audit)
    : policy_(&policy, [](Policy*) {}),
      audit_(&audit, [](AuditRing*) {}) {}

HookEngine::HookEngine(Policy& policy, std::size_t audit_capacity)
    : policy_(&policy, [](Policy*) {}),
      audit_(std::make_shared<AuditRing>(audit_capacity)) {}

void HookEngine::add_program(Program program) {
    programs_.push_back(std::move(program));
}

void HookEngine::add_program(const std::string& name,
                             Program::Callback callback) {
    programs_.emplace_back(name, std::move(callback));
}

HookResult HookEngine::on_open(const HookContext& context) {
    HookResult result;
    result.file = context.file;
    result.device = context.file.device;
    result.inode = context.file.inode;

    std::string denier;
    for (const Program& program : programs_) {
        result.executed_programs.push_back(program.name());
        Decision decision = Decision::Allow;
        try {
            decision = program.evaluate(context);
        } catch (...) {
            decision = Decision::Deny;
            result.callback_error = true;
        }
        if (decision != Decision::Allow) {
            result.decision = Decision::Deny;
            denier = program.name();
            break;
        }
    }

    if (result.decision == Decision::Allow && policy_->is_protected(context.file)) {
        result.decision = Decision::Deny;
        result.policy_denied = true;
        denier = kPolicyProgramName;
    }

    result.denying_program = denier;
    result.first_denying_program = denier;
    result.first_deny_program = denier;
    result.denied_by = denier;

    AuditRecord record;
    record.decision = result.decision;
    record.denying_program = denier;
    record.first_denying_program = denier;
    record.first_deny_program = denier;
    record.denied_by = denier;
    record.program = denier;
    record.inode = context.file.inode;
    record.device = context.file.device;
    record.ino = context.file.inode;
    record.dev = context.file.device;
    record.file = context.file;
    record.path = context.path;
    record.sequence = ++calls_;
    record.dropped = audit_->dropped();
    record.lost = record.dropped;
    record.dropped_events = record.dropped;
    if (auto hint = policy_->path_hint(context.file)) {
        record.policy_path = *hint;
    }

    result.audit_stored = audit_->push(record);
    result.stored = result.audit_stored;
    result.dropped = record.dropped;
    result.audit_dropped =
        result.audit_stored ? record.dropped : audit_->dropped();
    result.sequence = record.sequence;

    result.record = record;
    result.audit_record = record;
    result.audit = record;

    if (logger_) {
        try {
            logger_(result.record);
        } catch (...) {
        }
    }

    return result;
}

HookResult HookEngine::on_open(FileId file, std::string path_hint) {
    return on_open(HookContext(file, std::move(path_hint)));
}

HookResult HookEngine::on_open(DeviceNumber device, InodeNumber inode,
                               std::string path_hint) {
    return on_open(HookContext(device, inode, std::move(path_hint)));
}

HookResult HookEngine::on_open(const std::string& path_hint,
                               DeviceNumber device, InodeNumber inode) {
    return on_open(HookContext(device, inode, path_hint));
}

std::vector<std::string> HookEngine::program_names() const {
    std::vector<std::string> names;
    names.reserve(programs_.size());
    for (const Program& program : programs_) {
        names.push_back(program.name());
    }
    return names;
}

void HookEngine::set_audit_logger(AuditLogger logger) {
    logger_ = std::move(logger);
}

void HookEngine::clear_audit_logger() {
    logger_ = nullptr;
}

}  // namespace netsec
