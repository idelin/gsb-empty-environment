#include "netsec.hpp"

#include <unordered_map>
#include <utility>

namespace netsec {
namespace {

enum class Phase : std::uint8_t {
    idle,
    wait_records,
    wait_barrier,
    wait_readback,
    wait_install,
    verify_install,
};

struct DirectionState {
    explicit DirectionState(Direction dir, std::uint64_t sequence_limit)
        : direction(dir), limit(sequence_limit) {}

    Direction direction;
    std::uint64_t limit;
    std::uint64_t traffic_generation{0};
    std::uint64_t next_sequence{0};
    bool exhausted{false};
    bool frozen{false};
    bool update_pending{false};
    Phase phase{Phase::idle};
    TrafficSecret next_secret{};
    std::uint64_t next_base{0};
    std::unordered_map<std::uint64_t, RecordTicket> pending;
    std::uint64_t barrier_token{0};
    std::uint64_t install_token{0};
    bool barrier_waiting{false};
    bool install_waiting{false};
};

bool valid_material(const KeyMaterial& material) {
    constexpr std::size_t max_size = 64;
    return !material.key.empty() && material.key.size() <= max_size &&
           !material.iv.empty() && material.iv.size() <= max_size;
}

}  // namespace

struct KeyUpdateCoordinator::Impl {
    enum class Lifecycle : std::uint8_t {
        closed,
        active,
        failed,
    };

    Impl(Driver& backend, Options opts)
        : driver(backend),
          options(opts),
          send(Direction::send, opts.send_sequence_limit),
          receive(Direction::receive, opts.receive_sequence_limit) {}

    Driver& driver;
    Options options;
    Lifecycle lifecycle{Lifecycle::closed};
    ConnectionId connection{};
    Error last_error{};
    DirectionState send;
    DirectionState receive;
    std::uint64_t next_record_token{0};
    std::unordered_map<std::uint64_t, std::uint64_t> last_generation_by_fd;

    DirectionState& state_for(Direction direction) {
        return direction == Direction::send ? send : receive;
    }

    const DirectionState& state_for(Direction direction) const {
        return direction == Direction::send ? send : receive;
    }

    static void reset_direction(DirectionState& state) {
        state.traffic_generation = 0;
        state.next_sequence = 0;
        state.exhausted = false;
        state.frozen = false;
        state.update_pending = false;
        state.phase = Phase::idle;
        state.next_secret = {};
        state.next_base = 0;
        state.pending.clear();
        state.barrier_token = 0;
        state.install_token = 0;
        state.barrier_waiting = false;
        state.install_waiting = false;
    }

    Result<void> check_connection(const ConnectionId& candidate) const {
        if (lifecycle == Lifecycle::failed) {
            return make_error(ErrorCode::failed, last_error.message);
        }
        if (lifecycle != Lifecycle::active) {
            return make_error(ErrorCode::not_open, "connection is not open");
        }
        if (!(candidate == connection)) {
            return make_error(ErrorCode::stale_connection,
                              "operation belongs to another connection generation");
        }
        return Result<void>{};
    }

    Error fail(Error error) {
        if (lifecycle == Lifecycle::failed) {
            return last_error;
        }
        lifecycle = Lifecycle::failed;
        last_error = error;
        send.frozen = true;
        receive.frozen = true;
        driver.abort(connection, error);
        return error;
    }

    static void consume_sequence(DirectionState& state) {
        if (state.next_sequence == state.limit) {
            state.exhausted = true;
        } else {
            ++state.next_sequence;
        }
    }

    std::uint64_t issue_token() {
        ++next_record_token;
        if (next_record_token == 0) {
            ++next_record_token;
        }
        return next_record_token;
    }

