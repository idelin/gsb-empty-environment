#include "borrow/borrow.hpp"

#include <iostream>
#include <string>
#include <vector>

using namespace borrow;

namespace {

HistoryProblem make_problem() {
    HistoryProblem p;
    p.add_object(1, 8, 0);

    Segment s0;
    s0.id = 1;
    s0.label = "setup";
    s0.events = {
        Event::enter(kRootFrame, 10),
        Event::exclusive(10, 1, Range(0, 8)),
        Event::enter(10, 11),
        Event::enter(10, 12),
    };
    p.add_segment(s0);

    Segment gap;
    gap.id = 2;
    gap.summary = "gap-A";
    gap.frame = 11;
    gap.object = 1;
    gap.range = Range(0, 4);
    gap.categories = {"noop", "shared"};
    p.add_gap(gap);
    return p;
}

void print_decision(const char* label, const HistoryCertificate& cert) {
    std::cout << label << ": " << decision_name(cert.decision)
              << "  histories=" << cert.compatible_histories
              << "  allowed=" << cert.common_allowed.str()
              << "  forbidden=" << cert.common_forbidden.str() << "\n";
}

}  // namespace

int main() {
    HistoryProblem p = make_problem();
    HistoryQuery q;
    q.frame = 12;
    q.object = 1;
    q.range = Range(0, 4);
    q.kind = QueryKind::Write;

    HistoryCertificate before = p.solve(q);
    print_decision("before gap fill", before);
    std::cout << "  certificate verifies: "
              << (before.verify(p) ? "yes" : "no") << "\n";

    p.fix_summary("gap-A", "noop");
    HistoryCertificate noop = p.solve(q);
    print_decision("gap = noop", noop);
    std::cout << "  certificate verifies: "
              << (noop.verify(p) ? "yes" : "no") << "\n";

    p.fix_summary("gap-A", "shared");
    HistoryCertificate shared = p.solve(q);
    print_decision("gap = shared", shared);
    std::cout << "  certificate verifies: "
              << (shared.verify(p) ? "yes" : "no") << "\n";

    Authority authority;
    ByteMemory memory(authority);
    ObjectRef ref = memory.create(1, 8);
    FrameId frame = authority.enter();
    if (!authority.acquire(frame, ref, Range(0, 8), BorrowMode::Exclusive)) {
        std::cerr << "demo setup failed\n";
        return 1;
    }
    const std::vector<std::uint8_t> bytes = {1, 2, 3, 4, 5, 6, 7, 8};
    if (!memory.write(frame, ref, Range(0, 8), bytes)) {
        std::cerr << "byte write failed\n";
        return 1;
    }
    if (authority.release(frame, ref, Range(4, 8)) != Status::Ok) {
        std::cerr << "partial release failed\n";
        return 1;
    }
    QueryReport report =
        authority.evaluate(frame, ref, Range(0, 8), QueryKind::Return);
    std::cout << "partial return: " << decision_name(report.decision)
              << "  owned-after=" << report.allowed.str() << "\n";
    return 0;
}
