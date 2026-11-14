#include "netsec.hpp"

#include <array>
#include <cstdint>
#include <iostream>
#include <optional>
#include <set>
#include <string>
#include <utility>
#include <vector>

namespace {

using netsec::ConnectionId;
using netsec::Direction;
using netsec::Driver;
using netsec::DriverOperation;
using netsec::DriverReadback;
using netsec::Error;
using netsec::ErrorCode;
using netsec::InstallRequest;
using netsec::KeyMaterial;
using netsec::KeyUpdateCoordinator;
using netsec::Options;
using netsec::RecordTicket;
using netsec::TrafficSecret;
using netsec::UpdateSequencePolicy;

int failures = 0;

#define CHECK(condition)                                                       \
    do {                                                                       \
        if (!(condition)) {                                                    \
            std::cerr << __FILE__ << ':' << __LINE__                           \
                      << ": CHECK failed: " #condition << '\n';                \
            ++failures;                                                        \
        }                                                                      \
    } while (false)

#define CHECK_OK(expression)                                                   \
    do {                                                                       \
        const auto result_ = (expression);                                     \
        if (!result_) {                                                        \
            std::cerr << __FILE__ << ':' << __LINE__                           \
                      << ": expected success, got "                            \
                      << static_cast<int>(result_.error().code) << " ("        \
                      << result_.error().message << ")\n";                     \
            ++failures;                                                        \
        }                                                                      \
    } while (false)

#define CHECK_ERROR(expression, expected_code)                                 \
    do {                                                                       \
        const auto result_ = (expression);                                     \
        if (result_) {                                                         \
            std::cerr << __FILE__ << ':' << __LINE__                           \
                      << ": expected error " << static_cast<int>(expected_code) \
                      << ", got success\n";                                    \
            ++failures;                                                        \
        } else if (result_.error().code != (expected_code)) {                  \
            std::cerr << __FILE__ << ':' << __LINE__                           \
                      << ": expected error " << static_cast<int>(expected_code) \
                      << ", got " << static_cast<int>(result_.error().code)    \
                      << " (" << result_.error().message << ")\n";             \
            ++failures;                                                        \
        }                                                                      \
    } while (false)

std::size_t index(Direction direction) {
    return direction == Direction::send ? 0 : 1;
}

KeyMaterial material(std::uint8_t fill) {
    KeyMaterial result;
    result.key.assign(16, fill);
    result.iv.assign(12, static_cast<std::uint8_t>(fill + 1));
    return result;
}

TrafficSecret secret(std::uint64_t generation, std::uint8_t fill,
                     std::optional<std::uint64_t> first_sequence = std::nullopt) {
    TrafficSecret result;
    result.generation = generation;
    result.material = material(fill);
    result.first_record_sequence = first_sequence;
    return result;
}

struct PendingBarrier {
    std::uint64_t token{0};
    std::uint64_t traffic_generation{0};
    std::uint64_t expected_sequence{0};
};

struct PendingInstall {
    std::uint64_t token{0};
    InstallRequest request;
};

class PausableDriver final : public Driver {
public:
    struct DirectionState {
        DriverReadback readback{};
        bool pause_barrier{false};
        bool pause_install{false};
        bool fail_next_barrier{false};
        bool fail_next_install{false};
        bool install_does_not_advance{false};
        bool corrupt_next_read{false};
        std::optional<PendingBarrier> pending_barrier;
        std::optional<PendingInstall> pending_install;
        std::vector<InstallRequest> installs;
        std::vector<std::string> events;
    };

    std::array<DirectionState, 2> state;
    KeyUpdateCoordinator* coordinator{nullptr};
    int abort_count{0};
    Error abort_error{};
    std::uint64_t next_token{1000};

    void initialize(Direction direction, const ConnectionId& connection,
                    std::uint64_t generation, std::uint64_t sequence) {
        DirectionState& direction_state = state[index(direction)];
        direction_state.readback = DriverReadback{connection, generation, sequence};
    }

