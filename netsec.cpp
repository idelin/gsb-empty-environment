#include "netsec.hpp"

#include <cctype>
#include <stdexcept>

namespace netsec {

namespace {

constexpr std::uint16_t kEdnsClientSubnet = 8;
constexpr std::uint16_t kEdnsPrivateRangeBegin = 65001;

std::string normalize_qname(std::string name) {
    for (char& c : name) {
        c = static_cast<char>(
            std::tolower(static_cast<unsigned char>(c)));
    }
    return name;
}

void hash_combine(std::size_t& seed, std::size_t value) noexcept {
    seed ^= value + 0x9e3779b97f4a7c15ULL + (seed << 6) + (seed >> 2);
}

}  // namespace

std::vector<EdnsOption> sanitize_edns(
    const std::vector<EdnsOption>& options) {
    std::vector<EdnsOption> clean;
    clean.reserve(options.size());
    for (const auto& opt : options) {
        if (opt.carries_identity) continue;
        if (opt.code == kEdnsClientSubnet) continue;
        if (opt.code >= kEdnsPrivateRangeBegin) continue;
        clean.push_back(opt);
    }
    return clean;
}

std::size_t ResponseCache::KeyHash::operator()(
    const DnsQuestion& q) const noexcept {
    std::size_t seed = std::hash<std::string>{}(q.qname);
    hash_combine(seed, q.qtype);
    hash_combine(seed, q.qclass);
    return seed;
}

void ResponseCache::put(const DnsQuestion& question, Answer answer,
                        Clock::time_point expiry) {
    entries_[question] = Entry{std::move(answer), expiry};
}

std::optional<Answer> ResponseCache::get(
    const DnsQuestion& question, Clock::time_point now) const {
    auto it = entries_.find(question);
    if (it == entries_.end()) return std::nullopt;
    if (now >= it->second.expiry) return std::nullopt;
    return it->second.answer;
}

std::size_t ResponseCache::size() const { return entries_.size(); }

std::shared_ptr<TenantContext> TenantRegistry::get_or_create(
    const TenantId& tenant, TenantPolicy policy) {
    auto it = contexts_.find(tenant);
    if (it != contexts_.end()) return it->second;
    auto ctx = std::make_shared<TenantContext>(policy);
    contexts_.emplace(tenant, ctx);
    return ctx;
}

UpstreamConnection::UpstreamConnection(ConnectionKey key,
                                       std::shared_ptr<TenantContext> tenant,
                                       TransportFactory factory)
    : key_(std::move(key)),
      tenant_(std::move(tenant)),
      factory_(std::move(factory)) {
    if (!tenant_) throw std::invalid_argument("tenant context required");
    connect();
}

void UpstreamConnection::connect() {
    transport_ = factory_ ? factory_() : nullptr;
}

bool UpstreamConnection::is_open() const {
    return transport_ && transport_->is_open();
}

std::uint16_t UpstreamConnection::allocate_id() {
    for (std::uint32_t attempt = 0; attempt < 65536; ++attempt) {
        std::uint16_t candidate =
            static_cast<std::uint16_t>(next_id_ + attempt);
        if (pending_.find(candidate) == pending_.end()) {
            next_id_ = static_cast<std::uint16_t>(candidate + 1);
            return candidate;
        }
    }
    throw std::runtime_error("query ID space exhausted");
}

void UpstreamConnection::submit(Query query, Callback cb,
                                Clock::time_point now) {
    query.question.qname = normalize_qname(query.question.qname);

    // Tenant-scoped cache: a hit never touches the wire and can only
    // return data this tenant previously fetched.
    if (auto cached = tenant_->cache().get(query.question, now)) {
        cb(QueryResult{true, *cached, QueryError::None, true});
        return;
    }

    if (!is_open()) {
        cb(QueryResult{false, {}, QueryError::ConnectionClosed, false});
        return;
    }
    if (pending_.size() >= tenant_->policy().max_in_flight) {
        cb(QueryResult{false, {}, QueryError::TooManyInFlight, false});
        return;
    }

    std::uint16_t wire_id;
    try {
        wire_id = allocate_id();
    } catch (...) {
        cb(QueryResult{false, {}, QueryError::IdSpaceExhausted, false});
        return;
    }

    Pending pending;
    pending.question = query.question;
    pending.cb = std::move(cb);
    pending.deadline = now + tenant_->policy().query_timeout;
    pending_.emplace(wire_id, std::move(pending));

    transport_->send(wire_id, query.question,
                     sanitize_edns(query.edns_options));
}

bool UpstreamConnection::on_response(std::uint16_t wire_id,
                                     const DnsQuestion& question,
                                     Answer answer, Clock::time_point now) {
    auto it = pending_.find(wire_id);
    if (it == pending_.end()) return false;

    DnsQuestion normalized = question;
    normalized.qname = normalize_qname(normalized.qname);
    if (!(normalized == it->second.question)) return false;

    Pending pending = std::move(it->second);
    pending_.erase(it);

    tenant_->cache().put(pending.question, answer,
                         now + tenant_->policy().cache_ttl);
    pending.cb(QueryResult{true, std::move(answer), QueryError::None, false});
    return true;
}

void UpstreamConnection::tick(Clock::time_point now) {
    std::vector<std::uint16_t> expired;
    for (const auto& [id, pending] : pending_) {
        if (now >= pending.deadline) expired.push_back(id);
    }
    for (std::uint16_t id : expired) {
        auto it = pending_.find(id);
        Callback cb = std::move(it->second.cb);
        pending_.erase(it);
        cb(QueryResult{false, {}, QueryError::Timeout, false});
    }
}

void UpstreamConnection::on_transport_closed() {
    if (transport_) transport_->close();
    // Fail in-flight queries; they belong to this tenant's session and
    // must not be replayed onto the replacement session implicitly.
    auto pending = std::move(pending_);
    pending_.clear();
    for (auto& [id, p] : pending) {
        p.cb(QueryResult{false, {}, QueryError::ConnectionClosed, false});
    }
    ++reconnect_count_;
    connect();
}

std::size_t ConnectionKeyHash::operator()(
    const ConnectionKey& k) const noexcept {
    std::size_t seed = std::hash<std::string>{}(k.resolver_id);
    hash_combine(seed, std::hash<std::string>{}(k.tenant_id));
    hash_combine(seed, std::hash<std::string>{}(k.sni));
    hash_combine(seed, std::hash<PolicyGeneration>{}(k.policy_generation));
    return seed;
}

std::shared_ptr<UpstreamConnection> ConnectionPool::acquire(
    const ConnectionKey& key, TenantPolicy policy) {
    auto it = connections_.find(key);
    if (it != connections_.end()) return it->second;
    auto tenant = tenants_->get_or_create(key.tenant_id, policy);
    auto conn = std::make_shared<UpstreamConnection>(key, tenant, factory_);
    connections_.emplace(key, conn);
    return conn;
}

}  // namespace netsec
