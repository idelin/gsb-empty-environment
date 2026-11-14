#pragma once

#include <algorithm>
#include <array>
#include <chrono>
#include <cstddef>
#include <cstdint>
#include <functional>
#include <map>
#include <mutex>
#include <optional>
#include <string>
#include <string_view>
#include <type_traits>
#include <utility>

namespace netsec {

using ClockPoint = std::chrono::steady_clock::time_point;
using Duration = std::chrono::milliseconds;

//----------------------------------------------------------------------------
// Address and identity types
//----------------------------------------------------------------------------

struct MacAddress {
    std::array<std::uint8_t, 6> bytes{};

    MacAddress() = default;
    explicit MacAddress(std::array<std::uint8_t, 6> value) : bytes(value) {}

    MacAddress(std::uint8_t b0, std::uint8_t b1, std::uint8_t b2,
               std::uint8_t b3, std::uint8_t b4, std::uint8_t b5)
        : bytes{b0, b1, b2, b3, b4, b5} {}

    static std::optional<MacAddress> parse(std::string_view text) {
        while (!text.empty() &&
               (text.front() == ' ' || text.front() == '\t' ||
                text.front() == '\r' || text.front() == '\n')) {
            text.remove_prefix(1);
        }
        while (!text.empty() &&
               (text.back() == ' ' || text.back() == '\t' ||
                text.back() == '\r' || text.back() == '\n')) {
            text.remove_suffix(1);
        }

        std::array<std::uint8_t, 6> out{};
        std::size_t nibble = 0;
        for (char c : text) {
            int value = -1;
            if (c >= '0' && c <= '9') {
                value = c - '0';
            } else if (c >= 'a' && c <= 'f') {
                value = c - 'a' + 10;
            } else if (c >= 'A' && c <= 'F') {
                value = c - 'A' + 10;
            } else if (c == ':' || c == '-' || c == '.') {
                continue;
            } else {
                return std::nullopt;
            }

            if (nibble >= 12) {
                return std::nullopt;
            }
            if ((nibble & 1U) == 0) {
                out[nibble / 2] =
                    static_cast<std::uint8_t>(static_cast<unsigned>(value) << 4);
            } else {
                out[nibble / 2] = static_cast<std::uint8_t>(
                    out[nibble / 2] | static_cast<unsigned>(value));
            }
            ++nibble;
        }
        if (nibble != 12) {
            return std::nullopt;
        }
        return MacAddress{out};
    }

    static MacAddress from_string(std::string_view text) {
        return parse(text).value_or(MacAddress{});
    }

    // Intentionally implicit: lets identities and requests be written as
    // aggregates with a printable MAC address.
    MacAddress(std::string_view text) : MacAddress(from_string(text)) {}
    MacAddress(const char* text)
        : MacAddress(text == nullptr ? std::string_view{}
                                     : std::string_view{text}) {}
    MacAddress(const std::string& text)
        : MacAddress(std::string_view{text}) {}

    [[nodiscard]] bool is_group() const noexcept {
        return (bytes[0] & 0x01U) != 0;
    }

    [[nodiscard]] bool is_broadcast() const noexcept {
        return std::all_of(bytes.begin(), bytes.end(),
                           [](std::uint8_t b) { return b == 0xFFU; });
    }

    [[nodiscard]] bool is_zero() const noexcept {
        return std::all_of(bytes.begin(), bytes.end(),
                           [](std::uint8_t b) { return b == 0U; });
    }

    [[nodiscard]] bool is_usable_owner() const noexcept {
        return !is_zero() && !is_broadcast() && !is_group();
    }

    [[nodiscard]] std::string to_string() const {
        static constexpr char hex[] = "0123456789abcdef";
        std::string result;
        result.reserve(17);
        for (std::size_t i = 0; i < bytes.size(); ++i) {
            if (i != 0) {
                result.push_back(':');
            }
            result.push_back(hex[bytes[i] >> 4]);
            result.push_back(hex[bytes[i] & 0x0FU]);
        }
        return result;
    }

    friend bool operator==(const MacAddress&, const MacAddress&) = default;
    friend bool operator==(const MacAddress& address, std::string_view text) {
        const auto parsed = MacAddress::parse(text);
        return parsed && address == *parsed;
    }
    friend bool operator==(std::string_view text, const MacAddress& address) {
        return address == text;
    }
    friend bool operator!=(const MacAddress& address, std::string_view text) {
        return !(address == text);
    }
    friend bool operator!=(std::string_view text, const MacAddress& address) {
        return !(address == text);
    }
};

struct DeviceIdentity {
    MacAddress mac;
    std::uint16_t port{0};
    std::string station;

    static DeviceIdentity from(std::string_view mac_text, std::uint16_t port_number,
                               std::string_view station_id) {
        DeviceIdentity identity;
        identity.mac = MacAddress::from_string(mac_text);
        identity.port = port_number;
        identity.station = std::string(station_id);
        return identity;
    }

    static DeviceIdentity fromStationFirst(std::string_view mac_text,
                                           std::string_view station_id,
                                           std::uint16_t port_number) {
        return from(mac_text, port_number, station_id);
    }

