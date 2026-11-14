#include "netsec.hpp"

#include <cstdint>
#include <cstdio>
#include <string>

namespace {

int failures = 0;

#define CHECK(condition)                                                       \
    do {                                                                       \
        if (!(condition)) {                                                    \
            std::fprintf(stderr, "FAIL %s:%d: %s\n", __FILE__, __LINE__,       \
                         #condition);                                          \
            ++failures;                                                        \
        }                                                                      \
    } while (false)

constexpr netsec::Pc kBranchA = 0x401000;
constexpr netsec::Pc kBranchB = 0x402000;

netsec::IsolatorConfig test_config() {
    netsec::IsolatorConfig config;
    config.seed = 0x123456789ABCDEF0ULL;
    config.history_window = 16;
    config.anomaly_min_samples = 8;
    config.anomaly_mispredict_threshold = 0.50;
    return config;
}

void test_basic_partition_and_legitimate_switch() {
    netsec::PredictorIsolator iso(test_config());
    CHECK(iso.register_domain({.id = 1, .sensitive = false, .name = "attacker"}));
    CHECK(iso.register_domain({.id = 2, .sensitive = true, .name = "victim"}));

    auto report = iso.context_switch(1);
    CHECK(report.accepted);
    CHECK(iso.active_domain() == netsec::DomainId{1});

    for (int i = 0; i < 4; ++i) {
        CHECK(iso.train(1, kBranchA, true));
    }
    netsec::Prediction attacker = iso.predict(1, kBranchA);
    CHECK(attacker.hit);
    CHECK(attacker.taken);
    CHECK(attacker.tag_match);
    CHECK(iso.predict_active(kBranchA).hit);
    CHECK(iso.train_active(kBranchA, true).accepted);

    report = iso.context_switch(2);
    CHECK(report.accepted);
    CHECK(report.from == 1);
    CHECK(iso.active_domain() == netsec::DomainId{2});

    netsec::Prediction victim = iso.predict(2, kBranchA);
    CHECK(!victim.hit);
    CHECK(!victim.taken);

    for (int i = 0; i < 4; ++i) {
        CHECK(iso.train(2, kBranchA, false));
    }
    victim = iso.predict(2, kBranchA);
    CHECK(victim.hit);
    CHECK(!victim.taken);

    // A legitimate switch back must restore the attacker's own partition.
    report = iso.context_switch(1);
    CHECK(report.accepted);
    CHECK(!report.from_flushed);
    attacker = iso.predict(1, kBranchA);
    CHECK(attacker.hit);
    CHECK(attacker.taken);

    // Switching between two threads of one security domain is also legal and
    // must not wipe or retag its state.
    netsec::PredictorTag before = *iso.tag_for(1);
    report = iso.context_switch(1);
    CHECK(report.accepted);
    CHECK(!report.from_flushed);
    CHECK(iso.tag_for(1) == before);
    CHECK(iso.predict(1, kBranchA).hit);
}

void test_cross_domain_training_is_rejected() {
    netsec::PredictorIsolator iso(test_config());
    CHECK(iso.register_domain({.id = 10, .name = "attacker"}));
    CHECK(iso.register_domain({.id = 20, .sensitive = true, .name = "victim"}));
    CHECK(iso.context_switch(10).accepted);

    netsec::SecurityDomain spoof{.id = 10, .label = 0xDEADBEEF};
    CHECK(!iso.train(spoof, kBranchA, true).accepted);
    CHECK(iso.predict(spoof, kBranchA).rejected);

    netsec::TrainResult train = iso.train(20, kBranchA, true);
    CHECK(!train.accepted);
    CHECK(train.status == netsec::Status::CrossDomainViolation);
    CHECK(iso.alert_count(netsec::AlertKind::CrossDomainTraining) == 2);

    netsec::Prediction prediction = iso.predict(20, kBranchA);
    CHECK(prediction.rejected);
    CHECK(iso.alert_count(netsec::AlertKind::CrossDomainPrediction) == 2);

    auto stats = iso.stats(20);
    CHECK(stats.has_value());
    CHECK(stats->cross_domain_attempts == 2);
    CHECK(iso.stats(10)->cross_domain_attempts == 2);
    CHECK(iso.active_domain() == netsec::DomainId{10});
}

void test_path_inference_with_sensitive_partition() {
    netsec::PredictorIsolator iso(test_config());
    CHECK(iso.register_domain({.id = 100, .name = "attacker"}));
    CHECK(iso.register_domain(
        {.id = 200, .sensitive = true, .name = "victim"}));

    CHECK(iso.context_switch(100).accepted);
    for (int i = 0; i < 8; ++i) {
        CHECK(iso.train(100, kBranchA, true));
    }
    CHECK(iso.predict(100, kBranchA).taken);
    const netsec::PredictorTag attacker_tag = *iso.tag_for(100);

    CHECK(iso.context_switch(200).accepted);
    netsec::Prediction inference = iso.predict(200, kBranchA);
    CHECK(!inference.hit);
    CHECK(!inference.taken);
    CHECK(inference.tag != attacker_tag);

    // Attacker training while the victim runs must never touch the victim.
    CHECK(!iso.train(100, kBranchA, true).accepted);
    CHECK(!iso.predict(200, kBranchA).taken);

    netsec::PredictorTag victim_tag_before = *iso.tag_for(200);
    CHECK(iso.context_switch(100).accepted);
    CHECK(iso.predict(100, kBranchA).taken);
    CHECK(iso.context_switch(200).accepted);
    CHECK(iso.tag_for(200) == victim_tag_before);
    CHECK(!iso.predict(200, kBranchA).taken);
}

void test_sensitive_wipe_policy() {
    netsec::IsolatorConfig config = test_config();
    config.wipe_sensitive_on_cross_domain = true;
    netsec::PredictorIsolator iso(config);
    CHECK(iso.register_domain({.id = 7, .sensitive = true, .name = "secret"}));
    CHECK(iso.register_domain({.id = 8, .name = "untrusted"}));

    CHECK(iso.context_switch(7).accepted);
    for (int i = 0; i < 4; ++i) {
        CHECK(iso.train(7, kBranchA, true));
    }
    const netsec::PredictorTag old_tag = *iso.tag_for(7);
    const netsec::Epoch old_epoch = old_tag.epoch;
    CHECK(iso.predict(7, kBranchA).taken);

    netsec::SwitchReport away = iso.context_switch(8);
    CHECK(away.accepted);
    CHECK(away.from_flushed);
    CHECK(iso.alert_count(netsec::AlertKind::SensitiveStateWiped) == 1);

    netsec::SwitchReport back = iso.context_switch(7);
    CHECK(back.accepted);
    CHECK(iso.tag_for(7)->epoch > old_epoch);
    CHECK(!iso.predict(7, kBranchA).hit);

    // The untrusted domain trains the same address heavily, but the sensitive
    // partition remains empty and cannot infer that training.
    CHECK(iso.context_switch(8).accepted);
    for (int i = 0; i < 16; ++i) {
        CHECK(iso.train(8, kBranchA, true));
    }
    CHECK(iso.context_switch(7).accepted);
    CHECK(!iso.predict(7, kBranchA).hit);
    CHECK(!iso.predict(7, kBranchA).taken);
}

void test_interrupt_paths() {
    netsec::PredictorIsolator iso(test_config());
    CHECK(iso.register_domain({.id = 1, .sensitive = true, .name = "task"}));
    CHECK(iso.register_domain({.id = 2, .sensitive = true, .name = "irq"}));
    CHECK(iso.register_domain({.id = 3, .name = "nested"}));

    CHECK(iso.context_switch(1).accepted);
    for (int i = 0; i < 4; ++i) {
        CHECK(iso.train(1, kBranchA, true));
    }
    const netsec::PredictorTag task_tag = *iso.tag_for(1);

    netsec::SwitchReport irq = iso.enter_interrupt(2);
    CHECK(irq.accepted);
    CHECK(iso.active_domain() == netsec::DomainId{2});
    CHECK(iso.alert_count(netsec::AlertKind::CrossDomainPrediction) == 0);

    // The handler has a separate partition and cannot read the task's state.
    CHECK(!iso.predict(2, kBranchA).hit);
    CHECK(iso.predict(1, kBranchA).rejected);
    for (int i = 0; i < 4; ++i) {
        CHECK(iso.train(2, kBranchA, false));
    }
    CHECK(!iso.predict(2, kBranchA).taken);

    netsec::SwitchReport nested = iso.enter_interrupt(3);
    CHECK(nested.accepted);
    CHECK(iso.active_domain() == netsec::DomainId{3});
    CHECK(iso.return_from_interrupt().accepted);
    CHECK(iso.active_domain() == netsec::DomainId{2});

    netsec::SwitchReport ret = iso.return_from_interrupt();
    CHECK(ret.accepted);
    CHECK(iso.active_domain() == netsec::DomainId{1});
    CHECK(iso.tag_for(1) == task_tag);
    CHECK(iso.predict(1, kBranchA).hit);
    CHECK(iso.predict(1, kBranchA).taken);

    CHECK(iso.return_from_interrupt().status ==
          netsec::Status::EmptyInterruptStack);
    CHECK(iso.alert_count(netsec::AlertKind::InterruptStackUnderflow) == 1);
}

void test_interrupt_wipe_policy() {
    netsec::IsolatorConfig config = test_config();
    config.wipe_sensitive_on_cross_domain = true;
    netsec::PredictorIsolator iso(config);
    CHECK(iso.register_domain({.id = 1, .sensitive = true, .name = "task"}));
    CHECK(iso.register_domain({.id = 2, .sensitive = true, .name = "irq"}));
    CHECK(iso.context_switch(1).accepted);
    for (int i = 0; i < 4; ++i) {
        CHECK(iso.train(1, kBranchA, true));
    }

    CHECK(iso.enter_interrupt(2).accepted);
    CHECK(iso.alert_count(netsec::AlertKind::SensitiveStateWiped) == 1);
    CHECK(iso.return_from_interrupt().accepted);
    CHECK(!iso.predict(1, kBranchA).hit);
}

void test_migration_preserves_only_owner_state() {
    netsec::PredictorIsolator iso(test_config());
    CHECK(iso.register_domain({.id = 1, .name = "worker"}));
    CHECK(iso.register_domain({.id = 2, .sensitive = true, .name = "victim"}));

    CHECK(iso.context_switch(1, 0).accepted);
    for (int i = 0; i < 4; ++i) {
        CHECK(iso.train(1, kBranchA, true, 0));
    }
    CHECK(iso.predict(1, kBranchA, 0).taken);

    netsec::SwitchReport moved = iso.migrate(1, 0, 1);
    CHECK(moved.accepted);
    CHECK(iso.active_domain(0) == std::nullopt);
    CHECK(iso.active_domain(1) == netsec::DomainId{1});
    CHECK(iso.predict(1, kBranchA, 1).hit);
    CHECK(iso.predict(1, kBranchA, 1).taken);

    // A different domain scheduled on the source CPU must start clean.
    CHECK(iso.context_switch(2, 0).accepted);
    CHECK(!iso.predict(2, kBranchA, 0).hit);
    CHECK(!iso.predict(2, kBranchA, 0).taken);
    CHECK(iso.stats(1)->migrations == 1);

    // The migrated domain's state is also not visible after another switch.
    CHECK(iso.context_switch(2, 1).accepted);
    CHECK(!iso.predict(2, kBranchA, 1).hit);
}

void test_sensitive_migration_wipes() {
    netsec::IsolatorConfig config = test_config();
    config.wipe_sensitive_on_cross_domain = true;
    netsec::PredictorIsolator iso(config);
    CHECK(iso.register_domain({.id = 5, .sensitive = true, .name = "secret"}));
    CHECK(iso.register_domain({.id = 6, .name = "other"}));
    CHECK(iso.context_switch(5, 0).accepted);
    for (int i = 0; i < 4; ++i) {
        CHECK(iso.train(5, kBranchA, true, 0));
    }

    netsec::SwitchReport moved = iso.migrate(5, 0, 1);
    CHECK(moved.accepted);
    CHECK(moved.from_flushed);
    CHECK(!iso.predict(5, kBranchA, 1).hit);
    CHECK(iso.alert_count(netsec::AlertKind::SensitiveStateWiped) == 1);

    CHECK(iso.context_switch(6, 0).accepted);
    CHECK(!iso.predict(6, kBranchA, 0).taken);
}

void test_prediction_rate_anomaly() {
    netsec::PredictorIsolator iso(test_config());
    CHECK(iso.register_domain({.id = 9, .name = "noisy"}));
    CHECK(iso.context_switch(9).accepted);

    for (int i = 0; i < 12; ++i) {
        const bool actual = (i % 2) == 0;
        (void)iso.predict(9, kBranchB);
        netsec::TrainResult result = iso.train(9, kBranchB, actual);
        CHECK(result.accepted);
        if (i == 0) {
            CHECK(!result.predicted_taken);
            CHECK(result.mispredicted);
        }
    }
    CHECK(iso.alert_count(netsec::AlertKind::PredictionRateAnomaly) >= 1);
    CHECK(iso.stats(9)->anomaly_episodes >= 1);
    CHECK(iso.stats(9)->mispredict_rate >= 0.5);

    netsec::PredictorIsolator stable(test_config());
    CHECK(stable.register_domain({.id = 11, .name = "stable"}));
    CHECK(stable.context_switch(11).accepted);
    for (int i = 0; i < 32; ++i) {
        (void)stable.predict(11, kBranchA);
        CHECK(stable.train(11, kBranchA, true).accepted);
    }
    CHECK(stable.alert_count(netsec::AlertKind::PredictionRateAnomaly) == 0);
}

void test_registry_and_unknown_domains() {
    netsec::PredictorIsolator iso(test_config());
    CHECK(iso.register_domain({.id = 1, .label = 0x1111}));
    CHECK(!iso.register_domain({.id = 1, .label = 0x1111}));
    CHECK(!iso.register_domain({.id = 1, .label = 0x2222}));
    CHECK(!iso.register_domain({.id = 2, .label = 0x1111}));
    CHECK(iso.alert_count(netsec::AlertKind::DuplicateDomain) == 2);
    CHECK(iso.alert_count(netsec::AlertKind::TagConflict) == 1);

    CHECK(!iso.context_switch(99).accepted);
    CHECK(iso.context_switch(99).status == netsec::Status::UnknownDomain);
    CHECK(iso.alert_count(netsec::AlertKind::UnknownDomain) == 2);
    CHECK(iso.active_domain() == std::nullopt);

    CHECK(iso.register_domain({.id = 3, .sensitive = true}));
    CHECK(iso.register_domain(4, true, 0, "convenience"));
    CHECK(iso.domain(1)->label == 0x1111);
    CHECK(iso.tag_for(3)->sensitive);
    CHECK(iso.tag_for(4)->sensitive);
    CHECK(iso.tag_for(1)->label != iso.tag_for(3)->label);
}

void test_snapshot_import_is_domain_bound() {
    netsec::PredictorIsolator iso(test_config());
    CHECK(iso.register_domain({.id = 1, .name = "owner"}));
    CHECK(iso.register_domain({.id = 2, .name = "other"}));
    CHECK(iso.context_switch(1, 0).accepted);
    for (int i = 0; i < 4; ++i) {
        CHECK(iso.train(1, kBranchA, true, 0));
    }

    auto snapshot = iso.export_snapshot(1, 0);
    CHECK(snapshot.has_value());
    CHECK(!snapshot->entries.empty());

    CHECK(!iso.import_snapshot(*snapshot, 1, false));

    netsec::ImportResult imported = iso.import_snapshot(*snapshot, 1, true);
    CHECK(imported.accepted);
    CHECK(iso.predict(1, kBranchA, 1).hit);
    CHECK(iso.predict(1, kBranchA, 1).taken);

    CHECK(iso.context_switch(2, 1).accepted);
    CHECK(!iso.import_snapshot(*snapshot, 1, false).accepted);
    netsec::ImportResult takeover = iso.import_snapshot(*snapshot, 1, true);
    CHECK(takeover.accepted);
    CHECK(iso.active_domain(1) == netsec::DomainId{1});
    CHECK(iso.predict(1, kBranchA, 1).hit);

    netsec::PredictorSnapshot forged = *snapshot;
    forged.domain.id = 2;
    forged.domain.label = iso.domain(2)->label;
    forged.tag.domain = 2;
    forged.tag.label = forged.domain.label;
    CHECK(!iso.import_snapshot(forged, 1, true));
    CHECK(iso.alert_count(netsec::AlertKind::StateImportRejected) >= 2);
}

void test_snapshot_sensitive_migration_policy() {
    netsec::IsolatorConfig config = test_config();
    config.wipe_sensitive_on_cross_domain = true;
    netsec::PredictorIsolator iso(config);
    CHECK(iso.register_domain({.id = 1, .sensitive = true, .name = "secret"}));
    CHECK(iso.context_switch(1, 0).accepted);
    for (int i = 0; i < 4; ++i) {
        CHECK(iso.train(1, kBranchA, true, 0));
    }
    auto snapshot = iso.export_snapshot(1, 0);
    CHECK(snapshot.has_value());

    netsec::ImportResult imported = iso.import_snapshot(*snapshot, 1, true);
    CHECK(imported.accepted);
    CHECK(imported.flushed);
    CHECK(!iso.predict(1, kBranchA, 1).hit);
}

void test_reregistration_invalidates_old_snapshots() {
    netsec::PredictorIsolator iso(test_config());
    CHECK(iso.register_domain({.id = 1, .label = 0x4242, .name = "tenant"}));
    CHECK(iso.context_switch(1, 0).accepted);
    for (int i = 0; i < 4; ++i) {
        CHECK(iso.train(1, kBranchA, true, 0));
    }
    auto snapshot = iso.export_snapshot(1, 0);
    CHECK(snapshot.has_value());

    CHECK(iso.unregister_domain(1));
    CHECK(iso.register_domain({.id = 1, .label = 0x4242, .name = "tenant"}));
    CHECK(iso.context_switch(1, 0).accepted);
    netsec::ImportResult replayed = iso.import_snapshot(*snapshot, 0, false);
    CHECK(!replayed.accepted);
    CHECK(replayed.status == netsec::Status::TagMismatch);
}

}  // namespace

int main() {
    test_basic_partition_and_legitimate_switch();
    test_cross_domain_training_is_rejected();
    test_path_inference_with_sensitive_partition();
    test_sensitive_wipe_policy();
    test_interrupt_paths();
    test_interrupt_wipe_policy();
    test_migration_preserves_only_owner_state();
    test_sensitive_migration_wipes();
    test_prediction_rate_anomaly();
    test_registry_and_unknown_domains();
    test_snapshot_import_is_domain_bound();
    test_snapshot_sensitive_migration_policy();
    test_reregistration_invalidates_old_snapshots();

    if (failures != 0) {
        std::fprintf(stderr, "%d test assertion(s) failed\n", failures);
        return 1;
    }
    std::printf("all predictor isolation tests passed\n");
    return 0;
}
