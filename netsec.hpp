#pragma once

#include <cstddef>
#include <cstdint>
#include <limits>
#include <memory>
#include <optional>
#include <vector>

namespace netsec {

enum class Direction : std::uint8_t {
    send = 0,
    receive = 1,
    Send = send,
    Receive = receive,
};

struct ConnectionId {
    std::uint64_t fd{0};
    std::uint64_t generation{0};

    friend bool operator==(const ConnectionId&, const ConnectionId&) = default;
};

struct KeyMaterial {
    std::vector<std::uint8_t> key;
    std::vector<std::uint8_t> iv;
};

struct TrafficSecret {
    std::uint64_t generation{0};
    KeyMaterial material;

    // TLS 1.3 resets the record sequence when a new traffic key is installed.
    // A caller may override the post-install sequence for other record layers.
    std::optional<std::uint64_t> first_record_sequence;
};

enum class ErrorCode : std::uint8_t {
    none = 0,
    not_open,
    already_open,
    connection_reuse,
    stale_connection,
    frozen,
    update_in_progress,
    invalid_generation,
    invalid_key_material,
    replay,
    sequence_gap,
    sequence_exhausted,
    stale_record,
    driver_read_failed,
    driver_barrier_failed,
    driver_install_failed,
    readback_mismatch,
    install_readback_mismatch,
    stale_barrier,
    stale_install,
    ciphertext_generation_mismatch,
    closed,
    failed,
};

struct Error {
    ErrorCode code{ErrorCode::none};
    const char* message{""};

    explicit operator bool() const noexcept { return code != ErrorCode::none; }
};

inline Error make_error(ErrorCode code, const char* message = "") noexcept {
    return Error{code, message};
}

template <typename T>
class Result {
public:
    Result(T value) : value_(std::move(value)) {}
    Result(Error error) : error_(error) {}

    bool ok() const noexcept { return value_.has_value(); }
    explicit operator bool() const noexcept { return ok(); }

    T& value() & { return *value_; }
    const T& value() const& { return *value_; }
    T&& value() && { return std::move(*value_); }

    T* operator->() { return &*value_; }
    const T* operator->() const { return &*value_; }
    T& operator*() { return *value_; }
    const T& operator*() const { return *value_; }

    Error error() const noexcept { return error_; }

private:
    std::optional<T> value_;
    Error error_{};
};

template <>
class Result<void> {
public:
    Result() = default;
    Result(Error error) : error_(error) {}

    bool ok() const noexcept { return !error_; }
    explicit operator bool() const noexcept { return ok(); }
    Error error() const noexcept { return error_; }

private:
    Error error_{};
};

enum class UpdateSequencePolicy : std::uint8_t {
    // TLS 1.3: the new key starts at record sequence zero.
    reset_to_zero,
    // Non-TLS record layers that retain a connection-wide sequence.
    continue_sequence,
    ResetToZero = reset_to_zero,
    Continue = continue_sequence,
};

struct Options {
    std::uint64_t send_sequence_limit{std::numeric_limits<std::uint64_t>::max()};
    std::uint64_t receive_sequence_limit{std::numeric_limits<std::uint64_t>::max()};
    UpdateSequencePolicy update_sequence_policy{UpdateSequencePolicy::reset_to_zero};
    bool verify_install_readback{true};
};

struct RecordTicket {
    Direction direction{Direction::send};
    ConnectionId connection{};
    std::uint64_t traffic_generation{0};
    std::uint64_t sequence{0};
    std::uint64_t token{0};
};

struct DirectionSnapshot {
    Direction direction{Direction::send};
    std::uint64_t traffic_generation{0};
    std::uint64_t next_sequence{0};
    std::size_t pending_records{0};
    bool frozen{false};
    bool update_pending{false};
    bool sequence_exhausted{false};
};

struct DriverReadback {
    ConnectionId connection{};
    std::uint64_t traffic_generation{0};
    std::uint64_t next_sequence{0};
};

enum class DriverOperation : std::uint8_t {
    ok = 0,
    pending = 1,
    failed = 2,
};

struct InstallRequest {
    ConnectionId connection{};
    Direction direction{Direction::send};
    std::uint64_t traffic_generation{0};
    std::uint64_t next_sequence{0};
    KeyMaterial material;
};

// Backend contract:
//  * read() returns the kernel's current generation and next record sequence.
//  * barrier() completes only after every previously submitted record for that
//    direction has left the kernel/DMA path.
//  * install() atomically installs the key, IV, generation and sequence base.
//    It must not expose an intermediate key/sequence combination.
//  * Pending operations are completed through the coordinator callbacks.
class Driver {
public:
    virtual ~Driver() = default;

