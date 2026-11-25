#include "certrev/reference.hpp"

#include <iostream>
#include <random>
#include <sstream>

using namespace certrev;

namespace {

int g_failures = 0;
int g_checks = 0;

void check(bool condition, const std::string& expression, const char* file, int line) {
    ++g_checks;
    if (!condition) {
        ++g_failures;
        std::cerr << "CHECK failed: " << expression << " (" << file << ':' << line << ")\n";
    }
}

template <class A, class B>
void checkEq(const A& actual, const B& expected, const std::string& expression,
             const char* file, int line) {
    ++g_checks;
    if (!(actual == static_cast<A>(expected))) {
        ++g_failures;
        std::cerr << "CHECK_EQ failed: " << expression << " (" << file << ':' << line << ")\n";
    }
}

#define CHECK(condition) check(static_cast<bool>(condition), #condition, __FILE__, __LINE__)
#define CHECK_EQ(actual, expected) checkEq((actual), (expected), #actual " == " #expected, __FILE__, __LINE__)

SlotRange R(SlotIndex first, SlotIndex limit) { return SlotRange{first, limit}; }

MeasurementClaim claim(Id id, SlotRange physical, Digest digest,
                       VerificationState state = VerificationState::Verified) {
    MeasurementClaim result;
    result.id = std::move(id);
    result.physical = physical;
    result.digest = std::move(digest);
    result.proof_state = state;
    return result;
}

LogicalMapping mapping(Id version, std::vector<MappingSegment> segments) {
    LogicalMapping result;
    result.version = std::move(version);
    result.segments = std::move(segments);
    result.verification = VerificationState::Verified;
    return result;
}

Receipt receipt(Id id, Id signer, Generation generation, Digest root, LogicalMapping map,
                std::vector<MeasurementClaim> claims,
                VerificationState signature = VerificationState::Verified) {
    Receipt result;
    result.id = std::move(id);
    result.signer = std::move(signer);
    result.generation = generation;
    result.proof_root = std::move(root);
    result.mapping = std::move(map);
    result.claims = std::move(claims);
    result.signature_state = signature;
    SlotIndex first = result.claims.front().physical.first;
    SlotIndex limit = result.claims.front().physical.limit;
    for (const MeasurementClaim& item : result.claims) {
        first = std::min(first, item.physical.first);
        limit = std::max(limit, item.physical.limit);
    }
    result.physical_range = {first, limit};
    return result;
}

AdmissionTicket ticket(Id id, Generation generation, std::vector<Requirement> requirements,
                       std::optional<Id> mapping_version = std::nullopt) {
    AdmissionTicket result;
    result.id = std::move(id);
    result.generation = generation;
    result.requirements = std::move(requirements);
    result.mapping_version = std::move(mapping_version);
    return result;
}

const Decision* requireDecision(const EvaluationReport& report, const Id& id) {
    const Decision* decision = report.find(id);
    CHECK(decision != nullptr);
    return decision;
}

bool containsId(const std::vector<Id>& ids, const Id& wanted) {
    return std::find(ids.begin(), ids.end(), wanted) != ids.end();
}

void checkMatchesReference(const RevocationComponent& component) {
    Evaluator evaluator(component.bank(), component.revocations());
    for (const AdmissionTicket& ticket : component.bank().tickets) {
        Decision actual = evaluator.evaluateTicket(ticket);
        reference::ExpandedResult expected =
            reference::fullyExpandedEvaluate(ticket, component.bank(), component.revocations());
        checkEq(actual.status, expected.status, "curated scenario reference status",
                __FILE__, __LINE__);
    }
}

void testSharedAttestationRevocation() {
    RevocationComponent component;
    LogicalMapping m = mapping("m-shared", {{R(0, 2), R(10, 12)}});
    component.addReceipt(receipt(
        "r-shared", "signer-a", 1, "root-shared", m,
        {claim("c0", R(10, 11), "h0"), claim("c1", R(11, 12), "h1")}));
    component.addReceipt(receipt("r-other", "signer-b", 1, "root-other",
                                 mapping("m-other", {{R(0, 1), R(20, 21)}}),
                                 {claim("c2", R(20, 21), "h2")}));

    component.addTicket(ticket("t1", 1, {Requirement::logical("a", R(0, 1), "h0")}, "m-shared"));
    component.addTicket(ticket("t2", 1, {Requirement::logical("b", R(1, 2), "h1")}, "m-shared"));
    component.addTicket(ticket("t3", 1, {Requirement::logical("c", R(0, 1), "h2")}, "m-other"));

    EvaluationReport before = component.evaluate();
    CHECK_EQ(requireDecision(before, "t1")->status, TicketStatus::Kept);
    CHECK_EQ(requireDecision(before, "t2")->status, TicketStatus::Kept);
    CHECK_EQ(requireDecision(before, "t3")->status, TicketStatus::Kept);

    component.prime();
    component.revocations().revokeReceipt("r-shared", "rev-shared-1");
    TicketDiff diff = component.reevaluate();

    CHECK_EQ(diff.changes.size(), 2u);
    CHECK(diff.support_updated.empty());
    CHECK(containsId(diff.newly_withdrawn, "t1"));
    CHECK(containsId(diff.newly_withdrawn, "t2"));
    CHECK(!containsId(diff.newly_withdrawn, "t3"));
    CHECK_EQ(diff.remaining_support.size(), 1u);
    CHECK_EQ(diff.remaining_support[0].ticket_id, Id("t3"));
    CHECK(containsId(diff.definitively_revoked, "t1"));
    CHECK(containsId(diff.definitively_revoked, "t2"));

    EvaluationReport after = component.evaluate();
    const Decision* t1 = requireDecision(after, "t1");
    const Decision* t3 = requireDecision(after, "t3");
    CHECK_EQ(t1->status, TicketStatus::Revoked);
    CHECK_EQ(t3->status, TicketStatus::Kept);
    CHECK_EQ(t3->support.receipt_ids.size(), 1u);
    CHECK_EQ(t3->support.receipt_ids[0], Id("r-other"));

    BundleVerificationResult verified =
        verifySupportBundle(t3->support, component.revocations());
    CHECK(verified.valid);
    checkMatchesReference(component);
}

void testPartialPhysicalRange() {
    RevocationComponent component;
    LogicalMapping identity = mapping("m-bank", {{R(0, 8), R(0, 8)}});
    component.addReceipt(receipt(
        "r-bank", "bank-signer", 1, "root-bank", identity,
        {claim("left", R(0, 4), "h-left"), claim("right", R(4, 8), "h-right")}));

    component.addTicket(ticket("t-left", 1,
                               {Requirement::logical("left", R(0, 4), "h-left")}, "m-bank"));
    component.addTicket(ticket("t-right", 1,
                               {Requirement::logical("right", R(4, 8), "h-right")}, "m-bank"));
    component.addTicket(ticket("t-all", 1,
                               {Requirement::logical("left", R(0, 4), "h-left"),
                                Requirement::logical("right", R(4, 8), "h-right")},
                               "m-bank"));

    component.revocations().revokeRange(R(4, 5), 1, "suspect slot", "range-4");
    TicketDiff diff = component.reevaluate();

    EvaluationReport report = component.evaluate();
    const Decision* left = requireDecision(report, "t-left");
    const Decision* right = requireDecision(report, "t-right");
    const Decision* all = requireDecision(report, "t-all");

    CHECK_EQ(left->status, TicketStatus::Kept);
    CHECK_EQ(right->status, TicketStatus::Revoked);
    CHECK_EQ(all->status, TicketStatus::Revoked);
    CHECK_EQ(left->support.receipt_ids.size(), 1u);
    CHECK_EQ(left->support.physical_union.size(), 1u);
    CHECK(left->support.physical_union[0] == R(0, 4));
    CHECK(containsId(diff.newly_withdrawn, "t-right"));
    CHECK(containsId(diff.newly_withdrawn, "t-all"));
    CHECK(!containsId(diff.newly_withdrawn, "t-left"));

    BundleVerificationResult verified =
        verifySupportBundle(left->support, component.revocations());
    CHECK(verified.valid);

    bool found_range_blocker = false;
    for (const Blocker& blocker : right->blockers) {
        if (blocker.code == "physical_range_revoked" && blocker.notice_id == "range-4") {
            found_range_blocker = true;
        }
    }
    CHECK(found_range_blocker);
    checkMatchesReference(component);
}

void testSignerRestorationGenerationBoundary() {
    RevocationComponent component;
    LogicalMapping m1 = mapping("m1", {{R(0, 1), R(10, 11)}});
    LogicalMapping m2 = mapping("m2", {{R(0, 1), R(20, 21)}});

    component.addReceipt(receipt("r-old", "ca", 1, "root-old", m1,
                                 {claim("c-old", R(10, 11), "h-old")}));
    component.addReceipt(receipt("r-new", "ca", 2, "root-new", m2,
                                 {claim("c-new", R(20, 21), "h-new")}));
    component.addTicket(ticket("t-old", 1,
                               {Requirement::logical("q", R(0, 1), "h-old")}, "m1"));
    component.addTicket(ticket("t-new", 2,
                               {Requirement::logical("q", R(0, 1), "h-new")}, "m2"));

    component.revocations().revokeSigner("ca");
    component.revocations().restoreSigner("ca", 2);

    CHECK(component.revocations().isSignerRevoked("ca", 1));
    CHECK(!component.revocations().isSignerRevoked("ca", 2));

    TicketDiff diff = component.reevaluate();
    EvaluationReport report = component.evaluate();
    CHECK_EQ(requireDecision(report, "t-old")->status, TicketStatus::Revoked);
    CHECK_EQ(requireDecision(report, "t-new")->status, TicketStatus::Kept);
    CHECK_EQ(diff.newly_withdrawn.size(), 1u);
    CHECK_EQ(diff.newly_withdrawn[0], Id("t-old"));
    checkMatchesReference(component);
}

void testUnknownConverges() {
    RevocationComponent component;
    LogicalMapping m = mapping("m", {{R(0, 2), R(10, 12)}});
    component.addReceipt(receipt("r-good", "s", 1, "root-good", m,
                                 {claim("c-good", R(10, 11), "h-good")},
                                 VerificationState::Unverified));
    component.addReceipt(receipt("r-bad", "s", 1, "root-bad", m,
                                 {claim("c-bad", R(11, 12), "h-bad")},
                                 VerificationState::Unverified));
    component.addTicket(ticket("t-good", 1,
                               {Requirement::logical("q", R(0, 1), "h-good")}, "m"));
    component.addTicket(ticket("t-bad", 1,
                               {Requirement::logical("q2", R(1, 2), "h-bad")}, "m"));

    TicketDiff first = component.reevaluate();
    CHECK_EQ(first.changes.size(), 2u);
    CHECK(containsId(first.newly_pending, "t-good"));
    CHECK(containsId(first.newly_pending, "t-bad"));

    for (Receipt& item : component.bank().receipts) {
        if (item.id == "r-good") item.signature_state = VerificationState::Verified;
        if (item.id == "r-bad") item.signature_state = VerificationState::Invalid;
    }

    TicketDiff second = component.reevaluate();
    EvaluationReport report = component.evaluate();
    CHECK_EQ(requireDecision(report, "t-good")->status, TicketStatus::Kept);
    CHECK_EQ(requireDecision(report, "t-bad")->status, TicketStatus::Revoked);
    CHECK(containsId(second.restored_to_kept, "t-good"));
    CHECK(containsId(second.definitively_revoked, "t-bad"));

    const Decision* good = requireDecision(report, "t-good");
    BundleVerificationResult verified =
        verifySupportBundle(good->support, component.revocations());
    CHECK(verified.valid);
    checkMatchesReference(component);
}

void testAliasesDoNotCreatePhysicalSlots() {
    RevocationComponent component;
    LogicalMapping alias_map = mapping("m-alias", {
        {R(0, 1), R(7, 8)},
        {R(100, 101), R(7, 8)},
    });
    component.addReceipt(receipt("r-physical", "s", 1, "root-p", alias_map,
                                 {claim("c", R(7, 8), "same-h")}));
    component.addTicket(ticket("t-alias", 1,
                               {Requirement::logical("first-alias", R(0, 1), "same-h"),
                                Requirement::logical("second-alias", R(100, 101), "same-h")},
                               "m-alias"));

    EvaluationReport report = component.evaluate();
    const Decision* decision = requireDecision(report, "t-alias");
    CHECK_EQ(decision->status, TicketStatus::Kept);
    CHECK_EQ(decision->support.receipt_ids.size(), 1u);
    CHECK_EQ(decision->support.receipt_ids[0], Id("r-physical"));
    CHECK_EQ(decision->support.measurements.size(), 2u);
    CHECK(decision->support.physical_union.size() == 1u);
    CHECK(decision->support.physical_union[0] == R(7, 8));

    BundleVerificationResult verified =
        verifySupportBundle(decision->support, component.revocations());
    CHECK(verified.valid);
    checkMatchesReference(component);
}

void testCannotMixForksItemByItem() {
    RevocationComponent component;
    LogicalMapping m = mapping("m-fork", {{R(0, 2), R(0, 2)}});
    component.addReceipt(receipt("r-x", "s", 1, "root-x", m,
                                 {claim("c0", R(0, 1), "h0")}));
    component.addReceipt(receipt("r-y", "s", 1, "root-y", m,
                                 {claim("c0", R(0, 1), "h0")}));
    component.addReceipt(receipt("r-z", "s", 1, "root-z", m,
                                 {claim("c1", R(1, 2), "h1")}));
    component.addReceipt(receipt("r-w", "s", 1, "root-w", m,
                                 {claim("c1", R(1, 2), "h1")}));
    component.addTicket(ticket("t-fork", 1,
                               {Requirement::logical("m0", R(0, 1), "h0"),
                                Requirement::logical("m1", R(1, 2), "h1")},
                               "m-fork"));

    EvaluationReport report = component.evaluate();
    const Decision* decision = requireDecision(report, "t-fork");
    CHECK_EQ(decision->status, TicketStatus::Revoked);
    CHECK(!decision->incompatibilities.empty());
    checkMatchesReference(component);
}

void testMappingVersionRevocationIsScoped() {
    RevocationComponent component;
    LogicalMapping m = mapping("m-v1", {{R(100, 101), R(7, 8)}});
    component.addReceipt(receipt("r-p7", "s", 1, "root", m,
                                 {claim("c", R(7, 8), "h")}));
    component.addTicket(ticket("t-logical", 1,
                               {Requirement::logical("q", R(100, 101), "h")}, "m-v1"));
    component.addTicket(ticket("t-physical", 1,
                               {Requirement::physical("q", R(7, 8), "h")}));

    CHECK_EQ(component.evaluate().decisions[0].status, TicketStatus::Kept);
    component.revocations().revokeMappingVersion("m-v1", 1, "map-v1-bad");
    TicketDiff diff = component.reevaluate();
    EvaluationReport report = component.evaluate();

    CHECK_EQ(requireDecision(report, "t-logical")->status, TicketStatus::Revoked);
    CHECK_EQ(requireDecision(report, "t-physical")->status, TicketStatus::Kept);
    CHECK(containsId(diff.newly_withdrawn, "t-logical"));
    CHECK(!containsId(diff.newly_withdrawn, "t-physical"));
    checkMatchesReference(component);
}

void testSharedProofRootRevocation() {
    RevocationComponent component;
    LogicalMapping m = mapping("m-root", {{R(0, 2), R(0, 2)}});
    component.addReceipt(receipt("r-a", "s", 1, "shared-root", m,
                                 {claim("c0", R(0, 1), "h0")}));
    component.addReceipt(receipt("r-b", "s", 1, "shared-root", m,
                                 {claim("c1", R(1, 2), "h1")}));
    component.addTicket(ticket("t", 1,
                               {Requirement::logical("q0", R(0, 1), "h0"),
                                Requirement::logical("q1", R(1, 2), "h1")},
                               "m-root"));
    CHECK_EQ(component.evaluate().decisions[0].status, TicketStatus::Kept);
    component.revocations().revokeProofRoot("shared-root", 1, "root-bad");
    CHECK_EQ(component.evaluate().decisions[0].status, TicketStatus::Revoked);
    checkMatchesReference(component);
}

void testRandomBanksAgainstFullyExpandedReference() {
    std::mt19937_64 rng(0x10528B4ULL);
    for (int iteration = 0; iteration < 500; ++iteration) {
        Bank bank;
        RevocationBook book;
        LogicalMapping common = mapping("m", {{R(0, 4), R(0, 4)}});

        int receipt_count = static_cast<int>(rng() % 7);
        for (int i = 0; i < receipt_count; ++i) {
            std::vector<MeasurementClaim> claims;
            for (SlotIndex slot = 0; slot < 4; ++slot) {
                if ((rng() % 100) < 45 || claims.empty()) {
                    VerificationState proof_state = VerificationState::Verified;
                    if (rng() % 10 == 0) proof_state = VerificationState::Unverified;
                    if (rng() % 20 == 0) proof_state = VerificationState::Invalid;
                    claims.push_back(claim("claim-" + std::to_string(i) + "-" +
                                              std::to_string(slot),
                                          R(slot, slot + 1),
                                          "h" + std::to_string(slot), proof_state));
                }
            }
            VerificationState signature_state = VerificationState::Verified;
            if (rng() % 8 == 0) signature_state = VerificationState::Unverified;
            if (rng() % 16 == 0) signature_state = VerificationState::Invalid;
            bank.receipts.push_back(receipt("r" + std::to_string(i),
                                            (rng() % 2 == 0 ? "s0" : "s1"), 1,
                                            "root" + std::to_string(rng() % 3), common,
                                            std::move(claims), signature_state));
        }

        AdmissionTicket random_ticket = ticket("random-ticket", 1, {}, "m");
        int requirement_count = 1 + static_cast<int>(rng() % 3);
        for (int i = 0; i < requirement_count; ++i) {
            SlotIndex slot = static_cast<SlotIndex>(rng() % 4);
            random_ticket.requirements.push_back(Requirement::logical(
                "q" + std::to_string(i), R(slot, slot + 1), "h" + std::to_string(slot)));
        }
        bank.tickets.push_back(random_ticket);

        if (rng() % 4 == 0) {
            if (rng() % 2 == 0) book.revokeSigner("s0", 1, "rand-signer");
            else book.revokeSigner("s0", std::nullopt, "rand-signer-all");
        }
        if (rng() % 4 == 0) {
            SlotIndex slot = static_cast<SlotIndex>(rng() % 4);
            book.revokeRange(R(slot, slot + 1), 1, {}, "rand-range");
        }
        if (receipt_count > 0 && rng() % 5 == 0) {
            book.revokeReceipt("r" + std::to_string(rng() % receipt_count), "rand-receipt");
        }
        if (rng() % 5 == 0) {
            book.revokeProofRoot("root" + std::to_string(rng() % 3), 1, "rand-root");
        }
        if (rng() % 20 == 0) book.revokeMappingVersion("m", 1, "rand-map");

        Decision actual = Evaluator(bank, book).evaluateTicket(random_ticket);
        reference::ExpandedResult expected =
            reference::fullyExpandedEvaluate(random_ticket, bank, book);

        ++g_checks;
        if (actual.status != expected.status) {
            ++g_failures;
            std::cerr << "reference mismatch iteration " << iteration << ": actual="
                      << toString(actual.status) << " expected=" << toString(expected.status)
                      << " receipts=" << bank.receipts.size() << '\n';
        }

        if (actual.status == TicketStatus::Kept) {
            BundleVerificationResult verified =
                verifySupportBundle(actual.support, book);
            ++g_checks;
            if (!verified.valid) {
                ++g_failures;
                std::cerr << "bundle verification failed iteration " << iteration << '\n';
                for (const std::string& error : verified.errors) {
                    std::cerr << "  " << error << '\n';
                }
            }
        }
    }
}

} // namespace

int main() {
    testSharedAttestationRevocation();
    testPartialPhysicalRange();
    testSignerRestorationGenerationBoundary();
    testUnknownConverges();
    testAliasesDoNotCreatePhysicalSlots();
    testCannotMixForksItemByItem();
    testMappingVersionRevocationIsScoped();
    testSharedProofRootRevocation();
    testRandomBanksAgainstFullyExpandedReference();

    if (g_failures != 0) {
        std::cerr << g_failures << " failure(s) in " << g_checks << " checks\n";
        return 1;
    }
    std::cout << "all revocation tests passed (" << g_checks << " checks)\n";
    return 0;
}
