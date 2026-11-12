#include "netsec.hpp"

#include <algorithm>
#include <stdexcept>
#include <utility>

namespace netsec {

namespace {

bool isAllowedOpcode(std::uint8_t opcode) {
    switch (opcode) {
        case admin_opcode::kDeleteIoSubmissionQueue:
        case admin_opcode::kCreateIoSubmissionQueue:
        case admin_opcode::kGetLogPage:
        case admin_opcode::kDeleteIoCompletionQueue:
        case admin_opcode::kCreateIoCompletionQueue:
        case admin_opcode::kIdentify:
        case admin_opcode::kGetFeatures:
            return true;
        default:
            return false;
    }
}

bool isVendorSpecificOpcode(std::uint8_t opcode) {
    return opcode >= admin_opcode::kVendorSpecificFirst &&
           opcode <= admin_opcode::kVendorSpecificLast;
}

GateDecision reject(GateReason reason) {
    GateDecision decision;
    decision.allowed = false;
    decision.reason = reason;
    return decision;
}

}  // namespace

AdminQueueGate::AdminQueueGate(
    std::vector<NamespaceId> tenant_namespaces,
    std::unordered_set<std::uint8_t> log_page_whitelist)
    : tenant_namespaces_(std::move(tenant_namespaces)),
      log_page_whitelist_(std::move(log_page_whitelist)) {
    for (NamespaceId nsid : tenant_namespaces_) {
        if (nsid == kControllerNsid || nsid == kBroadcastNsid) {
            throw std::invalid_argument("tenant namespace IDs must be concrete");
        }
    }

    std::sort(tenant_namespaces_.begin(), tenant_namespaces_.end());
    tenant_namespaces_.erase(
        std::unique(tenant_namespaces_.begin(), tenant_namespaces_.end()),
        tenant_namespaces_.end());
    tenant_namespace_set_.insert(tenant_namespaces_.begin(),
                                 tenant_namespaces_.end());
}

GateDecision AdminQueueGate::evaluate(
    const AdminCommandRequest& request) const {
    if (isVendorSpecificOpcode(request.opcode)) {
        return reject(GateReason::kVendorSpecificOpcode);
    }

    if (!isAllowedOpcode(request.opcode)) {
        return reject(GateReason::kOpcodeNotAllowed);
    }

    if (request.opcode == admin_opcode::kGetLogPage &&
        log_page_whitelist_.find(request.log_page_id) ==
            log_page_whitelist_.end()) {
        return reject(GateReason::kLogPageNotWhitelisted);
    }

    GateDecision decision;
    decision.allowed = true;
    decision.reason = GateReason::kAllowed;

    if (request.nsid == kBroadcastNsid) {
        if (tenant_namespaces_.empty()) {
            return reject(GateReason::kBroadcastWithoutTenantNamespaces);
        }
        decision.target_namespaces = tenant_namespaces_;
        return decision;
    }

    if (request.nsid == kControllerNsid) {
        return decision;
    }

    if (!ownsNamespace(request.nsid)) {
        return reject(GateReason::kNamespaceNotTenantOwned);
    }

    decision.target_namespaces.push_back(request.nsid);
    return decision;
}

bool AdminQueueGate::allows(const AdminCommandRequest& request) const {
    return evaluate(request).allowed;
}

bool AdminQueueGate::ownsNamespace(NamespaceId nsid) const noexcept {
    return tenant_namespace_set_.find(nsid) != tenant_namespace_set_.end();
}

const std::unordered_set<std::uint8_t>&
AdminQueueGate::defaultLogPageWhitelist() {
    static const std::unordered_set<std::uint8_t> whitelist = {
        log_page::kSupportedLogPages,
        log_page::kErrorInformation,
        log_page::kSmartHealthInformation,
        log_page::kFirmwareSlotInformation,
        log_page::kCommandsSupportedAndEffects,
    };
    return whitelist;
}

GateDecision authorizeAdminCommand(const AdminQueueGate& gate,
                                  const AdminCommandRequest& request) {
    return gate.evaluate(request);
}

}  // namespace netsec
