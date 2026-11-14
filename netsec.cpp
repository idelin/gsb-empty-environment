#include "netsec.hpp"

#include <algorithm>
#include <atomic>
#include <deque>
#include <mutex>
#include <shared_mutex>
#include <unordered_map>
#include <unordered_set>
#include <utility>

namespace netsec::hpke {
namespace {

constexpr std::string_view kLabeledPrefix{"HPKE-v1"};

ByteSpan as_bytes(std::string_view value) noexcept {
    return ByteSpan{reinterpret_cast<const Byte*>(value.data()), value.size()};
}

void secure_wipe(MutableByteSpan bytes) noexcept {
    volatile Byte* data = bytes.data();
    for (std::size_t i = 0; i < bytes.size(); ++i) {
        data[i] = 0;
    }
    std::atomic_signal_fence(std::memory_order_seq_cst);
}

void append_bytes(std::vector<Byte>& out, ByteSpan bytes) {
    out.insert(out.end(), bytes.begin(), bytes.end());
}

void append_u16(std::vector<Byte>& out, std::uint16_t value) {
    out.push_back(static_cast<Byte>((value >> 8U) & 0xffU));
    out.push_back(static_cast<Byte>(value & 0xffU));
}

[[noreturn]] void fail(ErrorCode code, std::string message) {
    throw Error(code, std::move(message));
}

bool has_psk(HpkeMode mode) noexcept {
    return mode == HpkeMode::psk || mode == HpkeMode::auth_psk;
}

bool has_auth(HpkeMode mode) noexcept {
    return mode == HpkeMode::auth || mode == HpkeMode::auth_psk;
}

void validate_psk_inputs(HpkeMode mode,
                         const std::optional<std::vector<Byte>>& psk,
                         const std::optional<std::vector<Byte>>& psk_id) {
    if (!has_psk(mode)) {
        if (psk.has_value() || psk_id.has_value()) {
            fail(ErrorCode::unexpected_psk,
                 "PSK inputs are only valid for PSK and AuthPSK modes");
        }
        return;
    }

    if (!psk.has_value()) {
        fail(ErrorCode::missing_psk, "PSK mode requires psk");
    }
    if (psk->empty()) {
        fail(ErrorCode::empty_psk, "PSK must contain at least one byte");
    }
    if (!psk_id.has_value()) {
        fail(ErrorCode::missing_psk_id, "PSK mode requires psk_id");
    }
}

void validate_sender_inputs(HpkeMode mode, const SenderSetupParams& params) {
    if (params.receiver_public_key.empty()) {
        fail(ErrorCode::missing_receiver_key,
             "receiver_public_key is required for every setup mode");
    }
    if (params.info.size() > kMaxExporterContextLength) {
        fail(ErrorCode::length_limit, "info exceeds the RFC 9180 length limit");
    }

    validate_psk_inputs(mode, params.psk, params.psk_id);

    if (has_auth(mode)) {
        if (!params.sender_static_key.has_value() ||
            *params.sender_static_key == kInvalidHandle) {
            fail(ErrorCode::missing_sender_key,
                 "Auth modes require a sender static-key handle");
        }
        if (params.sender_public_key.empty()) {
            fail(ErrorCode::missing_sender_key,
                 "Auth modes require the sender public-key identity");
        }
    } else {
        if (params.sender_static_key.has_value() ||
            !params.sender_public_key.empty()) {
            fail(ErrorCode::unexpected_sender_key,
                 "Base and PSK modes must not carry a sender static key");
        }
    }
}

void validate_receiver_inputs(HpkeMode mode, const ReceiverSetupParams& params) {
    if (params.enc.empty()) {
        fail(ErrorCode::invalid_argument, "enc must contain the raw KEM ciphertext");
    }
    if (params.receiver_public_key.empty()) {
        fail(ErrorCode::missing_receiver_key,
             "receiver_public_key is required for every setup mode");
    }
    if (params.receiver_private_key == kInvalidHandle) {
        fail(ErrorCode::missing_receiver_key,
             "receiver_private_key is required for decapsulation");
    }
    if (params.info.size() > kMaxExporterContextLength) {
        fail(ErrorCode::length_limit, "info exceeds the RFC 9180 length limit");
    }

    validate_psk_inputs(mode, params.psk, params.psk_id);

    if (has_auth(mode)) {
        if (params.sender_public_key.empty()) {
            fail(ErrorCode::missing_sender_key,
                 "Auth modes require the sender public-key identity");
        }
    } else if (!params.sender_public_key.empty()) {
        fail(ErrorCode::unexpected_sender_key,
             "Base and PSK receiver setup must not carry a sender public key");
    }
}

class SecretLease {
public:
    SecretLease() = default;

    SecretLease(OpaqueHandle handle, std::function<void(OpaqueHandle)> destroy)
        : handle_(handle), destroy_(std::move(destroy)) {}

    ~SecretLease() { reset(); }

    SecretLease(const SecretLease&) = delete;
    SecretLease& operator=(const SecretLease&) = delete;

    SecretLease(SecretLease&& other) noexcept
        : handle_(std::exchange(other.handle_, kInvalidHandle)),
          destroy_(std::move(other.destroy_)) {}

    SecretLease& operator=(SecretLease&& other) noexcept {
        if (this != &other) {
            reset();
            handle_ = std::exchange(other.handle_, kInvalidHandle);
            destroy_ = std::move(other.destroy_);
        }
        return *this;
    }