    [[nodiscard]] const MacAddress& mac_address() const noexcept { return mac; }
    [[nodiscard]] std::uint16_t port_id() const noexcept { return port; }
    [[nodiscard]] const std::string& station_name() const noexcept {
        return station;
    }

    [[nodiscard]] std::string to_string() const {
        return mac.to_string() + "/" + std::to_string(port) + "/" + station;
    }

    friend bool operator==(const DeviceIdentity&, const DeviceIdentity&) = default;
};

//----------------------------------------------------------------------------
// Requests, policy and results
//----------------------------------------------------------------------------

enum class ConflictPolicy {
    KeepOwner = 0,             // never replace an existing owner
    FirstOwnerWins = KeepOwner,
    RejectOnConflict = KeepOwner,
    AllowAfterReservation = 1, // permit reuse once the reservation has elapsed
    AllowAfterLeaseAndReservation = AllowAfterReservation
};

// Common spellings used by discovery-service integrations.
inline constexpr ConflictPolicy RejectAll = ConflictPolicy::KeepOwner;
inline constexpr ConflictPolicy AllowAfterRetention =
    ConflictPolicy::AllowAfterReservation;

struct Config {
    Duration default_lease{std::chrono::seconds(60)};
    Duration default_reservation{std::chrono::seconds(30)};
    ConflictPolicy conflict_policy{ConflictPolicy::AllowAfterReservation};
};

struct Request {
    std::string name;
    DeviceIdentity owner;
    std::optional<Duration> lease;
    bool multicast{false};
};

enum class Status {
    Ok = 0,
    Success = 0,
    Assigned = 0,
    InvalidName,
    InvalidMac,
    InvalidLease,
    NotFound,
    Conflict,
    NameConflict = Conflict,
    ReservationActive,
    RetentionActive = ReservationActive,
    OwnershipMismatch,
    NotOwner = OwnershipMismatch,
    MulticastRestricted,
    MulticastNotAllowed = MulticastRestricted,
    RenameTargetConflict
};

[[nodiscard]] inline std::string_view to_string(Status status) noexcept {
    switch (status) {
        case Status::Ok:
            return "ok";
        case Status::InvalidName:
            return "invalid name";
        case Status::InvalidMac:
            return "invalid MAC";
        case Status::InvalidLease:
            return "invalid lease";
        case Status::NotFound:
            return "not found";
        case Status::Conflict:
            return "name conflict";
        case Status::ReservationActive:
            return "name is in its reservation period";
        case Status::OwnershipMismatch:
            return "name belongs to another device";
        case Status::MulticastRestricted:
            return "ownership changes are not allowed for multicast requests";
        case Status::RenameTargetConflict:
            return "rename target conflicts with another owner";
    }
    return "unknown";
}

struct Result {
    Status status{Status::Ok};
    std::string detail;

    Result() = default;
    explicit Result(Status value) : status(value) {}
    Result(Status value, std::string message)
        : status(value), detail(std::move(message)) {}

    [[nodiscard]] bool ok() const noexcept { return status == Status::Ok; }
    [[nodiscard]] bool success() const noexcept { return ok(); }
    [[nodiscard]] bool succeeded() const noexcept { return ok(); }
    [[nodiscard]] bool failed() const noexcept { return !ok(); }
    [[nodiscard]] bool isOk() const noexcept { return ok(); }
    [[nodiscard]] bool is_ok() const noexcept { return ok(); }
    [[nodiscard]] operator bool() const noexcept { return ok(); }
    [[nodiscard]] bool operator==(Status other) const noexcept {
        return status == other;
    }
    [[nodiscard]] bool operator!=(Status other) const noexcept {
        return status != other;
    }
    [[nodiscard]] friend bool operator==(Status other, const Result& result) {
        return result.status == other;
    }
    [[nodiscard]] friend bool operator!=(Status other, const Result& result) {
        return result.status != other;
    }
    [[nodiscard]] bool operator==(bool expected) const noexcept {
        return ok() == expected;
    }
    [[nodiscard]] bool operator!=(bool expected) const noexcept {
        return ok() != expected;
    }
    [[nodiscard]] friend bool operator==(bool expected, const Result& result) {
        return result.ok() == expected;
    }
    [[nodiscard]] friend bool operator!=(bool expected, const Result& result) {
        return result.ok() != expected;
    }
    [[nodiscard]] friend bool operator==(const Result&, const Result&) = default;
    [[nodiscard]] Status code() const noexcept { return status; }
    [[nodiscard]] std::string_view reason() const noexcept { return error(); }
    [[nodiscard]] const std::string& message() const noexcept { return detail; }
    [[nodiscard]] std::string_view error() const noexcept {
        return detail.empty() ? to_string(status) : std::string_view(detail);
    }
};

//----------------------------------------------------------------------------
// Public view of a name lease
//----------------------------------------------------------------------------

struct NameBinding {
    std::string name;
    DeviceIdentity owner;
    Duration lease{std::chrono::seconds(60)};
    ClockPoint assigned_at{};
    ClockPoint lease_expires_at{};
    ClockPoint reservation_until{};
    bool online{true};
    std::optional<ClockPoint> offline_since;