    Result<RecordTicket> begin_receive_impl(
        const ConnectionId& candidate, std::uint64_t sequence,
        std::optional<std::uint64_t> observed_generation) {
        const Result<void> usable = check_connection(candidate);
        if (!usable) {
            return usable.error();
        }

        DirectionState& state = receive;
        if (observed_generation &&
            *observed_generation != state.traffic_generation) {
            return fail(make_error(
                ErrorCode::ciphertext_generation_mismatch,
                "ciphertext was protected by a different traffic generation"));
        }
        if (state.frozen) {
            return make_error(ErrorCode::frozen,
                              "receive direction is frozen for a key update");
        }
        if (state.exhausted || state.next_sequence > state.limit) {
            return fail(make_error(ErrorCode::sequence_exhausted,
                                   "receive record sequence would overflow"));
        }
        if (sequence < state.next_sequence) {
            return fail(make_error(
                ErrorCode::replay,
                "inbound record reuses a completed sequence"));
        }
        if (sequence > state.next_sequence) {
            return fail(make_error(ErrorCode::sequence_gap,
                                   "inbound record skipped a sequence"));
        }

        RecordTicket ticket;
        ticket.direction = Direction::receive;
        ticket.connection = candidate;
        ticket.traffic_generation = state.traffic_generation;
        ticket.sequence = sequence;
        ticket.token = issue_token();
        consume_sequence(state);
        state.pending.emplace(ticket.token, ticket);
        return ticket;
    }