    [[nodiscard]] explicit operator bool() const noexcept {
        return handle_ != kInvalidHandle;
    }

    [[nodiscard]] OpaqueHandle get() const noexcept { return handle_; }

    void reset() noexcept {
        const OpaqueHandle old = std::exchange(handle_, kInvalidHandle);
        if (old != kInvalidHandle && destroy_) {
            try {
                destroy_(old);
            } catch (...) {
                // Destruction must not throw out of a cleanup path.
            }
        }
    }

private:
    OpaqueHandle handle_ = kInvalidHandle;
    std::function<void(OpaqueHandle)> destroy_;
};

struct SuiteRuntime {
    SuiteId id;
    std::array<Byte, 10> serialized_id;
    SuiteCallbacks callbacks;
    mutable std::mutex callback_mutex;
};

struct CachedSchedule {
    CachedSchedule(SecretLease key_secret,
                   SecretLease nonce_secret,
                   SecretLease exporter_secret)
        : key(std::move(key_secret)),
          nonce(std::move(nonce_secret)),
          exporter(std::move(exporter_secret)) {}

    CachedSchedule(const CachedSchedule&) = delete;
    CachedSchedule& operator=(const CachedSchedule&) = delete;

    void revoke() noexcept {
        SecretLease retired_key;
        SecretLease retired_nonce;
        SecretLease retired_exporter;
        {
            std::lock_guard lock(mutex);
            if (revoked) {
                return;
            }
            revoked = true;
            retired_key = std::move(key);
            retired_nonce = std::move(nonce);
            retired_exporter = std::move(exporter);
        }
        // Provider destroy callbacks run after unlocking so they may safely
        // call back into the manager.
    }

    mutable std::mutex mutex;
    bool revoked = false;
    SecretLease key;
    SecretLease nonce;
    SecretLease exporter;
};

struct ContextKey {
    SuiteId suite;
    HpkeMode mode = HpkeMode::base;
    std::uint64_t generation = 0;
    bool receiver_side = false;
    std::vector<Byte> enc;
    std::vector<Byte> receiver_public_key;
    std::vector<Byte> sender_public_key;
    std::vector<Byte> info;
    std::optional<std::vector<Byte>> psk_id;
    std::vector<Byte> psk_binding;

    friend bool operator==(const ContextKey&, const ContextKey&) = default;
};

struct SuiteIdHash {
    std::size_t operator()(const SuiteId& id) const noexcept {
        std::size_t seed = static_cast<std::size_t>(id.kem_id);
        seed ^= static_cast<std::size_t>(id.kdf_id) << 16U;
        seed ^= static_cast<std::size_t>(id.aead_id) * 0x9e3779b1U;
        return seed;
    }
};

struct ContextKeyHash {
    static void mix(std::size_t& seed, ByteSpan bytes) noexcept {
        seed ^= bytes.size() + 0x9e3779b97f4a7c15ULL + (seed << 6U) + (seed >> 2U);
        for (const Byte byte : bytes) {
            seed ^= static_cast<std::size_t>(byte);
            seed *= static_cast<std::size_t>(0x100000001b3ULL);
        }
    }

