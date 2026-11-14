#pragma once

#include <cstdint>
#include <limits>
#include <memory>
#include <optional>
#include <string>
#include <string_view>
#include <vector>

namespace netsec {

using SliceId = std::string;
using SubscriptionId = std::string;
using SessionId = std::string;
using Supi = std::string;

inline constexpr std::uint64_t unlimited_quota =
    std::numeric_limits<std::uint64_t>::max();

// Every rejected operation has a stable, distinguishable reason.  None is
// reserved for successful operations.
enum class RejectReason : std::uint8_t {
    None = 0,
    InvalidRequest,
    UnknownSlice,
    SliceDisabled,
    IsolationPolicyViolation,
    UnknownSubscription,
    SubscriptionDisabled,
    SubscriptionForgery,
    UnsubscribedSlice,
    UnknownSession,
    SessionAlreadyBound,
    SessionMismatch,
    CrossSliceAccess,
    SubscriptionMismatch,
    UserIdentityMismatch,
    UnknownResource,
    ResourceDisabled,
    CrossSliceResource,
    ResourceOwnershipMismatch,
    SessionQuotaExceeded,
    ResourceQuotaExceeded,
    DuplicateRegistration
};

std::string_view to_string(RejectReason reason) noexcept;

struct Decision {
    bool allowed = false;
    RejectReason reason = RejectReason::None;

    [[nodiscard]] explicit operator bool() const noexcept { return allowed; }
    [[nodiscard]] bool ok() const noexcept { return allowed; }
    [[nodiscard]] std::string_view code() const noexcept {
        return to_string(reason);
    }

    static Decision allow() noexcept { return {true, RejectReason::None}; }
    static Decision reject(RejectReason why) noexcept {
        return {false, why};
    }
};

using BindingResult = Decision;
using ValidationResult = Decision;
using AccessDecision = Decision;

// A slice policy is immutable after registration.  session_quota limits
// concurrently established sessions; resource_quota limits the total number
// of resource units that may be consumed by this slice.  A zero quota is a
// valid zero allowance, not "unlimited".
struct SlicePolicy {
    SliceId slice_id;
    std::uint64_t session_quota = unlimited_quota;
    std::uint64_t resource_quota = unlimited_quota;
    bool isolated = true;
    bool enabled = true;

    friend bool operator==(const SlicePolicy&, const SlicePolicy&) = default;
};

// The subscription binds a permanent user identity to the slices it may use.
// A request cannot invent a new subscription/slice relationship at runtime.
struct Subscription {
    SubscriptionId subscription_id;
    Supi supi;
    std::vector<SliceId> allowed_slices;
    bool enabled = true;

    friend bool operator==(const Subscription&, const Subscription&) = default;
};

// Optional server-side resource catalogue.  When a request names a resource,
// its registered owner is checked against the session's bound slice instead of
// trusting a slice identifier supplied in the request.
struct Resource {
    std::string resource_id;
    SliceId slice_id;
    bool enabled = true;

    friend bool operator==(const Resource&, const Resource&) = default;
};

// Used for both establishment and subsequent authorization.  At establishment
// the four identity fields are committed to an immutable binding.  At access
// time every one of them must match that binding exactly.
struct SessionRequest {
    SliceId slice_id;
    SubscriptionId subscription_id;
    SessionId session_id;
    Supi supi;
    SliceId resource_slice_id;
    std::uint64_t resource_units = 0;
    std::string resource_id;
};

using SessionContext = SessionRequest;
using AccessRequest = SessionRequest;

struct SessionBinding {
    SliceId slice_id;
    SubscriptionId subscription_id;
    SessionId session_id;
    Supi supi;
    std::uint64_t consumed_resource_units = 0;

    friend bool operator==(const SessionBinding&, const SessionBinding&) =
        default;
};

struct SliceUsage {
    std::uint64_t active_sessions = 0;
    std::uint64_t consumed_resource_units = 0;
};

class SliceBindingManager {
public:
    SliceBindingManager();
    ~SliceBindingManager();

