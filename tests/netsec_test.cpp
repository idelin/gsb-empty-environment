#include "netsec.hpp"

#include <algorithm>
#include <cstdint>
#include <iostream>
#include <stdexcept>
#include <string>
#include <unordered_set>
#include <vector>

namespace {

int failures = 0;

void expect(bool condition, const std::string& message) {
    if (!condition) {
        std::cerr << "FAIL: " << message << '\n';
        ++failures;
    }
}

netsec::AdminCommandRequest request(std::uint8_t opcode,
                                    netsec::NamespaceId nsid,
                                    std::uint8_t log_page_id = 0) {
    netsec::AdminCommandRequest result;
    result.opcode = opcode;
    result.nsid = nsid;
    result.log_page_id = log_page_id;
    return result;
}

std::vector<netsec::NamespaceId> expectedTenantNamespaces() {
    return {2, 5, 9};
}

std::unordered_set<std::uint8_t> allowedOpcodes() {
    return {
        netsec::admin_opcode::kDeleteIoSubmissionQueue,
        netsec::admin_opcode::kCreateIoSubmissionQueue,
        netsec::admin_opcode::kGetLogPage,
        netsec::admin_opcode::kDeleteIoCompletionQueue,
        netsec::admin_opcode::kCreateIoCompletionQueue,
        netsec::admin_opcode::kIdentify,
        netsec::admin_opcode::kGetFeatures,
    };
}

void testEveryOpcode() {
    netsec::AdminQueueGate gate(expectedTenantNamespaces());
    const auto allowed = allowedOpcodes();

    for (std::uint16_t raw = 0; raw <= 0xFF; ++raw) {
        const auto opcode = static_cast<std::uint8_t>(raw);
        const auto decision = gate.evaluate(
            request(opcode, 2, netsec::log_page::kSmartHealthInformation));

        expect(decision.allowed == (allowed.count(opcode) != 0),
               "unexpected decision for admin opcode " +
                   std::to_string(opcode));
    }
}

void testSensitiveOpcodesAreRejected() {
    netsec::AdminQueueGate gate(expectedTenantNamespaces());
    const std::vector<std::uint8_t> sensitive = {
        netsec::admin_opcode::kFormatNvm,
        netsec::admin_opcode::kSanitize,
        netsec::admin_opcode::kFirmwareCommit,
        netsec::admin_opcode::kFirmwareImageDownload,
        netsec::admin_opcode::kSecuritySend,
        netsec::admin_opcode::kSecurityReceive,
        netsec::admin_opcode::kNamespaceManagement,
    };

    for (std::uint8_t opcode : sensitive) {
        const auto decision = gate.evaluate(request(opcode, 2));
        expect(!decision.allowed,
               "sensitive opcode must be rejected: " +
                   std::to_string(opcode));
        expect(decision.reason == netsec::GateReason::kOpcodeNotAllowed,
               "sensitive opcode should use opcode-not-allowed reason");
        expect(decision.target_namespaces.empty(),
               "rejected command must not expose targets");
    }
}

void testAllVendorOpcodesAreRejected() {
    netsec::AdminQueueGate gate(expectedTenantNamespaces());

    for (std::uint16_t raw = netsec::admin_opcode::kVendorSpecificFirst;
         raw <= netsec::admin_opcode::kVendorSpecificLast; ++raw) {
        const auto opcode = static_cast<std::uint8_t>(raw);
        const auto decision = gate.evaluate(
            request(opcode, netsec::kBroadcastNsid));
        expect(!decision.allowed,
               "vendor opcode must be rejected: " + std::to_string(opcode));
        expect(decision.reason == netsec::GateReason::kVendorSpecificOpcode,
               "vendor opcode should use vendor-specific reason");
        expect(decision.target_namespaces.empty(),
               "vendor opcode must not expand broadcast NSID");
    }

    const auto c0 = gate.evaluate(
        request(0xC0, netsec::kBroadcastNsid));
    expect(!c0.allowed, "0xC0 must be rejected");
}

void testLogPageWhitelist() {
    netsec::AdminQueueGate gate(expectedTenantNamespaces());

    for (std::uint8_t page : netsec::AdminQueueGate::defaultLogPageWhitelist()) {
        const auto decision = gate.evaluate(
            request(netsec::admin_opcode::kGetLogPage, 2, page));
        expect(decision.allowed,
               "whitelisted log page must be accepted: " +
                   std::to_string(page));
        expect(decision.target_namespaces == std::vector<netsec::NamespaceId>{2},
               "specific NSID should be retained as target");
    }

    for (std::uint8_t page : {std::uint8_t{0x04}, std::uint8_t{0x06},
                              std::uint8_t{0x07}, std::uint8_t{0xFF}}) {
        const auto decision = gate.evaluate(
            request(netsec::admin_opcode::kGetLogPage, 2, page));
        expect(!decision.allowed,
               "non-whitelisted log page must be rejected: " +
                   std::to_string(page));
        expect(decision.reason == netsec::GateReason::kLogPageNotWhitelisted,
               "log page rejection reason mismatch");
    }

    netsec::AdminQueueGate custom_gate(
        expectedTenantNamespaces(), {std::uint8_t{0x04}});
    expect(custom_gate.allows(
               request(netsec::admin_opcode::kGetLogPage, 2, 0x04)),
           "caller-supplied log page whitelist should be honored");
    expect(!custom_gate.allows(
               request(netsec::admin_opcode::kGetLogPage, 2,
                       netsec::log_page::kSmartHealthInformation)),
           "caller-supplied whitelist should replace the default");
}

void testBroadcastNamespaceExpansion() {
    netsec::AdminQueueGate gate({9, 2, 5, 2, 9});

    const auto identify = gate.evaluate(
        request(netsec::admin_opcode::kIdentify, netsec::kBroadcastNsid));
    expect(identify.allowed, "Identify with broadcast NSID should be allowed");
    expect(identify.target_namespaces == expectedTenantNamespaces(),
           "broadcast NSID must expand only to concrete tenant namespaces");

    for (std::uint8_t opcode : allowedOpcodes()) {
        const auto decision = gate.evaluate(
            request(opcode, netsec::kBroadcastNsid,
                    netsec::log_page::kSmartHealthInformation));
        expect(decision.allowed,
               "allowed opcode with broadcast should be accepted");
        expect(decision.target_namespaces == expectedTenantNamespaces(),
               "every allowed opcode with broadcast must use tenant set");
    }

    const auto foreign = gate.evaluate(
        request(netsec::admin_opcode::kIdentify, 7));
    expect(!foreign.allowed, "non-tenant concrete NSID must be rejected");
    expect(foreign.reason ==
               netsec::GateReason::kNamespaceNotTenantOwned,
           "foreign namespace rejection reason mismatch");
    expect(foreign.target_namespaces.empty(),
           "foreign namespace must not become a target");

    const auto controller = gate.evaluate(
        request(netsec::admin_opcode::kIdentify, netsec::kControllerNsid));
    expect(controller.allowed, "controller-scoped NSID 0 should be allowed");
    expect(controller.target_namespaces.empty(),
           "NSID 0 should not inject namespace targets");
}

void testBroadcastWithEmptyTenantSet() {
    netsec::AdminQueueGate gate({});
    const auto decision = gate.evaluate(
        request(netsec::admin_opcode::kIdentify, netsec::kBroadcastNsid));
    expect(!decision.allowed,
           "broadcast with no tenant namespaces must fail closed");
    expect(decision.reason ==
               netsec::GateReason::kBroadcastWithoutTenantNamespaces,
           "empty broadcast rejection reason mismatch");
    expect(decision.target_namespaces.empty(),
           "empty broadcast must not produce namespace targets");
}

void testSensitiveBroadcastDoesNotExpandOrMutate() {
    netsec::AdminQueueGate gate(expectedTenantNamespaces());

    for (std::uint8_t opcode :
         {netsec::admin_opcode::kFormatNvm,
          netsec::admin_opcode::kSanitize,
          netsec::admin_opcode::kFirmwareImageDownload,
          netsec::admin_opcode::kSecuritySend,
          netsec::admin_opcode::kNamespaceManagement,
          std::uint8_t{0xC0}}) {
        const auto decision = gate.evaluate(
            request(opcode, netsec::kBroadcastNsid));
        expect(!decision.allowed,
               "broadcast sensitive command must be rejected");
        expect(decision.target_namespaces.empty(),
               "broadcast sensitive command must not expand tenant set");
    }

    const auto non_whitelisted_log = gate.evaluate(
        request(netsec::admin_opcode::kGetLogPage, netsec::kBroadcastNsid,
                std::uint8_t{0x04}));
    expect(!non_whitelisted_log.allowed,
           "non-whitelisted broadcast log page must be rejected");
    expect(non_whitelisted_log.target_namespaces.empty(),
           "rejected log page must not expand tenant set");
}

void testInvalidTenantConfigurationIsRejected() {
    try {
        netsec::AdminQueueGate gate({1, netsec::kBroadcastNsid});
        expect(false, "broadcast NSID in tenant set should throw");
    } catch (const std::invalid_argument&) {
    }

    try {
        netsec::AdminQueueGate gate({1, netsec::kControllerNsid});
        expect(false, "controller NSID in tenant set should throw");
    } catch (const std::invalid_argument&) {
    }
}

}  // namespace

int main() {
    testEveryOpcode();
    testSensitiveOpcodesAreRejected();
    testAllVendorOpcodesAreRejected();
    testLogPageWhitelist();
    testBroadcastNamespaceExpansion();
    testBroadcastWithEmptyTenantSet();
    testSensitiveBroadcastDoesNotExpandOrMutate();
    testInvalidTenantConfigurationIsRejected();

    if (failures != 0) {
        std::cerr << failures << " test expectation(s) failed\n";
        return 1;
    }

    std::cout << "all netsec tests passed\n";
    return 0;
}
