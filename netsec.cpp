#include "netsec.hpp"

#include <algorithm>
#include <mutex>
#include <unordered_map>
#include <utility>

namespace netsec {
namespace {

bool valid_id(std::string_view value) {
    return !value.empty();
}

bool valid_identity(const SessionRequest& request) {
    return valid_id(request.slice_id) &&
           valid_id(request.subscription_id) &&
           valid_id(request.session_id) && valid_id(request.supi);
}

bool allows_slice(const Subscription& subscription, std::string_view slice_id) {
    return std::find(subscription.allowed_slices.begin(),
                     subscription.allowed_slices.end(),
                     slice_id) != subscription.allowed_slices.end();
}

Subscription normalized(Subscription subscription) {
    std::sort(subscription.allowed_slices.begin(),
              subscription.allowed_slices.end());
    subscription.allowed_slices.erase(
        std::unique(subscription.allowed_slices.begin(),
                    subscription.allowed_slices.end()),
        subscription.allowed_slices.end());
    return subscription;
}

bool same_subscription(const Subscription& lhs, const Subscription& rhs) {
    return lhs.subscription_id == rhs.subscription_id &&
           lhs.supi == rhs.supi &&
           lhs.allowed_slices == rhs.allowed_slices &&
           lhs.enabled == rhs.enabled;
}

bool can_charge(std::uint64_t used, std::uint64_t units,
                std::uint64_t quota) {
    return units <= quota && used <= quota - units;
}

}  // namespace

struct SliceBindingManager::Impl {
    mutable std::mutex mutex;
    std::unordered_map<SliceId, SlicePolicy> slices;
    std::unordered_map<SubscriptionId, Subscription> subscriptions;
    std::unordered_map<std::string, Resource> resources;
    std::unordered_map<SessionId, SessionBinding> bindings;
    std::unordered_map<SliceId, SliceUsage> usage;
};

Decision SliceBindingManager::check_resource(
    const Impl& impl, const SessionRequest& request,
    const SessionBinding& binding, const SlicePolicy& policy,
    SliceUsage& slice_usage) {
    if (!request.resource_id.empty()) {
        const auto resource_it = impl.resources.find(request.resource_id);
        if (resource_it == impl.resources.end()) {
            return Decision::reject(RejectReason::UnknownResource);
        }
        const Resource& resource = resource_it->second;
        if (!resource.enabled) {
            return Decision::reject(RejectReason::ResourceDisabled);
        }
        if (!request.resource_slice_id.empty() &&
            request.resource_slice_id != resource.slice_id) {
            return Decision::reject(
                RejectReason::ResourceOwnershipMismatch);
        }
        if (resource.slice_id != binding.slice_id) {
            return Decision::reject(RejectReason::CrossSliceResource);
        }
    }

    const std::string& resource_slice =
        request.resource_slice_id.empty() ? binding.slice_id
                                          : request.resource_slice_id;
    if (resource_slice != binding.slice_id) {
        return Decision::reject(RejectReason::CrossSliceResource);
    }

    if (request.resource_units > 0 &&
        !can_charge(slice_usage.consumed_resource_units,
                    request.resource_units, policy.resource_quota)) {
        return Decision::reject(RejectReason::ResourceQuotaExceeded);
    }
    if (request.resource_units > 0 &&
        !can_charge(binding.consumed_resource_units, request.resource_units,
                    unlimited_quota)) {
        return Decision::reject(RejectReason::ResourceQuotaExceeded);
    }

    slice_usage.consumed_resource_units += request.resource_units;
    return Decision::allow();
}

std::string_view to_string(RejectReason reason) noexcept {
    switch (reason) {
        case RejectReason::None:
            return "allowed";
        case RejectReason::InvalidRequest:
            return "invalid_request";
        case RejectReason::UnknownSlice:
            return "unknown_slice";
        case RejectReason::SliceDisabled:
            return "slice_disabled";
        case RejectReason::IsolationPolicyViolation:
            return "isolation_policy_violation";
        case RejectReason::UnknownSubscription:
            return "unknown_subscription";
        case RejectReason::SubscriptionDisabled:
            return "subscription_disabled";
        case RejectReason::SubscriptionForgery:
            return "subscription_forgery";
        case RejectReason::UnsubscribedSlice:
            return "unsubscribed_slice";
        case RejectReason::UnknownSession:
            return "unknown_session";
        case RejectReason::SessionAlreadyBound:
            return "session_already_bound";
        case RejectReason::SessionMismatch:
            return "session_mismatch";
        case RejectReason::CrossSliceAccess:
            return "cross_slice_access";
        case RejectReason::SubscriptionMismatch:
            return "subscription_mismatch";
        case RejectReason::UserIdentityMismatch:
            return "user_identity_mismatch";
        case RejectReason::UnknownResource:
            return "unknown_resource";
        case RejectReason::ResourceDisabled:
            return "resource_disabled";
        case RejectReason::CrossSliceResource:
            return "cross_slice_resource";
        case RejectReason::ResourceOwnershipMismatch:
            return "resource_ownership_mismatch";
        case RejectReason::SessionQuotaExceeded:
            return "session_quota_exceeded";
        case RejectReason::ResourceQuotaExceeded:
            return "resource_quota_exceeded";
        case RejectReason::DuplicateRegistration:
            return "duplicate_registration";
    }
    return "unknown_reason";
}

SliceBindingManager::SliceBindingManager()
    : impl_(std::make_unique<Impl>()) {}

SliceBindingManager::~SliceBindingManager() = default;

Decision SliceBindingManager::register_slice(const SlicePolicy& policy) {
    if (!valid_id(policy.slice_id)) {
        return Decision::reject(RejectReason::InvalidRequest);
    }
    if (!policy.isolated) {
        return Decision::reject(RejectReason::IsolationPolicyViolation);
    }

    std::lock_guard lock(impl_->mutex);
    const auto it = impl_->slices.find(policy.slice_id);
    if (it != impl_->slices.end()) {
        return it->second == policy
                   ? Decision::allow()
                   : Decision::reject(RejectReason::DuplicateRegistration);
    }

    impl_->slices.emplace(policy.slice_id, policy);
    impl_->usage.emplace(policy.slice_id, SliceUsage{});
    return Decision::allow();
}

Decision SliceBindingManager::register_slice(
    SliceId slice_id, std::uint64_t session_quota,
    std::uint64_t resource_quota, bool isolated) {
    return register_slice(SlicePolicy{std::move(slice_id), session_quota,
                                      resource_quota, isolated,
                                      true});
}

Decision SliceBindingManager::register_subscription(
    const Subscription& subscription) {
    if (!valid_id(subscription.subscription_id) ||
        !valid_id(subscription.supi)) {
        return Decision::reject(RejectReason::InvalidRequest);
    }
    for (const auto& slice : subscription.allowed_slices) {
        if (!valid_id(slice)) {
            return Decision::reject(RejectReason::InvalidRequest);
        }
    }

    Subscription record = normalized(subscription);
    std::lock_guard lock(impl_->mutex);
    const auto it = impl_->subscriptions.find(record.subscription_id);
    if (it != impl_->subscriptions.end()) {
        return same_subscription(it->second, record)
                   ? Decision::allow()
                   : Decision::reject(RejectReason::DuplicateRegistration);
    }

    impl_->subscriptions.emplace(record.subscription_id, std::move(record));
    return Decision::allow();
}

Decision SliceBindingManager::register_subscription(
    SubscriptionId subscription_id, Supi supi,
    std::vector<SliceId> allowed_slices, bool enabled) {
    return register_subscription(Subscription{std::move(subscription_id),
                                              std::move(supi),
                                              std::move(allowed_slices),
                                              enabled});
}

Decision SliceBindingManager::register_resource(const Resource& resource) {
    if (!valid_id(resource.resource_id) || !valid_id(resource.slice_id)) {
        return Decision::reject(RejectReason::InvalidRequest);
    }

    std::lock_guard lock(impl_->mutex);
    const auto it = impl_->resources.find(resource.resource_id);
    if (it != impl_->resources.end()) {
        return it->second == resource
                   ? Decision::allow()
                   : Decision::reject(RejectReason::DuplicateRegistration);
    }

    impl_->resources.emplace(resource.resource_id, resource);
    return Decision::allow();
}

Decision SliceBindingManager::register_resource(std::string resource_id,
                                                SliceId slice_id,
                                                bool enabled) {
    return register_resource(
        Resource{std::move(resource_id), std::move(slice_id), enabled});
}

Decision SliceBindingManager::establish(const SessionRequest& request) {
    if (!valid_identity(request)) {
        return Decision::reject(RejectReason::InvalidRequest);
    }

    std::lock_guard lock(impl_->mutex);

    const auto slice_it = impl_->slices.find(request.slice_id);
    if (slice_it == impl_->slices.end()) {
        return Decision::reject(RejectReason::UnknownSlice);
    }
    const SlicePolicy& policy = slice_it->second;
    if (!policy.enabled) {
        return Decision::reject(RejectReason::SliceDisabled);
    }
    if (!policy.isolated) {
        return Decision::reject(RejectReason::IsolationPolicyViolation);
    }

    const auto subscription_it =
        impl_->subscriptions.find(request.subscription_id);
    if (subscription_it == impl_->subscriptions.end()) {
        return Decision::reject(RejectReason::UnknownSubscription);
    }
    const Subscription& subscription = subscription_it->second;
    if (!subscription.enabled) {
        return Decision::reject(RejectReason::SubscriptionDisabled);
    }
    if (subscription.supi != request.supi) {
        return Decision::reject(RejectReason::SubscriptionForgery);
    }
    if (!allows_slice(subscription, request.slice_id)) {
        return Decision::reject(RejectReason::UnsubscribedSlice);
    }
    if (impl_->bindings.contains(request.session_id)) {
        return Decision::reject(RejectReason::SessionAlreadyBound);
    }

    SliceUsage& usage = impl_->usage[request.slice_id];
    if (usage.active_sessions >= policy.session_quota) {
        return Decision::reject(RejectReason::SessionQuotaExceeded);
    }

    SessionBinding binding{request.slice_id, request.subscription_id,
                           request.session_id, request.supi, 0};
    const Decision resource_decision =
        check_resource(*impl_, request, binding, policy, usage);
    if (!resource_decision) {
        return resource_decision;
    }

    binding.consumed_resource_units = request.resource_units;
    ++usage.active_sessions;
    impl_->bindings.emplace(binding.session_id, std::move(binding));
    return Decision::allow();
}

Decision SliceBindingManager::establish(
    SliceId slice_id, SubscriptionId subscription_id, SessionId session_id,
    Supi supi) {
    return establish(SessionRequest{std::move(slice_id),
                                    std::move(subscription_id),
                                    std::move(session_id), std::move(supi),
                                    {}, 0, {}});
}

Decision SliceBindingManager::validate(const SessionRequest& request) {
    if (!valid_identity(request)) {
        return Decision::reject(RejectReason::InvalidRequest);
    }

    std::lock_guard lock(impl_->mutex);

    const auto binding_it = impl_->bindings.find(request.session_id);
    if (binding_it == impl_->bindings.end()) {
        return Decision::reject(RejectReason::UnknownSession);
    }
    SessionBinding& binding = binding_it->second;

    if (request.slice_id != binding.slice_id) {
        return Decision::reject(RejectReason::CrossSliceAccess);
    }
    if (request.subscription_id != binding.subscription_id) {
        return Decision::reject(RejectReason::SubscriptionMismatch);
    }
    if (request.supi != binding.supi) {
        return Decision::reject(RejectReason::UserIdentityMismatch);
    }
    if (request.session_id != binding.session_id) {
        return Decision::reject(RejectReason::SessionMismatch);
    }

    const auto subscription_it =
        impl_->subscriptions.find(binding.subscription_id);
    if (subscription_it == impl_->subscriptions.end()) {
        return Decision::reject(RejectReason::UnknownSubscription);
    }
    const Subscription& subscription = subscription_it->second;
    if (!subscription.enabled) {
        return Decision::reject(RejectReason::SubscriptionDisabled);
    }
    if (subscription.supi != binding.supi) {
        return Decision::reject(RejectReason::SubscriptionForgery);
    }
    if (!allows_slice(subscription, binding.slice_id)) {
        return Decision::reject(RejectReason::UnsubscribedSlice);
    }

    const auto slice_it = impl_->slices.find(binding.slice_id);
    if (slice_it == impl_->slices.end()) {
        return Decision::reject(RejectReason::UnknownSlice);
    }
    const SlicePolicy& policy = slice_it->second;
    if (!policy.enabled) {
        return Decision::reject(RejectReason::SliceDisabled);
    }
    if (!policy.isolated) {
        return Decision::reject(RejectReason::IsolationPolicyViolation);
    }

    SliceUsage& usage = impl_->usage[binding.slice_id];
    const Decision resource_decision =
        check_resource(*impl_, request, binding, policy, usage);
    if (!resource_decision) {
        return resource_decision;
    }

    binding.consumed_resource_units += request.resource_units;
    return Decision::allow();
}

Decision SliceBindingManager::authorize_resource(
    const SessionRequest& request, SliceId resource_slice_id,
    std::uint64_t resource_units) {
    SessionRequest access = request;
    access.resource_slice_id = std::move(resource_slice_id);
    access.resource_units = resource_units;
    return validate(access);
}

Decision SliceBindingManager::release_session(std::string_view session_id) {
    std::lock_guard lock(impl_->mutex);
    const auto it = impl_->bindings.find(std::string(session_id));
    if (it == impl_->bindings.end()) {
        return Decision::reject(RejectReason::UnknownSession);
    }

    auto usage_it = impl_->usage.find(it->second.slice_id);
    if (usage_it != impl_->usage.end() && usage_it->second.active_sessions > 0) {
        --usage_it->second.active_sessions;
    }
    impl_->bindings.erase(it);
    return Decision::allow();
}

std::optional<SessionBinding> SliceBindingManager::binding_for(
    std::string_view session_id) const {
    std::lock_guard lock(impl_->mutex);
    const auto it = impl_->bindings.find(std::string(session_id));
    if (it == impl_->bindings.end()) {
        return std::nullopt;
    }
    return it->second;
}

std::optional<SlicePolicy> SliceBindingManager::slice_policy_for(
    std::string_view slice_id) const {
    std::lock_guard lock(impl_->mutex);
    const auto it = impl_->slices.find(std::string(slice_id));
    if (it == impl_->slices.end()) {
        return std::nullopt;
    }
    return it->second;
}

std::optional<Subscription> SliceBindingManager::subscription_for(
    std::string_view subscription_id) const {
    std::lock_guard lock(impl_->mutex);
    const auto it = impl_->subscriptions.find(std::string(subscription_id));
    if (it == impl_->subscriptions.end()) {
        return std::nullopt;
    }
    return it->second;
}

std::optional<Resource> SliceBindingManager::resource_for(
    std::string_view resource_id) const {
    std::lock_guard lock(impl_->mutex);
    const auto it = impl_->resources.find(std::string(resource_id));
    if (it == impl_->resources.end()) {
        return std::nullopt;
    }
    return it->second;
}

SliceUsage SliceBindingManager::usage_for(
    std::string_view slice_id) const {
    std::lock_guard lock(impl_->mutex);
    const auto it = impl_->usage.find(std::string(slice_id));
    return it == impl_->usage.end() ? SliceUsage{} : it->second;
}

void SliceBindingManager::clear() noexcept {
    std::lock_guard lock(impl_->mutex);
    impl_->slices.clear();
    impl_->subscriptions.clear();
    impl_->resources.clear();
    impl_->bindings.clear();
    impl_->usage.clear();
}

}  // namespace netsec
