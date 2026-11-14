#pragma once

#include <chrono>
#include <cstddef>
#include <cstdint>
#include <filesystem>
#include <functional>
#include <map>
#include <memory>
#include <mutex>
#include <optional>
#include <string>
#include <string_view>
#include <unordered_map>
#include <utility>
#include <vector>

namespace netsec {

using Octets = std::vector<std::uint8_t>;

inline constexpr std::uint32_t kDefaultTimeWindowSeconds = 150;
inline constexpr std::uint32_t kMaxEngineBoots = 2'147'483'647u;
inline constexpr std::size_t kDefaultReplayCapacity = 2'048;

enum class AuthProtocol : std::uint8_t {
    None = 0,
    HmacSha1 = 1,
    Sha1 = HmacSha1,
    HmacSha256 = 2,
    Sha256 = HmacSha256,
    HmacMd5 = 3,
    Md5 = HmacMd5,
};

enum class PrivProtocol : std::uint8_t {
    None = 0,
    Des = 1,
    TripleDes = 2,
    Aes128 = 3,
    AES128 = Aes128,
    Aes192 = 4,
    AES192 = Aes192,
    Aes256 = 5,
    AES256 = Aes256,
};

[[nodiscard]] std::string_view to_string(AuthProtocol protocol) noexcept;
[[nodiscard]] std::string_view to_string(PrivProtocol protocol) noexcept;

struct EngineConfig {
    Octets engine_id;
    std::uint32_t boots = 1;
    std::uint32_t engine_time = 0;
    std::uint32_t time_window_seconds = kDefaultTimeWindowSeconds;
    std::size_t replay_capacity = kDefaultReplayCapacity;
};

struct PersistedEngineState {
    Octets engine_id;
    std::uint32_t boots = 0;
    std::uint32_t engine_time = 0;
};

// This store is deliberately independent of the configuration backup.  An
// agent should point a FileEngineStateStore at local persistent storage that
// is not restored together with the configuration.
class EngineStateStore {
public:
    virtual ~EngineStateStore() = default;

    [[nodiscard]] virtual std::optional<PersistedEngineState> load() const = 0;
    virtual void save(const PersistedEngineState& state) = 0;
};

class MemoryEngineStateStore final : public EngineStateStore {
public:
    [[nodiscard]] std::optional<PersistedEngineState> load() const override;
    void save(const PersistedEngineState& state) override;

private:
    mutable std::mutex mutex_;
    std::optional<PersistedEngineState> state_;
};

class FileEngineStateStore final : public EngineStateStore {
public:
    explicit FileEngineStateStore(std::filesystem::path path);

    [[nodiscard]] std::optional<PersistedEngineState> load() const override;
    void save(const PersistedEngineState& state) override;

    [[nodiscard]] const std::filesystem::path& path() const noexcept;

private:
    std::filesystem::path path_;
    mutable std::mutex mutex_;
};

using BootStore = EngineStateStore;
using MemoryBootStore = MemoryEngineStateStore;
using FileBootStore = FileEngineStateStore;
using MonotonicClock = std::function<std::chrono::steady_clock::time_point()>;

enum class RestoreResult {
    Unchanged,
    Advanced,
    RequiresResynchronization,
    EngineIdMismatch,
    PersistenceFailure,
};

struct EngineSnapshot {
    Octets engine_id;
    std::uint32_t boots = 0;
    std::uint32_t engine_time = 0;
    std::uint64_t boot_generation = 0;
    bool resynchronization_required = false;
};

class EngineIdentity {
public:
    explicit EngineIdentity(
        EngineConfig config,
        std::shared_ptr<EngineStateStore> store = {},
        MonotonicClock clock = {});
    ~EngineIdentity();

    EngineIdentity(const EngineIdentity&) = delete;
    EngineIdentity& operator=(const EngineIdentity&) = delete;

    [[nodiscard]] EngineSnapshot snapshot() const;
    [[nodiscard]] Octets engineId() const;
    [[nodiscard]] std::uint32_t boots() const;
    [[nodiscard]] std::uint32_t engineTime() const;
    [[nodiscard]] std::uint64_t bootGeneration() const;
    [[nodiscard]] bool resynchronizationRequired() const;
    [[nodiscard]] std::uint32_t timeWindowSeconds() const;