    [[nodiscard]] const MacAddress& mac() const noexcept { return owner.mac; }
    [[nodiscard]] const MacAddress& mac_address() const noexcept {
        return owner.mac;
    }
    [[nodiscard]] std::uint16_t port() const noexcept { return owner.port; }
    [[nodiscard]] std::uint16_t port_id() const noexcept { return owner.port; }
    [[nodiscard]] const std::string& station() const noexcept {
        return owner.station;
    }
    [[nodiscard]] const std::string& station_name() const noexcept {
        return owner.station;
    }
    [[nodiscard]] bool leased_at(ClockPoint now) const noexcept {
        return now < lease_expires_at;
    }
    [[nodiscard]] ClockPoint lease_end() const noexcept {
        return lease_expires_at;
    }
    [[nodiscard]] ClockPoint reservation_end() const noexcept {
        return reservation_until;
    }
    [[nodiscard]] std::int64_t lease_seconds() const noexcept {
        return std::chrono::duration_cast<std::chrono::seconds>(lease).count();
    }
    [[nodiscard]] Duration remaining_lease(ClockPoint now) const noexcept {
        if (now >= lease_expires_at) {
            return Duration::zero();
        }
        return std::chrono::duration_cast<Duration>(lease_expires_at - now);
    }
    [[nodiscard]] bool is_online() const noexcept { return online; }
    [[nodiscard]] bool is_offline() const noexcept { return !online; }
    [[nodiscard]] bool reserved_at(ClockPoint now) const noexcept {
        if (now >= reservation_until) {
            return false;
        }
        return !online || now >= lease_expires_at;
    }
};

//----------------------------------------------------------------------------
// Name ownership registry
//----------------------------------------------------------------------------

class NameOwnership {
public:
    using Clock = std::function<ClockPoint()>;

    NameOwnership() = default;
    explicit NameOwnership(Config config) : config_(std::move(config)) {}
    explicit NameOwnership(Duration default_lease)
        : config_{default_lease, std::chrono::seconds(30),
                  ConflictPolicy::AllowAfterReservation} {}
    NameOwnership(Duration default_lease, Duration default_reservation)
        : config_{default_lease, default_reservation,
                  ConflictPolicy::AllowAfterReservation} {}
    NameOwnership(Duration default_lease, Duration default_reservation,
                  ConflictPolicy policy)
        : config_{default_lease, default_reservation, policy} {}
    NameOwnership(Config config, Clock clock)
        : config_(std::move(config)), clock_(std::move(clock)) {}

    NameOwnership(const NameOwnership& other) {
        std::lock_guard lock(other.mutex_);
        config_ = other.config_;
        names_ = other.names_;
        clock_ = other.clock_;
        frozen_now_ = other.frozen_now_;
    }

    NameOwnership& operator=(const NameOwnership& other) {
        if (this == &other) {
            return *this;
        }
        std::scoped_lock lock(mutex_, other.mutex_);
        config_ = other.config_;
        names_ = other.names_;
        clock_ = other.clock_;
        frozen_now_ = other.frozen_now_;
        return *this;
    }

    NameOwnership(NameOwnership&& other) noexcept {
        std::lock_guard lock(other.mutex_);
        config_ = other.config_;
        names_ = std::move(other.names_);
        clock_ = std::move(other.clock_);
        frozen_now_ = other.frozen_now_;
    }

    NameOwnership& operator=(NameOwnership&& other) noexcept {
        if (this == &other) {
            return *this;
        }
        std::scoped_lock lock(mutex_, other.mutex_);
        config_ = other.config_;
        names_ = std::move(other.names_);
        clock_ = std::move(other.clock_);
        frozen_now_ = other.frozen_now_;
        return *this;
    }

    // Clock injection keeps reservation-period decisions deterministic in
    // tests and lets the discovery service feed in its own notion of time.
    void setClock(Clock clock) {
        std::lock_guard lock(mutex_);
        clock_ = std::move(clock);
        frozen_now_.reset();
    }
    void set_clock(Clock clock) { setClock(std::move(clock)); }

    void setNow(ClockPoint point) {
        std::lock_guard lock(mutex_);
        frozen_now_ = point;
    }
    void set_now(ClockPoint point) { setNow(point); }

    void freeze(ClockPoint start = ClockPoint{}) { setNow(start); }

    void advance(Duration delta) {
        std::lock_guard lock(mutex_);
        const ClockPoint base = currentTimeLocked();
        frozen_now_ = base + delta;
    }
    void advance_time(Duration delta) { advance(delta); }
    void advanceTime(Duration delta) { advance(delta); }

    template <class Rep, class Period>
    void advance(std::chrono::duration<Rep, Period> delta) {
        advance(std::chrono::duration_cast<Duration>(delta));
    }

    [[nodiscard]] ClockPoint now() const {
        std::lock_guard lock(mutex_);
        return currentTimeLocked();
    }

    [[nodiscard]] Config config() const {
        std::lock_guard lock(mutex_);
        return config_;
    }

