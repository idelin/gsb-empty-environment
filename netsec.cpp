#include "netsec.hpp"

#include <algorithm>
#include <cmath>
#include <stdexcept>
#include <utility>

namespace netsec {
namespace {

std::string bucket_key(const SenderId& sender, const GroupAddress& group) {
    std::string key = sender;
    key.push_back('\x1f');
    key.append(std::to_string(group.raw()));
    return key;
}

std::string sequence_key(const IncomingCommand& command) {
    std::string key = command.claimed_source;
    key.push_back('\x1f');
    key.append(std::to_string(command.target.raw()));
    key.push_back('\x1f');
    key.append(std::to_string(command.sequence));
    return key;
}

std::string content_key(const IncomingCommand& command) {
    std::string key = command.claimed_source;
    key.push_back('\x1f');
    key.append(std::to_string(command.target.raw()));
    key.push_back('\x1f');
    key.append(std::to_string(command.function));
    key.push_back('\x1f');
    key.append(command.payload);
    return key;
}

}  // namespace

bool GroupRegistry::register_device(DeviceRecord device) {
    if (device.id.empty() || device.zone > 31U || device.device_type > 7U) {
        return false;
    }
    SenderId id = device.id;
    return devices_.try_emplace(std::move(id), std::move(device)).second;
}

bool GroupRegistry::register_device(SenderId id,
                                    std::uint8_t zone,
                                    std::uint8_t device_type) {
    return register_device(DeviceRecord{std::move(id), zone, device_type});
}

bool GroupRegistry::register_group(GroupPolicy policy) {
    if (!policy.address.valid()) {
        return false;
    }
    const std::uint16_t raw = policy.address.raw();
    return groups_.try_emplace(raw, std::move(policy)).second;
}

bool GroupRegistry::register_group(GroupAddress address, GroupKind kind) {
    return register_group(GroupPolicy{address, kind, {}});
}

bool GroupRegistry::add_member(const GroupAddress& group,
                               const SenderId& sender,
                               std::set<FunctionCode> functions) {
    const auto group_it = groups_.find(group.raw());
    if (group_it == groups_.end()) {
        return false;
    }

    const auto device_it = devices_.find(sender);
    if (device_it == devices_.end()) {
        return false;
    }

    if (group_it->second.kind == GroupKind::zone &&
        (device_it->second.zone != group.zone ||
         device_it->second.device_type != group.device_type)) {
        return false;
    }

    // Updating ordinary membership must not erase an already granted special
    // authorization.
    auto& member = group_it->second.members[sender];
    member.functions = std::move(functions);
    return true;
}

bool GroupRegistry::grant_broadcast(const GroupAddress& group,
                                    const SenderId& sender,
                                    std::set<FunctionCode> functions) {
    const auto group_it = groups_.find(group.raw());
    if (group_it == groups_.end() ||
        group_it->second.kind != GroupKind::broadcast ||
        devices_.find(sender) == devices_.end()) {
        return false;
    }

    auto& member = group_it->second.members[sender];
    member.functions = std::move(functions);
    member.broadcast_authorized = true;
    return true;
}

bool GroupRegistry::grant_central(const GroupAddress& group,
                                  const SenderId& sender,
                                  std::set<FunctionCode> functions) {
    const auto group_it = groups_.find(group.raw());
    if (group_it == groups_.end() ||
        group_it->second.kind != GroupKind::central ||
        devices_.find(sender) == devices_.end()) {
        return false;
    }

    auto& member = group_it->second.members[sender];
    member.functions = std::move(functions);
    member.central_authorized = true;
    return true;
}

bool GroupRegistry::has_device(const SenderId& sender) const {
    return devices_.find(sender) != devices_.end();
}

bool GroupRegistry::has_group(const GroupAddress& group) const {
    return groups_.find(group.raw()) != groups_.end();
}

std::optional<DeviceRecord> GroupRegistry::device(const SenderId& sender) const {
    const auto it = devices_.find(sender);
    if (it == devices_.end()) {
        return std::nullopt;
    }
    return it->second;
}

std::optional<GroupPolicy> GroupRegistry::group(const GroupAddress& group) const {
    const auto it = groups_.find(group.raw());
    if (it == groups_.end()) {
        return std::nullopt;
    }
    return it->second;
}

const DeviceRecord* GroupRegistry::find_device(const SenderId& sender) const noexcept {
    const auto it = devices_.find(sender);
    return it == devices_.end() ? nullptr : &it->second;
}

const GroupPolicy* GroupRegistry::find_group(const GroupAddress& group) const noexcept {
    const auto it = groups_.find(group.raw());
    return it == groups_.end() ? nullptr : &it->second;
}

std::string_view AuthorizationResult::reason() const noexcept {
    switch (code) {
        case AuthorizationCode::allow:
            return "authorized";
        case AuthorizationCode::invalid_command:
            return "invalid command";
        case AuthorizationCode::invalid_address:
            return "invalid group address";
        case AuthorizationCode::function_mismatch:
            return "command function does not match group address";
        case AuthorizationCode::source_mismatch:
            return "claimed source differs from authenticated source";
        case AuthorizationCode::unknown_sender:
            return "sender is not registered";
        case AuthorizationCode::unknown_group:
            return "group is not registered";
        case AuthorizationCode::cross_zone:
            return "sender zone or device type does not match group";
        case AuthorizationCode::sender_not_member:
            return "sender is not a member of the group";
        case AuthorizationCode::function_not_permitted:
            return "sender lacks permission for the group function";
        case AuthorizationCode::broadcast_not_authorized:
            return "broadcast requires explicit authorization";
        case AuthorizationCode::central_not_authorized:
            return "central group requires explicit authorization";
        case AuthorizationCode::duplicate:
            return "duplicate or replayed command";
        case AuthorizationCode::rate_limited:
            return "rate limit exceeded";
    }
    return "unknown authorization result";
}

GroupAuthorizer::GroupAuthorizer()
    : GroupAuthorizer(GroupRegistry{}, RateLimitConfig{}, {}) {}

GroupAuthorizer::GroupAuthorizer(GroupRegistry registry,
                                 RateLimitConfig config,
                                 TimeSource time_source)
    : registry_(std::move(registry)),
      config_(config),
      time_source_(std::move(time_source)) {
    if (config_.burst == 0) {
        throw std::invalid_argument("rate limit burst must be at least one");
    }
    if (!std::isfinite(config_.refill_per_second) ||
        config_.refill_per_second < 0.0) {
        throw std::invalid_argument("invalid rate limit refill rate");
    }
    if (config_.duplicate_window.count() < 0 || config_.replay_window.count() < 0) {
        throw std::invalid_argument("replay windows must not be negative");
    }
    if (config_.max_replay_entries == 0) {
        throw std::invalid_argument("replay cache must hold at least one entry");
    }
}

GroupAuthorizer::GroupAuthorizer(RateLimitConfig config, TimeSource time_source)
    : GroupAuthorizer(GroupRegistry{}, config, std::move(time_source)) {}

bool GroupAuthorizer::register_device(DeviceRecord device) {
    const std::lock_guard lock(mutex_);
    return registry_.register_device(std::move(device));
}

bool GroupAuthorizer::register_device(SenderId id,
                                      std::uint8_t zone,
                                      std::uint8_t device_type) {
    return register_device(DeviceRecord{std::move(id), zone, device_type});
}

bool GroupAuthorizer::register_group(GroupPolicy policy) {
    const std::lock_guard lock(mutex_);
    return registry_.register_group(std::move(policy));
}

bool GroupAuthorizer::register_group(GroupAddress address, GroupKind kind) {
    return register_group(GroupPolicy{address, kind, {}});
}

bool GroupAuthorizer::add_member(const GroupAddress& group,
                                 const SenderId& sender,
                                 std::set<FunctionCode> functions) {
    const std::lock_guard lock(mutex_);
    return registry_.add_member(group, sender, std::move(functions));
}

bool GroupAuthorizer::grant_broadcast(const GroupAddress& group,
                                      const SenderId& sender,
                                      std::set<FunctionCode> functions) {
    const std::lock_guard lock(mutex_);
    return registry_.grant_broadcast(group, sender, std::move(functions));
}

bool GroupAuthorizer::grant_central(const GroupAddress& group,
                                    const SenderId& sender,
                                    std::set<FunctionCode> functions) {
    const std::lock_guard lock(mutex_);
    return registry_.grant_central(group, sender, std::move(functions));
}

bool GroupAuthorizer::has_device(const SenderId& sender) const {
    const std::lock_guard lock(mutex_);
    return registry_.has_device(sender);
}

bool GroupAuthorizer::has_group(const GroupAddress& group) const {
    const std::lock_guard lock(mutex_);
    return registry_.has_group(group);
}

std::optional<DeviceRecord> GroupAuthorizer::device(const SenderId& sender) const {
    const std::lock_guard lock(mutex_);
    return registry_.device(sender);
}

std::optional<GroupPolicy> GroupAuthorizer::group(const GroupAddress& group) const {
    const std::lock_guard lock(mutex_);
    return registry_.group(group);
}

AuthorizationResult GroupAuthorizer::authorize(
    const IncomingCommand& command,
    const SenderId& authenticated_source) {
    const auto current_time = now();
    const std::lock_guard lock(mutex_);
    return authorize_locked(command, authenticated_source, current_time);
}

AuthorizationResult GroupAuthorizer::authorize(const TrustedCommand& command) {
    return authorize(command.command, command.authenticated_source);
}

RoutingDecision GroupAuthorizer::authorize_many(
    const IncomingCommand& command,
    const SenderId& authenticated_source,
    std::span<const GroupAddress> requested_targets) {
    const auto current_time = now();
    const std::lock_guard lock(mutex_);

    RoutingDecision decision;
    decision.results.reserve(requested_targets.size());
    decision.authorized_targets.reserve(requested_targets.size());

    for (const GroupAddress& target : requested_targets) {
        IncomingCommand routed = command;
        routed.target = target;
        const auto result =
            authorize_locked(routed, authenticated_source, current_time);
        if (result.allowed()) {
            decision.authorized_targets.push_back(target);
        }
        decision.results.push_back(result);
    }
    return decision;
}

AuthorizationResult GroupAuthorizer::authorize_locked(
    const IncomingCommand& command,
    const SenderId& authenticated_source,
    Clock::time_point current_time) {
    AuthorizationResult result;
    result.target = command.target;

    if (command.claimed_source.empty() || authenticated_source.empty()) {
        result.code = AuthorizationCode::invalid_command;
        return result;
    }
    if (!command.target.valid()) {
        result.code = AuthorizationCode::invalid_address;
        return result;
    }
    if (command.function != command.target.function) {
        result.code = AuthorizationCode::function_mismatch;
        return result;
    }
    if (command.claimed_source != authenticated_source) {
        result.code = AuthorizationCode::source_mismatch;
        return result;
    }

    const DeviceRecord* sender = registry_.find_device(authenticated_source);
    if (sender == nullptr) {
        result.code = AuthorizationCode::unknown_sender;
        return result;
    }

    const GroupPolicy* group = registry_.find_group(command.target);
    if (group == nullptr) {
        result.code = AuthorizationCode::unknown_group;
        return result;
    }

    if (group->kind == GroupKind::zone &&
        (sender->zone != command.target.zone ||
         sender->device_type != command.target.device_type)) {
        result.code = AuthorizationCode::cross_zone;
        return result;
    }

    const auto member_it = group->members.find(authenticated_source);
    if (member_it == group->members.end()) {
        result.code = AuthorizationCode::sender_not_member;
        return result;
    }
    const MemberPolicy& member = member_it->second;

    if (group->kind == GroupKind::broadcast && !member.broadcast_authorized) {
        result.code = AuthorizationCode::broadcast_not_authorized;
        return result;
    }
    if (group->kind == GroupKind::central && !member.central_authorized) {
        result.code = AuthorizationCode::central_not_authorized;
        return result;
    }
    if (member.functions.find(command.function) == member.functions.end()) {
        result.code = AuthorizationCode::function_not_permitted;
        return result;
    }

    if (is_duplicate_locked(command, current_time)) {
        result.code = AuthorizationCode::duplicate;
        return result;
    }

    const std::string key = bucket_key(authenticated_source, command.target);
    auto [bucket_it, inserted] = buckets_.try_emplace(key);
    Bucket& bucket = bucket_it->second;
    if (inserted) {
        bucket.tokens = static_cast<double>(config_.burst);
        bucket.last = current_time;
    } else if (current_time > bucket.last) {
        const double elapsed =
            std::chrono::duration<double>(current_time - bucket.last).count();
        bucket.tokens =
            std::min(static_cast<double>(config_.burst),
                     bucket.tokens + elapsed * config_.refill_per_second);
        bucket.last = current_time;
    } else if (current_time < bucket.last) {
        bucket.last = current_time;
    }

    if (bucket.tokens < 1.0) {
        result.code = AuthorizationCode::rate_limited;
        return result;
    }
    bucket.tokens -= 1.0;

    record_command_locked(command, current_time);
    result.code = AuthorizationCode::allow;
    return result;
}

bool GroupAuthorizer::is_duplicate_locked(const IncomingCommand& command,
                                          Clock::time_point current_time) const {
    if (command.sequence != 0 && config_.replay_window.count() > 0) {
        const auto it = sequence_replay_.find(sequence_key(command));
        if (it != sequence_replay_.end() &&
            current_time - it->second <= config_.replay_window) {
            return true;
        }
    }

    if (config_.duplicate_window.count() > 0) {
        const auto it = content_replay_.find(content_key(command));
        if (it != content_replay_.end() &&
            current_time - it->second <= config_.duplicate_window) {
            return true;
        }
    }
    return false;
}

void GroupAuthorizer::record_command_locked(const IncomingCommand& command,
                                            Clock::time_point current_time) {
    if (command.sequence != 0 && config_.replay_window.count() > 0) {
        sequence_replay_[sequence_key(command)] = current_time;
    }
    if (config_.duplicate_window.count() > 0) {
        content_replay_[content_key(command)] = current_time;
    }

    ++cleanup_tick_;
    if (cleanup_tick_ % 256 == 0 ||
        sequence_replay_.size() > config_.max_replay_entries ||
        content_replay_.size() > config_.max_replay_entries) {
        prune_replay_locked(current_time);
    }
}

void GroupAuthorizer::prune_replay_locked(Clock::time_point current_time) {
    if (config_.replay_window.count() > 0) {
        std::erase_if(sequence_replay_, [&](const auto& entry) {
            return current_time - entry.second > config_.replay_window;
        });
    } else {
        sequence_replay_.clear();
    }

    if (config_.duplicate_window.count() > 0) {
        std::erase_if(content_replay_, [&](const auto& entry) {
            return current_time - entry.second > config_.duplicate_window;
        });
    } else {
        content_replay_.clear();
    }

    while (sequence_replay_.size() > config_.max_replay_entries) {
        sequence_replay_.erase(sequence_replay_.begin());
    }
    while (content_replay_.size() > config_.max_replay_entries) {
        content_replay_.erase(content_replay_.begin());
    }
}

GroupAuthorizer::Clock::time_point GroupAuthorizer::now() const {
    return time_source_ ? time_source_() : Clock::now();
}

}  // namespace netsec
