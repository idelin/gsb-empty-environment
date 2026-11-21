#include "rdpauth/types.hpp"

namespace rdp::auth {

const char* to_string(RejectReason reason) noexcept {
    switch (reason) {
    case RejectReason::none: return "none";
    case RejectReason::nla_required_downgrade: return "nla_required_downgrade";
    case RejectReason::weak_auth_not_allowed: return "weak_auth_not_allowed";
    case RejectReason::client_not_in_isolated_network: return "client_not_in_isolated_network";
    case RejectReason::protocol_version_rollback: return "protocol_version_rollback";
    case RejectReason::certificate_mismatch: return "certificate_mismatch";
    }
    return "unknown";
}

const char* to_string(AuthProtocol protocol) noexcept {
    switch (protocol) {
    case AuthProtocol::legacy_rdp: return "legacy_rdp";
    case AuthProtocol::tls: return "tls";
    case AuthProtocol::credssp: return "credssp";
    }
    return "unknown";
}

} // namespace rdp::auth