    Result<void> advance_direction(DirectionState& state) {
        for (unsigned step = 0; step < 16; ++step) {
            switch (state.phase) {
            case Phase::idle:
            case Phase::wait_barrier:
            case Phase::wait_install:
                return Result<void>{};

            case Phase::wait_records:
                if (!state.pending.empty()) {
                    return Result<void>{};
                } else {
                    std::uint64_t token = 0;
                    const DriverOperation operation = driver.barrier(
                        connection, state.direction, state.traffic_generation,
                        state.next_sequence, token);
                    if (operation == DriverOperation::failed) {
                        return fail(make_error(ErrorCode::driver_barrier_failed,
                                               "completion barrier failed"));
                    }
                    if (operation == DriverOperation::pending) {
                        state.phase = Phase::wait_barrier;
                        state.barrier_token = token;
                        state.barrier_waiting = true;
                        return Result<void>{};
                    }
                    state.phase = Phase::wait_readback;
                }
                break;

            case Phase::wait_readback: {
                DriverReadback readback{};
                if (driver.read(connection, state.direction, readback) !=
                    DriverOperation::ok) {
                    return fail(make_error(ErrorCode::driver_read_failed,
                                           "could not read kernel record state"));
                }
                if (!(readback.connection == connection) ||
                    readback.traffic_generation != state.traffic_generation ||
                    readback.next_sequence != state.next_sequence) {
                    return fail(make_error(
                        ErrorCode::readback_mismatch,
                        "kernel generation/sequence did not match old key boundary"));
                }

                InstallRequest request;
                request.connection = connection;
                request.direction = state.direction;
                request.traffic_generation = state.next_secret.generation;
                request.next_sequence = state.next_base;
                request.material = state.next_secret.material;

                std::uint64_t token = 0;
                const DriverOperation operation = driver.install(request, token);
                if (operation == DriverOperation::failed) {
                    return fail(make_error(ErrorCode::driver_install_failed,
                                           "atomic key installation failed"));
                }
                if (operation == DriverOperation::pending) {
                    state.phase = Phase::wait_install;
                    state.install_token = token;
                    state.install_waiting = true;
                    return Result<void>{};
                }
                state.phase = Phase::verify_install;
                break;
            }

            case Phase::verify_install: {
                if (options.verify_install_readback) {
                    DriverReadback readback{};
                    if (driver.read(connection, state.direction, readback) !=
                        DriverOperation::ok) {
                        return fail(make_error(
                            ErrorCode::driver_read_failed,
                            "could not read installed kernel record state"));
                    }
                    if (!(readback.connection == connection) ||
                        readback.traffic_generation !=
                            state.next_secret.generation ||
                        readback.next_sequence != state.next_base) {
                        return fail(make_error(
                            ErrorCode::install_readback_mismatch,
                            "kernel did not expose the new generation/sequence boundary"));
                    }
                }

                state.traffic_generation = state.next_secret.generation;
                state.next_sequence = state.next_base;
                state.exhausted = false;
                state.frozen = false;
                state.update_pending = false;
                state.phase = Phase::idle;
                state.next_secret = {};
                state.next_base = 0;
                state.barrier_token = 0;
                state.install_token = 0;
                state.barrier_waiting = false;
                state.install_waiting = false;
                return Result<void>{};
            }
            }
        }
        return fail(make_error(ErrorCode::driver_install_failed,
                               "key update state machine made no progress"));
    }
};

KeyUpdateCoordinator::KeyUpdateCoordinator(Driver& driver, Options options)
    : impl_(std::make_unique<Impl>(driver, options)) {}

KeyUpdateCoordinator::~KeyUpdateCoordinator() = default;

Result<void> KeyUpdateCoordinator::open(const ConnectionId& connection,
                                        const TrafficSecret& send_initial,
                                        const TrafficSecret& receive_initial) {
    Impl& impl = *impl_;
    if (impl.lifecycle == Impl::Lifecycle::active) {
        return make_error(ErrorCode::already_open, "connection is already open");
    }
    if (impl.lifecycle == Impl::Lifecycle::failed) {
        return make_error(ErrorCode::failed, impl.last_error.message);
    }
    const auto last_generation =
        impl.last_generation_by_fd.find(connection.fd);
    if (last_generation != impl.last_generation_by_fd.end() &&
        connection.generation <= last_generation->second) {
        return make_error(ErrorCode::connection_reuse,
                          "reused fd requires a monotonic connection generation");
    }
    if (!valid_material(send_initial.material) ||
        !valid_material(receive_initial.material)) {
        return make_error(ErrorCode::invalid_key_material,
                          "initial key and IV must be non-empty");
    }

    const std::uint64_t send_sequence =
        send_initial.first_record_sequence.value_or(0);
    const std::uint64_t receive_sequence =
        receive_initial.first_record_sequence.value_or(0);
    if (send_sequence > impl.options.send_sequence_limit ||
        receive_sequence > impl.options.receive_sequence_limit) {
        return make_error(ErrorCode::sequence_exhausted,
                          "initial record sequence exceeds the configured limit");
    }

    Impl::reset_direction(impl.send);
    Impl::reset_direction(impl.receive);
    impl.connection = connection;
    impl.last_error = {};
    impl.lifecycle = Impl::Lifecycle::active;
    impl.send.limit = impl.options.send_sequence_limit;
    impl.receive.limit = impl.options.receive_sequence_limit;
    impl.send.traffic_generation = send_initial.generation;
    impl.send.next_sequence = send_sequence;
    impl.receive.traffic_generation = receive_initial.generation;
    impl.receive.next_sequence = receive_sequence;
    impl.last_generation_by_fd[connection.fd] = connection.generation;
    return Result<void>{};
}

void KeyUpdateCoordinator::close() noexcept {
    Impl& impl = *impl_;
    Impl::reset_direction(impl.send);
    Impl::reset_direction(impl.receive);
    impl.lifecycle = Impl::Lifecycle::closed;
    impl.last_error = {};
}

Result<RecordTicket> KeyUpdateCoordinator::allocate_send(
    const ConnectionId& connection) {
    Impl& impl = *impl_;
    const Result<void> usable = impl.check_connection(connection);
    if (!usable) {
        return usable.error();
    }

    DirectionState& state = impl.send;
    if (state.frozen) {
        return make_error(ErrorCode::frozen,
                          "send direction is frozen for a key update");
    }
    if (state.exhausted || state.next_sequence > state.limit) {
        return impl.fail(make_error(ErrorCode::sequence_exhausted,
                                    "send record sequence would overflow"));
    }

    RecordTicket ticket;
    ticket.direction = Direction::send;
    ticket.connection = connection;
    ticket.traffic_generation = state.traffic_generation;
    ticket.sequence = state.next_sequence;
    ticket.token = impl.issue_token();
    Impl::consume_sequence(state);
    state.pending.emplace(ticket.token, ticket);
    return ticket;
}

Result<RecordTicket> KeyUpdateCoordinator::begin_receive(
    const ConnectionId& connection, std::uint64_t sequence) {
    return impl_->begin_receive_impl(connection, sequence, std::nullopt);
}

Result<RecordTicket> KeyUpdateCoordinator::begin_receive(
    const ConnectionId& connection, std::uint64_t sequence,
    std::uint64_t observed_traffic_generation) {
    return impl_->begin_receive_impl(connection, sequence,
                                     observed_traffic_generation);
}

Result<void> KeyUpdateCoordinator::complete_record(const RecordTicket& ticket) {
    Impl& impl = *impl_;
    if (impl.lifecycle == Impl::Lifecycle::closed) {
        return make_error(ErrorCode::not_open, "connection is not open");
    }
    if (impl.lifecycle == Impl::Lifecycle::failed) {
        return make_error(ErrorCode::failed, impl.last_error.message);
    }
    if (!(ticket.connection == impl.connection)) {
        return make_error(ErrorCode::stale_connection,
                          "record completion belongs to a stale connection");
    }
    if (ticket.direction != Direction::send &&
        ticket.direction != Direction::receive) {
        return make_error(ErrorCode::stale_record, "invalid record direction");
    }

    DirectionState& state = impl.state_for(ticket.direction);
    const auto found = state.pending.find(ticket.token);
    if (found == state.pending.end()) {
        return make_error(ErrorCode::stale_record,
                          "record is not pending (duplicate or stale completion)");
    }
    const RecordTicket& expected = found->second;
    if (!(expected.connection == ticket.connection) ||
        expected.direction != ticket.direction ||
        expected.traffic_generation != ticket.traffic_generation ||
        expected.sequence != ticket.sequence) {
        return make_error(ErrorCode::stale_record,
                          "record completion did not match its reservation");
    }
    state.pending.erase(found);

    if (state.update_pending) {
        return impl.advance_direction(state);
    }
    return Result<void>{};
}

Result<void> KeyUpdateCoordinator::begin_key_update(
    const ConnectionId& connection, Direction direction,
    const TrafficSecret& next) {
    Impl& impl = *impl_;
    const Result<void> usable = impl.check_connection(connection);
    if (!usable) {
        return usable.error();
    }
    if (direction != Direction::send && direction != Direction::receive) {
        return impl.fail(make_error(ErrorCode::invalid_generation,
                                    "invalid key-update direction"));
    }

    DirectionState& state = impl.state_for(direction);
    if (state.frozen || state.update_pending) {
        return impl.fail(make_error(
            ErrorCode::update_in_progress,
            "duplicate KeyUpdate before the prior update completed"));
    }
    if (state.traffic_generation ==
            std::numeric_limits<std::uint64_t>::max() ||
        next.generation != state.traffic_generation + 1) {
        return impl.fail(make_error(ErrorCode::invalid_generation,
                                    "traffic secret generation must be current+1"));
    }
    if (!valid_material(next.material)) {
        return impl.fail(make_error(ErrorCode::invalid_key_material,
                                    "new key and IV must be non-empty"));
    }

    std::uint64_t base = 0;
    if (next.first_record_sequence) {
        base = *next.first_record_sequence;
    } else if (impl.options.update_sequence_policy ==
               UpdateSequencePolicy::continue_sequence) {
        if (state.exhausted) {
            return impl.fail(make_error(ErrorCode::sequence_exhausted,
                                        "record sequence cannot continue"));
        }
        base = state.next_sequence;
    }
    if (base > state.limit) {
        return impl.fail(make_error(
            ErrorCode::sequence_exhausted,
            "post-update record sequence exceeds the configured limit"));
    }

    state.next_secret = next;
    state.next_base = base;
    state.frozen = true;
    state.update_pending = true;
    state.phase = Phase::wait_records;
    state.barrier_token = 0;
    state.install_token = 0;
    state.barrier_waiting = false;
    state.install_waiting = false;
    return impl.advance_direction(state);
}

Result<void> KeyUpdateCoordinator::advance() {
    Impl& impl = *impl_;
    if (impl.lifecycle == Impl::Lifecycle::closed) {
        return make_error(ErrorCode::not_open, "connection is not open");
    }
    if (impl.lifecycle == Impl::Lifecycle::failed) {
        return make_error(ErrorCode::failed, impl.last_error.message);
    }
    if (impl.send.update_pending) {
        const Result<void> result = impl.advance_direction(impl.send);
        if (!result) {
            return result;
        }
    }
    if (impl.receive.update_pending) {
        return impl.advance_direction(impl.receive);
    }
    return Result<void>{};
}

Result<void> KeyUpdateCoordinator::on_barrier_complete(
    const ConnectionId& connection, Direction direction, std::uint64_t token,
    bool success) {
    Impl& impl = *impl_;
    const Result<void> usable = impl.check_connection(connection);
    if (!usable) {
        return usable.error();
    }
    if (direction != Direction::send && direction != Direction::receive) {
        return make_error(ErrorCode::stale_barrier, "invalid direction");
    }

    DirectionState& state = impl.state_for(direction);
    if (state.phase != Phase::wait_barrier || !state.barrier_waiting ||
        state.barrier_token != token) {
        return make_error(ErrorCode::stale_barrier,
                          "barrier completion does not match a pending barrier");
    }
    state.barrier_waiting = false;
    state.barrier_token = 0;
    if (!success) {
        return impl.fail(make_error(ErrorCode::driver_barrier_failed,
                                    "asynchronous completion barrier failed"));
    }
    state.phase = Phase::wait_readback;
    return impl.advance_direction(state);
}

Result<void> KeyUpdateCoordinator::on_install_complete(
    const ConnectionId& connection, Direction direction, std::uint64_t token,
    bool success) {
    Impl& impl = *impl_;
    const Result<void> usable = impl.check_connection(connection);
    if (!usable) {
        return usable.error();
    }
    if (direction != Direction::send && direction != Direction::receive) {
        return make_error(ErrorCode::stale_install, "invalid direction");
    }

    DirectionState& state = impl.state_for(direction);
    if (state.phase != Phase::wait_install || !state.install_waiting ||
        state.install_token != token) {
        return make_error(ErrorCode::stale_install,
                          "install completion does not match a pending install");
    }
    state.install_waiting = false;
    state.install_token = 0;
    if (!success) {
        return impl.fail(make_error(ErrorCode::driver_install_failed,
                                    "asynchronous key installation failed"));
    }
    state.phase = Phase::verify_install;
    return impl.advance_direction(state);
}

Result<DirectionSnapshot> KeyUpdateCoordinator::snapshot(
    const ConnectionId& connection, Direction direction) const {
    const Impl& impl = *impl_;
    if (impl.lifecycle == Impl::Lifecycle::closed) {
        return make_error(ErrorCode::not_open, "connection is not open");
    }
    if (!(connection == impl.connection)) {
        return make_error(ErrorCode::stale_connection,
                          "snapshot belongs to another connection generation");
    }
    if (direction != Direction::send && direction != Direction::receive) {
        return make_error(ErrorCode::invalid_generation, "invalid direction");
    }

    const DirectionState& state = impl.state_for(direction);
    DirectionSnapshot result;
    result.direction = direction;
    result.traffic_generation = state.traffic_generation;
    result.next_sequence = state.next_sequence;
    result.pending_records = state.pending.size();
    result.frozen = state.frozen;
    result.update_pending = state.update_pending;
    result.sequence_exhausted = state.exhausted;
    return result;
}

bool KeyUpdateCoordinator::is_open() const noexcept {
    return impl_->lifecycle == Impl::Lifecycle::active;
}

bool KeyUpdateCoordinator::is_failed() const noexcept {
    return impl_->lifecycle == Impl::Lifecycle::failed;
}

Error KeyUpdateCoordinator::last_error() const noexcept {
    return impl_->last_error;
}

}  // namespace netsec