    virtual DriverOperation read(const ConnectionId& connection,
                                 Direction direction,
                                 DriverReadback& out) = 0;

    virtual DriverOperation barrier(const ConnectionId& connection,
                                    Direction direction,
                                    std::uint64_t traffic_generation,
                                    std::uint64_t expected_next_sequence,
                                    std::uint64_t& token) = 0;

    virtual DriverOperation install(const InstallRequest& request,
                                    std::uint64_t& token) = 0;

    virtual void abort(const ConnectionId&, Error) noexcept {}
};

// Serializable, event-loop driven rekey coordinator. The caller owns the
// plaintext/ciphertext records; this class owns the key-generation boundary.
class KeyUpdateCoordinator {
public:
    explicit KeyUpdateCoordinator(Driver& driver, Options options = {});
    ~KeyUpdateCoordinator();

    KeyUpdateCoordinator(const KeyUpdateCoordinator&) = delete;
    KeyUpdateCoordinator& operator=(const KeyUpdateCoordinator&) = delete;

    // Starts a fresh logical connection. Reopening the same fd requires a new
    // connection.generation; close() must be called first.
    Result<void> open(const ConnectionId& connection,
                      const TrafficSecret& send_initial,
                      const TrafficSecret& receive_initial);

    void close() noexcept;

    // Allocates the next send sequence and returns the exact generation-bound
    // ticket that must later be passed to complete_record().
    Result<RecordTicket> allocate_send(const ConnectionId& connection);

    // Registers an inbound record at the exact next sequence. The optional
    // generation is the key generation observed by the KTLS backend; supplying
    // it turns cross-generation ciphertext replay into a fatal error.
    Result<RecordTicket> begin_receive(const ConnectionId& connection,
                                       std::uint64_t sequence);
    Result<RecordTicket> begin_receive(const ConnectionId& connection,
                                       std::uint64_t sequence,
                                       std::uint64_t observed_traffic_generation);

    // Asynchronous completion for a previously allocated/registered record.
    Result<void> complete_record(const RecordTicket& ticket);

    // Freezes one direction and schedules gen+1. Any KeyUpdate record itself
    // must be registered before this call. Registers an idempotent duplicate as
    // a fatal protocol error rather than allowing it to advance twice.
    Result<void> begin_key_update(const ConnectionId& connection,
                                  Direction direction,
                                  const TrafficSecret& next);

    // Drives synchronous phases after external state changes.
    Result<void> advance();

    // Driver callbacks for asynchronous barrier/install completions.
    Result<void> on_barrier_complete(const ConnectionId& connection,
                                     Direction direction,
                                     std::uint64_t token,
                                     bool success);
    Result<void> on_install_complete(const ConnectionId& connection,
                                     Direction direction,
                                     std::uint64_t token,
                                     bool success);

    Result<DirectionSnapshot> snapshot(const ConnectionId& connection,
                                       Direction direction) const;

    bool is_open() const noexcept;
    bool is_failed() const noexcept;
    Error last_error() const noexcept;

private:
    struct Impl;
    std::unique_ptr<Impl> impl_;
};

}  // namespace netsec
