#pragma once

#include <array>
#include <cstddef>
#include <cstdint>
#include <map>
#include <optional>
#include <set>
#include <stdexcept>
#include <string>
#include <string_view>
#include <utility>
#include <vector>

namespace netsec::radius {

using Octets = std::vector<std::uint8_t>;

inline constexpr std::uint8_t kAccessRequest = 1;
inline constexpr std::uint8_t kAccessAccept = 2;
inline constexpr std::uint8_t kAccessReject = 3;
inline constexpr std::uint8_t kAccountingRequest = 4;
inline constexpr std::uint8_t kAccountingResponse = 5;
inline constexpr std::uint8_t kAccessChallenge = 11;

inline constexpr std::uint8_t kStateAttribute = 24;
inline constexpr std::uint8_t kClassAttribute = 25;
inline constexpr std::uint8_t kVendorSpecificAttribute = 26;
inline constexpr std::uint8_t kNasIdentifierAttribute = 32;
inline constexpr std::uint8_t kProxyStateAttribute = 33;
inline constexpr std::uint8_t kMessageAuthenticatorAttribute = 80;
inline constexpr std::uint8_t kOperatorNameAttribute = 126;

class RadiusError : public std::runtime_error {
public:
    using std::runtime_error::runtime_error;
};

class ParseError : public RadiusError {
public:
    using RadiusError::RadiusError;
};

class AuthenticationError : public RadiusError {
public:
    using RadiusError::RadiusError;
};

class TrustError : public RadiusError {
public:
    using RadiusError::RadiusError;
};

class ChainError : public RadiusError {
public:
    using RadiusError::RadiusError;
};

struct Attribute {
    std::uint8_t type{};
    Octets value;

    friend bool operator==(const Attribute&, const Attribute&) = default;
};

struct VendorAttribute {
    std::uint32_t vendor_id{};
    std::uint8_t type{};
    Octets value;

    friend bool operator==(const VendorAttribute&, const VendorAttribute&) = default;
};

struct AttributeRules {
    std::set<std::uint8_t> repeatable_standard_types{18, 25, 33, 79};
    std::set<std::pair<std::uint32_t, std::uint8_t>> single_value_vendor_attributes;
    bool strict_standard_lengths{true};
    bool reject_duplicate_single_value{true};

    static AttributeRules secure_defaults();
};

struct ParseOptions {
    AttributeRules rules{};
    bool require_exact_packet_length{true};
};

struct Message {
    std::uint8_t code{};
    std::uint8_t identifier{};
    Octets authenticator;
    std::vector<Attribute> attributes;

    static Message parse(const Octets& wire, const ParseOptions& options = {});

    Octets serialize() const;
    Octets serialize(const AttributeRules& rules) const;
    std::vector<Attribute> attributes_of_type(std::uint8_t type) const;
    std::size_t count_of_type(std::uint8_t type) const;
};

void validate_attribute(const Attribute& attribute, const AttributeRules& rules = {});
void validate_message(const Message& message, const AttributeRules& rules = {});

std::optional<VendorAttribute> decode_vendor_specific(const Attribute& attribute);
Attribute encode_vendor_specific(const VendorAttribute& attribute);

bool is_operator_attribute(const Attribute& attribute);
std::vector<Attribute> operator_attributes(const std::vector<Attribute>& attributes);

Octets secure_random(std::size_t size);

struct SharedKey {
    std::string peer;
    std::uint64_t generation{};
    Octets secret;
    bool active{true};
};

class KeyRing {
public:
    void install(std::string peer, std::uint64_t generation, Octets secret,
                 bool active = true);
    void activate(std::string_view peer, std::uint64_t generation);
    void retire(std::string_view peer, std::uint64_t generation);

    [[nodiscard]] const SharedKey* find(std::string_view peer,
                                        std::uint64_t generation) const;
    [[nodiscard]] const SharedKey& require_active(std::string_view peer,
                                                  std::uint64_t generation) const;
    [[nodiscard]] bool contains(std::string_view peer,
                                std::uint64_t generation) const;

private:
    std::map<std::string, std::map<std::uint64_t, SharedKey>> keys_;
};

struct SourceIdentity {
    std::string nas_identifier;
    std::uint64_t key_generation{};
    std::string roaming_peer;

    friend bool operator==(const SourceIdentity&, const SourceIdentity&) = default;
};

struct PolicyRoute {
    SourceIdentity source;
    std::string home_server;
    std::string billing_group;
    std::vector<Attribute> operator_attributes;