    void setDefaultLease(Duration lease) {
        std::lock_guard lock(mutex_);
        config_.default_lease = lease;
    }
    void set_default_lease(Duration lease) { setDefaultLease(lease); }
    void setDefaultReservation(Duration reservation) {
        std::lock_guard lock(mutex_);
        config_.default_reservation = reservation;
    }
    void set_default_reservation(Duration reservation) {
        setDefaultReservation(reservation);
    }
    void setRetention(Duration reservation) {
        setDefaultReservation(reservation);
    }
    void set_retention(Duration reservation) {
        setDefaultReservation(reservation);
    }
    [[nodiscard]] Duration defaultLease() const {
        std::lock_guard lock(mutex_);
        return config_.default_lease;
    }
    [[nodiscard]] Duration defaultReservation() const {
        std::lock_guard lock(mutex_);
        return config_.default_reservation;
    }
    [[nodiscard]] Duration default_reservation() const {
        std::lock_guard lock(mutex_);
        return config_.default_reservation;
    }
    void setConflictPolicy(ConflictPolicy policy) {
        std::lock_guard lock(mutex_);
        config_.conflict_policy = policy;
    }
    void set_conflict_policy(ConflictPolicy policy) {
        setConflictPolicy(policy);
    }

    // Claim (or renew) a name.  The complete device identity must match the
    // identity recorded when the name was leased.
    Result assign(std::string_view name, const DeviceIdentity& owner,
                  std::optional<Duration> lease = std::nullopt,
                  bool multicast = false) {
        std::lock_guard lock(mutex_);
        return assignLocked(name, owner, lease, multicast);
    }

    Result assign(const Request& request) {
        return assign(request.name, request.owner, request.lease,
                      request.multicast);
    }

    template <class Rep, class Period>
    Result assign(std::string_view name, std::string_view mac,
                  std::uint16_t port, std::string_view station,
                  std::chrono::duration<Rep, Period> lease,
                  bool multicast = false) {
        return assign(name, DeviceIdentity::from(mac, port, station),
                      std::chrono::duration_cast<Duration>(lease), multicast);
    }

    template <class Rep, class Period>
    Result assign(std::string_view name, std::string_view mac,
                  std::string_view station, std::uint16_t port,
                  std::chrono::duration<Rep, Period> lease,
                  bool multicast = false) {
        return assign(name, DeviceIdentity::fromStationFirst(mac, station, port),
                      std::chrono::duration_cast<Duration>(lease), multicast);
    }

    Result assign(std::string_view name, std::string_view mac,
                  std::uint16_t port, std::string_view station,
                  bool multicast = false) {
        return assign(name, DeviceIdentity::from(mac, port, station), std::nullopt,
                      multicast);
    }

    Result assign(std::string_view name, std::string_view mac,
                  std::string_view station, std::uint16_t port,
                  bool multicast = false) {
        return assign(name, DeviceIdentity::fromStationFirst(mac, station, port),
                      std::nullopt,
                      multicast);
    }

    template <class Mac, class Rep, class Period,
              std::enable_if_t<
                  std::is_same_v<std::remove_cvref_t<Mac>, MacAddress>, int> = 0>
    Result assign(std::string_view name, Mac&& mac,
                  std::uint16_t port, std::string_view station,
                  std::chrono::duration<Rep, Period> lease,
                  bool multicast = false) {
        return assign(name,
                      DeviceIdentity{std::forward<Mac>(mac), port,
                                     std::string(station)},
                      std::chrono::duration_cast<Duration>(lease), multicast);
    }

    template <class Mac,
              std::enable_if_t<
                  std::is_same_v<std::remove_cvref_t<Mac>, MacAddress>, int> = 0>
    Result assign(std::string_view name, Mac&& mac,
                  std::uint16_t port, std::string_view station,
                  bool multicast = false) {
        return assign(name,
                      DeviceIdentity{std::forward<Mac>(mac), port,
                                     std::string(station)},
                      std::nullopt, multicast);
    }

    // Conventional aliases for the discovery service's call sites.
    Result claim(std::string_view name, const DeviceIdentity& owner,
                 std::optional<Duration> lease = std::nullopt,
                 bool multicast = false) {
        return assign(name, owner, lease, multicast);
    }
    Result allocate(std::string_view name, const DeviceIdentity& owner,
                    std::optional<Duration> lease = std::nullopt,
                    bool multicast = false) {
        return assign(name, owner, lease, multicast);
    }
    Result request(std::string_view name, const DeviceIdentity& owner,
                   std::optional<Duration> lease = std::nullopt,
                   bool multicast = false) {
        return assign(name, owner, lease, multicast);
    }
    Result requestName(std::string_view name, const DeviceIdentity& owner,
                       std::optional<Duration> lease = std::nullopt,
                       bool multicast = false) {
        return assign(name, owner, lease, multicast);
    }
    Result registerName(std::string_view name, const DeviceIdentity& owner,
                        std::optional<Duration> lease = std::nullopt,
                        bool multicast = false) {
        return assign(name, owner, lease, multicast);
    }
    Result assignName(std::string_view name, const DeviceIdentity& owner,
                      std::optional<Duration> lease = std::nullopt,
                      bool multicast = false) {
        return assign(name, owner, lease, multicast);
    }
    Result assign_name(std::string_view name, const DeviceIdentity& owner,
                       std::optional<Duration> lease = std::nullopt,
                       bool multicast = false) {
        return assign(name, owner, lease, multicast);
    }
    Result claimName(std::string_view name, const DeviceIdentity& owner,
                     std::optional<Duration> lease = std::nullopt,
                     bool multicast = false) {
        return assign(name, owner, lease, multicast);
    }
    Result acquire(std::string_view name, const DeviceIdentity& owner,
                   std::optional<Duration> lease = std::nullopt,
                   bool multicast = false) {
        return assign(name, owner, lease, multicast);
    }