    std::size_t operator()(const ContextKey& key) const noexcept {
        std::size_t seed = SuiteIdHash{}(key.suite);
        seed ^= static_cast<std::size_t>(key.mode) << 8U;
        seed ^= static_cast<std::size_t>(key.generation);
        seed ^= key.receiver_side ? 0x517cc1b727220a95ULL : 0;
        mix(seed, key.enc);
        mix(seed, key.receiver_public_key);
        mix(seed, key.sender_public_key);
        mix(seed, key.info);
        if (key.psk_id.has_value()) {
            mix(seed, *key.psk_id);
        }
        mix(seed, key.psk_binding);
        return seed;
    }
};

struct ManagerState {
    mutable std::shared_mutex mutex;
    std::string exporter_namespace;
    std::uint64_t generation = 1;
    bool closed = false;
    std::size_t max_cache_entries = 128;
    std::unordered_map<SuiteId, std::shared_ptr<SuiteRuntime>, SuiteIdHash> suites;
    std::unordered_map<ContextKey, std::shared_ptr<CachedSchedule>, ContextKeyHash> cache;
    std::deque<ContextKey> cache_order;
    std::vector<std::weak_ptr<CachedSchedule>> live_schedules;
};

}  // namespace

struct HpkeContext::Impl {
    ContextMetadata metadata;
    std::shared_ptr<SuiteRuntime> suite;
    std::shared_ptr<ManagerState> state;
    std::shared_ptr<CachedSchedule> schedule;
};

struct HpkeContextManager::Impl {
    std::shared_ptr<ManagerState> state;
};

namespace detail {

struct HpkeContextAccess {
    static HpkeContext make(ContextMetadata metadata,
                            std::shared_ptr<SuiteRuntime> suite,
                            std::shared_ptr<ManagerState> state,
                            std::shared_ptr<CachedSchedule> schedule) {
        auto impl = std::make_shared<HpkeContext::Impl>();
        impl->metadata = std::move(metadata);
        impl->suite = std::move(suite);
        impl->state = std::move(state);
        impl->schedule = std::move(schedule);
        return HpkeContext(std::move(impl));
    }
};

}  // namespace detail

namespace {

std::shared_ptr<SuiteRuntime> find_suite(const std::shared_ptr<ManagerState>& state,
                                         const SuiteId& id) {
    std::shared_lock lock(state->mutex);
    if (state->closed) {
        fail(ErrorCode::closed, "HPKE context manager is closed");
    }
    const auto it = state->suites.find(id);
    if (it == state->suites.end()) {
        fail(ErrorCode::invalid_suite, "suite has not been registered");
    }
    return it->second;
}

std::uint64_t capture_generation(const std::shared_ptr<ManagerState>& state,
                                 std::optional<std::uint64_t> requested) {
    std::shared_lock lock(state->mutex);
    if (state->closed) {
        fail(ErrorCode::closed, "HPKE context manager is closed");
    }
    if (requested.has_value()) {
        if (*requested == 0 || *requested != state->generation) {
            fail(ErrorCode::stale_generation,
                 "setup was started for a stale handshake generation");
        }
    }
    return state->generation;
}

std::shared_ptr<CachedSchedule> cache_lookup(
    const std::shared_ptr<ManagerState>& state,
    const ContextKey& key,
    std::uint64_t expected_generation) {
    std::shared_lock lock(state->mutex);
    if (state->closed || state->generation != expected_generation) {
        fail(ErrorCode::stale_generation,
             "receiver key rotated while setup was in progress");
    }
    const auto it = state->cache.find(key);
    if (it == state->cache.end()) {
        return nullptr;
    }
    return it->second;
}

std::shared_ptr<CachedSchedule> commit_schedule(
    const std::shared_ptr<ManagerState>& state,
    const ContextKey& key,
    std::uint64_t expected_generation,
    std::shared_ptr<CachedSchedule> candidate) {
    const auto remove_expired = [&state]() {
        state->live_schedules.erase(
            std::remove_if(state->live_schedules.begin(),
                           state->live_schedules.end(),
                           [](const std::weak_ptr<CachedSchedule>& weak) {
                               return weak.expired();
                           }),
            state->live_schedules.end());
    };

    std::unique_lock lock(state->mutex);
    if (state->closed || state->generation != expected_generation) {
        fail(ErrorCode::stale_generation,
             "receiver key rotated while setup was in progress");
    }

    const auto existing = state->cache.find(key);
    if (existing != state->cache.end()) {
        return existing->second;
    }

    if (state->max_cache_entries != 0) {
        while (state->cache.size() >= state->max_cache_entries &&
               !state->cache_order.empty()) {
            const ContextKey& oldest = state->cache_order.front();
            state->cache.erase(oldest);
            state->cache_order.pop_front();
        }
        state->cache.emplace(key, candidate);
        state->cache_order.push_back(key);
    }

    remove_expired();
    state->live_schedules.push_back(candidate);
    return candidate;
}

std::vector<std::shared_ptr<CachedSchedule>> collect_schedules(
    ManagerState& state) {
    std::unordered_set<CachedSchedule*> seen;
    std::vector<std::shared_ptr<CachedSchedule>> result;

    for (auto& [unused, schedule] : state.cache) {
        (void)unused;
        if (seen.insert(schedule.get()).second) {
            result.push_back(schedule);
        }
    }
    for (const auto& weak : state.live_schedules) {
        if (auto schedule = weak.lock()) {
            if (seen.insert(schedule.get()).second) {
                result.push_back(std::move(schedule));
            }
        }
    }

    state.cache.clear();
    state.cache_order.clear();
    state.live_schedules.clear();
    return result;
}

void revoke_all(std::vector<std::shared_ptr<CachedSchedule>>& schedules) {
    for (const auto& schedule : schedules) {
        schedule->revoke();
    }
}

void close_state(const std::shared_ptr<ManagerState>& state) {
    std::vector<std::shared_ptr<CachedSchedule>> schedules;
    {
        std::unique_lock lock(state->mutex);
        if (state->closed) {
            return;
        }
        state->closed = true;
        ++state->generation;
        if (state->generation == 0) {
            state->generation = 1;
        }
        schedules = collect_schedules(*state);
    }
    revoke_all(schedules);
}

OpaqueHandle call_extract(SuiteRuntime& suite,
                          ByteSpan salt,
                          const KdfExtractInput& input) {
    std::lock_guard lock(suite.callback_mutex);
    try {
        const OpaqueHandle handle = suite.callbacks.kdf.extract(salt, input);
        if (handle == kInvalidHandle) {
            fail(ErrorCode::provider_failure, "KDF Extract returned an invalid handle");
        }
        return handle;
    } catch (const Error&) {
        throw;
    } catch (...) {
        fail(ErrorCode::provider_failure, "KDF Extract callback failed");
    }
}

SecretLease extract_lease(SuiteRuntime& suite,
                          ByteSpan salt,
                          const KdfExtractInput& input) {
    const OpaqueHandle handle = call_extract(suite, salt, input);
    return SecretLease(handle, suite.callbacks.kdf.destroy_secret);
}

OpaqueHandle call_expand(SuiteRuntime& suite,
                         OpaqueHandle prk,
                         ByteSpan info,
                         std::size_t length) {
    std::lock_guard lock(suite.callback_mutex);
    try {
        const OpaqueHandle handle =
            suite.callbacks.kdf.expand(prk, info, length);
        if (handle == kInvalidHandle) {
            fail(ErrorCode::provider_failure, "KDF Expand returned an invalid handle");
        }
        return handle;
    } catch (const Error&) {
        throw;
    } catch (...) {
        fail(ErrorCode::provider_failure, "KDF Expand callback failed");
    }
}

SecretLease expand_lease(SuiteRuntime& suite,
                         OpaqueHandle prk,
                         ByteSpan info,
                         std::size_t length) {
    const OpaqueHandle handle = call_expand(suite, prk, info, length);
    return SecretLease(handle, suite.callbacks.kdf.destroy_secret);
}

void read_secret(SuiteRuntime& suite,
                 OpaqueHandle secret,
                 MutableByteSpan output) {
    std::lock_guard lock(suite.callback_mutex);
    try {
        if (!suite.callbacks.kdf.read(secret, output)) {
            fail(ErrorCode::provider_failure, "KDF secret read callback failed");
        }
    } catch (const Error&) {
        throw;
    } catch (...) {
        fail(ErrorCode::provider_failure, "KDF secret read callback failed");
    }
}

SecureBytes read_secret_bytes(SuiteRuntime& suite,
                              OpaqueHandle secret,
                              std::size_t length) {
    SecureBytes output(length);
    if (length != 0) {
        read_secret(suite, secret, output.mutable_bytes());
    }
    return output;
}

std::vector<Byte> hash_psk(SuiteRuntime& suite, ByteSpan psk) {
    std::lock_guard lock(suite.callback_mutex);
    try {
        std::vector<Byte> digest = suite.callbacks.kdf.hash(psk);
        if (digest.size() != suite.callbacks.kdf.hash_size) {
            fail(ErrorCode::provider_failure,
                 "KDF hash callback returned the wrong digest size");
        }
        return digest;
    } catch (const Error&) {
        throw;
    } catch (...) {
        fail(ErrorCode::provider_failure, "KDF hash callback failed");
    }
}

std::vector<Byte> labeled_bytes(SuiteRuntime& suite,
                                ByteSpan label,
                                ByteSpan value) {
    std::vector<Byte> input;
    input.reserve(kLabeledPrefix.size() + suite.serialized_id.size() +
                  label.size() + value.size());
    append_bytes(input, as_bytes(kLabeledPrefix));
    append_bytes(input, suite.serialized_id);
    append_bytes(input, label);
    append_bytes(input, value);
    return input;
}

SecretLease labeled_extract_bytes(SuiteRuntime& suite,
                                  ByteSpan salt,
                                  ByteSpan label,
                                  ByteSpan value) {
    const std::vector<Byte> input = labeled_bytes(suite, label, value);
    return extract_lease(
        suite, salt, KdfExtractInput::from_bytes(input));
}

SecretLease labeled_extract_secret(SuiteRuntime& suite,
                                   ByteSpan salt,
                                   ByteSpan label,
                                   OpaqueHandle secret) {
    std::vector<Byte> prefix;
    prefix.reserve(kLabeledPrefix.size() + suite.serialized_id.size() +
                   label.size());
    append_bytes(prefix, as_bytes(kLabeledPrefix));
    append_bytes(prefix, suite.serialized_id);
    append_bytes(prefix, label);
    return extract_lease(
        suite, salt, KdfExtractInput::from_secret(prefix, secret));
}

SecretLease labeled_expand(SuiteRuntime& suite,
                           OpaqueHandle prk,
                           ByteSpan label,
                           ByteSpan info,
                           std::size_t length) {
    if (info.size() > kMaxExporterContextLength) {
        fail(ErrorCode::length_limit, "labeledExpand context is too long");
    }
    if (length == 0) {
        fail(ErrorCode::invalid_argument, "KDF Expand length must be non-zero");
    }

    std::vector<Byte> expanded_info;
    expanded_info.reserve(kLabeledPrefix.size() + suite.serialized_id.size() +
                          label.size() + 2 + info.size());
    append_bytes(expanded_info, as_bytes(kLabeledPrefix));
    append_bytes(expanded_info, suite.serialized_id);
    append_bytes(expanded_info, label);
    append_u16(expanded_info, static_cast<std::uint16_t>(info.size()));
    append_bytes(expanded_info, info);
    return expand_lease(suite, prk, expanded_info, length);
}

struct ScheduleSecrets {
    SecretLease key;
    SecretLease nonce;
    SecretLease exporter;
};

ScheduleSecrets derive_schedule(SuiteRuntime& suite,
                                SecretLease& shared_secret,
                                HpkeMode mode,
                                std::optional<ByteSpan> psk,
                                ByteSpan psk_id,
                                ByteSpan info) {
    const ByteSpan empty_salt{};
    constexpr std::string_view psk_id_hash_label{"psk_id_hash"};
    constexpr std::string_view info_hash_label{"info_hash"};
    constexpr std::string_view secret_label{"secret"};
    constexpr std::string_view key_label{"key"};
    constexpr std::string_view nonce_label{"base_nonce"};
    constexpr std::string_view exporter_label{"exp"};

    SecretLease psk_id_hash = labeled_extract_bytes(
        suite,
        empty_salt,
        as_bytes(psk_id_hash_label),
        psk_id);
    const SecureBytes psk_id_hash_bytes = read_secret_bytes(
        suite, psk_id_hash.get(), suite.callbacks.kdf.hash_size);

    SecretLease info_hash = labeled_extract_bytes(
        suite,
        empty_salt,
        as_bytes(info_hash_label),
        info);
    const SecureBytes info_hash_bytes = read_secret_bytes(
        suite, info_hash.get(), suite.callbacks.kdf.hash_size);

    std::vector<Byte> key_schedule_context;
    key_schedule_context.reserve(1 + 2 * suite.callbacks.kdf.hash_size);
    key_schedule_context.push_back(static_cast<Byte>(mode));
    append_bytes(key_schedule_context, psk_id_hash_bytes.bytes());
    append_bytes(key_schedule_context, info_hash_bytes.bytes());

    const ByteSpan psk_salt =
        psk.has_value() ? *psk : ByteSpan{};
    SecretLease secret = labeled_extract_secret(
        suite,
        psk_salt,
        as_bytes(secret_label),
        shared_secret.get());

    // The KEM shared secret is no longer needed after the HKDF Extract step.
    shared_secret.reset();

    ScheduleSecrets result;
    result.key = labeled_expand(
        suite,
        secret.get(),
        as_bytes(key_label),
        key_schedule_context,
        suite.callbacks.aead.key_size);
    result.nonce = labeled_expand(
        suite,
        secret.get(),
        as_bytes(nonce_label),
        key_schedule_context,
        suite.callbacks.aead.nonce_size);
    result.exporter = labeled_expand(
        suite,
        secret.get(),
        as_bytes(exporter_label),
        key_schedule_context,
        suite.callbacks.kdf.hash_size);
    return result;
}

std::optional<Encapsulation> call_encapsulate(
    SuiteRuntime& suite,
    ByteSpan recipient_public_key,
    std::optional<OpaqueHandle> sender_static_key,
    ByteSpan sender_public_key) {
    std::lock_guard lock(suite.callback_mutex);
    try {
        return suite.callbacks.kem.encapsulate(
            recipient_public_key, sender_static_key, sender_public_key);
    } catch (...) {
        fail(ErrorCode::provider_failure, "KEM Encapsulate callback failed");
    }
}

Decapsulation call_decapsulate(SuiteRuntime& suite,
                               ByteSpan enc,
                               OpaqueHandle receiver_private_key,
                               ByteSpan sender_public_key) {
    std::lock_guard lock(suite.callback_mutex);
    try {
        return suite.callbacks.kem.decapsulate(
            enc, receiver_private_key, sender_public_key);
    } catch (...) {
        fail(ErrorCode::provider_failure, "KEM Decapsulate callback failed");
    }
}

std::string normalize_namespace(std::string value) {
    while (!value.empty() &&
           (value.back() == '.' || value.back() == '/' || value.back() == ':')) {
        value.pop_back();
    }
    if (value.empty()) {
        fail(ErrorCode::invalid_argument,
             "exporter namespace must contain at least one character");
    }
    if (value.size() > kMaxLabelLength) {
        fail(ErrorCode::length_limit, "exporter namespace is too long");
    }
    return value;
}

bool label_in_namespace(std::string_view label, std::string_view ns) noexcept {
    if (label.size() <= ns.size() || !label.starts_with(ns)) {
        return false;
    }
    const char separator = label[ns.size()];
    return separator == '.' || separator == '/' || separator == ':';
}

ContextKey make_context_key(const ContextMetadata& metadata,
                            ByteSpan psk_binding) {
    ContextKey key;
    key.suite = metadata.suite;
    key.mode = metadata.mode;
    key.generation = metadata.generation;
    key.receiver_side = metadata.receiver_side;
    key.enc = metadata.enc;
    key.receiver_public_key = metadata.receiver_public_key;
    key.sender_public_key = metadata.sender_public_key;
    key.info = metadata.info;
    key.psk_id = metadata.psk_id;
    key.psk_binding.assign(psk_binding.begin(), psk_binding.end());
    return key;
}

std::shared_ptr<CachedSchedule> schedule_for_context(
    const std::shared_ptr<ManagerState>& state,
    SuiteRuntime& suite,
    const ContextKey& key,
    std::uint64_t generation,
    SecretLease& shared_secret,
    HpkeMode mode,
    std::optional<ByteSpan> psk,
    ByteSpan psk_id,
    ByteSpan info) {
    if (auto cached = cache_lookup(state, key, generation)) {
        return cached;
    }

    ScheduleSecrets secrets =
        derive_schedule(suite, shared_secret, mode, psk, psk_id, info);
    auto candidate = std::make_shared<CachedSchedule>(
        std::move(secrets.key),
        std::move(secrets.nonce),
        std::move(secrets.exporter));
    return commit_schedule(state, key, generation, std::move(candidate));
}

HpkeContext setup_sender(const std::shared_ptr<ManagerState>& state,
                         HpkeMode mode,
                         const SenderSetupParams& params) {
    auto suite = find_suite(state, params.suite);
    validate_sender_inputs(mode, params);
    const std::uint64_t generation = capture_generation(state, params.generation);

    std::vector<Byte> psk_binding;
    if (params.psk.has_value()) {
        psk_binding = hash_psk(*suite, ByteSpan{*params.psk});
    }

    const std::optional<OpaqueHandle> sender_handle =
        has_auth(mode) ? params.sender_static_key : std::nullopt;
    const auto encapsulation = call_encapsulate(
        *suite,
        params.receiver_public_key,
        sender_handle,
        params.sender_public_key);
    if (!encapsulation.has_value()) {
        fail(ErrorCode::provider_failure, "KEM Encapsulate returned no result");
    }

    SecretLease shared_secret(
        encapsulation->shared_secret, suite->callbacks.kem.destroy_secret);
    if (!shared_secret || encapsulation->enc.empty()) {
        fail(ErrorCode::provider_failure,
             "KEM Encapsulate returned an invalid encapsulation");
    }

    ContextMetadata metadata;
    metadata.suite = params.suite;
    metadata.mode = mode;
    metadata.generation = generation;
    metadata.receiver_side = false;
    metadata.enc = encapsulation->enc;
    metadata.receiver_public_key = params.receiver_public_key;
    metadata.sender_public_key = params.sender_public_key;
    metadata.info = params.info;
    metadata.psk_id = params.psk_id;

    const ContextKey key = make_context_key(metadata, psk_binding);
    const std::optional<ByteSpan> psk =
        params.psk.has_value() ? std::optional<ByteSpan>{ByteSpan{*params.psk}}
                               : std::nullopt;
    const ByteSpan psk_id =
        params.psk_id.has_value() ? ByteSpan{*params.psk_id} : ByteSpan{};
    const auto schedule = schedule_for_context(
        state,
        *suite,
        key,
        generation,
        shared_secret,
        mode,
        psk,
        psk_id,
        params.info);
    return detail::HpkeContextAccess::make(
        std::move(metadata), std::move(suite), state, schedule);
}

HpkeContext setup_receiver(const std::shared_ptr<ManagerState>& state,
                           HpkeMode mode,
                           const ReceiverSetupParams& params) {
    auto suite = find_suite(state, params.suite);
    validate_receiver_inputs(mode, params);
    const std::uint64_t generation = capture_generation(state, params.generation);

    std::vector<Byte> psk_binding;
    if (params.psk.has_value()) {
        psk_binding = hash_psk(*suite, ByteSpan{*params.psk});
    }

    const Decapsulation decapsulation = call_decapsulate(
        *suite, params.enc, params.receiver_private_key, params.sender_public_key);
    SecretLease shared_secret(
        decapsulation.shared_secret, suite->callbacks.kem.destroy_secret);

    if (decapsulation.status == KemStatus::low_order_point) {
        fail(ErrorCode::low_order_point,
             "KEM rejected a low-order public point");
    }
    if (decapsulation.status != KemStatus::ok || !shared_secret) {
        fail(ErrorCode::provider_failure,
             "KEM Decapsulate returned no valid shared secret");
    }

    ContextMetadata metadata;
    metadata.suite = params.suite;
    metadata.mode = mode;
    metadata.generation = generation;
    metadata.receiver_side = true;
    metadata.enc = params.enc;
    metadata.receiver_public_key = params.receiver_public_key;
    metadata.sender_public_key = params.sender_public_key;
    metadata.info = params.info;
    metadata.psk_id = params.psk_id;

    const ContextKey key = make_context_key(metadata, psk_binding);
    const std::optional<ByteSpan> psk =
        params.psk.has_value() ? std::optional<ByteSpan>{ByteSpan{*params.psk}}
                               : std::nullopt;
    const ByteSpan psk_id =
        params.psk_id.has_value() ? ByteSpan{*params.psk_id} : ByteSpan{};
    const auto schedule = schedule_for_context(
        state,
        *suite,
        key,
        generation,
        shared_secret,
        mode,
        psk,
        psk_id,
        params.info);
    return detail::HpkeContextAccess::make(
        std::move(metadata), std::move(suite), state, schedule);
}

enum class HeldSecret {
    key,
    nonce,
};

SecureBytes read_held_secret(const HpkeContext::Impl& impl, HeldSecret held) {
    auto& state = *impl.state;
    auto& schedule = *impl.schedule;

    std::shared_lock state_lock(state.mutex);
    if (state.closed || state.generation != impl.metadata.generation) {
        state_lock.unlock();
        schedule.revoke();
        fail(ErrorCode::stale_generation,
             "context belongs to an old handshake generation");
    }

    std::lock_guard schedule_lock(schedule.mutex);
    if (schedule.revoked) {
        fail(ErrorCode::stale_generation, "context secret has been revoked");
    }

    const SecretLease& lease =
        held == HeldSecret::key ? schedule.key : schedule.nonce;
    if (!lease) {
        fail(ErrorCode::provider_failure, "context secret is unavailable");
    }

    const std::size_t length = held == HeldSecret::key
                                   ? impl.suite->callbacks.aead.key_size
                                   : impl.suite->callbacks.aead.nonce_size;
    return read_secret_bytes(*impl.suite, lease.get(), length);
}

void validate_suite_callbacks(const SuiteCallbacks& callbacks) {
    if (!callbacks.kem.encapsulate || !callbacks.kem.decapsulate ||
        !callbacks.kem.destroy_secret) {
        fail(ErrorCode::missing_callback,
             "KEM encapsulate, decapsulate, and destroy callbacks are required");
    }
    if (!callbacks.kdf.hash || !callbacks.kdf.extract ||
        !callbacks.kdf.expand || !callbacks.kdf.read ||
        !callbacks.kdf.destroy_secret) {
        fail(ErrorCode::missing_callback,
             "KDF hash, extract, expand, read, and destroy callbacks are required");
    }
    if (callbacks.kdf.hash_size == 0 ||
        callbacks.kdf.hash_size > kMaxExporterContextLength) {
        fail(ErrorCode::invalid_argument, "KDF hash size is out of range");
    }
    if (callbacks.aead.key_size == 0 ||
        callbacks.aead.key_size > kMaxExportLength ||
        callbacks.aead.nonce_size == 0 ||
        callbacks.aead.nonce_size > kMaxExportLength) {
        fail(ErrorCode::invalid_argument, "AEAD key or nonce size is out of range");
    }
}

}  // namespace

SecureBytes::SecureBytes(std::size_t size) : bytes_(size) {}

SecureBytes::SecureBytes(std::vector<Byte> bytes) : bytes_(std::move(bytes)) {}

SecureBytes::~SecureBytes() { clear(); }

SecureBytes::SecureBytes(SecureBytes&& other) noexcept
    : bytes_(std::move(other.bytes_)) {
    other.clear();
}

SecureBytes& SecureBytes::operator=(SecureBytes&& other) noexcept {
    if (this != &other) {
        clear();
        bytes_ = std::move(other.bytes_);
        other.clear();
    }
    return *this;
}

std::vector<Byte> SecureBytes::copy() const {
    return bytes_;
}

void SecureBytes::clear() noexcept {
    secure_wipe(bytes_);
    bytes_.clear();
}

std::array<Byte, 10> SuiteId::serialized() const noexcept {
    return serialize_suite_id(*this);
}

std::array<Byte, 10> serialize_suite_id(const SuiteId& id) noexcept {
    std::array<Byte, 10> result{};
    result[0] = 'H';
    result[1] = 'P';
    result[2] = 'K';
    result[3] = 'E';
    result[4] = static_cast<Byte>((id.kem_id >> 8U) & 0xffU);
    result[5] = static_cast<Byte>(id.kem_id & 0xffU);
    result[6] = static_cast<Byte>((id.kdf_id >> 8U) & 0xffU);
    result[7] = static_cast<Byte>(id.kdf_id & 0xffU);
    result[8] = static_cast<Byte>((id.aead_id >> 8U) & 0xffU);
    result[9] = static_cast<Byte>(id.aead_id & 0xffU);
    return result;
}

KdfExtractInput KdfExtractInput::from_bytes(ByteSpan value) noexcept {
    KdfExtractInput input;
    input.prefix = ByteSpan{};
    input.bytes = value;
    input.secret = kInvalidHandle;
    return input;
}

KdfExtractInput KdfExtractInput::from_secret(ByteSpan prefix,
                                             OpaqueHandle value) noexcept {
    KdfExtractInput input;
    input.prefix = prefix;
    input.bytes = ByteSpan{};
    input.secret = value;
    return input;
}

HpkeContext::HpkeContext(std::shared_ptr<const Impl> impl) noexcept
    : impl_(std::move(impl)) {}

HpkeContext::operator bool() const noexcept {
    if (!impl_ || !impl_->schedule) {
        return false;
    }
    try {
        auto& state = *impl_->state;
        auto& schedule = *impl_->schedule;
        std::shared_lock state_lock(state.mutex);
        if (state.closed || state.generation != impl_->metadata.generation) {
            return false;
        }
        std::lock_guard schedule_lock(schedule.mutex);
        return !schedule.revoked && static_cast<bool>(schedule.exporter);
    } catch (...) {
        return false;
    }
}

const ContextMetadata& HpkeContext::metadata() const {
    if (!impl_) {
        fail(ErrorCode::invalid_argument, "empty HPKE context");
    }
    return impl_->metadata;
}

SecureBytes HpkeContext::export_secret(std::string_view label,
                                       ByteSpan exporter_context,
                                       std::size_t length) const {
    if (!impl_ || !impl_->schedule) {
        fail(ErrorCode::invalid_argument, "empty HPKE context");
    }
    if (label.size() > kMaxLabelLength) {
        fail(ErrorCode::length_limit, "exporter label is too long");
    }
    if (!label_in_namespace(label, impl_->state->exporter_namespace)) {
        fail(ErrorCode::namespace_violation,
             "exporter label is outside the configured namespace");
    }
    if (exporter_context.size() > kMaxExporterContextLength) {
        fail(ErrorCode::length_limit, "exporter context is too long");
    }
    if (length == 0 || length > kMaxExportLength) {
        fail(ErrorCode::length_limit, "export length must be in [1, 65535]");
    }

    auto& state = *impl_->state;
    auto& schedule = *impl_->schedule;
    std::shared_lock state_lock(state.mutex);
    if (state.closed || state.generation != impl_->metadata.generation) {
        state_lock.unlock();
        schedule.revoke();
        fail(ErrorCode::stale_generation,
             "context belongs to an old handshake generation");
    }

    std::lock_guard schedule_lock(schedule.mutex);
    if (schedule.revoked) {
        fail(ErrorCode::stale_generation, "context secret has been revoked");
    }
    if (!schedule.exporter) {
        fail(ErrorCode::provider_failure, "exporter secret is unavailable");
    }

    const std::vector<Byte> context_bytes(
        exporter_context.begin(), exporter_context.end());
    SecretLease expanded = labeled_expand(
        *impl_->suite,
        schedule.exporter.get(),
        as_bytes(label),
        context_bytes,
        length);
    return read_secret_bytes(*impl_->suite, expanded.get(), length);
}

SecureBytes HpkeContext::aead_key() const {
    if (!impl_ || !impl_->schedule) {
        fail(ErrorCode::invalid_argument, "empty HPKE context");
    }
    return read_held_secret(*impl_, HeldSecret::key);
}

SecureBytes HpkeContext::base_nonce() const {
    if (!impl_ || !impl_->schedule) {
        fail(ErrorCode::invalid_argument, "empty HPKE context");
    }
    return read_held_secret(*impl_, HeldSecret::nonce);
}

HpkeContextManager::HpkeContextManager(std::string exporter_namespace,
                                       std::size_t max_cache_entries)
    : impl_(std::make_shared<Impl>()) {
    impl_->state = std::make_shared<ManagerState>();
    impl_->state->exporter_namespace =
        normalize_namespace(std::move(exporter_namespace));
    impl_->state->max_cache_entries = max_cache_entries;
}

HpkeContextManager::~HpkeContextManager() {
    if (impl_) {
        close_state(impl_->state);
    }
}

HpkeContextManager::HpkeContextManager(HpkeContextManager&&) noexcept = default;

HpkeContextManager& HpkeContextManager::operator=(HpkeContextManager&& other) noexcept {
    if (this != &other) {
        if (impl_) {
            close_state(impl_->state);
        }
        impl_ = std::move(other.impl_);
    }
    return *this;
}

void HpkeContextManager::register_suite(SuiteId id, SuiteCallbacks callbacks) {
    if (!impl_) {
        fail(ErrorCode::invalid_argument, "empty HPKE context manager");
    }
    validate_suite_callbacks(callbacks);

    auto runtime = std::make_shared<SuiteRuntime>();
    runtime->id = id;
    runtime->serialized_id = serialize_suite_id(id);
    runtime->callbacks = std::move(callbacks);

    std::unique_lock lock(impl_->state->mutex);
    if (impl_->state->closed) {
        fail(ErrorCode::closed, "HPKE context manager is closed");
    }
    if (impl_->state->suites.contains(id)) {
        fail(ErrorCode::duplicate_suite, "suite is already registered");
    }
    impl_->state->suites.emplace(id, std::move(runtime));
}

HpkeContext HpkeContextManager::setup_base(const SenderSetupParams& params) {
    if (!impl_) {
        fail(ErrorCode::invalid_argument, "empty HPKE context manager");
    }
    return setup_sender(impl_->state, HpkeMode::base, params);
}

HpkeContext HpkeContextManager::setup_psk(const SenderSetupParams& params) {
    if (!impl_) {
        fail(ErrorCode::invalid_argument, "empty HPKE context manager");
    }
    return setup_sender(impl_->state, HpkeMode::psk, params);
}

HpkeContext HpkeContextManager::setup_auth(const SenderSetupParams& params) {
    if (!impl_) {
        fail(ErrorCode::invalid_argument, "empty HPKE context manager");
    }
    return setup_sender(impl_->state, HpkeMode::auth, params);
}

HpkeContext HpkeContextManager::setup_auth_psk(const SenderSetupParams& params) {
    if (!impl_) {
        fail(ErrorCode::invalid_argument, "empty HPKE context manager");
    }
    return setup_sender(impl_->state, HpkeMode::auth_psk, params);
}

HpkeContext HpkeContextManager::setup_base_receiver(
    const ReceiverSetupParams& params) {
    if (!impl_) {
        fail(ErrorCode::invalid_argument, "empty HPKE context manager");
    }
    return setup_receiver(impl_->state, HpkeMode::base, params);
}

HpkeContext HpkeContextManager::setup_psk_receiver(
    const ReceiverSetupParams& params) {
    if (!impl_) {
        fail(ErrorCode::invalid_argument, "empty HPKE context manager");
    }
    return setup_receiver(impl_->state, HpkeMode::psk, params);
}

HpkeContext HpkeContextManager::setup_auth_receiver(
    const ReceiverSetupParams& params) {
    if (!impl_) {
        fail(ErrorCode::invalid_argument, "empty HPKE context manager");
    }
    return setup_receiver(impl_->state, HpkeMode::auth, params);
}

HpkeContext HpkeContextManager::setup_auth_psk_receiver(
    const ReceiverSetupParams& params) {
    if (!impl_) {
        fail(ErrorCode::invalid_argument, "empty HPKE context manager");
    }
    return setup_receiver(impl_->state, HpkeMode::auth_psk, params);
}

std::string HpkeContextManager::make_label(std::string_view leaf) const {
    if (!impl_) {
        fail(ErrorCode::invalid_argument, "empty HPKE context manager");
    }
    if (leaf.empty()) {
        fail(ErrorCode::invalid_argument, "exporter label leaf must not be empty");
    }
    std::string label = impl_->state->exporter_namespace;
    label.push_back('.');
    label.append(leaf);
    if (label.size() > kMaxLabelLength) {
        fail(ErrorCode::length_limit, "exporter label is too long");
    }
    return label;
}

std::uint64_t HpkeContextManager::current_generation() const {
    if (!impl_) {
        fail(ErrorCode::invalid_argument, "empty HPKE context manager");
    }
    std::shared_lock lock(impl_->state->mutex);
    if (impl_->state->closed) {
        fail(ErrorCode::closed, "HPKE context manager is closed");
    }
    return impl_->state->generation;
}

std::uint64_t HpkeContextManager::rotate_receiver_key() {
    if (!impl_) {
        fail(ErrorCode::invalid_argument, "empty HPKE context manager");
    }

    std::vector<std::shared_ptr<CachedSchedule>> schedules;
    std::uint64_t generation = 0;
    {
        std::unique_lock lock(impl_->state->mutex);
        if (impl_->state->closed) {
            fail(ErrorCode::closed, "HPKE context manager is closed");
        }
        ++impl_->state->generation;
        if (impl_->state->generation == 0) {
            impl_->state->generation = 1;
        }
        generation = impl_->state->generation;
        schedules = collect_schedules(*impl_->state);
    }
    revoke_all(schedules);
    return generation;
}

Error::Error(ErrorCode code, std::string message)
    : std::runtime_error(std::move(message)), code_(code) {}

}  // namespace netsec::hpke
