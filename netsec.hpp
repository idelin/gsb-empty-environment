#pragma once

#include <chrono>
#include <cstdint>
#include <functional>
#include <memory>
#include <optional>
#include <string>
#include <unordered_map>
#include <unordered_set>
#include <vector>

namespace netsec {

using TenantId = std::string;
using ResolverId = std::string;
using PolicyGeneration = std::uint64_t;
using Clock = std::chrono::steady_clock;

// A connection is bound to (resolver identity, tenant, SNI, policy
// generation). Two keys that differ in any field must never share a
// session, so cross-tenant reuse is impossible by construction.
struct ConnectionKey {
    ResolverId resolver_id;
    TenantId tenant_id;
    std::string sni;
    PolicyGeneration policy_generation = 0;
    bool operator==(const ConnectionKey&) const = default;
};

struct DnsQuestion {
    std::string qname;  // normalized to lowercase by the connection
    std::uint16_t qtype = 1;   // A
    std::uint16_t qclass = 1;  // IN
    bool operator==(const DnsQuestion&) const = default;
};

struct EdnsOption {
    std::uint16_t code = 0;
    std::vector<std::uint8_t> data;
    // Set by the gateway when the option payload was derived from
    // downstream client identity (client IP, tenant tag, ...).
    bool carries_identity = false;
    bool operator==(const EdnsOption&) const = default;
};

struct Query {
    DnsQuestion question;
    std::vector<EdnsOption> edns_options;
};

struct Answer {
    std::vector<std::uint8_t> rdata;  // opaque upstream payload
    bool operator==(const Answer&) const = default;
};

enum class QueryError {
    None,
    IdSpaceExhausted,
    TooManyInFlight,
    Timeout,
    ConnectionClosed,
};

struct QueryResult {
    bool ok = false;
    Answer answer{};
    QueryError error = QueryError::None;
    bool from_cache = false;
};

// Removes every EDNS option that could carry downstream identity:
// options flagged by the gateway, EDNS Client Subnet (8), and the
// private/experimental range 65001-65534.
std::vector<EdnsOption> sanitize_edns(const std::vector<EdnsOption>& options);

// Transport abstraction: the gateway owns the real TLS session I/O and
// plugs it in here. The component never opens sockets itself.
class ITransport {
public:
    virtual ~ITransport() = default;
    virtual bool is_open() const = 0;
    virtual void close() = 0;
    virtual void send(std::uint16_t wire_id, const DnsQuestion& question,
                      const std::vector<EdnsOption>& options) = 0;
};

using TransportFactory = std::function<std::unique_ptr<ITransport>()>;

// Per-tenant response cache. Entries live and die with the tenant's
// context; there is no global cache, so answers cannot leak sideways.
class ResponseCache {
public:
    void put(const DnsQuestion& question, Answer answer,
             Clock::time_point expiry);
    std::optional<Answer> get(const DnsQuestion& question,
                              Clock::time_point now) const;
    std::size_t size() const;

private:
    struct KeyHash {
        std::size_t operator()(const DnsQuestion& q) const noexcept;
    };
    struct Entry {
        Answer answer;
        Clock::time_point expiry;
    };
    std::unordered_map<DnsQuestion, Entry, KeyHash> entries_;
};

struct TenantPolicy {
    std::chrono::milliseconds query_timeout{5000};
    std::chrono::milliseconds cache_ttl{300000};
    std::size_t max_in_flight = 1024;
};

// Everything tenant-scoped (policy, cache) lives here. Timeouts and
// cached answers are therefore isolated per tenant.
class TenantContext {
public:
    explicit TenantContext(TenantPolicy policy) : policy_(policy) {}
    const TenantPolicy& policy() const { return policy_; }
    ResponseCache& cache() { return cache_; }

private:
    TenantPolicy policy_;
    ResponseCache cache_;
};

class TenantRegistry {
public:
    std::shared_ptr<TenantContext> get_or_create(
        const TenantId& tenant, TenantPolicy policy = {});
    std::size_t size() const { return contexts_.size(); }

private:
    std::unordered_map<TenantId, std::shared_ptr<TenantContext>> contexts_;
};

// One upstream DoT session bound to a ConnectionKey. Query IDs are
// unique among in-flight queries on this connection, and responses are
// accepted only when both ID and question match a pending query.
class UpstreamConnection {
public:
    using Callback = std::function<void(const QueryResult&)>;

    UpstreamConnection(ConnectionKey key, std::shared_ptr<TenantContext> tenant,
                       TransportFactory factory);

    const ConnectionKey& key() const { return key_; }
    TenantId tenant() const { return key_.tenant_id; }

    // Submits a query. Serves from the tenant cache when possible,
    // otherwise allocates a unique wire ID, strips identity-bearing
    // EDNS options, and sends on the current transport.
    void submit(Query query, Callback cb,
                Clock::time_point now = Clock::now());

    // Feeds a response from the wire. Returns true only if the ID is
    // pending and the question matches; mismatches are rejected and
    // the pending query is left untouched.
    bool on_response(std::uint16_t wire_id, const DnsQuestion& question,
                     Answer answer, Clock::time_point now = Clock::now());

    // Expires timed-out queries (per-tenant timeout).
    void tick(Clock::time_point now = Clock::now());

    // Called by the gateway when the TLS session drops. Fails all
    // in-flight queries and establishes a fresh transport. Pending
    // queries are never replayed onto a session another tenant could
    // observe; callers must resubmit.
    void on_transport_closed();

    std::size_t in_flight() const { return pending_.size(); }
    std::size_t reconnect_count() const { return reconnect_count_; }
    bool is_open() const;

private:
    struct Pending {
        DnsQuestion question;
        Callback cb;
        Clock::time_point deadline;
    };

    std::uint16_t allocate_id();
    void connect();

    ConnectionKey key_;
    std::shared_ptr<TenantContext> tenant_;
    TransportFactory factory_;
    std::unique_ptr<ITransport> transport_;
    std::unordered_map<std::uint16_t, Pending> pending_;
    std::uint16_t next_id_ = 0;
    std::size_t reconnect_count_ = 0;
};

struct ConnectionKeyHash {
    std::size_t operator()(const ConnectionKey& k) const noexcept;
};

// Hands out connections strictly by ConnectionKey. Any difference in
// resolver, tenant, SNI, or policy generation yields a distinct
// connection, so sessions are never shared across tenants.
class ConnectionPool {
public:
    ConnectionPool(TransportFactory factory,
                   std::shared_ptr<TenantRegistry> tenants)
        : factory_(std::move(factory)), tenants_(std::move(tenants)) {}

    std::shared_ptr<UpstreamConnection> acquire(
        const ConnectionKey& key,
        TenantPolicy policy = {});

    std::size_t size() const { return connections_.size(); }

private:
    TransportFactory factory_;
    std::shared_ptr<TenantRegistry> tenants_;
    std::unordered_map<ConnectionKey, std::shared_ptr<UpstreamConnection>,
                       ConnectionKeyHash>
        connections_;
};

}  // namespace netsec
