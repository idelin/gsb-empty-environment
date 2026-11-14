#include "netsec.hpp"

#include <algorithm>
#include <atomic>
#include <chrono>
#include <condition_variable>
#include <cstdint>
#include <functional>
#include <iostream>
#include <mutex>
#include <optional>
#include <span>
#include <string>
#include <string_view>
#include <thread>
#include <unordered_map>
#include <utility>
#include <vector>

namespace {

using namespace netsec::hpke;
using Bytes = std::vector<Byte>;

int g_failures = 0;

ByteSpan as_bytes(std::string_view text) {
    return ByteSpan{reinterpret_cast<const Byte*>(text.data()), text.size()};
}

void check(bool condition, std::string_view message) {
    if (!condition) {
        std::cerr << "FAIL: " << message << '\n';
        ++g_failures;
    }
}

Bytes bytes(std::string_view text) {
    return Bytes(text.begin(), text.end());
}

Bytes digest(ByteSpan input, std::size_t length = 32) {
    std::uint64_t first = 0xcbf29ce484222325ULL;
    std::uint64_t second = 0x84222325cbf29ce4ULL;
    for (const Byte value : input) {
        first ^= value;
        first *= 0x100000001b3ULL;
        second ^= static_cast<Byte>(value + 0x5dU);
        second *= 0x9e3779b185ebca87ULL;
        second ^= second >> 29U;
    }

    Bytes output(length);
    for (std::size_t i = 0; i < length; ++i) {
        first ^= first << 13U;
        first ^= first >> 7U;
        first ^= first << 17U;
        second += 0x9e3779b97f4a7c15ULL + i;
        second ^= second >> 30U;
        second *= 0xbf58476d1ce4e5b9ULL;
        second ^= second >> 27U;
        output[i] = static_cast<Byte>((first ^ second) & 0xffU);
    }
    return output;
}

Bytes expand_bytes(ByteSpan prk, ByteSpan info, std::size_t length) {
    Bytes material;
    material.reserve(prk.size() + info.size() + sizeof(length));
    material.insert(material.end(), prk.begin(), prk.end());
    material.insert(material.end(), info.begin(), info.end());
    for (std::size_t i = 0; i < sizeof(length); ++i) {
        material.push_back(static_cast<Byte>((length >> (8U * i)) & 0xffU));
    }
    return digest(material, length);
}

void append(Bytes& output, ByteSpan input) {
    output.insert(output.end(), input.begin(), input.end());
}

void append(Bytes& output, std::string_view input) {
    output.insert(output.end(), input.begin(), input.end());
}

bool equal(ByteSpan lhs, ByteSpan rhs) {
    return lhs.size() == rhs.size() && std::equal(lhs.begin(), lhs.end(), rhs.begin());
}

void expect_error(const std::function<void()>& operation,
                  ErrorCode expected,
                  std::string_view message) {
    try {
        operation();
        check(false, message);
    } catch (const Error& error) {
        check(error.code() == expected, message);
    } catch (...) {
        check(false, message);
    }
}

class MockKem {
public:
    std::optional<Encapsulation> encapsulate(ByteSpan recipient_public_key,
                                              std::optional<OpaqueHandle> sender_static_key,
                                              ByteSpan sender_public_key) {
        {
            std::unique_lock lock(block_mutex_);
            if (block_encapsulation_) {
                entered_.store(true);
                block_cv_.notify_all();
                block_cv_.wait(lock, [this] { return release_.load(); });
            }
        }

        if (fail_encapsulation_.load()) {
            return std::nullopt;
        }

        Bytes material;
        append(material, recipient_public_key);
        append(material, sender_public_key);
        material.push_back(sender_static_key.has_value() ? 1 : 0);
        const Bytes secret = digest(material);

        std::lock_guard lock(mutex_);
        const OpaqueHandle handle = allocate(secret);
        Encapsulation result;
        result.enc = Bytes(recipient_public_key.begin(), recipient_public_key.end());
        result.shared_secret = handle;
        return result;
    }

    Decapsulation decapsulate(ByteSpan enc,
                              OpaqueHandle,
                              ByteSpan sender_public_key) {
        std::lock_guard lock(mutex_);
        if (low_order_.load() || equal(enc, as_bytes("low-order"))) {
            const OpaqueHandle handle = allocate(bytes("low-order-secret"));
            return Decapsulation{KemStatus::low_order_point, handle};
        }
        if (fail_decapsulation_.load()) {
            const OpaqueHandle handle = allocate(bytes("failed-secret"));
            return Decapsulation{KemStatus::failure, handle};
        }

        Bytes material;
        append(material, enc);
        append(material, sender_public_key);
        material.push_back(sender_public_key.empty() ? 0 : 1);
        return Decapsulation{KemStatus::ok, allocate(digest(material))};
    }