    // Forwarding aliases accept the same overload set as assign().
    template <class... Args>
    Result assignName(Args&&... args) {
        return assign(std::forward<Args>(args)...);
    }
    template <class... Args>
    Result assign_name(Args&&... args) {
        return assign(std::forward<Args>(args)...);
    }
    template <class... Args>
    Result requestName(Args&&... args) {
        return assign(std::forward<Args>(args)...);
    }
    template <class... Args>
    Result request_name(Args&&... args) {
        return assign(std::forward<Args>(args)...);
    }
    template <class... Args>
    Result claim(Args&&... args) {
        return assign(std::forward<Args>(args)...);
    }
    template <class... Args>
    Result claimName(Args&&... args) {
        return assign(std::forward<Args>(args)...);
    }
    template <class... Args>
    Result allocate(Args&&... args) {
        return assign(std::forward<Args>(args)...);
    }

    // Renew a lease without changing ownership.  A device reclaiming a name
    // during its own reservation period is also treated as a renewal.
    Result renew(std::string_view name, const DeviceIdentity& owner,
                 std::optional<Duration> lease = std::nullopt,
                 bool multicast = false) {
        return assign(name, owner, lease, multicast);
    }
    Result refresh(std::string_view name, const DeviceIdentity& owner,
                   std::optional<Duration> lease = std::nullopt,
                   bool multicast = false) {
        return assign(name, owner, lease, multicast);
    }
    Result renew(std::string_view name, std::string_view mac,
                 std::uint16_t port, std::string_view station,
                 std::optional<Duration> lease = std::nullopt,
                 bool multicast = false) {
        return assign(name, DeviceIdentity::from(mac, port, station), lease,
                      multicast);
    }

    // Rename atomically: the old name is only released when the new name has
    // passed all ownership, conflict and reservation checks.
    Result rename(std::string_view old_name, std::string_view new_name,
                  const DeviceIdentity& owner,
                  std::optional<Duration> lease = std::nullopt,
                  bool multicast = false) {
        std::lock_guard lock(mutex_);
        if (multicast) {
            return Result(Status::MulticastRestricted);
        }
        if (!owner.mac.is_usable_owner()) {
            return Result(Status::InvalidMac);
        }
        const auto old_key = canonicalName(old_name);
        if (!old_key) {
            return Result(Status::InvalidName);
        }
        const auto new_key = canonicalName(new_name);
        if (!new_key) {
            return Result(Status::InvalidName);
        }

        const auto old_it = names_.find(*old_key);
        if (old_it == names_.end()) {
            return Result(Status::NotFound);
        }
        if (old_it->second.owner != owner) {
            return Result(Status::OwnershipMismatch);
        }

        const Duration effective_lease =
            normalizeLease(lease, old_it->second.lease);
        if (effective_lease.count() <= 0) {
            return Result(Status::InvalidLease);
        }

        if (*new_key == *old_key) {
            return renewEntry(old_it->second, effective_lease);
        }

        const auto new_it = names_.find(*new_key);
        if (new_it != names_.end() && new_it->second.owner != owner) {
            const Status conflict = conflictStatus(new_it->second);
            if (conflict != Status::Ok) {
                return Result(conflict);
            }
        }

        Entry replacement = makeEntry(std::string(new_name), owner,
                                      effective_lease);
        if (new_it != names_.end() && new_it->second.owner == owner) {
            replacement = new_it->second;
            touchEntry(replacement, effective_lease);
        }

        names_.erase(old_it);
        names_[*new_key] = std::move(replacement);
        return Result(Status::Ok);
    }
    Result renameDevice(std::string_view old_name, std::string_view new_name,
                        const DeviceIdentity& owner,
                        std::optional<Duration> lease = std::nullopt,
                        bool multicast = false) {
        return rename(old_name, new_name, owner, lease, multicast);
    }
    Result rename(const DeviceIdentity& owner, std::string_view old_name,
                  std::string_view new_name,
                  std::optional<Duration> lease = std::nullopt,
                  bool multicast = false) {
        return rename(old_name, new_name, owner, lease, multicast);
    }
    Result rename(std::string_view old_name, std::string_view new_name,
                  std::string_view mac, std::uint16_t port,
                  std::string_view station,
                  std::optional<Duration> lease = std::nullopt,
                  bool multicast = false) {
        return rename(old_name, new_name,
                      DeviceIdentity::from(mac, port, station), lease,
                      multicast);
    }

