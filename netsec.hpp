#pragma once

#include <cstdint>
#include <unordered_set>
#include <vector>

namespace netsec {

using NamespaceId = std::uint32_t;

inline constexpr NamespaceId kControllerNsid = 0x00000000u;
inline constexpr NamespaceId kBroadcastNsid = 0xFFFFFFFFu;

namespace admin_opcode {

inline constexpr std::uint8_t kDeleteIoSubmissionQueue = 0x00;
inline constexpr std::uint8_t kCreateIoSubmissionQueue = 0x01;
inline constexpr std::uint8_t kGetLogPage = 0x02;
inline constexpr std::uint8_t kDeleteIoCompletionQueue = 0x04;
inline constexpr std::uint8_t kCreateIoCompletionQueue = 0x05;
inline constexpr std::uint8_t kIdentify = 0x06;
inline constexpr std::uint8_t kGetFeatures = 0x0A;
inline constexpr std::uint8_t kNamespaceManagement = 0x0D;
inline constexpr std::uint8_t kFirmwareCommit = 0x10;
inline constexpr std::uint8_t kFirmwareImageDownload = 0x11;
inline constexpr std::uint8_t kFormatNvm = 0x80;
inline constexpr std::uint8_t kSecuritySend = 0x81;
inline constexpr std::uint8_t kSecurityReceive = 0x82;
inline constexpr std::uint8_t kSanitize = 0x84;
inline constexpr std::uint8_t kVendorSpecificFirst = 0xC0;
inline constexpr std::uint8_t kVendorSpecificLast = 0xFF;

}  // namespace admin_opcode

namespace log_page {

inline constexpr std::uint8_t kSupportedLogPages = 0x00;
inline constexpr std::uint8_t kErrorInformation = 0x01;
inline constexpr std::uint8_t kSmartHealthInformation = 0x02;
inline constexpr std::uint8_t kFirmwareSlotInformation = 0x03;
inline constexpr std::uint8_t kCommandsSupportedAndEffects = 0x05;

}  // namespace log_page

struct AdminCommandRequest {
    std::uint8_t opcode = 0;
    NamespaceId nsid = kControllerNsid;
    std::uint8_t log_page_id = 0;
};

enum class GateReason {
    kAllowed,
    kOpcodeNotAllowed,
    kVendorSpecificOpcode,
    kLogPageNotWhitelisted,
    kNamespaceNotTenantOwned,
    kBroadcastWithoutTenantNamespaces,
};

struct GateDecision {
    bool allowed = false;
    GateReason reason = GateReason::kOpcodeNotAllowed;
    std::vector<NamespaceId> target_namespaces;

    explicit operator bool() const noexcept { return allowed; }
};

class AdminQueueGate {
public:
    explicit AdminQueueGate(
        std::vector<NamespaceId> tenant_namespaces,
        std::unordered_set<std::uint8_t> log_page_whitelist =
            defaultLogPageWhitelist());

    GateDecision evaluate(const AdminCommandRequest& request) const;

    bool allows(const AdminCommandRequest& request) const;

    const std::vector<NamespaceId>& tenantNamespaces() const noexcept {
        return tenant_namespaces_;
    }

    bool ownsNamespace(NamespaceId nsid) const noexcept;

    std::vector<NamespaceId> expandBroadcast() const noexcept {
        return tenant_namespaces_;
    }

    static const std::unordered_set<std::uint8_t>& defaultLogPageWhitelist();

private:
    std::vector<NamespaceId> tenant_namespaces_;
    std::unordered_set<NamespaceId> tenant_namespace_set_;
    std::unordered_set<std::uint8_t> log_page_whitelist_;
};

using AdminOpcodeGate = AdminQueueGate;

GateDecision authorizeAdminCommand(const AdminQueueGate& gate,
                                  const AdminCommandRequest& request);

}  // namespace netsec