    SliceBindingManager(const SliceBindingManager&) = delete;
    SliceBindingManager& operator=(const SliceBindingManager&) = delete;
    SliceBindingManager(SliceBindingManager&&) = delete;
    SliceBindingManager& operator=(SliceBindingManager&&) = delete;

    // Configuration.  Re-registering an identical slice/subscription/resource
    // is idempotent; a conflicting duplicate is rejected.
    [[nodiscard]] Decision register_slice(const SlicePolicy& policy);
    [[nodiscard]] Decision register_slice(
        SliceId slice_id,
        std::uint64_t session_quota = unlimited_quota,
        std::uint64_t resource_quota = unlimited_quota,
        bool isolated = true);
    [[nodiscard]] Decision configure_slice(const SlicePolicy& policy) {
        return register_slice(policy);
    }
    [[nodiscard]] Decision add_slice(const SlicePolicy& policy) {
        return register_slice(policy);
    }

    [[nodiscard]] Decision register_subscription(
        const Subscription& subscription);
    [[nodiscard]] Decision register_subscription(
        SubscriptionId subscription_id,
        Supi supi,
        std::vector<SliceId> allowed_slices,
        bool enabled = true);
    [[nodiscard]] Decision add_subscription(const Subscription& subscription) {
        return register_subscription(subscription);
    }

    [[nodiscard]] Decision register_resource(const Resource& resource);
    [[nodiscard]] Decision register_resource(
        std::string resource_id,
        SliceId slice_id,
        bool enabled = true);
    [[nodiscard]] Decision add_resource(const Resource& resource) {
        return register_resource(resource);
    }

    // Establish the one-time, immutable association among slice, subscription,
    // session and SUPI.  Quotas are checked before any state is committed.
    [[nodiscard]] Decision establish(const SessionRequest& request);
    [[nodiscard]] Decision bind(const SessionRequest& request) {
        return establish(request);
    }
    [[nodiscard]] Decision establish(
        SliceId slice_id,
        SubscriptionId subscription_id,
        SessionId session_id,
        Supi supi);

    // Validate every identity field, resource ownership, isolation policy and
    // quota atomically.  Resource units are charged only on an allowed call.
    [[nodiscard]] Decision validate(const SessionRequest& request);
    [[nodiscard]] Decision authorize(const SessionRequest& request) {
        return validate(request);
    }
    [[nodiscard]] Decision validate_session(const SessionRequest& request) {
        return validate(request);
    }
    [[nodiscard]] Decision authorize_resource(
        const SessionRequest& request,
        SliceId resource_slice_id,
        std::uint64_t resource_units);

    // Releases the session-quota slot.  Consumed resource units remain charged
    // for the lifetime of this manager.
    [[nodiscard]] Decision release_session(std::string_view session_id);
    [[nodiscard]] Decision revoke_session(std::string_view session_id) {
        return release_session(session_id);
    }

    [[nodiscard]] std::optional<SessionBinding> binding_for(
        std::string_view session_id) const;
    [[nodiscard]] std::optional<SlicePolicy> slice_policy_for(
        std::string_view slice_id) const;
    [[nodiscard]] std::optional<Subscription> subscription_for(
        std::string_view subscription_id) const;
    [[nodiscard]] std::optional<Resource> resource_for(
        std::string_view resource_id) const;
    [[nodiscard]] SliceUsage usage_for(std::string_view slice_id) const;

    void clear() noexcept;

private:
    struct Impl;
    std::unique_ptr<Impl> impl_;

    static Decision check_resource(const Impl& impl,
                                   const SessionRequest& request,
                                   const SessionBinding& binding,
                                   const SlicePolicy& policy,
                                   SliceUsage& slice_usage);
};

// Conventional aliases make the component easy to discover for session
// components that use different domain terminology.
using SliceBindingComponent = SliceBindingManager;
using SliceBindingService = SliceBindingManager;
using SessionSliceBinder = SliceBindingManager;

}  // namespace netsec