    // A reset releases the caller's own lease.  It cannot be used by another
    // device to evict an offline owner, and it is never accepted over
    // multicast.
    Result reset(std::string_view name, const DeviceIdentity& owner,
                 bool multicast = false) {
        std::lock_guard lock(mutex_);
        if (multicast) {
            return Result(Status::MulticastRestricted);
        }
        if (!owner.mac.is_usable_owner()) {
            return Result(Status::InvalidMac);
        }
        const auto key = canonicalName(name);
        if (!key) {
            return Result(Status::InvalidName);
        }
        const auto it = names_.find(*key);
        if (it == names_.end()) {
            return Result(Status::NotFound);
        }
        if (it->second.owner != owner) {
            return Result(Status::OwnershipMismatch);
        }
        names_.erase(it);
        return Result(Status::Ok);
    }
    Result release(std::string_view name, const DeviceIdentity& owner,
                   bool multicast = false) {
        return reset(name, owner, multicast);
    }
    Result unassign(std::string_view name, const DeviceIdentity& owner,
                    bool multicast = false) {
        return reset(name, owner, multicast);
    }
    Result revoke(std::string_view name, const DeviceIdentity& owner,
                  bool multicast = false) {
        return reset(name, owner, multicast);
    }
    Result factoryReset(std::string_view name, const DeviceIdentity& owner,
                        bool multicast = false) {
        return reset(name, owner, multicast);
    }
    Result reset(std::string_view name, std::string_view mac,
                 std::uint16_t port, std::string_view station,
                 bool multicast = false) {
        return reset(name, DeviceIdentity::from(mac, port, station), multicast);
    }
    template <class... Args>
    Result resetName(Args&&... args) {
        return reset(std::forward<Args>(args)...);
    }
    template <class... Args>
    Result reset_name(Args&&... args) {
        return reset(std::forward<Args>(args)...);
    }
    template <class... Args>
    Result releaseName(Args&&... args) {
        return reset(std::forward<Args>(args)...);
    }
    template <class... Args>
    Result release_name(Args&&... args) {
        return reset(std::forward<Args>(args)...);
    }

    // Explicit offline notifications start (or extend) the reservation clock.
    Result markOffline(std::string_view name, const DeviceIdentity& owner,
                       bool multicast = false) {
        std::lock_guard lock(mutex_);
        if (multicast) {
            return Result(Status::MulticastRestricted);
        }
        if (!owner.mac.is_usable_owner()) {
            return Result(Status::InvalidMac);
        }
        const auto key = canonicalName(name);
        if (!key) {
            return Result(Status::InvalidName);
        }
        const auto it = names_.find(*key);
        if (it == names_.end()) {
            return Result(Status::NotFound);
        }
        if (it->second.owner != owner) {
            return Result(Status::OwnershipMismatch);
        }

        const ClockPoint now = currentTimeLocked();
        it->second.online = false;
        it->second.offline_since = now;
        it->second.reservation_until =
            std::max(it->second.lease_expires_at,
                     now + config_.default_reservation);
        return Result(Status::Ok);
    }
    Result mark_offline(std::string_view name, const DeviceIdentity& owner,
                        bool multicast = false) {
        return markOffline(name, owner, multicast);
    }
    Result deviceOffline(std::string_view name, const DeviceIdentity& owner,
                         bool multicast = false) {
        return markOffline(name, owner, multicast);
    }
    Result markOffline(std::string_view name, std::string_view mac,
                       std::uint16_t port, std::string_view station,
                       bool multicast = false) {
        return markOffline(name, DeviceIdentity::from(mac, port, station),
                           multicast);
    }
    Result setOffline(std::string_view name, const DeviceIdentity& owner,
                      bool multicast = false) {
        return markOffline(name, owner, multicast);
    }
    Result set_offline(std::string_view name, const DeviceIdentity& owner,
                       bool multicast = false) {
        return markOffline(name, owner, multicast);
    }

    // An online announcement from the rightful owner refreshes the lease.
    Result markOnline(std::string_view name, const DeviceIdentity& owner,
                      std::optional<Duration> lease = std::nullopt) {
        return renew(name, owner, lease, false);
    }
    Result mark_online(std::string_view name, const DeviceIdentity& owner,
                       std::optional<Duration> lease = std::nullopt) {
        return markOnline(name, owner, lease);
    }
    Result setOnline(std::string_view name, const DeviceIdentity& owner,
                     std::optional<Duration> lease = std::nullopt) {
        return markOnline(name, owner, lease);
    }
    Result set_online(std::string_view name, const DeviceIdentity& owner,
                      std::optional<Duration> lease = std::nullopt) {
        return markOnline(name, owner, lease);
    }

    //------------------------------------------------------------------------
    // Read-only inspection
    //------------------------------------------------------------------------

    [[nodiscard]] std::optional<NameBinding> lookup(std::string_view name) const {
        std::lock_guard lock(mutex_);
        const auto key = canonicalName(name);
        if (!key) {
            return std::nullopt;
        }
        const auto it = names_.find(*key);
        if (it == names_.end()) {
            return std::nullopt;
        }
        return publicBinding(it->second);
    }
    [[nodiscard]] std::optional<NameBinding> binding(std::string_view name) const {
        return lookup(name);
    }
    [[nodiscard]] std::optional<NameBinding> find(std::string_view name) const {
        return lookup(name);
    }
    [[nodiscard]] std::optional<NameBinding> get(std::string_view name) const {
        return lookup(name);
    }