    DriverOperation read(const ConnectionId& connection, Direction direction,
                         DriverReadback& out) override {
        DirectionState& direction_state = state[index(direction)];
        direction_state.events.push_back("read");
        if (!(direction_state.readback.connection == connection)) {
            return DriverOperation::failed;
        }
        out = direction_state.readback;
        if (direction_state.corrupt_next_read) {
            ++out.next_sequence;
            direction_state.corrupt_next_read = false;
        }
        return DriverOperation::ok;
    }

    DriverOperation barrier(const ConnectionId& connection, Direction direction,
                            std::uint64_t traffic_generation,
                            std::uint64_t expected_next_sequence,
                            std::uint64_t& token) override {
        DirectionState& direction_state = state[index(direction)];
        direction_state.events.push_back("barrier");
        if (!(direction_state.readback.connection == connection)) {
            return DriverOperation::failed;
        }
        if (direction_state.fail_next_barrier) {
            direction_state.fail_next_barrier = false;
            return DriverOperation::failed;
        }
        if (direction_state.pause_barrier) {
            token = next_token++;
            direction_state.pending_barrier =
                PendingBarrier{token, traffic_generation, expected_next_sequence};
            return DriverOperation::pending;
        }
        return DriverOperation::ok;
    }

    DriverOperation install(const InstallRequest& request,
                            std::uint64_t& token) override {
        DirectionState& direction_state = state[index(request.direction)];
        direction_state.events.push_back("install");
        if (!(direction_state.readback.connection == request.connection)) {
            return DriverOperation::failed;
        }
        if (direction_state.fail_next_install) {
            direction_state.fail_next_install = false;
            return DriverOperation::failed;
        }
        direction_state.installs.push_back(request);
        if (direction_state.pause_install) {
            token = next_token++;
            direction_state.pending_install =
                PendingInstall{token, request};
            return DriverOperation::pending;
        }
        if (!direction_state.install_does_not_advance) {
            direction_state.readback = DriverReadback{
                request.connection, request.traffic_generation,
                request.next_sequence};
        }
        return DriverOperation::ok;
    }

    void abort(const ConnectionId&, Error error) noexcept override {
        ++abort_count;
        abort_error = error;
    }

    netsec::Result<void> resume_barrier(Direction direction, bool success) {
        DirectionState& direction_state = state[index(direction)];
        if (!direction_state.pending_barrier) {
            return netsec::make_error(ErrorCode::stale_barrier,
                                      "test driver has no pending barrier");
        }
        const PendingBarrier pending = *direction_state.pending_barrier;
        direction_state.pending_barrier.reset();
        return coordinator->on_barrier_complete(
            direction_state.readback.connection, direction, pending.token,
            success);
    }

