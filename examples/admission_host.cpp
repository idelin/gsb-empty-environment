#include "certrev/revocation.hpp"

#include <iostream>

using namespace certrev;

int main() {
    RevocationComponent component;

    LogicalMapping bank_mapping{
        "bank-v3",
        "mapping-root-1",
        {
            {{0, 4}, {0, 4}},
            {{100, 104}, {0, 4}},
        },
        VerificationState::Verified,
    };

    Receipt receipt;
    receipt.id = "receipt-bank";
    receipt.signer = "measurement-ca";
    receipt.generation = 7;
    receipt.proof_root = "complete-root-7";
    receipt.mapping = bank_mapping;
    receipt.signature_state = VerificationState::Verified;
    receipt.claims = {
        {"slot-0-2", {0, 2}, "digest-0-2", {}, VerificationState::Verified},
        {"slot-2-4", {2, 4}, "digest-2-4", {}, VerificationState::Verified},
    };
    receipt.physical_range = {0, 4};
    component.addReceipt(std::move(receipt));

    AdmissionTicket ticket;
    ticket.id = "admission-ticket-42";
    ticket.generation = 7;
    ticket.mapping_version = "bank-v3";
    ticket.requirements = {
        Requirement::logical("primary-alias", {0, 2}, "digest-0-2"),
        Requirement::logical("repartition-alias", {100, 102}, "digest-0-2"),
        Requirement::logical("other-half", {2, 4}, "digest-2-4"),
    };
    component.addTicket(std::move(ticket));

    AdmissionTicket kept_ticket;
    kept_ticket.id = "admission-ticket-unaffected";
    kept_ticket.generation = 7;
    kept_ticket.mapping_version = "bank-v3";
    kept_ticket.requirements = {
        Requirement::logical("primary-alias", {0, 2}, "digest-0-2"),
        Requirement::logical("repartition-alias", {100, 102}, "digest-0-2"),
    };
    component.addTicket(std::move(kept_ticket));
    component.prime();

    component.revocations().revokeRange({2, 3}, 7, "one suspect physical slot");
    TicketDiff diff = component.reevaluate();

    std::cout << "changed tickets: " << diff.changes.size() << '\n';
    for (const Id& id : diff.newly_withdrawn) {
        std::cout << "withdraw/suspend: " << id << '\n';
    }

    int exit_code = diff.changes.empty() ? 1 : 0;
    EvaluationReport report = component.evaluate();
    for (const Decision& decision : report.decisions) {
        std::cout << decision.ticket_id << ": " << toString(decision.status) << '\n';
        if (decision.status == TicketStatus::Kept) {
            BundleVerificationResult verification =
                verifySupportBundle(decision.support, component.revocations());
            std::cout << "independent support verification: "
                      << (verification.valid ? "ok" : "failed") << '\n';
            if (!verification.valid) exit_code = 2;
        }
    }
    return exit_code;
}