    // Applies an engine identity read from restored configuration.  The
    // persisted identity always wins: a lower boots value is never applied.
    RestoreResult restoreConfiguration(const EngineConfig& restored);

    // Starts a new engine boot.  Boots is advanced at least to minimum_boots
    // and engineTime restarts at zero.  The new boots value is persisted before
    // it becomes visible.
    std::uint32_t resynchronize(std::uint32_t minimum_boots = 0);

    // Convenience name for use when the agent process starts.
    std::uint32_t engineBooted();

    // Saves the current engine-time checkpoint.  Returns false on I/O failure;
    // boots changes are never best-effort and throw instead.
    bool persistCheckpoint() noexcept;

private:
    void saveOrThrow(std::uint32_t boots, std::uint32_t engine_time) const;

    mutable std::mutex mutex_;
    std::shared_ptr<EngineStateStore> store_;
    MonotonicClock clock_;
    std::chrono::steady_clock::time_point clock_origin_;
    Octets engine_id_;
    std::uint32_t boots_ = 0;
    std::uint32_t base_engine_time_ = 0;
    std::uint32_t time_window_seconds_ = kDefaultTimeWindowSeconds;
    std::uint64_t boot_generation_ = 0;
    bool resynchronization_required_ = false;
};

struct UsmUser {
    std::string name;
    std::uint32_t key_generation = 1;
    AuthProtocol auth_protocol = AuthProtocol::HmacSha256;
    PrivProtocol priv_protocol = PrivProtocol::None;
    Octets auth_key;
    Octets privacy_key;
};

struct UsmRequest {
    Octets authoritative_engine_id;
    std::uint32_t boots = 0;
    std::uint32_t engine_time = 0;
    std::string user_name;
    std::uint32_t key_generation = 0;
    AuthProtocol auth_protocol = AuthProtocol::None;
    PrivProtocol priv_protocol = PrivProtocol::None;

    // The received msgAuthenticationParameters.
    Octets authentication_parameters;

    // The complete authenticated message with the authentication-parameter
    // field zeroed, as required by USM.  This is also the replay identity.
    Octets message;
};

enum class UserMutationResult {
    Added,
    Updated,
    AlreadyExists,
    NotFound,
    StaleKeyGeneration,
    Invalid,
};

enum class WindowResult {
    Accepted,
    Duplicate,
    OutsideTimeWindow,
    Full,
};

class SlidingReplayWindow {
public:
    explicit SlidingReplayWindow(
        std::uint32_t window_seconds = kDefaultTimeWindowSeconds,
        std::size_t capacity = kDefaultReplayCapacity);

    // Time is checked inclusively at both ends.  Distinct messages may arrive
    // in any order inside the window; an identical fingerprint is a replay.
    WindowResult accept(
        std::uint32_t boots,
        std::uint32_t request_engine_time,
        std::uint32_t local_engine_time,
        const Octets& fingerprint);

    WindowResult check(
        std::uint32_t boots,
        std::uint32_t request_engine_time,
        std::uint32_t local_engine_time,
        const Octets& fingerprint) {
        return accept(boots, request_engine_time, local_engine_time, fingerprint);
    }

    void reset();
    void reconfigure(std::uint32_t window_seconds, std::size_t capacity);
    [[nodiscard]] std::size_t size() const noexcept;
    [[nodiscard]] std::size_t capacity() const noexcept;
    [[nodiscard]] std::uint32_t windowSeconds() const noexcept;

private:
    struct Entry {
        std::uint32_t boots = 0;
        std::uint32_t engine_time = 0;
    };

    void prune(std::uint32_t local_engine_time);

    std::uint32_t window_seconds_;
    std::size_t capacity_;
    std::uint32_t boots_ = 0;
    bool have_boots_ = false;
    std::unordered_map<std::string, Entry> seen_;
    std::multimap<std::uint32_t, std::string> order_;
};

enum class ReplayStatus {
    Accepted,
    Rejected,
};

// Rejection reasons are intentionally coarse.  In particular, an unknown
// user, a bad authenticator and an incorrect engine ID are not distinguishable
// through this result.  The diagnostic is always a fixed string.
enum class ReplayReason {
    None,
    InvalidSecurityParameters,
    OutsideTimeWindow,
    ReplayedPdu,
    CapacityExceeded,
};

struct ReplayResult {
    ReplayStatus status = ReplayStatus::Rejected;
    ReplayReason reason = ReplayReason::InvalidSecurityParameters;