    [[nodiscard]] std::optional<DeviceIdentity> ownerOf(
        std::string_view name) const {
        const auto value = lookup(name);
        if (!value) {
            return std::nullopt;
        }
        return value->owner;
    }
    [[nodiscard]] std::optional<DeviceIdentity> owner_of(
        std::string_view name) const {
        return ownerOf(name);
    }
    [[nodiscard]] std::optional<DeviceIdentity> owner(
        std::string_view name) const {
        return ownerOf(name);
    }

    [[nodiscard]] bool owns(std::string_view name,
                            const DeviceIdentity& candidate) const {
        const auto value = lookup(name);
        return value && value->owner == candidate;
    }
    [[nodiscard]] bool isOwner(std::string_view name,
                               const DeviceIdentity& candidate) const {
        return owns(name, candidate);
    }
    [[nodiscard]] bool is_owner(std::string_view name,
                                const DeviceIdentity& candidate) const {
        return owns(name, candidate);
    }
    [[nodiscard]] bool owns(std::string_view name, std::string_view mac,
                            std::uint16_t port,
                            std::string_view station) const {
        return owns(name, DeviceIdentity::from(mac, port, station));
    }
    [[nodiscard]] bool isOwner(std::string_view name, std::string_view mac,
                               std::uint16_t port,
                               std::string_view station) const {
        return owns(name, mac, port, station);
    }
    template <class Mac,
              std::enable_if_t<
                  std::is_same_v<std::remove_cvref_t<Mac>, MacAddress>, int> = 0>
    [[nodiscard]] bool owns(std::string_view name, Mac&& mac,
                            std::uint16_t port,
                            std::string_view station) const {
        return owns(name, DeviceIdentity{std::forward<Mac>(mac), port,
                                         std::string(station)});
    }
    template <class Mac,
              std::enable_if_t<
                  std::is_same_v<std::remove_cvref_t<Mac>, MacAddress>, int> = 0>
    [[nodiscard]] bool isOwner(std::string_view name, Mac&& mac,
                               std::uint16_t port,
                               std::string_view station) const {
        return owns(name, std::forward<Mac>(mac), port, station);
    }

    [[nodiscard]] bool isReserved(std::string_view name) const {
        std::lock_guard lock(mutex_);
        const auto key = canonicalName(name);
        if (!key) {
            return false;
        }
        const auto it = names_.find(*key);
        if (it == names_.end()) {
            return false;
        }
        const ClockPoint now = currentTimeLocked();
        if (now >= it->second.reservation_until) {
            return false;
        }
        // An explicit offline event protects the name immediately.  Without
        // one, lease expiry is the point at which the device is presumed
        // offline and retention begins.
        return !it->second.online || now >= it->second.lease_expires_at;
    }
    [[nodiscard]] bool is_reserved(std::string_view name) const {
        return isReserved(name);
    }

    [[nodiscard]] bool isAvailable(std::string_view name,
                                   const DeviceIdentity& candidate) const {
        return check(name, candidate) == Status::Ok;
    }
    [[nodiscard]] bool is_available(std::string_view name,
                                    const DeviceIdentity& candidate) const {
        return isAvailable(name, candidate);
    }

    // Returns Ok when the candidate may take the name, otherwise the exact
    // reason it is protected.  This is useful as a preflight check for the
    // discovery service, while assign() performs the same check atomically.
    [[nodiscard]] Status check(std::string_view name,
                               const DeviceIdentity& candidate) const {
        std::lock_guard lock(mutex_);
        const auto key = canonicalName(name);
        if (!key) {
            return Status::InvalidName;
        }
        const auto it = names_.find(*key);
        if (it == names_.end()) {
            return Status::Ok;
        }
        if (it->second.owner == candidate) {
            return Status::Ok;
        }
        return conflictStatus(it->second);
    }
    [[nodiscard]] Status checkConflict(std::string_view name,
                                       const DeviceIdentity& candidate) const {
        return check(name, candidate);
    }

    [[nodiscard]] std::size_t size() const {
        std::lock_guard lock(mutex_);
        return names_.size();
    }

    void clear() {
        std::lock_guard lock(mutex_);
        names_.clear();
    }

    Result remove(std::string_view name, const DeviceIdentity& owner,
                  bool multicast = false) {
        return reset(name, owner, multicast);
    }
    Result erase(std::string_view name, const DeviceIdentity& owner,
                 bool multicast = false) {
        return reset(name, owner, multicast);
    }

private:
    struct Entry {
        std::string name;
        DeviceIdentity owner;
        Duration lease{std::chrono::seconds(60)};
        ClockPoint assigned_at{};
        ClockPoint lease_expires_at{};
        ClockPoint reservation_until{};
        bool online{true};
        std::optional<ClockPoint> offline_since;
    };

    static std::optional<std::string> canonicalName(std::string_view name) {
        while (!name.empty() &&
               (name.front() == ' ' || name.front() == '\t' ||
                name.front() == '\r' || name.front() == '\n')) {
            name.remove_prefix(1);
        }
        while (!name.empty() &&
               (name.back() == ' ' || name.back() == '\t' ||
                name.back() == '\r' || name.back() == '\n')) {
            name.remove_suffix(1);
        }
        if (name.empty() || name.size() > 255) {
            return std::nullopt;
        }

        std::string result;
        result.reserve(name.size());
        for (char c : name) {
            const unsigned char u = static_cast<unsigned char>(c);
            if (u < 0x20U || u == 0x7FU) {
                return std::nullopt;
            }
            if (u >= 'A' && u <= 'Z') {
                result.push_back(static_cast<char>(u - 'A' + 'a'));
            } else {
                result.push_back(c);
            }
        }
        return result;
    }

