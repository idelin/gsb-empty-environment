#include "netsec.hpp"

#include <atomic>
#include <cstdlib>
#include <iostream>
#include <memory>
#include <string>
#include <thread>
#include <vector>

namespace {

using namespace netsec;

#define CHECK(condition)                                                     \
    do {                                                                     \
        if (!(condition)) {                                                  \
            std::cerr << __FILE__ << ':' << __LINE__                         \
                      << ": check failed: " #condition << '\n';              \
            std::exit(1);                                                    \
        }                                                                    \
    } while (false)

void check_status(const BlockResult& result, BlockStatus expected,
                  int line) {
    if (result.status != expected) {
        std::cerr << "line " << line << ": expected "
                  << status_name(expected) << ", got "
                  << status_name(result.status) << '\n';
        std::exit(1);
    }
}

void check_reply(const BlockReply& reply, BlockStatus expected, int line) {
    if (reply.status != expected) {
        std::cerr << "line " << line << ": expected "
                  << status_name(expected) << ", got "
                  << status_name(reply.status) << '\n';
        std::exit(1);
    }
}

BlockKey make_key(std::string uri, std::string session,
                  std::uint32_t block_size, std::uint64_t total_length,
                  std::string etag) {
    BlockKey key;
    key.token = bytes_from_string("shared-token");
    key.method = "GET";
    key.uri = std::move(uri);
    key.content_format = 50;
    key.block_size = block_size;
    key.start_sequence = 0;
    key.requester.session_id = std::move(session);
    key.requester.endpoint = "192.0.2.10:5683";
    key.total_length = total_length;
    key.etag = bytes_from_string(etag);
    return key;
}

Bytes bytes(std::string_view text) {
    return bytes_from_string(text);
}

void test_legal_continuation_and_duplicates() {
    const Bytes body =
        bytes("The quick brown fox jumps over the lazy dog. 0123456789");
    const Bytes etag = bytes("etag-legal");
    BlockKey key = make_key("/legal", "session-legal", 8, body.size(),
                            "etag-legal");
    const auto blocks = make_blocks(key, body, etag);
    CHECK(blocks.size() == 7);

    BlockTransfer transfer;
    BlockResult result;
    for (std::size_t i = 0; i < blocks.size(); ++i) {
        result = transfer.receive(blocks[i]);
        if (i + 1 == blocks.size()) {
            check_status(result, BlockStatus::Complete, __LINE__);
        } else {
            check_status(result, BlockStatus::Accepted, __LINE__);
        }
    }
    CHECK(result.body == body);
    CHECK(result.received_bytes == body.size());
    CHECK(result.expected_length == body.size());
    CHECK(transfer.is_complete(key));

    const auto stored = transfer.body(key);
    CHECK(stored.has_value());
    CHECK(*stored == body);

    const BlockResult duplicate = transfer.receive(blocks.back());
    check_status(duplicate, BlockStatus::Duplicate, __LINE__);
    CHECK(duplicate.body.empty());
    CHECK(transfer.body(key) == std::optional<Bytes>(body));

    const BlockResult first_duplicate = transfer.receive(blocks.front());
    check_status(first_duplicate, BlockStatus::Duplicate, __LINE__);
    CHECK(transfer.body(key) == std::optional<Bytes>(body));

    const BlockResult stale = transfer.receive(blocks[2]);
    check_status(stale, BlockStatus::Duplicate, __LINE__);
    CHECK(transfer.body(key) == std::optional<Bytes>(body));
}

void test_nonzero_start_and_stale_discard() {
    const Bytes body = bytes("0123456789abcdefghij");
    BlockKey key = make_key("/resume", "session-resume", 4, body.size(),
                            "etag-resume");
    key.start_sequence = 2;
    const auto blocks = make_blocks(key, body, bytes("etag-resume"));
    CHECK(blocks.front().sequence == 2);

    BlockTransfer transfer;
    check_status(transfer.receive(blocks[0]), BlockStatus::Accepted, __LINE__);

    check_status(transfer.receive(blocks[0]), BlockStatus::Duplicate,
                 __LINE__);
    CHECK(transfer.next_sequence(key) == std::optional<std::uint32_t>(3));

    Block stale = blocks[0];
    stale.sequence = 1;
    stale.payload = bytes("XXXX");
    check_status(transfer.receive(stale), BlockStatus::Stale, __LINE__);
    CHECK(transfer.next_sequence(key) == std::optional<std::uint32_t>(3));

    Block conflicting = blocks[0];
    conflicting.etag = bytes("other-etag");
    check_status(transfer.receive(conflicting),
                 BlockStatus::ConflictingDuplicate, __LINE__);
    CHECK(transfer.next_sequence(key) == std::optional<std::uint32_t>(3));

    Block short_duplicate = blocks[0];
    short_duplicate.payload.pop_back();
    check_status(transfer.receive(short_duplicate),
                 BlockStatus::ConflictingDuplicate, __LINE__);
    CHECK(transfer.next_sequence(key) == std::optional<std::uint32_t>(3));

    BlockResult complete;
    for (std::size_t i = 1; i < blocks.size(); ++i) {
        complete = transfer.receive(blocks[i]);
    }
    check_status(complete, BlockStatus::Complete, __LINE__);
    CHECK(complete.body == body);
}

void test_token_reuse_rejects_and_restarts_cleanly() {
    const Bytes body_a = bytes("AAAAAAAAAAAAAAAAAAAA");
    const Bytes body_b = bytes("BBBBBBBBBBBBBBBBBBBB");
    const Bytes etag_a = bytes("etag-a");
    const Bytes etag_b = bytes("etag-b");

    BlockKey key_a = make_key("/resource/a", "session-a", 4, body_a.size(),
                              "etag-a");
    BlockKey key_b = make_key("/resource/b", "session-a", 4, body_b.size(),
                              "etag-b");
    const auto blocks_a = make_blocks(key_a, body_a, etag_a);
    const auto blocks_b = make_blocks(key_b, body_b, etag_b);

    BlockTransfer rejecting;
    check_status(rejecting.receive(blocks_a[0]), BlockStatus::Accepted,
                 __LINE__);

    // Same token and same session, but another URI at block 1.  The first
    // transfer must not be extended.
    const BlockResult mixed = rejecting.receive(blocks_b[1]);
    check_status(mixed, BlockStatus::ContextMismatch, __LINE__);
    CHECK(mixed.state_reset);
    CHECK(mixed.body.empty());
    CHECK(!rejecting.has_transfer(key_a));

    // A fresh block 0 can begin the other resource.  Its result contains only
    // resource B.
    for (std::size_t i = 0; i < blocks_b.size(); ++i) {
        const BlockResult step = rejecting.receive(blocks_b[i]);
        if (i + 1 == blocks_b.size()) {
            check_status(step, BlockStatus::Complete, __LINE__);
            CHECK(step.body == body_b);
            CHECK(step.body != body_a);
        } else {
            check_status(step, BlockStatus::Accepted, __LINE__);
        }
    }

    BlockTransferOptions restart_options;
    restart_options.restart_on_context_mismatch = true;
    BlockTransfer restarting(restart_options);
    check_status(restarting.receive(blocks_a[0]), BlockStatus::Accepted,
                 __LINE__);

    BlockResult restarted = restarting.receive(blocks_b[0]);
    check_status(restarted, BlockStatus::Accepted, __LINE__);
    CHECK(restarted.restarted);
    CHECK(restarted.received_bytes == 4);
    CHECK(restarted.body.empty());

    for (std::size_t i = 1; i < blocks_b.size(); ++i) {
        const BlockResult step = restarting.receive(blocks_b[i]);
        if (i + 1 == blocks_b.size()) {
            check_status(step, BlockStatus::Complete, __LINE__);
            CHECK(step.body == body_b);
        } else {
            check_status(step, BlockStatus::Accepted, __LINE__);
        }
    }
}

void test_each_binding_change_is_rejected() {
    const Bytes body = bytes("0123456789abcdefghij");
    const Bytes etag = bytes("etag-bind");
    const BlockKey base = make_key("/binding", "session-bind", 4,
                                   body.size(), "etag-bind");
    const auto blocks = make_blocks(base, body, etag);
    BlockTransfer transfer;

    auto start_base = [&] {
        check_status(transfer.receive(blocks[0]), BlockStatus::Accepted,
                     __LINE__);
    };

    start_base();
    Block changed_uri = blocks[1];
    changed_uri.key.uri = "/binding/other";
    check_status(transfer.receive(changed_uri), BlockStatus::ContextMismatch,
                 __LINE__);

    start_base();
    Block changed_method = blocks[1];
    changed_method.key.method = "POST";
    check_status(transfer.receive(changed_method),
                 BlockStatus::ContextMismatch, __LINE__);

    start_base();
    Block changed_format = blocks[1];
    changed_format.key.content_format = 0;
    check_status(transfer.receive(changed_format),
                 BlockStatus::ContextMismatch, __LINE__);

    start_base();
    Block changed_size = blocks[1];
    changed_size.key.block_size = 8;
    check_status(transfer.receive(changed_size),
                 BlockStatus::ContextMismatch, __LINE__);

    start_base();
    Block changed_start = blocks[1];
    changed_start.key.start_sequence = 1;
    check_status(transfer.receive(changed_start),
                 BlockStatus::ContextMismatch, __LINE__);

    // A different requester is a different session, not a context mismatch.
    // It cannot consume or extend the first session's state.
    start_base();
    Block other_session = blocks[1];
    other_session.key.requester.session_id = "session-bind-2";
    check_status(transfer.receive(other_session),
                 BlockStatus::SequenceGap, __LINE__);
    CHECK(transfer.next_sequence(base) ==
          std::optional<std::uint32_t>(1));
    check_status(transfer.receive(blocks[1]), BlockStatus::Accepted,
                 __LINE__);
}

void test_sequence_gap_does_not_append() {
    const Bytes body = bytes("abcdefghijklmnopqrstuvwxyz");
    const Bytes etag = bytes("etag-gap");
    BlockKey key = make_key("/gap", "session-gap", 8, body.size(),
                            "etag-gap");
    const auto blocks = make_blocks(key, body, etag);
    BlockTransfer transfer;

    check_status(transfer.receive(blocks[0]), BlockStatus::Accepted, __LINE__);
    const BlockResult gap = transfer.receive(blocks[2]);
    check_status(gap, BlockStatus::SequenceGap, __LINE__);
    CHECK(gap.body.empty());
    CHECK(transfer.next_sequence(key) == std::optional<std::uint32_t>(1));

    check_status(transfer.receive(blocks[1]), BlockStatus::Accepted, __LINE__);
    BlockResult complete;
    for (std::size_t i = 2; i < blocks.size(); ++i) {
        complete = transfer.receive(blocks[i]);
    }
    check_status(complete, BlockStatus::Complete, __LINE__);
    CHECK(complete.body == body);
}

void test_etag_mismatch_discards_state() {
    const Bytes body = bytes("etag mismatch payload");
    BlockKey key = make_key("/etag", "session-etag", 4, body.size(),
                            "etag-one");
    auto blocks = make_blocks(key, body, bytes("etag-one"));
    BlockTransfer transfer;

    check_status(transfer.receive(blocks[0]), BlockStatus::Accepted, __LINE__);
    blocks[1].etag = bytes("etag-two");
    const BlockResult mismatch = transfer.receive(blocks[1]);
    check_status(mismatch, BlockStatus::EtagMismatch, __LINE__);
    CHECK(mismatch.state_reset);
    CHECK(mismatch.body.empty());
    CHECK(!transfer.has_transfer(key));

    // Restarting with one consistent ETag yields only that representation.
    auto good = make_blocks(key, body, bytes("etag-one"));
    BlockResult complete;
    for (const auto& block : good) {
        complete = transfer.receive(block);
    }
    check_status(complete, BlockStatus::Complete, __LINE__);
    CHECK(complete.body == body);
}

void test_length_must_match_etag_metadata() {
    const Bytes body = bytes("length checked body");
    const Bytes etag = bytes("etag-length");
    BlockKey key = make_key("/length", "session-length", 5,
                            body.size() + 1U, "etag-length");
    const auto blocks = make_blocks(key, body, etag);
    BlockTransfer transfer;

    BlockResult result;
    for (const auto& block : blocks) {
        result = transfer.receive(block);
    }
    check_status(result, BlockStatus::LengthMismatch, __LINE__);
    CHECK(result.state_reset);
    CHECK(result.body.empty());
    CHECK(!transfer.has_transfer(key));

    BlockKey registered = make_key("/length", "session-length-2", 5,
                                   body.size(), "etag-registered");
    registered.total_length.reset();
    const auto registered_blocks =
        make_blocks(registered, body, bytes("etag-registered"));
    transfer.register_etag(bytes("etag-registered"), body.size());
    CHECK(transfer.etag_length(bytes("etag-registered")) ==
          std::optional<std::uint64_t>(body.size()));

    BlockResult complete;
    for (const auto& block : registered_blocks) {
        complete = transfer.receive(block);
    }
    check_status(complete, BlockStatus::Complete, __LINE__);
    CHECK(complete.body == body);
}

void test_sessions_do_not_share_cache() {
    const Bytes body_a = bytes("session A body");
    const Bytes body_b = bytes("session B body");
    const Bytes etag = bytes("same-etag-on-purpose");
    BlockKey key_a = make_key("/shared", "session-A", 5, body_a.size(),
                              "same-etag-on-purpose");
    BlockKey key_b = make_key("/shared", "session-B", 5, body_b.size(),
                              "same-etag-on-purpose");
    const auto blocks_a = make_blocks(key_a, body_a, etag);
    const auto blocks_b = make_blocks(key_b, body_b, etag);

    BlockTransfer transfer;
    for (std::size_t i = 0; i < std::max(blocks_a.size(), blocks_b.size());
         ++i) {
        if (i < blocks_a.size()) {
            const BlockResult result = transfer.receive(blocks_a[i]);
            if (i + 1 == blocks_a.size()) {
                check_status(result, BlockStatus::Complete, __LINE__);
                CHECK(result.body == body_a);
            } else {
                check_status(result, BlockStatus::Accepted, __LINE__);
            }
        }
        if (i < blocks_b.size()) {
            const BlockResult result = transfer.receive(blocks_b[i]);
            if (i + 1 == blocks_b.size()) {
                check_status(result, BlockStatus::Complete, __LINE__);
                CHECK(result.body == body_b);
            } else {
                check_status(result, BlockStatus::Accepted, __LINE__);
            }
        }
    }

    CHECK(transfer.body(key_a) == std::optional<Bytes>(body_a));
    CHECK(transfer.body(key_b) == std::optional<Bytes>(body_b));
    CHECK(transfer.active_transfers() == 2);

    // A distinct endpoint is also a distinct session.
    BlockKey key_c = key_a;
    key_c.requester.endpoint = "192.0.2.99:5683";
    Block key_c_block = blocks_a[1];
    key_c_block.key = key_c;
    check_status(transfer.receive(key_c_block), BlockStatus::SequenceGap,
                 __LINE__);
}

void test_observations_are_separate_from_block_state() {
    const Bytes body = bytes("observed body");
    BlockKey key = make_key("/observe", "session-obs", 4, body.size(),
                            "etag-obs");
    const auto blocks = make_blocks(key, body, bytes("etag-obs"));

    ObservationKey observation;
    observation.requester = key.requester;
    observation.token = key.token;
    observation.method = key.method;
    observation.uri = key.uri;

    BlockTransfer transfer;
    transfer.observe(observation);
    CHECK(transfer.is_observed(observation));
    CHECK(transfer.active_transfers() == 0);

    check_status(transfer.receive(blocks[0]), BlockStatus::Accepted, __LINE__);
    CHECK(transfer.is_observed(observation));
    CHECK(transfer.reset(key.requester, key.token));
    CHECK(transfer.is_observed(observation));
    CHECK(transfer.active_transfers() == 0);

    BlockResult complete;
    for (const auto& block : blocks) {
        complete = transfer.receive(block);
    }
    check_status(complete, BlockStatus::Complete, __LINE__);
    CHECK(transfer.is_observed(observation));
    CHECK(transfer.cancel_observation(observation));
    CHECK(!transfer.is_observed(observation));
}

void test_expiry_discards_block_state() {
    const Bytes body = bytes("expiring body");
    BlockKey key = make_key("/expiry", "session-exp", 4, body.size(),
                            "etag-exp");
    const auto blocks = make_blocks(key, body, bytes("etag-exp"));

    auto current = std::make_shared<TimePoint>(TimePoint{});
    BlockTransferOptions options;
    options.ttl = std::chrono::milliseconds(100);
    options.clock = [current] { return *current; };
    BlockTransfer transfer(options);

    check_status(transfer.receive(blocks[0]), BlockStatus::Accepted, __LINE__);
    *current += std::chrono::milliseconds(250);
    check_status(transfer.receive(blocks[1]), BlockStatus::Expired, __LINE__);
    CHECK(!transfer.has_transfer(key));
}

void test_concurrent_sessions() {
    BlockTransfer transfer;
    constexpr std::size_t thread_count = 8;
    constexpr std::size_t block_size = 7;
    std::vector<Bytes> bodies;
    std::vector<BlockKey> keys;
    std::vector<std::vector<Block>> blocks;
    bodies.reserve(thread_count);
    keys.reserve(thread_count);
    blocks.reserve(thread_count);

    for (std::size_t i = 0; i < thread_count; ++i) {
        const std::string body_text =
            "session-" + std::to_string(i) + ": concurrent block payload";
        bodies.push_back(bytes(body_text));
        keys.push_back(make_key("/concurrent", "concurrent-" + std::to_string(i),
                                block_size, bodies.back().size(),
                                "etag-concurrent"));
        blocks.push_back(
            make_blocks(keys.back(), bodies.back(), bytes("etag-concurrent")));
    }

    std::atomic<bool> go{false};
    std::atomic<bool> failed{false};
    std::vector<std::thread> threads;
    threads.reserve(thread_count);

    for (std::size_t i = 0; i < thread_count; ++i) {
        threads.emplace_back([&, i] {
            while (!go.load(std::memory_order_acquire)) {
                std::this_thread::yield();
            }
            BlockResult final;
            for (const auto& block : blocks[i]) {
                final = transfer.receive(block);
            }
            if (final.status != BlockStatus::Complete ||
                final.body != bodies[i]) {
                failed.store(true, std::memory_order_release);
            }
        });
    }

    go.store(true, std::memory_order_release);
    for (auto& thread : threads) {
        thread.join();
    }
    CHECK(!failed.load(std::memory_order_acquire));

    for (std::size_t i = 0; i < thread_count; ++i) {
        const auto body = transfer.body(keys[i]);
        CHECK(body.has_value());
        CHECK(*body == bodies[i]);
    }
}

void test_download_server_never_mixes_requests() {
    const Bytes body_a = bytes("AAAAAAAAAAAAAAAAAAAA");
    const Bytes body_b = bytes("BBBBBBBBBBBBBBBBBBBB");
    const Bytes etag_a = bytes("download-etag-a");
    const Bytes etag_b = bytes("download-etag-b");
    const BlockKey key_a = make_key("/download/a", "session-down", 4,
                                    body_a.size(), "download-etag-a");
    const BlockKey key_b = make_key("/download/b", "session-down", 4,
                                    body_b.size(), "download-etag-b");

    Representation rep_a{body_a, etag_a, 50};
    Representation rep_b{body_b, etag_b, 50};

    BlockRequest request_a0;
    request_a0.key = key_a;
    request_a0.sequence = 0;
    BlockRequest request_a1;
    request_a1.key = key_a;
    request_a1.sequence = 1;
    BlockRequest request_b0;
    request_b0.key = key_b;
    request_b0.sequence = 0;
    BlockRequest request_b1;
    request_b1.key = key_b;
    request_b1.sequence = 1;

    BlockServer server;
    const BlockReply a0 = server.serve(request_a0, rep_a);
    check_reply(a0, BlockStatus::Accepted, __LINE__);
    CHECK(a0.payload == Bytes(body_a.begin(), body_a.begin() + 4));
    CHECK(a0.more);

    // Same token/session, changed URI, block 1: reject instead of serving A's
    // next block.
    check_reply(server.serve(request_b1, rep_b), BlockStatus::ContextMismatch,
                __LINE__);

    const BlockReply b0 = server.serve(request_b0, rep_b);
    check_reply(b0, BlockStatus::Accepted, __LINE__);
    CHECK(b0.payload == Bytes(body_b.begin(), body_b.begin() + 4));
    CHECK(b0.payload != Bytes(body_a.begin() + 4, body_a.begin() + 8));

    const BlockReply b1 = server.serve(request_b1, rep_b);
    check_reply(b1, BlockStatus::Accepted, __LINE__);
    CHECK(b1.payload == Bytes(body_b.begin() + 4, body_b.begin() + 8));

    BlockServerOptions restart_options;
    restart_options.restart_on_context_mismatch = true;
    BlockServer restarting(restart_options);
    check_reply(restarting.serve(request_a0, rep_a), BlockStatus::Accepted,
                __LINE__);
    const BlockReply restarted_b0 = restarting.serve(request_b0, rep_b);
    check_reply(restarted_b0, BlockStatus::Accepted, __LINE__);
    CHECK(restarted_b0.restarted);
    CHECK(restarted_b0.payload == Bytes(body_b.begin(), body_b.begin() + 4));

    // Retransmission of the first block is answered from request B only.
    const BlockReply duplicate = restarting.serve(request_b0, rep_b);
    check_reply(duplicate, BlockStatus::Duplicate, __LINE__);
    CHECK(duplicate.payload == Bytes(body_b.begin(), body_b.begin() + 4));

    BlockRequest conditional = request_b1;
    conditional.etag = bytes("wrong-client-etag");
    check_reply(restarting.serve(conditional, rep_b),
                BlockStatus::EtagMismatch, __LINE__);
}

}  // namespace

int main() {
    test_legal_continuation_and_duplicates();
    test_nonzero_start_and_stale_discard();
    test_token_reuse_rejects_and_restarts_cleanly();
    test_each_binding_change_is_rejected();
    test_sequence_gap_does_not_append();
    test_etag_mismatch_discards_state();
    test_length_must_match_etag_metadata();
    test_sessions_do_not_share_cache();
    test_observations_are_separate_from_block_state();
    test_expiry_discards_block_state();
    test_concurrent_sessions();
    test_download_server_never_mixes_requests();
    std::cout << "all block transfer tests passed\n";
    return 0;
}