    [[nodiscard]] bool accepted() const noexcept {
        return status == ReplayStatus::Accepted;
    }
    operator bool() const noexcept { return accepted(); }
    friend bool operator==(
        const ReplayResult& result,
        ReplayStatus status) noexcept {
        return result.status == status;
    }
    [[nodiscard]] std::string_view diagnostic() const noexcept {
        return accepted() ? std::string_view{"accepted"}
                          : std::string_view{"request rejected"};
    }
};

struct ReplayStatistics {
    std::uint64_t total_requests = 0;
    std::uint64_t accepted_requests = 0;
    std::uint64_t dropped_requests = 0;
    std::uint64_t engine_id_rejections = 0;
    std::uint64_t authentication_rejections = 0;
    std::uint64_t key_generation_rejections = 0;
    std::uint64_t time_window_rejections = 0;
    std::uint64_t replay_rejections = 0;
    std::uint64_t capacity_rejections = 0;
};

class ReplayEngine {
public:
    explicit ReplayEngine(
        EngineConfig config,
        std::shared_ptr<EngineStateStore> store = {},
        MonotonicClock clock = {});

    ReplayEngine(const ReplayEngine&) = delete;
    ReplayEngine& operator=(const ReplayEngine&) = delete;

    [[nodiscard]] ReplayResult verify(const UsmRequest& request);
    [[nodiscard]] ReplayResult checkRequest(const UsmRequest& request) {
        return verify(request);
    }
    [[nodiscard]] ReplayResult authenticate(const UsmRequest& request) {
        return verify(request);
    }

    UserMutationResult addUser(UsmUser user);
    UserMutationResult upsertUser(UsmUser user);
    UserMutationResult updateUser(UsmUser user);
    UserMutationResult rotateUserKeys(
        std::string_view user_name,
        std::uint32_t new_key_generation,
        AuthProtocol auth_protocol,
        PrivProtocol priv_protocol,
        Octets auth_key,
        Octets privacy_key);
    bool removeUser(std::string_view user_name);
    void clearUsers();

    RestoreResult restoreConfiguration(const EngineConfig& restored);
    std::uint32_t resynchronize(std::uint32_t minimum_boots = 0);
    std::uint32_t engineBooted();
    [[nodiscard]] bool resynchronizationRequired() const;
    void clearReplayWindows();

    [[nodiscard]] EngineIdentity& identity() noexcept;
    [[nodiscard]] const EngineIdentity& identity() const noexcept;
    [[nodiscard]] EngineSnapshot snapshot() const;
    [[nodiscard]] ReplayStatistics statistics() const;
    void resetStatistics();

private:
    void clearWindowsLocked();
    static Octets replayFingerprint(const UsmRequest& request);

    EngineIdentity identity_;
    std::uint32_t time_window_seconds_;
    std::size_t replay_capacity_;
    mutable std::mutex mutex_;
    std::unordered_map<std::string, UsmUser> users_;
    std::unordered_map<std::string, SlidingReplayWindow> windows_;
    std::uint64_t observed_boot_generation_ = 0;
    ReplayStatistics statistics_;
};

using SnmpEngine = ReplayEngine;
using ReplayGuard = ReplayEngine;
using UsmEngine = ReplayEngine;
using UsmReplayEngine = ReplayEngine;
using SnmpV3Engine = ReplayEngine;
using SnmpReplayGuard = ReplayEngine;

[[nodiscard]] Octets hmac(
    AuthProtocol protocol,
    const Octets& key,
    const Octets& message);

// Returns the USM msgAuthenticationParameters value.  MD5 and SHA-1 are
// truncated to 96 bits and SHA-256 to 192 bits.
[[nodiscard]] Octets makeAuthenticator(
    AuthProtocol protocol,
    const Octets& key,
    const Octets& authenticated_message);

[[nodiscard]] bool verifyAuthenticator(
    AuthProtocol protocol,
    const Octets& key,
    const Octets& authenticated_message,
    const Octets& authentication_parameters);

// RFC 3414-style password-to-key and key localization for the supported
// authentication protocols.
[[nodiscard]] Octets localizeKey(
    AuthProtocol protocol,
    std::string_view password,
    const Octets& engine_id);

}  // namespace netsec