    std::string next_hop;
    std::uint64_t signing_generation{};
};

std::array<std::uint8_t, 16> identity_hash(std::string_view identity);
std::array<std::uint8_t, 16> route_id_hash(const PolicyRoute& route);

enum class IngressKind {
    Nas,
    Proxy,
};

struct SourceBinding {
    std::string transport_peer;
    IngressKind kind{IngressKind::Nas};
    std::string nas_identifier;
    std::uint64_t key_generation{};
    std::string roaming_peer;
};

struct IngressInfo {
    std::string transport_peer;
    bool transport_authenticated{false};
};

struct ProxyStateRecord {
    std::uint64_t signing_generation{};
    std::uint64_t origin_key_generation{};
    std::array<std::uint8_t, 16> nonce{};
    std::array<std::uint8_t, 16> request_digest{};
    std::array<std::uint8_t, 32> upstream_chain_digest{};
    std::array<std::uint8_t, 16> route_id{};
    std::array<std::uint8_t, 16> nas_identifier_hash{};
    std::array<std::uint8_t, 16> roaming_peer_hash{};
};

Octets encode_proxy_state(const ProxyStateRecord& record, const Octets& secret);
ProxyStateRecord decode_proxy_state(const Octets& value, const Octets& secret);
std::uint64_t proxy_state_signing_generation(const Octets& value);

std::array<std::uint8_t, 32> chain_digest(const std::vector<Octets>& values);
std::array<std::uint8_t, 16> request_digest(const Message& message,
                                            const AttributeRules& rules = {});

struct ForwardedRequest {
    Octets wire;
    Message forwarded;

    std::vector<Attribute> user_writable_attributes;
    std::vector<Attribute> proxy_generated_attributes;
    std::vector<Octets> proxy_state_chain;
    Octets generated_proxy_state;

    ProxyStateRecord state;
    SourceIdentity origin;
    PolicyRoute route;

    std::string upstream_peer;
    std::uint64_t upstream_generation{};
    Octets upstream_request_authenticator;
    Octets forwarded_request_authenticator;

    bool source_was_nas{false};
    std::size_t untrusted_proxy_state_count{};
};

struct VerifiedResponse {
    Message message;
    Octets upstream_wire;
    ProxyStateRecord state;
};

class ProxyEngine {
public:
    ProxyEngine(std::string local_peer, KeyRing keys,
                std::vector<SourceBinding> sources,
                std::vector<PolicyRoute> routes,
                AttributeRules rules = AttributeRules::secure_defaults());

    [[nodiscard]] ForwardedRequest forward(const Octets& request,
                                           const IngressInfo& ingress) const;
    [[nodiscard]] VerifiedResponse accept_response(
        const Octets& response, const ForwardedRequest& request) const;

    [[nodiscard]] const KeyRing& keys() const noexcept;
    [[nodiscard]] const std::vector<PolicyRoute>& routes() const noexcept;

private:
    [[nodiscard]] const SourceBinding* find_source(
        std::string_view transport_peer, IngressKind kind,
        std::string_view nas_identifier = {}) const;
    [[nodiscard]] const PolicyRoute* find_route(
        const SourceIdentity& identity) const;
    [[nodiscard]] const PolicyRoute* find_route_by_hashes(
        const ProxyStateRecord& state) const;

    std::string local_peer_;
    KeyRing keys_;
    std::vector<SourceBinding> sources_;
    std::vector<PolicyRoute> routes_;
    AttributeRules rules_;
};

struct PolicyDecision {
    bool accepted{false};
    std::string reason;

    std::string home_server;
    std::string billing_group;
    std::string nas_identifier;
    std::string roaming_peer;
    std::uint64_t origin_key_generation{};
    std::uint64_t signing_generation{};
    std::vector<Attribute> trusted_operator_attributes;
};

struct VerifiedRequest {
    Message message;
    ProxyStateRecord state;
    PolicyRoute route;
};

class HomeServer {
public:
    HomeServer(std::string home_server, KeyRing keys,
               std::vector<PolicyRoute> policies,
               AttributeRules rules = AttributeRules::secure_defaults());

    [[nodiscard]] PolicyDecision evaluate(const Octets& request,
                                          const IngressInfo& ingress) const;
    [[nodiscard]] PolicyDecision authorize(const Octets& request,
                                           const IngressInfo& ingress) const;
    [[nodiscard]] PolicyDecision account(const Octets& request,
                                         const IngressInfo& ingress) const;

    [[nodiscard]] VerifiedRequest verify_request(
        const Octets& request, const IngressInfo& ingress) const;

    [[nodiscard]] Octets respond(const Octets& request,
                                 const IngressInfo& ingress,
                                 std::uint8_t response_code,
                                 const std::vector<Attribute>& attributes = {}) const;

    [[nodiscard]] const std::string& home_server() const noexcept;

private:
    [[nodiscard]] PolicyDecision decide(const VerifiedRequest& verified) const;
    [[nodiscard]] const PolicyRoute* find_route(
        const ProxyStateRecord& state) const;

    std::string home_server_;
    KeyRing keys_;
    std::vector<PolicyRoute> policies_;
    AttributeRules rules_;
};

void compute_accounting_request_authenticator(
    Message& request, const Octets& secret, const AttributeRules& rules = {});
[[nodiscard]] bool verify_accounting_request_authenticator(
    const Message& request, const Octets& secret,
    const AttributeRules& rules = {});

void apply_message_authenticator(Message& message, const Octets& secret,
                                 const AttributeRules& rules = {});
[[nodiscard]] bool verify_message_authenticator(
    const Message& message, const Octets& secret,
    const AttributeRules& rules = {});

void seal_response_authenticator(Message& response,
                                 const Octets& request_authenticator,
                                 const Octets& secret,
                                 const AttributeRules& rules = {});
[[nodiscard]] bool verify_response_authenticator(
    const Message& response, const Octets& request_authenticator,
    const Octets& secret, const AttributeRules& rules = {});

}  // namespace netsec::radius