    ClockPoint currentTimeLocked() const {
        if (frozen_now_) {
            return *frozen_now_;
        }
        if (clock_) {
            return clock_();
        }
        return std::chrono::steady_clock::now();
    }

    Duration requestedLease(std::optional<Duration> lease,
                            Duration fallback) const {
        return lease.value_or(fallback);
    }

    Duration normalizeLease(std::optional<Duration> lease,
                            Duration fallback) const {
        return requestedLease(lease, fallback);
    }

    Entry makeEntry(std::string display_name, const DeviceIdentity& owner,
                    Duration lease) const {
        const ClockPoint now = currentTimeLocked();
        Entry entry;
        entry.name = std::move(display_name);
        entry.owner = owner;
        entry.lease = lease;
        entry.assigned_at = now;
        entry.lease_expires_at = now + lease;
        entry.reservation_until = entry.lease_expires_at +
                                  config_.default_reservation;
        entry.online = true;
        entry.offline_since.reset();
        return entry;
    }

    void touchEntry(Entry& entry, Duration lease) const {
        const ClockPoint now = currentTimeLocked();
        entry.lease = lease;
        entry.assigned_at = now;
        entry.lease_expires_at = now + lease;
        entry.reservation_until =
            entry.lease_expires_at + config_.default_reservation;
        entry.online = true;
        entry.offline_since.reset();
    }

    Result renewEntry(Entry& entry, Duration lease) const {
        touchEntry(entry, lease);
        return Result(Status::Ok);
    }

    Status conflictStatus(const Entry& entry) const {
        const ClockPoint now = currentTimeLocked();

        // An explicit offline event puts the name into retention immediately,
        // even if part of the lease is still outstanding.
        if (!entry.online && now < entry.reservation_until) {
            return Status::ReservationActive;
        }

        // A live lease belongs to its owner regardless of the online flag.
        if (now < entry.lease_expires_at) {
            return Status::Conflict;
        }

        if (now < entry.reservation_until) {
            return Status::ReservationActive;
        }

        if (entry.online && entry.offline_since.has_value()) {
            return Status::Conflict;
        }

        if (config_.conflict_policy == ConflictPolicy::KeepOwner) {
            return Status::Conflict;
        }
        return Status::Ok;
    }

    Result assignLocked(std::string_view name, const DeviceIdentity& owner,
                        std::optional<Duration> lease, bool multicast) {
        if (multicast) {
            return Result(Status::MulticastRestricted);
        }
        if (!owner.mac.is_usable_owner()) {
            return Result(Status::InvalidMac);
        }

        const auto key = canonicalName(name);
        if (!key) {
            return Result(Status::InvalidName);
        }

        Duration effective_lease = normalizeLease(lease, config_.default_lease);
        if (effective_lease.count() <= 0) {
            return Result(Status::InvalidLease);
        }

        const auto it = names_.find(*key);
        if (it == names_.end()) {
            Entry entry = makeEntry(std::string(name), owner, effective_lease);
            names_.emplace(*key, std::move(entry));
            return Result(Status::Ok);
        }

        if (it->second.owner == owner) {
            return renewEntry(it->second, effective_lease);
        }

        const Status conflict = conflictStatus(it->second);
        if (conflict != Status::Ok) {
            return Result(conflict);
        }

        it->second = makeEntry(std::string(name), owner, effective_lease);
        return Result(Status::Ok);
    }

    static NameBinding publicBinding(const Entry& entry) {
        NameBinding binding;
        binding.name = entry.name;
        binding.owner = entry.owner;
        binding.lease = entry.lease;
        binding.assigned_at = entry.assigned_at;
        binding.lease_expires_at = entry.lease_expires_at;
        binding.reservation_until = entry.reservation_until;
        binding.online = entry.online;
        binding.offline_since = entry.offline_since;
        return binding;
    }

    mutable std::mutex mutex_;
    Config config_{};
    std::map<std::string, Entry, std::less<>> names_;
    Clock clock_;
    std::optional<ClockPoint> frozen_now_;
};

// Compatibility names for integrations that refer to the component by a
// different role.
using NameRegistry = NameOwnership;
using DeviceNameRegistry = NameOwnership;
using NameOwnershipService = NameOwnership;
using NameAllocator = NameOwnership;
using NameOwnershipTable = NameOwnership;

using DeviceId = DeviceIdentity;
using DeviceName = DeviceIdentity;
using NameRequest = Request;
using ClaimRequest = Request;
using NameAssignmentRequest = Request;
using NameLease = NameBinding;
using LeaseRecord = NameBinding;
using NameOwnershipConfig = Config;
using Settings = Config;
using Policy = ConflictPolicy;
using ResultCode = Status;

inline NameOwnership makeNameOwnership(Config config = {}) {
    return NameOwnership(std::move(config));
}
inline NameOwnership makeNameRegistry(Config config = {}) {
    return NameOwnership(std::move(config));
}

}  // namespace netsec