    netsec::Result<void> resume_install(Direction direction, bool success) {
        DirectionState& direction_state = state[index(direction)];
        if (!direction_state.pending_install) {
            return netsec::make_error(ErrorCode::stale_install,
                                      "test driver has no pending install");
        }
        const PendingInstall pending = *direction_state.pending_install;
        direction_state.pending_install.reset();
        if (success && !direction_state.install_does_not_advance) {
            direction_state.readback =
                DriverReadback{pending.request.connection,
                               pending.request.traffic_generation,
                               pending.request.next_sequence};
        }
        return coordinator->on_install_complete(
            direction_state.readback.connection, direction, pending.token,
            success);
    }
};

netsec::Result<RecordTicket> allocate_send(KeyUpdateCoordinator& coordinator,
                                           PausableDriver& driver,
                                           const ConnectionId& connection) {
    auto ticket = coordinator.allocate_send(connection);
    if (ticket) {
        driver.state[index(Direction::send)].readback.next_sequence =
            ticket.value().sequence + 1;
    }
    return ticket;
}

netsec::Result<RecordTicket> begin_receive(KeyUpdateCoordinator& coordinator,
                                           PausableDriver& driver,
                                           const ConnectionId& connection,
                                           std::uint64_t sequence,
                                           std::uint64_t generation) {
    auto ticket = coordinator.begin_receive(connection, sequence, generation);
    if (ticket) {
        driver.state[index(Direction::receive)].readback.next_sequence =
            ticket.value().sequence + 1;
    }
    return ticket;
}

void send_boundary_waits_for_late_completion_and_resets_sequence() {
    const ConnectionId connection{7, 1};
    PausableDriver driver;
    driver.initialize(Direction::send, connection, 10, 0);
    driver.initialize(Direction::receive, connection, 20, 0);
    KeyUpdateCoordinator coordinator(driver);
    driver.coordinator = &coordinator;

    CHECK_OK(coordinator.open(connection, secret(10, 1), secret(20, 2)));

    const auto first = allocate_send(coordinator, driver, connection);
    const auto second = allocate_send(coordinator, driver, connection);
    CHECK(first.ok());
    CHECK(second.ok());
    CHECK(first.value().traffic_generation == 10);
    CHECK(first.value().sequence == 0);
    CHECK(second.value().traffic_generation == 10);
    CHECK(second.value().sequence == 1);

    CHECK_OK(coordinator.begin_key_update(connection, Direction::send,
                                          secret(11, 3)));
    CHECK(driver.state[index(Direction::send)].installs.empty());
    CHECK_ERROR(coordinator.allocate_send(connection), ErrorCode::frozen);

    CHECK_OK(coordinator.complete_record(first.value()));
    CHECK(driver.state[index(Direction::send)].installs.empty());
    CHECK_OK(coordinator.complete_record(second.value()));

    const auto snapshot = coordinator.snapshot(connection, Direction::send);
    CHECK(snapshot.ok());
    CHECK(snapshot.value().traffic_generation == 11);
    CHECK(snapshot.value().next_sequence == 0);
    CHECK(!snapshot.value().frozen);
    CHECK(snapshot.value().pending_records == 0);
    CHECK(driver.state[index(Direction::send)].installs.size() == 1);
    CHECK(driver.state[index(Direction::send)].installs[0].traffic_generation ==
          11);
    CHECK(driver.state[index(Direction::send)].installs[0].next_sequence == 0);

    const auto third = allocate_send(coordinator, driver, connection);
    CHECK(third.ok());
    CHECK(third.value().traffic_generation == 11);
    CHECK(third.value().sequence == 0);
    CHECK_OK(coordinator.complete_record(third.value()));

    std::set<std::pair<std::uint64_t, std::uint64_t>> ledger;
    ledger.emplace(first.value().traffic_generation, first.value().sequence);
    ledger.emplace(second.value().traffic_generation, second.value().sequence);
    ledger.emplace(third.value().traffic_generation, third.value().sequence);
    CHECK(ledger.size() == 3);

    CHECK_ERROR(coordinator.complete_record(first.value()),
                ErrorCode::stale_record);
    CHECK(coordinator.is_open());
}

void receive_freeze_rejects_both_sides_of_the_boundary() {
    const ConnectionId connection{8, 2};
    PausableDriver driver;
    driver.initialize(Direction::send, connection, 30, 0);
    driver.initialize(Direction::receive, connection, 31, 0);
    KeyUpdateCoordinator coordinator(driver);
    driver.coordinator = &coordinator;

    CHECK_OK(coordinator.open(connection, secret(30, 1), secret(31, 2)));

    const auto data = begin_receive(coordinator, driver, connection, 0, 31);
    CHECK(data.ok());
    CHECK_OK(coordinator.complete_record(data.value()));

    const auto key_update_record =
        begin_receive(coordinator, driver, connection, 1, 31);
    CHECK(key_update_record.ok());
    CHECK_OK(coordinator.begin_key_update(connection, Direction::receive,
                                          secret(32, 4)));
    CHECK_ERROR(coordinator.begin_receive(connection, 2, 31),
                ErrorCode::frozen);
    CHECK_ERROR(coordinator.begin_receive(connection, 0, 31),
                ErrorCode::frozen);
    CHECK(driver.state[index(Direction::receive)].installs.empty());

    CHECK_OK(coordinator.complete_record(key_update_record.value()));
    const auto snapshot = coordinator.snapshot(connection, Direction::receive);
    CHECK(snapshot.ok());
    CHECK(snapshot.value().traffic_generation == 32);
    CHECK(snapshot.value().next_sequence == 0);
    CHECK(!snapshot.value().frozen);

    const auto next = begin_receive(coordinator, driver, connection, 0, 32);
    CHECK(next.ok());
    CHECK(next.value().traffic_generation == 32);
    CHECK(next.value().sequence == 0);
    CHECK_OK(coordinator.complete_record(next.value()));
}

void simultaneous_half_duplex_updates_commit_independently() {
    const ConnectionId connection{9, 3};
    PausableDriver driver;
    driver.initialize(Direction::send, connection, 40, 0);
    driver.initialize(Direction::receive, connection, 50, 0);
    driver.state[index(Direction::send)].pause_barrier = true;
    driver.state[index(Direction::send)].pause_install = true;
    driver.state[index(Direction::receive)].pause_barrier = true;
    driver.state[index(Direction::receive)].pause_install = true;
    KeyUpdateCoordinator coordinator(driver);
    driver.coordinator = &coordinator;

    CHECK_OK(coordinator.open(connection, secret(40, 1), secret(50, 2)));
    const auto send_record = allocate_send(coordinator, driver, connection);
    const auto receive_record =
        begin_receive(coordinator, driver, connection, 0, 50);
    CHECK(send_record.ok());
    CHECK(receive_record.ok());

    CHECK_OK(coordinator.begin_key_update(connection, Direction::send,
                                          secret(41, 5)));
    CHECK_OK(coordinator.begin_key_update(connection, Direction::receive,
                                          secret(51, 6)));
    CHECK_ERROR(coordinator.allocate_send(connection), ErrorCode::frozen);
    CHECK_ERROR(coordinator.begin_receive(connection, 1, 50),
                ErrorCode::frozen);

    CHECK_OK(coordinator.complete_record(send_record.value()));
    CHECK_OK(coordinator.complete_record(receive_record.value()));
    CHECK(driver.state[index(Direction::send)].pending_barrier.has_value());
    CHECK(driver.state[index(Direction::receive)].pending_barrier.has_value());

    CHECK_OK(driver.resume_barrier(Direction::send, true));
    CHECK_OK(driver.resume_barrier(Direction::receive, true));
    CHECK(driver.state[index(Direction::send)].pending_install.has_value());
    CHECK(driver.state[index(Direction::receive)].pending_install.has_value());

    CHECK_OK(driver.resume_install(Direction::send, true));
    auto send_snapshot = coordinator.snapshot(connection, Direction::send);
    auto receive_snapshot = coordinator.snapshot(connection, Direction::receive);
    CHECK(send_snapshot.ok());
    CHECK(receive_snapshot.ok());
    CHECK(send_snapshot.value().traffic_generation == 41);
    CHECK(!send_snapshot.value().frozen);
    CHECK(receive_snapshot.value().traffic_generation == 50);
    CHECK(receive_snapshot.value().frozen);

    CHECK_OK(driver.resume_install(Direction::receive, true));
    receive_snapshot = coordinator.snapshot(connection, Direction::receive);
    CHECK(receive_snapshot.ok());
    CHECK(receive_snapshot.value().traffic_generation == 51);
    CHECK(!receive_snapshot.value().frozen);
    CHECK(receive_snapshot.value().next_sequence == 0);
}

void install_failure_closes_the_connection() {
    const ConnectionId connection{10, 4};
    PausableDriver driver;
    driver.initialize(Direction::send, connection, 60, 0);
    driver.initialize(Direction::receive, connection, 61, 0);
    driver.state[index(Direction::send)].pause_install = true;
    KeyUpdateCoordinator coordinator(driver);
    driver.coordinator = &coordinator;

    CHECK_OK(coordinator.open(connection, secret(60, 1), secret(61, 2)));
    CHECK_OK(coordinator.begin_key_update(connection, Direction::send,
                                          secret(61, 7)));
    CHECK(driver.state[index(Direction::send)].pending_install.has_value());
    CHECK_ERROR(driver.resume_install(Direction::send, false),
                ErrorCode::driver_install_failed);

    CHECK(coordinator.is_failed());
    CHECK(driver.abort_count == 1);
    CHECK(driver.abort_error.code == ErrorCode::driver_install_failed);
    CHECK_ERROR(coordinator.allocate_send(connection), ErrorCode::failed);
    const auto snapshot = coordinator.snapshot(connection, Direction::send);
    CHECK(snapshot.ok());
    CHECK(snapshot.value().traffic_generation == 60);
}

void barrier_failure_closes_the_connection() {
    const ConnectionId connection{10, 40};
    PausableDriver driver;
    driver.initialize(Direction::send, connection, 62, 0);
    driver.initialize(Direction::receive, connection, 63, 0);
    driver.state[index(Direction::send)].pause_barrier = true;
    KeyUpdateCoordinator coordinator(driver);
    driver.coordinator = &coordinator;

    CHECK_OK(coordinator.open(connection, secret(62, 1), secret(63, 2)));
    CHECK_OK(coordinator.begin_key_update(connection, Direction::send,
                                          secret(63, 7)));
    CHECK(driver.state[index(Direction::send)].pending_barrier.has_value());
    CHECK_ERROR(driver.resume_barrier(Direction::send, false),
                ErrorCode::driver_barrier_failed);
    CHECK(coordinator.is_failed());
    CHECK(driver.abort_count == 1);
    CHECK(driver.state[index(Direction::send)].installs.empty());
}

void readback_mismatch_never_installs() {
    const ConnectionId connection{11, 5};
    PausableDriver driver;
    driver.initialize(Direction::send, connection, 70, 3);
    driver.initialize(Direction::receive, connection, 71, 0);
    driver.state[index(Direction::send)].corrupt_next_read = true;
    KeyUpdateCoordinator coordinator(driver);
    driver.coordinator = &coordinator;

    CHECK_OK(coordinator.open(connection, secret(70, 1), secret(71, 2)));
    CHECK_ERROR(coordinator.begin_key_update(connection, Direction::send,
                                             secret(71, 8)),
                ErrorCode::readback_mismatch);
    CHECK(driver.state[index(Direction::send)].installs.empty());
    CHECK(coordinator.is_failed());
    CHECK(driver.abort_count == 1);
}

void install_that_does_not_advance_is_rejected() {
    const ConnectionId connection{12, 6};
    PausableDriver driver;
    driver.initialize(Direction::send, connection, 80, 0);
    driver.initialize(Direction::receive, connection, 81, 0);
    driver.state[index(Direction::send)].install_does_not_advance = true;
    KeyUpdateCoordinator coordinator(driver);
    driver.coordinator = &coordinator;

    CHECK_OK(coordinator.open(connection, secret(80, 1), secret(81, 2)));
    CHECK_ERROR(coordinator.begin_key_update(connection, Direction::send,
                                             secret(81, 9)),
                ErrorCode::install_readback_mismatch);
    CHECK(coordinator.is_failed());
    CHECK(driver.abort_count == 1);
}

void duplicate_key_update_cannot_advance_twice() {
    const ConnectionId connection{13, 7};
    PausableDriver driver;
    driver.initialize(Direction::send, connection, 90, 0);
    driver.initialize(Direction::receive, connection, 91, 0);
    KeyUpdateCoordinator coordinator(driver);
    driver.coordinator = &coordinator;

    CHECK_OK(coordinator.open(connection, secret(90, 1), secret(91, 2)));
    const auto pending = allocate_send(coordinator, driver, connection);
    CHECK(pending.ok());
    CHECK_OK(coordinator.begin_key_update(connection, Direction::send,
                                          secret(91, 10)));
    CHECK_ERROR(coordinator.begin_key_update(connection, Direction::send,
                                             secret(91, 10)),
                ErrorCode::update_in_progress);
    CHECK(coordinator.is_failed());
    CHECK(driver.abort_count == 1);
    CHECK_ERROR(coordinator.complete_record(pending.value()), ErrorCode::failed);
    CHECK(driver.state[index(Direction::send)].installs.empty());
}

void fd_reuse_rejects_stale_tickets_and_callbacks() {
    const ConnectionId first_connection{14, 1};
    const ConnectionId second_connection{14, 2};
    const ConnectionId third_connection{14, 3};
    PausableDriver driver;
    driver.initialize(Direction::send, first_connection, 100, 0);
    driver.initialize(Direction::receive, first_connection, 101, 0);
    KeyUpdateCoordinator coordinator(driver);
    driver.coordinator = &coordinator;

    CHECK_OK(coordinator.open(first_connection, secret(100, 1), secret(101, 2)));
    const auto old_ticket = allocate_send(coordinator, driver, first_connection);
    CHECK(old_ticket.ok());
    coordinator.close();

    driver.initialize(Direction::send, second_connection, 110, 0);
    driver.initialize(Direction::receive, second_connection, 111, 0);
    CHECK_OK(coordinator.open(second_connection, secret(110, 3), secret(111, 4)));
    CHECK_ERROR(coordinator.complete_record(old_ticket.value()),
                ErrorCode::stale_connection);
    const auto fresh = allocate_send(coordinator, driver, second_connection);
    CHECK(fresh.ok());
    CHECK(fresh.value().connection == second_connection);
    CHECK(fresh.value().traffic_generation == 110);
    CHECK_OK(coordinator.complete_record(fresh.value()));

    driver.state[index(Direction::send)].pause_install = true;
    CHECK_OK(coordinator.begin_key_update(second_connection, Direction::send,
                                          secret(111, 5)));
    auto pending = driver.state[index(Direction::send)].pending_install;
    CHECK(pending.has_value());
    coordinator.close();

    driver.initialize(Direction::send, third_connection, 120, 0);
    driver.initialize(Direction::receive, third_connection, 121, 0);
    CHECK_OK(coordinator.open(third_connection, secret(120, 6), secret(121, 7)));
    CHECK_ERROR(coordinator.on_install_complete(
                    second_connection, Direction::send, pending->token, true),
                ErrorCode::stale_connection);
    const auto third_snapshot =
        coordinator.snapshot(third_connection, Direction::send);
    CHECK(third_snapshot.ok());
    CHECK(third_snapshot.value().traffic_generation == 120);
    CHECK(!third_snapshot.value().frozen);

    coordinator.close();
    CHECK_ERROR(coordinator.open(second_connection, secret(110, 8),
                                 secret(111, 9)),
                ErrorCode::connection_reuse);
}

void sequence_overflow_is_fatal() {
    const ConnectionId connection{15, 1};
    Options options;
    options.send_sequence_limit = 2;
    options.receive_sequence_limit = 2;
    PausableDriver driver;
    driver.initialize(Direction::send, connection, 130, 0);
    driver.initialize(Direction::receive, connection, 131, 0);
    KeyUpdateCoordinator coordinator(driver, options);
    driver.coordinator = &coordinator;

    CHECK_OK(coordinator.open(connection, secret(130, 1), secret(131, 2)));
    const auto first = allocate_send(coordinator, driver, connection);
    const auto second = allocate_send(coordinator, driver, connection);
    CHECK(first.ok());
    CHECK(second.ok());
    CHECK_OK(coordinator.complete_record(first.value()));
    CHECK_OK(coordinator.complete_record(second.value()));
    CHECK_OK(allocate_send(coordinator, driver, connection));
    CHECK_ERROR(coordinator.allocate_send(connection),
                ErrorCode::sequence_exhausted);
    CHECK(coordinator.is_failed());
    CHECK(driver.abort_count == 1);
}

void continue_policy_uses_canonical_old_sequence() {
    const ConnectionId connection{16, 1};
    Options options;
    options.update_sequence_policy = UpdateSequencePolicy::continue_sequence;
    PausableDriver driver;
    driver.initialize(Direction::send, connection, 140, 0);
    driver.initialize(Direction::receive, connection, 141, 0);
    KeyUpdateCoordinator coordinator(driver, options);
    driver.coordinator = &coordinator;

    CHECK_OK(coordinator.open(connection, secret(140, 1), secret(141, 2)));
    const auto first = allocate_send(coordinator, driver, connection);
    const auto second = allocate_send(coordinator, driver, connection);
    CHECK(first.ok());
    CHECK(second.ok());
    CHECK_OK(coordinator.complete_record(first.value()));
    CHECK_OK(coordinator.complete_record(second.value()));
    CHECK_OK(coordinator.begin_key_update(connection, Direction::send,
                                          secret(141, 11)));
    CHECK(driver.state[index(Direction::send)].installs.size() == 1);
    CHECK(driver.state[index(Direction::send)].installs[0].next_sequence == 2);

    const auto next = allocate_send(coordinator, driver, connection);
    CHECK(next.ok());
    CHECK(next.value().traffic_generation == 141);
    CHECK(next.value().sequence == 2);
}

void explicit_sequence_override_is_honored() {
    const ConnectionId connection{17, 1};
    PausableDriver driver;
    driver.initialize(Direction::send, connection, 150, 0);
    driver.initialize(Direction::receive, connection, 151, 0);
    KeyUpdateCoordinator coordinator(driver);
    driver.coordinator = &coordinator;

    CHECK_OK(coordinator.open(connection, secret(150, 1), secret(151, 2)));
    CHECK_OK(coordinator.begin_key_update(
        connection, Direction::send, secret(151, 12, std::uint64_t{500})));
    CHECK(driver.state[index(Direction::send)].installs[0].next_sequence == 500);
    const auto ticket = allocate_send(coordinator, driver, connection);
    CHECK(ticket.ok());
    CHECK(ticket.value().traffic_generation == 151);
    CHECK(ticket.value().sequence == 500);
}

void stale_install_callback_cannot_commit_early() {
    const ConnectionId connection{18, 1};
    PausableDriver driver;
    driver.initialize(Direction::send, connection, 160, 0);
    driver.initialize(Direction::receive, connection, 161, 0);
    driver.state[index(Direction::send)].pause_install = true;
    KeyUpdateCoordinator coordinator(driver);
    driver.coordinator = &coordinator;

    CHECK_OK(coordinator.open(connection, secret(160, 1), secret(161, 2)));
    CHECK_OK(coordinator.begin_key_update(connection, Direction::send,
                                          secret(161, 13)));
    const auto pending = driver.state[index(Direction::send)].pending_install;
    CHECK(pending.has_value());
    CHECK_ERROR(coordinator.on_install_complete(connection, Direction::send,
                                                pending->token + 1, true),
                ErrorCode::stale_install);
    CHECK(coordinator.is_open());
    CHECK(coordinator.snapshot(connection, Direction::send)
              .value()
              .traffic_generation == 160);

    const PendingInstall stored = *pending;
    driver.state[index(Direction::send)].pending_install.reset();
    driver.state[index(Direction::send)].readback =
        DriverReadback{stored.request.connection,
                       stored.request.traffic_generation,
                       stored.request.next_sequence};
    CHECK_OK(coordinator.on_install_complete(connection, Direction::send,
                                             stored.token, true));
    CHECK(coordinator.snapshot(connection, Direction::send)
              .value()
              .traffic_generation == 161);
}

void valid_consecutive_records_stay_in_one_generation() {
    const ConnectionId connection{19, 1};
    PausableDriver driver;
    driver.initialize(Direction::send, connection, 170, 0);
    driver.initialize(Direction::receive, connection, 180, 0);
    KeyUpdateCoordinator coordinator(driver);
    driver.coordinator = &coordinator;

    CHECK_OK(coordinator.open(connection, secret(170, 1), secret(180, 2)));
    std::set<std::pair<std::uint64_t, std::uint64_t>> ciphertext_ledger;
    for (std::uint64_t sequence = 0; sequence < 4; ++sequence) {
        const auto outbound = allocate_send(coordinator, driver, connection);
        CHECK(outbound.ok());
        CHECK(outbound.value().traffic_generation == 170);
        CHECK(outbound.value().sequence == sequence);
        CHECK(ciphertext_ledger
                  .emplace(outbound.value().traffic_generation,
                           outbound.value().sequence)
                  .second);
        CHECK_OK(coordinator.complete_record(outbound.value()));

        const auto inbound =
            begin_receive(coordinator, driver, connection, sequence, 180);
        CHECK(inbound.ok());
        CHECK(inbound.value().traffic_generation == 180);
        CHECK(ciphertext_ledger
                  .emplace(inbound.value().traffic_generation,
                           inbound.value().sequence)
                  .second);
        CHECK_OK(coordinator.complete_record(inbound.value()));
    }
}

void replayed_old_ciphertext_cannot_enter_new_generation() {
    const ConnectionId connection{20, 1};
    PausableDriver driver;
    driver.initialize(Direction::send, connection, 200, 0);
    driver.initialize(Direction::receive, connection, 201, 0);
    KeyUpdateCoordinator coordinator(driver);
    driver.coordinator = &coordinator;

    CHECK_OK(coordinator.open(connection, secret(200, 1), secret(201, 2)));
    const auto old_record =
        begin_receive(coordinator, driver, connection, 0, 201);
    CHECK(old_record.ok());
    CHECK_OK(coordinator.complete_record(old_record.value()));
    CHECK_OK(coordinator.begin_key_update(connection, Direction::receive,
                                          secret(202, 3)));
    const auto next = begin_receive(coordinator, driver, connection, 0, 202);
    CHECK(next.ok());
    CHECK(next.value().traffic_generation == 202);

    // The same sequence and ciphertext from the old generation must not be
    // accepted merely because TLS 1.3 reset the new epoch to sequence zero.
    CHECK_ERROR(coordinator.begin_receive(connection, 1, 201),
                ErrorCode::ciphertext_generation_mismatch);
    CHECK(coordinator.is_failed());
}

}  // namespace

int main() {
    send_boundary_waits_for_late_completion_and_resets_sequence();
    receive_freeze_rejects_both_sides_of_the_boundary();
    simultaneous_half_duplex_updates_commit_independently();
    install_failure_closes_the_connection();
    barrier_failure_closes_the_connection();
    readback_mismatch_never_installs();
    install_that_does_not_advance_is_rejected();
    duplicate_key_update_cannot_advance_twice();
    fd_reuse_rejects_stale_tickets_and_callbacks();
    sequence_overflow_is_fatal();
    continue_policy_uses_canonical_old_sequence();
    explicit_sequence_override_is_honored();
    stale_install_callback_cannot_commit_early();
    valid_consecutive_records_stay_in_one_generation();
    replayed_old_ciphertext_cannot_enter_new_generation();

    if (failures != 0) {
        std::cerr << failures << " test assertion(s) failed\n";
        return 1;
    }
    std::cout << "all netsec tests passed\n";
    return 0;
}