    void destroy(OpaqueHandle handle) {
        std::lock_guard lock(mutex_);
        if (secrets_.erase(handle) != 0) {
            ++destroyed_;
        }
    }

    [[nodiscard]] std::optional<Bytes> secret_bytes(OpaqueHandle handle) {
        std::lock_guard lock(mutex_);
        const auto it = secrets_.find(handle);
        if (it == secrets_.end()) {
            return std::nullopt;
        }
        return it->second;
    }

    [[nodiscard]] std::size_t live_count() const {
        std::lock_guard lock(mutex_);
        return secrets_.size();
    }

    [[nodiscard]] int destroyed_count() const {
        std::lock_guard lock(mutex_);
        return destroyed_;
    }

    void set_fail_encapsulation(bool value) {
        fail_encapsulation_.store(value);
    }

    void set_fail_decapsulation(bool value) {
        fail_decapsulation_.store(value);
    }

    void set_low_order(bool value) { low_order_.store(value); }

    void arm_encapsulation_block() {
        std::lock_guard lock(block_mutex_);
        block_encapsulation_ = true;
        entered_.store(false);
        release_.store(false);
    }

    bool wait_for_encapsulation_entry() {
        std::unique_lock lock(block_mutex_);
        return block_cv_.wait_for(lock, std::chrono::seconds(2), [this] {
            return entered_.load();
        });
    }

    void release_encapsulation() {
        {
            std::lock_guard lock(block_mutex_);
            block_encapsulation_ = false;
            release_.store(true);
        }
        block_cv_.notify_all();
    }

private:
    OpaqueHandle allocate(Bytes secret) {
        const OpaqueHandle handle = next_handle_++;
        secrets_.emplace(handle, std::move(secret));
        return handle;
    }

    mutable std::mutex mutex_;
    std::unordered_map<OpaqueHandle, Bytes> secrets_;
    OpaqueHandle next_handle_ = 1;
    int destroyed_ = 0;
    std::atomic<bool> fail_encapsulation_{false};
    std::atomic<bool> fail_decapsulation_{false};
    std::atomic<bool> low_order_{false};

    std::mutex block_mutex_;
    std::condition_variable block_cv_;
    bool block_encapsulation_ = false;
    std::atomic<bool> entered_{false};
    std::atomic<bool> release_{false};
};

class MockKdf {
public:
    struct ExpandRecord {
        Bytes info;
        std::size_t length = 0;
    };

    explicit MockKdf(MockKem& kem) : kem_(kem) {}

    std::vector<Byte> hash(ByteSpan input) {
        std::lock_guard lock(mutex_);
        return digest(input, hash_size_);
    }

    OpaqueHandle extract(ByteSpan salt, const KdfExtractInput& input) {
        if (fail_extracts_.load() > 0) {
            --fail_extracts_;
            return kInvalidHandle;
        }

        Bytes material;
        append(material, salt);
        material.push_back(0x01);
        append(material, input.prefix);
        material.push_back(0x02);
        if (input.uses_secret()) {
            const auto secret = kem_.secret_bytes(input.secret);
            if (!secret.has_value()) {
                return kInvalidHandle;
            }
            append(material, *secret);
        } else {
            append(material, input.bytes);
        }

        std::lock_guard lock(mutex_);
        return allocate(digest(material, hash_size_));
    }

    OpaqueHandle expand(OpaqueHandle prk, ByteSpan info, std::size_t length) {
        std::lock_guard lock(mutex_);
        if (fail_expands_.load() > 0) {
            --fail_expands_;
            return kInvalidHandle;
        }
        const auto it = secrets_.find(prk);
        if (it == secrets_.end()) {
            return kInvalidHandle;
        }
        expands_.push_back(ExpandRecord{Bytes(info.begin(), info.end()), length});
        return allocate(expand_bytes(it->second, info, length));
    }

    bool read(OpaqueHandle secret, MutableByteSpan output) {
        std::lock_guard lock(mutex_);
        const auto it = secrets_.find(secret);
        if (it == secrets_.end() || it->second.size() < output.size()) {
            return false;
        }
        std::copy_n(it->second.begin(), output.size(), output.begin());
        return true;
    }

    void destroy(OpaqueHandle handle) {
        std::lock_guard lock(mutex_);
        if (secrets_.erase(handle) != 0) {
            ++destroyed_;
        }
    }

