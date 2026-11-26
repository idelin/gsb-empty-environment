#include "pacbti/label.hpp"

#include "test_util.hpp"

using namespace pacbti;

TEST_CASE("label.bottom_is_identity_for_join") {
    Label raw = Label::raw(7u, false);
    Label merged = mergeLabels(Label::bottom(), raw);
    CHECK(labelsEqual(merged, raw));
    merged = mergeLabels(raw, Label::bottom());
    CHECK(labelsEqual(merged, raw));
}

TEST_CASE("label.equal_signed_labels_join_to_themselves") {
    Label first = Label::signedPtr(KeyDomain::IA, 3u, true,
                                   OriginList{10u}, OriginList{11u});
    Label second = Label::signedPtr(KeyDomain::IA, 3u, true,
                                    OriginList{10u}, OriginList{11u});
    CHECK(labelsEqual(mergeLabels(first, second), first));
}

TEST_CASE("label.distinct_signed_labels_unify_origins_and_drop_identity") {
    Label first = Label::signedPtr(KeyDomain::IA, 3u, false,
                                   OriginList{10u}, OriginList{1u});
    Label second = Label::signedPtr(KeyDomain::IA, 4u, false,
                                    OriginList{20u}, OriginList{2u});
    Label merged = mergeLabels(first, second);
    CHECK(merged.isSigned());
    CHECK_EQ(merged.typeId, kInvalidType);
    CHECK_EQ(merged.modifierOrigins.size(), 2u);
    CHECK_EQ(merged.signOrigins.size(), 2u);
}

TEST_CASE("label.kind_mismatch_falls_back_to_unknown") {
    Label raw = Label::raw(1u, false);
    Label signedLabel = Label::signedPtr(KeyDomain::IB, 1u, false, {}, {});
    CHECK(mergeLabels(raw, signedLabel).isUnknown());
}

TEST_CASE("label.poison_join_preserves_all_origins") {
    Label first = Label::poisoned(OriginList{5u});
    Label second = Label::poisoned(OriginList{6u});
    Label merged = mergeLabels(first, second);
    CHECK(merged.isPoisoned());
    CHECK_EQ(merged.poisonOrigins.size(), 2u);
    CHECK(merged.attackerTainted);
}

TEST_CASE("state.join_merges_registers_and_stack_slots") {
    AbstractState left = AbstractState::entry();
    AbstractState right = AbstractState::entry();
    left.setReg(0u, Label::raw(1u, false));
    right.setReg(0u, Label::raw(1u, true));
    left.setStack(4u, Label::poisoned(OriginList{9u}));
    right.setStack(8u, Label::raw(2u, false));

    AbstractState joined = joinStates(left, right);
    CHECK(joined.getReg(0u).isRaw());
    CHECK(joined.getReg(0u).attackerTainted);
    CHECK(joined.getStack(4u).isPoisoned());
    CHECK(joined.getStack(8u).isRaw());
    CHECK(statesEqual(joinStates(joined, joined), joined));
}
