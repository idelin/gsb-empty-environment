#include "netsec.hpp"

#include <cstdlib>
#include <iostream>
#include <set>
#include <string>

namespace {

int g_failures = 0;
int g_checks = 0;

#define CHECK(cond)                                                  \
    do {                                                             \
        ++g_checks;                                                  \
        if (!(cond)) {                                               \
            ++g_failures;                                            \
            std::cerr << "FAIL " << __FILE__ << ':' << __LINE__      \
                      << ": " #cond << std::endl;                    \
        }                                                            \
    } while (0)

struct SentFrame {
    std::uint16_t id;
    netsec::DnsQuestion question;
    std::vector<netsec::EdnsOption> options;
};

struct MockTransport : netsec::ITransport {
    bool open = true;
    int close_calls = 0;
    std::vector<SentFrame> sent;

    bool is_open() const override { return open; }
    void close() override {
        open = false;
        ++close_calls;
    }
    void send(std::uint16_t wire_id, const netsec::DnsQuestion& q,
              const std::vector<netsec::EdnsOption>& opts) override {
        sent.push_back(SentFrame{wire_id, q, opts});
    }
};

// Factory that hands out fresh mock transports and keeps them
// reachable for inspection.
struct MockFactory {
    std::vector<MockTransport*> created;

    netsec::TransportFactory fn() {
        return [this]() {
            auto t = std::make_unique<MockTransport>();
            created.push_back(t.get());
            return t;
        };
    }
};

netsec::Query make_query(const std::string& name) {
    netsec::Query q;
    q.question.qname = name;
    return q;
}

netsec::Answer make_answer(std::uint8_t tag) {
    return netsec::Answer{std::vector<std::uint8_t>{tag}};
}

struct Captured {
    std::vector<netsec::QueryResult> results;
    netsec::UpstreamConnection::Callback cb() {
        return [this](const netsec::QueryResult& r) {
            results.push_back(r);
        };
    }
};

// 1. Cross-tenant reuse: connections are keyed by (resolver, tenant,
//    SNI, policy generation); differing tenants never share a session.
void test_cross_tenant_no_reuse() {
    MockFactory factory;
    auto tenants = std::make_shared<netsec::TenantRegistry>();
    netsec::ConnectionPool pool(factory.fn(), tenants);

    auto a1 = pool.acquire({"resolver-1", "tenant-a", "dns.example", 1});
    auto a2 = pool.acquire({"resolver-1", "tenant-a", "dns.example", 1});
    auto b = pool.acquire({"resolver-1", "tenant-b", "dns.example", 1});
    auto other_sni = pool.acquire({"resolver-1", "tenant-a", "dns2.example", 1});
    auto other_gen = pool.acquire({"resolver-1", "tenant-a", "dns.example", 2});

    CHECK(a1 == a2);             // identical key: legitimate reuse
    CHECK(a1 != b);              // different tenant: never shared
    CHECK(a1 != other_sni);      // different SNI: never shared
    CHECK(a1 != other_gen);      // different policy generation: never shared
    CHECK(pool.size() == 4);
    CHECK(b->tenant() == "tenant-b");
}

// 2. ID uniqueness and conflict handling within one connection.
void test_query_id_conflicts() {
    MockFactory factory;
    auto tenants = std::make_shared<netsec::TenantRegistry>();
    netsec::ConnectionPool pool(factory.fn(), tenants);
    auto conn = pool.acquire({"resolver-1", "tenant-a", "dns.example", 1});
    auto* transport = factory.created.back();

    std::set<std::uint16_t> ids;
    std::vector<Captured> waiters(64);
    for (int i = 0; i < 64; ++i) {
        conn->submit(make_query("q" + std::to_string(i) + ".example"),
                     waiters[i].cb());
    }
    for (const auto& frame : transport->sent) {
        CHECK(ids.insert(frame.id).second);  // no duplicate in-flight IDs
    }
    CHECK(conn->in_flight() == 64);

    // Response with an unknown ID is rejected.
    CHECK(!conn->on_response(65000, {"zzz.example", 1, 1}, make_answer(1)));

    // Response with a valid ID but the wrong question is rejected and
    // the pending query stays untouched.
    auto first = transport->sent.front();
    CHECK(!conn->on_response(first.id, {"wrong.example", 1, 1},
                             make_answer(2)));
    CHECK(waiters[0].results.empty());
    CHECK(conn->in_flight() == 64);

    // Correct ID + question pair is accepted.
    CHECK(conn->on_response(first.id, first.question, make_answer(3)));
    CHECK(waiters[0].results.size() == 1);
    CHECK(waiters[0].results[0].ok);
    CHECK(waiters[0].results[0].answer == make_answer(3));
    CHECK(conn->in_flight() == 63);
}

// 3. Cache isolation: tenant B must not observe tenant A's cached
//    answers; tenant A gets a real cache hit.
void test_cache_isolation() {
    MockFactory factory;
    auto tenants = std::make_shared<netsec::TenantRegistry>();
    netsec::ConnectionPool pool(factory.fn(), tenants);
    auto conn_a = pool.acquire({"resolver-1", "tenant-a", "dns.example", 1});
    auto conn_b = pool.acquire({"resolver-1", "tenant-b", "dns.example", 1});
    auto* transport_a = factory.created[0];
    auto* transport_b = factory.created[1];

    Captured a1, b1, a2;
    conn_a->submit(make_query("shared.example"), a1.cb());
    CHECK(transport_a->sent.size() == 1);
    auto frame = transport_a->sent.front();
    CHECK(conn_a->on_response(frame.id, frame.question, make_answer(0xA)));
    CHECK(a1.results.size() == 1);

    // Tenant B asks the same name: must go upstream (cache miss).
    conn_b->submit(make_query("shared.example"), b1.cb());
    CHECK(transport_b->sent.size() == 1);
    CHECK(b1.results.empty());  // not answered from tenant A's cache

    // Tenant A asks again: served from its own cache, no wire traffic.
    conn_a->submit(make_query("shared.example"), a2.cb());
    CHECK(transport_a->sent.size() == 1);
    CHECK(a2.results.size() == 1);
    CHECK(a2.results[0].ok);
    CHECK(a2.results[0].from_cache);
    CHECK(a2.results[0].answer == make_answer(0xA));
}

// 4. Legitimate same-tenant queries: distinct IDs, responses routed to
//    the right waiter by (ID, question).
void test_same_tenant_queries() {
    MockFactory factory;
    auto tenants = std::make_shared<netsec::TenantRegistry>();
    netsec::ConnectionPool pool(factory.fn(), tenants);
    auto conn = pool.acquire({"resolver-1", "tenant-a", "dns.example", 1});
    auto* transport = factory.created.back();

    Captured w1, w2;
    conn->submit(make_query("one.example"), w1.cb());
    conn->submit(make_query("two.example"), w2.cb());
    CHECK(transport->sent.size() == 2);
    CHECK(transport->sent[0].id != transport->sent[1].id);

    // Answer out of order; each waiter gets exactly its own answer.
    CHECK(conn->on_response(transport->sent[1].id,
                            transport->sent[1].question, make_answer(0x22)));
    CHECK(conn->on_response(transport->sent[0].id,
                            transport->sent[0].question, make_answer(0x11)));
    CHECK(w1.results.size() == 1 && w1.results[0].answer == make_answer(0x11));
    CHECK(w2.results.size() == 1 && w2.results[0].answer == make_answer(0x22));

    // Case-insensitive question matching (DNS names fold to lowercase).
    Captured w3;
    conn->submit(make_query("MiXeD.example"), w3.cb());
    auto frame = transport->sent.back();
    CHECK(frame.question.qname == "mixed.example");
    CHECK(conn->on_response(frame.id, {"MIXED.example", 1, 1},
                            make_answer(0x33)));
    CHECK(w3.results.size() == 1);
}

// 5. Timeout and reconnect: per-tenant timeout fires, reconnect fails
//    in-flight queries and swaps the transport; new queries still work.
void test_timeout_and_reconnect() {
    MockFactory factory;
    auto tenants = std::make_shared<netsec::TenantRegistry>();
    netsec::TenantPolicy policy;
    policy.query_timeout = std::chrono::milliseconds(100);
    netsec::ConnectionPool pool(factory.fn(), tenants);
    auto conn = pool.acquire({"resolver-1", "tenant-a", "dns.example", 1},
                             policy);
    auto* transport = factory.created.back();

    auto t0 = netsec::Clock::now();
    Captured w1;
    conn->submit(make_query("slow.example"), w1.cb(), t0);
    CHECK(conn->in_flight() == 1);

    conn->tick(t0 + std::chrono::milliseconds(50));
    CHECK(w1.results.empty());  // not yet timed out
    conn->tick(t0 + std::chrono::milliseconds(150));
    CHECK(w1.results.size() == 1);
    CHECK(!w1.results[0].ok);
    CHECK(w1.results[0].error == netsec::QueryError::Timeout);
    CHECK(conn->in_flight() == 0);

    // In-flight query when the session drops: failed, not replayed.
    Captured w2;
    conn->submit(make_query("dropped.example"), w2.cb(), t0);
    CHECK(conn->in_flight() == 1);
    transport->open = false;
    conn->on_transport_closed();
    CHECK(w2.results.size() == 1);
    CHECK(w2.results[0].error == netsec::QueryError::ConnectionClosed);
    CHECK(conn->reconnect_count() == 1);
    CHECK(factory.created.size() == 2);  // fresh transport established
    CHECK(conn->is_open());

    // After reconnect the connection keeps serving its own tenant.
    Captured w3;
    conn->submit(make_query("after.example"), w3.cb(), t0);
    auto* new_transport = factory.created.back();
    CHECK(new_transport->sent.size() == 1);
    CHECK(conn->on_response(new_transport->sent[0].id,
                            new_transport->sent[0].question,
                            make_answer(0x44), t0));
    CHECK(w3.results.size() == 1 && w3.results[0].ok);
}

// 6. EDNS identity stripping: nothing derived from downstream identity
//    reaches the upstream wire.
void test_edns_identity_stripped() {
    MockFactory factory;
    auto tenants = std::make_shared<netsec::TenantRegistry>();
    netsec::ConnectionPool pool(factory.fn(), tenants);
    auto conn = pool.acquire({"resolver-1", "tenant-a", "dns.example", 1});
    auto* transport = factory.created.back();

    netsec::Query q = make_query("plain.example");
    q.edns_options = {
        {8, {192, 0, 2, 1}, false},        // EDNS Client Subnet
        {65001, {'t', '1'}, false},        // private range
        {4000, {'x'}, true},               // flagged by gateway
        {10, {1, 2, 3, 4}, false},         // COOKIE: safe, kept
    };
    Captured w;
    conn->submit(q, w.cb());

    CHECK(transport->sent.size() == 1);
    const auto& opts = transport->sent[0].options;
    CHECK(opts.size() == 1);
    CHECK(opts[0].code == 10);
}

}  // namespace

int main() {
    test_cross_tenant_no_reuse();
    test_query_id_conflicts();
    test_cache_isolation();
    test_same_tenant_queries();
    test_timeout_and_reconnect();
    test_edns_identity_stripped();

    std::cout << (g_checks - g_failures) << '/' << g_checks
              << " checks passed" << std::endl;
    return g_failures == 0 ? EXIT_SUCCESS : EXIT_FAILURE;
}