    [[nodiscard]] std::size_t live_count() const {
        std::lock_guard lock(mutex_);
        return secrets_.size();
    }

    [[nodiscard]] int destroyed_count() const {
        std::lock_guard lock(mutex_);
        return destroyed_;
    }

    [[nodiscard]] std::vector<ExpandRecord> expands() const {
        std::lock_guard lock(mutex_);
        return expands_;
    }

    void fail_next_extracts(int count) { fail_extracts_.store(count); }
    void fail_next_expands(int count) { fail_expands_.store(count); }

private:
    OpaqueHandle allocate(Bytes value) {
        const OpaqueHandle handle = next_handle_++;
        secrets_.emplace(handle, std::move(value));
        return handle;
    }

    MockKem& kem_;
    mutable std::mutex mutex_;
    std::unordered_map<OpaqueHandle, Bytes> secrets_;
    std::vector<ExpandRecord> expands_;
    OpaqueHandle next_handle_ = 1;
    int destroyed_ = 0;
    std::size_t hash_size_ = 32;
    std::atomic<int> fail_extracts_{0};
    std::atomic<int> fail_expands_{0};
};

SuiteCallbacks callbacks_for(MockKem& kem, MockKdf& kdf) {
    SuiteCallbacks callbacks;
    callbacks.kem.encapsulate =
        [&kem](ByteSpan recipient_public_key,
               std::optional<OpaqueHandle> sender_static_key,
               ByteSpan sender_public_key) {
            return kem.encapsulate(
                recipient_public_key, sender_static_key, sender_public_key);
        };
    callbacks.kem.decapsulate =
        [&kem](ByteSpan enc,
               OpaqueHandle recipient_private_key,
               ByteSpan sender_public_key) {
            return kem.decapsulate(enc, recipient_private_key, sender_public_key);
        };
    callbacks.kem.destroy_secret = [&kem](OpaqueHandle handle) { kem.destroy(handle); };
    callbacks.kdf.hash = [&kdf](ByteSpan input) { return kdf.hash(input); };
    callbacks.kdf.extract =
        [&kdf](ByteSpan salt, const KdfExtractInput& input) {
            return kdf.extract(salt, input);
        };
    callbacks.kdf.expand =
        [&kdf](OpaqueHandle prk, ByteSpan info, std::size_t length) {
            return kdf.expand(prk, info, length);
        };
    callbacks.kdf.read =
        [&kdf](OpaqueHandle secret, MutableByteSpan output) {
            return kdf.read(secret, output);
        };
    callbacks.kdf.destroy_secret =
        [&kdf](OpaqueHandle handle) { kdf.destroy(handle); };
    callbacks.kdf.hash_size = 32;
    callbacks.aead.key_size = 16;
    callbacks.aead.nonce_size = 12;
    return callbacks;
}

SenderSetupParams base_sender_params(SuiteId suite) {
    SenderSetupParams params;
    params.suite = suite;
    params.receiver_public_key = bytes("recipient-public-key");
    params.info = bytes("message-host-info");
    return params;
}

ReceiverSetupParams base_receiver_params(SuiteId suite, const HpkeContext& context) {
    ReceiverSetupParams params;
    params.suite = suite;
    params.enc = context.metadata().enc;
    params.receiver_public_key = context.metadata().receiver_public_key;
    params.receiver_private_key = 0x5151;
    params.info = context.metadata().info;
    return params;
}

void add_psk(SenderSetupParams& params) {
    params.psk = bytes("shared-preshared-key");
    params.psk_id = bytes("message-host-psk-id");
}

void add_psk(ReceiverSetupParams& params) {
    params.psk = bytes("shared-preshared-key");
    params.psk_id = bytes("message-host-psk-id");
}

void add_auth(SenderSetupParams& params) {
    params.sender_static_key = 0x5151;
    params.sender_public_key = bytes("sender-public-key");
}

void add_auth(ReceiverSetupParams& params) {
    params.sender_public_key = bytes("sender-public-key");
}

void test_all_modes_and_receiver_exports() {
    MockKem kem;
    MockKdf kdf(kem);
    const SuiteId suite{0x0020, 0x0001, 0x0001};

    HpkeContextManager manager("message-host");
    manager.register_suite(suite, callbacks_for(kem, kdf));

    auto base = base_sender_params(suite);
    auto psk = base;
    add_psk(psk);
    auto auth = base;
    add_auth(auth);
    auto auth_psk = psk;
    add_auth(auth_psk);

    HpkeContext base_context = manager.setup_base(base);
    HpkeContext psk_context = manager.setup_psk(psk);
    HpkeContext auth_context = manager.setup_auth(auth);
    HpkeContext auth_psk_context = manager.setup_auth_psk(auth_psk);

    const std::string label = manager.make_label("stream");
    const Bytes exporter_context = bytes("epoch-1");
    const auto base_key = base_context.export_secret(label, exporter_context, 32).copy();
    const auto psk_key = psk_context.export_secret(label, exporter_context, 32).copy();
    const auto auth_key = auth_context.export_secret(label, exporter_context, 32).copy();
    const auto auth_psk_key =
        auth_psk_context.export_secret(label, exporter_context, 32).copy();

    check(!base_key.empty(), "Base mode exports a key");
    check(base_key != psk_key, "Base and PSK schedules are distinct");
    check(base_key != auth_key, "Base and Auth schedules are distinct");
    check(base_key != auth_psk_key, "Base and AuthPSK schedules are distinct");
    check(psk_key != auth_key, "PSK and Auth schedules are distinct");
    check(auth_key != auth_psk_key, "Auth and AuthPSK schedules are distinct");

    check(base_context.metadata().mode == HpkeMode::base, "Base mode is immutable metadata");
    check(auth_context.metadata().mode == HpkeMode::auth, "Auth mode is immutable metadata");
    check(base_context.metadata().generation == manager.current_generation(),
          "Context binds the current generation");
    check(base_context.metadata().suite == suite, "Context binds the full suite");

    auto receiver_base = base_receiver_params(suite, base_context);
    auto receiver_psk = base_receiver_params(suite, psk_context);
    add_psk(receiver_psk);
    auto receiver_auth = base_receiver_params(suite, auth_context);
    add_auth(receiver_auth);
    auto receiver_auth_psk = base_receiver_params(suite, auth_psk_context);
    add_auth(receiver_auth_psk);
    add_psk(receiver_auth_psk);

    check(manager.setup_base_receiver(receiver_base)
              .export_secret(label, exporter_context, 32)
              .copy() == base_key,
          "Receiver Base setup reproduces the sender exporter");
    check(manager.setup_psk_receiver(receiver_psk)
              .export_secret(label, exporter_context, 32)
              .copy() == psk_key,
          "Receiver PSK setup reproduces the sender exporter");
    check(manager.setup_auth_receiver(receiver_auth)
              .export_secret(label, exporter_context, 32)
              .copy() == auth_key,
          "Receiver Auth setup reproduces the sender exporter");
    check(manager.setup_auth_psk_receiver(receiver_auth_psk)
              .export_secret(label, exporter_context, 32)
              .copy() == auth_psk_key,
          "Receiver AuthPSK setup reproduces the sender exporter");
}

void test_psk_and_mode_validation() {
    MockKem kem;
    MockKdf kdf(kem);
    const SuiteId suite{0x0020, 0x0001, 0x0001};

    HpkeContextManager manager("message-host");
    manager.register_suite(suite, callbacks_for(kem, kdf));

    auto params = base_sender_params(suite);
    params.psk = Bytes{};
    params.psk_id = bytes("id");
    expect_error([&] { (void)manager.setup_psk(params); }, ErrorCode::empty_psk,
                 "Empty PSK is rejected");
    check(kem.live_count() == 0, "Empty PSK fails before KEM allocation");

    params.psk = bytes("psk");
    params.psk_id.reset();
    expect_error([&] { (void)manager.setup_psk(params); }, ErrorCode::missing_psk_id,
                 "PSK without psk_id is rejected");

    params.psk.reset();
    params.psk_id = bytes("id");
    expect_error([&] { (void)manager.setup_psk(params); }, ErrorCode::missing_psk,
                 "psk_id without PSK is rejected");

    auto base = base_sender_params(suite);
    base.psk = bytes("unexpected");
    base.psk_id = bytes("unexpected-id");
    expect_error([&] { (void)manager.setup_base(base); }, ErrorCode::unexpected_psk,
                 "Base mode rejects PSK inputs");

    auto auth = base_sender_params(suite);
    auth.psk = bytes("unexpected");
    auth.psk_id = bytes("unexpected-id");
    auth.sender_static_key = 0x5151;
    auth.sender_public_key = bytes("sender-public-key");
    expect_error([&] { (void)manager.setup_auth(auth); }, ErrorCode::unexpected_psk,
                 "Auth mode rejects PSK inputs");

    auto base_with_sender = base_sender_params(suite);
    base_with_sender.sender_static_key = 0x5151;
    base_with_sender.sender_public_key = bytes("sender-public-key");
    expect_error([&] { (void)manager.setup_base(base_with_sender); },
                 ErrorCode::unexpected_sender_key,
                 "Base mode rejects sender static-key binding");

    auto auth_without_key = base_sender_params(suite);
    auth_without_key.sender_public_key = bytes("sender-public-key");
    expect_error([&] { (void)manager.setup_auth(auth_without_key); },
                 ErrorCode::missing_sender_key,
                 "Auth mode requires the sender static-key handle");

    auto auth_psk_without_psk = base_sender_params(suite);
    add_auth(auth_psk_without_psk);
    expect_error([&] { (void)manager.setup_auth_psk(auth_psk_without_psk); },
                 ErrorCode::missing_psk,
                 "AuthPSK requires the PSK");

    auto first_psk = base_sender_params(suite);
    add_psk(first_psk);
    auto second_psk = first_psk;
    second_psk.psk = bytes("different-preshared-key");
    auto third_psk = first_psk;
    third_psk.psk_id = bytes("different-psk-id");

    const auto first_context = manager.setup_psk(first_psk);
    const auto second_context = manager.setup_psk(second_psk);
    const auto third_context = manager.setup_psk(third_psk);
    const std::string label = manager.make_label("psk-identity");
    const Bytes exporter_context = bytes("same-context");
    const auto first_key = first_context.export_secret(label, exporter_context, 32).copy();
    const auto second_key =
        second_context.export_secret(label, exporter_context, 32).copy();
    const auto third_key = third_context.export_secret(label, exporter_context, 32).copy();
    check(first_key != second_key,
          "Distinct PSK values with the same psk_id never share a cache entry");
    check(first_key != third_key,
          "psk_id is part of PSK cache identity");
}

void test_setup_failure_destroys_temporary_secrets() {
    MockKem kem;
    MockKdf kdf(kem);
    const SuiteId suite{0x0020, 0x0001, 0x0001};

    HpkeContextManager manager("message-host");
    manager.register_suite(suite, callbacks_for(kem, kdf));
    auto params = base_sender_params(suite);

    kdf.fail_next_extracts(1);
    expect_error([&] { (void)manager.setup_base(params); }, ErrorCode::provider_failure,
                 "A failed KDF Extract fails setup");
    check(kem.live_count() == 0, "Failed setup destroys the KEM shared secret");
    check(kem.destroyed_count() >= 1, "KEM destroy callback observed the cleanup");
    check(kdf.live_count() == 0, "Failed setup leaves no KDF handles alive");

    kem.set_fail_encapsulation(true);
    expect_error([&] { (void)manager.setup_base(params); }, ErrorCode::provider_failure,
                 "A failed encapsulation fails setup");
    check(kem.live_count() == 0, "Failed encapsulation leaves no KEM secret");
    kem.set_fail_encapsulation(false);

    kdf.fail_next_expands(1);
    expect_error([&] { (void)manager.setup_base(params); }, ErrorCode::provider_failure,
                 "A failed KDF Expand fails setup");
    check(kem.live_count() == 0,
          "KDF Expand failure still destroys the KEM shared secret");
    check(kdf.live_count() == 0,
          "KDF Expand failure destroys partially derived schedule handles");

    HpkeContext context = manager.setup_base(params);
    check(static_cast<bool>(context), "Manager remains usable after setup failures");
}

void test_low_order_decap_destroys_secret() {
    MockKem kem;
    MockKdf kdf(kem);
    const SuiteId suite{0x0020, 0x0001, 0x0001};

    HpkeContextManager manager("message-host");
    manager.register_suite(suite, callbacks_for(kem, kdf));

    ReceiverSetupParams params;
    params.suite = suite;
    params.enc = bytes("low-order");
    params.receiver_public_key = bytes("recipient-public-key");
    params.receiver_private_key = 0x5151;
    params.info = bytes("message-host-info");

    expect_error([&] { (void)manager.setup_base_receiver(params); },
                 ErrorCode::low_order_point,
                 "Low-order decapsulation is rejected");
    check(kem.live_count() == 0, "Low-order failure destroys the returned secret");
    check(kdf.live_count() == 0, "Low-order failure never enters the key schedule");
}

void test_suite_id_separation_with_colliding_handles() {
    MockKem kem;
    MockKdf kdf(kem);
    const SuiteId suite_a{0x0020, 0x0001, 0x0001};
    const SuiteId suite_b{0x0020, 0x0001, 0x0002};
    const SuiteId suite_c{0x0020, 0x0002, 0x0001};
    const SuiteId suite_d{0x0021, 0x0001, 0x0001};

    HpkeContextManager manager("message-host");
    manager.register_suite(suite_a, callbacks_for(kem, kdf));
    manager.register_suite(suite_b, callbacks_for(kem, kdf));
    manager.register_suite(suite_c, callbacks_for(kem, kdf));
    manager.register_suite(suite_d, callbacks_for(kem, kdf));

    auto context_a = manager.setup_base(base_sender_params(suite_a));
    auto context_b = manager.setup_base(base_sender_params(suite_b));
    auto context_c = manager.setup_base(base_sender_params(suite_c));
    auto context_d = manager.setup_base(base_sender_params(suite_d));

    const std::string label = manager.make_label("suite-isolation");
    const Bytes exporter_context = bytes("same-context");
    const auto key_a = context_a.export_secret(label, exporter_context, 32).copy();
    const auto key_b = context_b.export_secret(label, exporter_context, 32).copy();
    const auto key_c = context_c.export_secret(label, exporter_context, 32).copy();
    const auto key_d = context_d.export_secret(label, exporter_context, 32).copy();

    check(key_a != key_b, "AEAD ID is part of cache and schedule identity");
    check(key_a != key_c, "KDF ID is part of cache and schedule identity");
    check(key_a != key_d, "KEM ID is part of cache and schedule identity");
    check(key_b != key_c && key_b != key_d && key_c != key_d,
          "All four suite identities remain isolated");

    manager.rotate_receiver_key();
    check(kem.live_count() == 0, "Rotation destroys all colliding-handle KEM secrets");
    check(kdf.live_count() == 0, "Rotation destroys all colliding-handle KDF secrets");
}

void test_two_providers_reuse_the_same_opaque_numbers() {
    MockKem kem_a;
    MockKem kem_b;
    MockKdf kdf_a(kem_a);
    MockKdf kdf_b(kem_b);
    const SuiteId suite_a{0x0020, 0x0001, 0x0001};
    const SuiteId suite_b{0x0020, 0x0001, 0x0002};

    HpkeContextManager manager("message-host");
    manager.register_suite(suite_a, callbacks_for(kem_a, kdf_a));
    manager.register_suite(suite_b, callbacks_for(kem_b, kdf_b));

    const auto context_a = manager.setup_base(base_sender_params(suite_a));
    const auto context_b = manager.setup_base(base_sender_params(suite_b));
    const std::string label = manager.make_label("provider-collision");
    const auto key_a =
        context_a.export_secret(label, bytes("context"), 32).copy();
    const auto key_b =
        context_b.export_secret(label, bytes("context"), 32).copy();

    check(key_a != key_b,
          "Two providers with identical opaque IDs keep separate caches");
    check(kdf_a.live_count() == 3 && kdf_b.live_count() == 3,
          "Each provider retains only its own three schedule handles");
    check(kem_a.destroyed_count() >= 1 && kem_b.destroyed_count() >= 1,
          "Each provider saw only its own setup-secret destruction");

    manager.rotate_receiver_key();
    check(kdf_a.live_count() == 0 && kdf_b.live_count() == 0,
          "Rotation uses each suite's own destroy callback");
    check(kdf_a.destroyed_count() >= 3 && kdf_b.destroyed_count() >= 3,
          "Rotation enters both providers' destroy paths");
}

void test_mode_info_and_receiver_identity_separation() {
    MockKem kem;
    MockKdf kdf(kem);
    const SuiteId suite{0x0020, 0x0001, 0x0001};

    HpkeContextManager manager("message-host");
    manager.register_suite(suite, callbacks_for(kem, kdf));

    auto base = base_sender_params(suite);
    auto auth = base;
    add_auth(auth);
    auto other_info = base;
    other_info.info = bytes("other-info");
    auto other_receiver = base;
    other_receiver.receiver_public_key = bytes("different-recipient-key");

    const auto base_context = manager.setup_base(base);
    const auto auth_context = manager.setup_auth(auth);
    const auto info_context = manager.setup_base(other_info);
    const auto receiver_context = manager.setup_base(other_receiver);

    const std::string label = manager.make_label("identity");
    const Bytes exporter_context = bytes("raw-context");
    const auto base_key = base_context.export_secret(label, exporter_context, 32).copy();
    const auto auth_key = auth_context.export_secret(label, exporter_context, 32).copy();
    const auto info_key = info_context.export_secret(label, exporter_context, 32).copy();
    const auto receiver_key =
        receiver_context.export_secret(label, exporter_context, 32).copy();

    check(base_key != auth_key, "Base/Auth confusion does not share a cache entry");
    check(base_key != info_key, "Info differences do not share a cache entry");
    check(base_key != receiver_key,
          "Receiver public-key identity is part of cache identity");
    check(base_context.metadata().info == base.info,
          "Info is copied into immutable context metadata");
    check(auth_context.metadata().sender_public_key == auth.sender_public_key,
          "Sender static public-key identity is bound to Auth context");
}

void test_namespace_length_and_labeled_expand_wire_format() {
    MockKem kem;
    MockKdf kdf(kem);
    const SuiteId suite{0x0020, 0x0001, 0x0001};

    HpkeContextManager manager("message-host");
    manager.register_suite(suite, callbacks_for(kem, kdf));
    const auto context = manager.setup_base(base_sender_params(suite));

    const Bytes exporter_context = bytes("wire-format-context");
    const std::string label = manager.make_label("stream");
    const auto exported = context.export_secret(label, exporter_context, 48).copy();
    check(exported.size() == 48, "Export honors the requested length");

    const auto suite_id = suite.serialized();
    check(suite_id[0] == 'H' && suite_id[1] == 'P' && suite_id[2] == 'K' &&
              suite_id[3] == 'E' && suite_id[4] == 0x00 && suite_id[5] == 0x20 &&
              suite_id[6] == 0x00 && suite_id[7] == 0x01 && suite_id[8] == 0x00 &&
              suite_id[9] == 0x01,
          "suite_id uses the RFC 9180 HPKE/KEM/KDF/AEAD encoding");

    Bytes expected_info;
    append(expected_info, "HPKE-v1");
    expected_info.insert(expected_info.end(), suite_id.begin(), suite_id.end());
    append(expected_info, label);
    expected_info.push_back(static_cast<Byte>((exporter_context.size() >> 8U) & 0xffU));
    expected_info.push_back(static_cast<Byte>(exporter_context.size() & 0xffU));
    append(expected_info, exporter_context);

    const auto records = kdf.expands();
    const bool found = std::any_of(records.begin(), records.end(), [&](const auto& record) {
        return record.length == 48 && record.info == expected_info;
    });
    check(found, "Export uses RFC 9180 labeledExpand(scoped label, raw context)");

    const auto other_label_key =
        context.export_secret(manager.make_label("control"), exporter_context, 48).copy();
    check(exported != other_label_key,
          "Different application labels produce different exporter keys");

    expect_error([&] {
        (void)context.export_secret("other-host.stream", exporter_context, 16);
    }, ErrorCode::namespace_violation,
                 "Labels outside the caller namespace are rejected");

    expect_error([&] {
        (void)context.export_secret(manager.make_label("stream"), exporter_context, 0);
    }, ErrorCode::length_limit, "Zero-length export is rejected");

    expect_error([&] {
        (void)context.export_secret(
            manager.make_label("stream"), exporter_context, kMaxExportLength + 1);
    }, ErrorCode::length_limit, "Oversized export is rejected");

    expect_error([&] {
        const Bytes oversized_context(kMaxExporterContextLength + 1);
        (void)context.export_secret(
            manager.make_label("stream"), oversized_context, 16);
    }, ErrorCode::length_limit, "Oversized exporter context is rejected");
}

void test_concurrent_export() {
    MockKem kem;
    MockKdf kdf(kem);
    const SuiteId suite{0x0020, 0x0001, 0x0001};

    HpkeContextManager manager("message-host");
    manager.register_suite(suite, callbacks_for(kem, kdf));
    const auto context = manager.setup_base(base_sender_params(suite));

    const std::string label = manager.make_label("concurrent");
    const Bytes exporter_context = bytes("shared");
    const Bytes expected = context.export_secret(label, exporter_context, 64).copy();

    std::atomic<int> mismatches{0};
    std::atomic<int> exceptions{0};
    std::vector<std::thread> threads;
    for (int thread = 0; thread < 8; ++thread) {
        threads.emplace_back([&] {
            for (int iteration = 0; iteration < 100; ++iteration) {
                try {
                    if (context.export_secret(label, exporter_context, 64).copy() !=
                        expected) {
                        ++mismatches;
                    }
                } catch (...) {
                    ++exceptions;
                }
            }
        });
    }
    for (auto& thread : threads) {
        thread.join();
    }

    check(mismatches.load() == 0, "Concurrent exports are deterministic");
    check(exceptions.load() == 0, "Concurrent exports do not throw");
}

void test_rotation_invalidates_live_contexts_and_inflight_setup() {
    MockKem kem;
    MockKdf kdf(kem);
    const SuiteId suite{0x0020, 0x0001, 0x0001};

    HpkeContextManager manager("message-host");
    manager.register_suite(suite, callbacks_for(kem, kdf));
    auto context = manager.setup_base(base_sender_params(suite));

    const std::uint64_t old_generation = manager.current_generation();
    const std::uint64_t new_generation = manager.rotate_receiver_key();
    check(new_generation == old_generation + 1, "Rotation advances the generation");
    check(kdf.live_count() == 0, "Rotation destroys cached key-schedule secrets");
    check(!static_cast<bool>(context), "Rotation invalidates the context identity");

    const std::string label = manager.make_label("rotation");
    const Bytes exporter_context = bytes("context");
    expect_error([&] { (void)context.export_secret(label, exporter_context, 32); },
                 ErrorCode::stale_generation,
                 "A context from the old generation cannot export");
    expect_error([&] { (void)context.aead_key(); }, ErrorCode::stale_generation,
                 "A stale context cannot expose its AEAD key");

    kem.arm_encapsulation_block();
    std::atomic<bool> setup_failed{false};
    std::thread setup_thread([&] {
        try {
            auto stale_params = base_sender_params(suite);
            stale_params.generation = manager.current_generation();
            (void)manager.setup_base(stale_params);
        } catch (const Error& error) {
            setup_failed.store(error.code() == ErrorCode::stale_generation);
        } catch (...) {
            setup_failed.store(false);
        }
    });

    check(kem.wait_for_encapsulation_entry(),
          "KEM callback entered the blocked setup");
    manager.rotate_receiver_key();
    kem.release_encapsulation();
    setup_thread.join();

    check(setup_failed.load(),
          "Rotation during setup rejects the stale in-flight operation");
    check(kem.live_count() == 0,
          "Rotation during setup destroys the temporary KEM secret");
    check(kdf.live_count() == 0,
          "Rotation during setup leaves no temporary KDF handles");

    HpkeContext fresh = manager.setup_base(base_sender_params(suite));
    check(static_cast<bool>(fresh), "A new generation can set up a fresh context");
}

void test_rotation_revokes_evicted_contexts() {
    MockKem kem;
    MockKdf kdf(kem);
    const SuiteId suite{0x0020, 0x0001, 0x0001};

    HpkeContextManager manager("message-host", 1);
    manager.register_suite(suite, callbacks_for(kem, kdf));

    auto first_params = base_sender_params(suite);
    first_params.info = bytes("first-info");
    auto second_params = base_sender_params(suite);
    second_params.info = bytes("second-info");

    HpkeContext first = manager.setup_base(first_params);
    HpkeContext second = manager.setup_base(second_params);
    manager.rotate_receiver_key();

    expect_error(
        [&] {
            (void)first.export_secret(
                manager.make_label("evicted"), bytes("context"), 32);
        },
        ErrorCode::stale_generation,
        "Rotation reaches contexts whose cache entry was evicted");
    expect_error(
        [&] {
            (void)second.export_secret(
                manager.make_label("evicted"), bytes("context"), 32);
        },
        ErrorCode::stale_generation,
        "Rotation reaches the remaining cached context");
    check(kdf.live_count() == 0,
          "Rotation destroys evicted and cached schedule secrets");
}

void test_duplicate_suite_rejected() {
    MockKem kem;
    MockKdf kdf(kem);
    const SuiteId suite{0x0020, 0x0001, 0x0001};

    HpkeContextManager manager("message-host");
    manager.register_suite(suite, callbacks_for(kem, kdf));
    expect_error([&] { manager.register_suite(suite, callbacks_for(kem, kdf)); },
                 ErrorCode::duplicate_suite,
                 "A suite cannot be registered twice");
}

}  // namespace

int main() {
    test_all_modes_and_receiver_exports();
    test_psk_and_mode_validation();
    test_setup_failure_destroys_temporary_secrets();
    test_low_order_decap_destroys_secret();
    test_suite_id_separation_with_colliding_handles();
    test_two_providers_reuse_the_same_opaque_numbers();
    test_mode_info_and_receiver_identity_separation();
    test_namespace_length_and_labeled_expand_wire_format();
    test_concurrent_export();
    test_rotation_invalidates_live_contexts_and_inflight_setup();
    test_rotation_revokes_evicted_contexts();
    test_duplicate_suite_rejected();

    if (g_failures != 0) {
        std::cerr << g_failures << " test assertion(s) failed\n";
        return 1;
    }
    std::cout << "All HPKE context tests passed\n";
    return 0;
}
