#include "netsec.hpp"

#include <cstdint>
#include <iostream>
#include <string>
#include <vector>

namespace {

int failures = 0;

void check(bool condition, const char* expression, const char* file, int line) {
    if (!condition) {
        ++failures;
        std::cerr << file << ":" << line << ": CHECK failed: " << expression << "\n";
    }
}

#define CHECK(expr) check((expr), #expr, __FILE__, __LINE__)

netsec::QueryPlan plan_for(const std::string& partition,
                           netsec::ScanType scan = netsec::ScanType::SinglePartition) {
    netsec::QueryPlan plan;
    plan.table = "events";
    plan.scan_type = scan;
    plan.partition_key_column = "tenant_id";
    if (scan == netsec::ScanType::SinglePartition) {
        plan.scan_partition = netsec::Value(partition);
        plan.where = netsec::Condition::eq("tenant_id", netsec::Value(partition));
    } else {
        plan.where = netsec::Condition::eq("tenant_id", netsec::Value(partition));
    }
    plan.allow_filtering = true;
    return plan;
}

netsec::DbRow row_with_partition(const std::string& partition,
                                 const std::string& payload = "value") {
    netsec::DbRow row;
    row.partition = netsec::Value(partition);
    row.columns["payload"] = netsec::Value(payload);
    return row;
}

netsec::DbRow row_with_column(const std::string& partition,
                              const std::string& payload = "value") {
    netsec::DbRow row;
    row.columns["tenant_id"] = netsec::Value(partition);
    row.columns["payload"] = netsec::Value(payload);
    return row;
}

void expect_allowed(const netsec::AuthorizationResult& result) {
    CHECK(result.allowed);
    CHECK(result.code == netsec::DenyCode::None);
    CHECK(result.plan.requires_result_recheck);
}

void expect_denied(const netsec::AuthorizationResult& result, netsec::DenyCode code) {
    CHECK(!result.allowed);
    CHECK(result.code == code);
}

void only_partition_filter_denied(const netsec::AuthzEngine& engine,
                                  const netsec::AuthContext& identity,
                                  const netsec::QueryPlan& plan);

void test_full_table_filtering() {
    netsec::AuthzEngine engine;
    engine.grant("tenant-a", "reader", {"tenant-a"});
    const netsec::AuthContext identity{"tenant-a", "reader"};

    netsec::QueryPlan low_cardinality;
    low_cardinality.table = "events";
    low_cardinality.scan_type = netsec::ScanType::FullTable;
    low_cardinality.allow_filtering = true;
    low_cardinality.where = netsec::Condition::eq("category", netsec::Value("operations"));
    expect_denied(engine.authorize(low_cardinality, identity),
                  netsec::DenyCode::MissingPartitionEquality);

    auto filtered = plan_for("tenant-a", netsec::ScanType::FullTable);
    only_partition_filter_denied(engine, identity, filtered);

    auto policy = engine.default_policy();
    policy.allow_full_table_scan = true;
    engine.set_role_policy("reader", policy);
    auto result = engine.authorize(filtered, identity);
    expect_allowed(result);
    CHECK(result.plan.query.scan_type == netsec::ScanType::SinglePartition);
    CHECK(result.plan.authorized_partitions.size() == 1);
    CHECK(result.plan.authorized_partitions.front() == "tenant-a");

    auto guard = engine.guard(result);
    CHECK(guard.valid());
    const std::vector<netsec::DbRow> rows = {
        row_with_partition("tenant-a"),
        row_with_partition("tenant-b"),
        row_with_column("tenant-a"),
        row_with_column("tenant-b"),
        row_with_partition("tenant-c"),
    };
    const auto filtered_rows = guard.filter(rows);
    CHECK(filtered_rows.size() == 2);
    for (const auto& row : filtered_rows) {
        const auto partition = row.partition.has_value() ? *row.partition
                                                         : *row.find("tenant_id");
        CHECK(partition.to_string() == "tenant-a");
    }
}

void only_partition_filter_denied(const netsec::AuthzEngine& engine,
                                  const netsec::AuthContext& identity,
                                  const netsec::QueryPlan& plan) {
    expect_denied(engine.authorize(plan, identity), netsec::DenyCode::FullTableScanDenied);
}

void test_secondary_index() {
    netsec::AuthzEngine engine;
    engine.grant("tenant-a", "reader", {"tenant-a"});
    const netsec::AuthContext identity{"tenant-a", "reader"};

    auto plan = plan_for("tenant-a");
    plan.scan_type = netsec::ScanType::SecondaryIndex;
    plan.index_name = "idx_category";
    plan.scan_partition.reset();
    plan.requested_partitions = {netsec::Value("tenant-a")};
    plan.where = netsec::Condition::all(
        {netsec::Condition::eq("tenant_id", netsec::Value("tenant-a")),
         netsec::Condition::eq("category", netsec::Value("operations"))});

    expect_denied(engine.authorize(plan, identity), netsec::DenyCode::SecondaryIndexDenied);

    auto policy = engine.default_policy();
    policy.allow_secondary_index = true;
    engine.set_role_policy("reader", policy);
    auto result = engine.authorize(plan, identity);
    expect_allowed(result);
    CHECK(result.plan.query.scan_type == netsec::ScanType::SecondaryIndex);
    CHECK(result.plan.query.index_name.has_value());
    CHECK(result.plan.authorized_partitions.size() == 1);

    auto guard = engine.guard(result);
    CHECK(guard.accept(row_with_partition("tenant-a")));
    CHECK(!guard.accept(row_with_partition("tenant-b")));

    netsec::QueryPlan no_partition = plan;
    no_partition.where = netsec::Condition::eq("category", netsec::Value("operations"));
    no_partition.requested_partitions.clear();
    expect_denied(engine.authorize(no_partition, identity),
                  netsec::DenyCode::MissingPartitionEquality);

    netsec::QueryPlan crossed = plan;
    crossed.where = netsec::Condition::in(
        "tenant_id", {netsec::Value("tenant-a"), netsec::Value("tenant-b")});
    crossed.requested_partitions = {netsec::Value("tenant-a"), netsec::Value("tenant-b")};
    expect_denied(engine.authorize(crossed, identity),
                  netsec::DenyCode::UnauthorizedPartition);
}

void test_aggregation_leakage() {
    netsec::AuthzEngine engine;
    engine.grant("tenant-a", "reader", {"tenant-a"});
    const netsec::AuthContext identity{"tenant-a", "reader"};

    netsec::QueryPlan global = plan_for("tenant-a");
    global.operation = netsec::QueryOperation::Aggregate;
    global.scan_type = netsec::ScanType::FullTable;
    global.allow_filtering = true;
    global.where = netsec::Condition::eq("category", netsec::Value("operations"));
    global.aggregates = {{"count", "*", "n", false}};
    expect_denied(engine.authorize(global, identity),
                  netsec::DenyCode::MissingPartitionEquality);

    global.where = netsec::Condition::in(
        "tenant_id", {netsec::Value("tenant-a"), netsec::Value("tenant-b")});
    expect_denied(engine.authorize(global, identity),
                  netsec::DenyCode::UnauthorizedPartition);

    netsec::QueryPlan scoped = global;
    scoped.scan_type = netsec::ScanType::SinglePartition;
    scoped.scan_partition = netsec::Value("tenant-a");
    scoped.where = netsec::Condition::eq("tenant_id", netsec::Value("tenant-a"));
    scoped.group_by = {"tenant_id"};
    auto scoped_result = engine.authorize(scoped, identity);
    expect_allowed(scoped_result);
    auto scoped_guard = engine.guard(scoped_result);
    netsec::DbRow aggregate_row;
    aggregate_row.columns["n"] = netsec::Value(std::int64_t{7});
    CHECK(scoped_guard.accept(aggregate_row));

    auto policy = engine.default_policy();
    policy.allow_cross_partition_scan = true;
    policy.max_partitions_per_query = 2;
    policy.allow_cross_partition_aggregation = false;
    engine.set_role_policy("reader", policy);

    netsec::QueryPlan cross = scoped;
    cross.scan_type = netsec::ScanType::MultiPartition;
    cross.scan_partition.reset();
    cross.requested_partitions = {netsec::Value("tenant-a"), netsec::Value("tenant-b")};
    cross.where = netsec::Condition::in(
        "tenant_id", {netsec::Value("tenant-a"), netsec::Value("tenant-b")});
    engine.grant("tenant-a", "reader", {"tenant-b"});
    expect_denied(engine.authorize(cross, identity), netsec::DenyCode::AggregationDenied);

    cross.group_by = {"category"};
    expect_denied(engine.authorize(cross, identity), netsec::DenyCode::AggregationDenied);

    cross.group_by = {"tenant_id"};
    policy.allow_cross_partition_aggregation = true;
    engine.set_role_policy("reader", policy);
    auto result = engine.authorize(cross, identity);
    expect_allowed(result);
    auto guard = engine.guard(result);
    CHECK(guard.accept(row_with_partition("tenant-a")));
    CHECK(guard.accept(row_with_partition("tenant-b")));
    CHECK(!guard.accept(row_with_partition("tenant-c")));

    policy.allow_aggregation = false;
    engine.set_role_policy("reader", policy);
    expect_denied(engine.authorize(scoped, identity), netsec::DenyCode::AggregationDenied);
}

void test_legitimate_partition_query() {
    netsec::AuthzEngine engine;
    engine.grant("tenant-a", "reader", {"tenant-a"});
    const netsec::AuthContext identity{"tenant-a", "reader"};

    auto plan = plan_for("tenant-a");
    plan.where = netsec::Condition::all(
        {netsec::Condition::eq("tenant_id", netsec::Value("tenant-a")),
         netsec::Condition::eq("category", netsec::Value("operations"))});
    auto result = engine.authorize(plan, identity);
    expect_allowed(result);
    CHECK(result.plan.authorized_partitions == std::vector<std::string>{"tenant-a"});
    CHECK(result.plan.injected_partition_filter != nullptr);

    auto guard = engine.guard(result);
    CHECK(guard.valid());
    CHECK(guard.partitions() == std::vector<std::string>{"tenant-a"});
    CHECK(guard.accept(row_with_partition("tenant-a")));
    CHECK(guard.accept(row_with_column("tenant-a")));
    CHECK(!guard.accept(row_with_partition("tenant-b")));
    CHECK(!guard.accept(row_with_column("tenant-b")));
    netsec::DbRow qualified;
    qualified.columns["events.tenant_id"] = netsec::Value("tenant-a");
    CHECK(guard.accept(qualified));

    netsec::DbRow no_evidence;
    no_evidence.columns["payload"] = netsec::Value("value");
    CHECK(!guard.accept(no_evidence));

    netsec::QueryPlan single_value_in = plan;
    single_value_in.where = netsec::Condition::in(
        "tenant_id", {netsec::Value("tenant-a")});
    expect_allowed(engine.authorize(single_value_in, identity));
}

void test_filter_cannot_expand_scope() {
    netsec::AuthzEngine engine;
    engine.grant("tenant-a", "reader", {"tenant-a"});
    const netsec::AuthContext identity{"tenant-a", "reader"};

    auto plan = plan_for("tenant-a");
    plan.where = netsec::Condition::any(
        {netsec::Condition::eq("tenant_id", netsec::Value("tenant-a")),
         netsec::Condition::eq("category", netsec::Value("operations"))});
    expect_denied(engine.authorize(plan, identity),
                  netsec::DenyCode::MissingPartitionEquality);

    plan.where = netsec::Condition::any(
        {netsec::Condition::eq("tenant_id", netsec::Value("tenant-a")),
         netsec::Condition::eq("tenant_id", netsec::Value("tenant-b"))});
    expect_denied(engine.authorize(plan, identity),
                  netsec::DenyCode::UnauthorizedPartition);

    plan.scan_type = netsec::ScanType::MultiPartition;
    plan.scan_partition.reset();
    plan.requested_partitions = {netsec::Value("tenant-a"), netsec::Value("tenant-b")};
    expect_denied(engine.authorize(plan, identity),
                  netsec::DenyCode::UnauthorizedPartition);

    plan.where = netsec::Condition::eq("tenant_id", netsec::Value("tenant-a"));
    expect_denied(engine.authorize(plan, identity),
                  netsec::DenyCode::PredicateCannotBeBounded);

    plan.scan_type = netsec::ScanType::SinglePartition;
    plan.requested_partitions.clear();
    plan.scan_partition = netsec::Value("tenant-b");
    expect_denied(engine.authorize(plan, identity),
                  netsec::DenyCode::PredicateCannotBeBounded);

    plan.scan_partition = netsec::Value("tenant-a");
    plan.where = netsec::Condition::ne("tenant_id", netsec::Value("tenant-b"));
    expect_denied(engine.authorize(plan, identity),
                  netsec::DenyCode::MissingPartitionEquality);
}

void test_cross_partition_policy() {
    netsec::AuthzEngine engine;
    engine.grant("tenant-a", "reader", {"tenant-a", "tenant-b"});
    const netsec::AuthContext identity{"tenant-a", "reader"};

    netsec::QueryPlan plan;
    plan.table = "events";
    plan.scan_type = netsec::ScanType::MultiPartition;
    plan.requested_partitions = {netsec::Value("tenant-a"), netsec::Value("tenant-b")};
    plan.where = netsec::Condition::in(
        "tenant_id", {netsec::Value("tenant-a"), netsec::Value("tenant-b")});

    expect_denied(engine.authorize(plan, identity), netsec::DenyCode::CrossPartitionDenied);

    auto policy = engine.default_policy();
    policy.allow_cross_partition_scan = true;
    policy.max_partitions_per_query = 2;
    engine.set_role_policy("reader", policy);
    auto result = engine.authorize(plan, identity);
    expect_allowed(result);
    CHECK(result.plan.authorized_partitions.size() == 2);
    auto guard = engine.guard(result);
    CHECK(guard.accept(row_with_partition("tenant-a")));
    CHECK(guard.accept(row_with_partition("tenant-b")));
    CHECK(!guard.accept(row_with_partition("tenant-c")));

    policy.max_partitions_per_query = 1;
    engine.set_role_policy("reader", policy);
    expect_denied(engine.authorize(plan, identity), netsec::DenyCode::CrossPartitionDenied);
}

void test_filtering_policy() {
    netsec::AuthzEngine engine;
    engine.grant("tenant-a", "reader", {"tenant-a"});
    const netsec::AuthContext identity{"tenant-a", "reader"};

    auto policy = engine.default_policy();
    policy.allow_filtering = false;
    engine.set_role_policy("reader", policy);

    auto plan = plan_for("tenant-a");
    plan.where = netsec::Condition::all(
        {netsec::Condition::eq("tenant_id", netsec::Value("tenant-a")),
         netsec::Condition::eq("category", netsec::Value("operations"))});
    expect_denied(engine.authorize(plan, identity), netsec::DenyCode::FilteringDenied);
}

void test_authorization_change() {
    netsec::AuthzEngine engine;
    engine.grant("tenant-a", "reader", {"tenant-a"});
    const netsec::AuthContext identity{"tenant-a", "reader"};

    auto result = engine.authorize(plan_for("tenant-a"), identity);
    expect_allowed(result);
    auto guard = engine.guard(result);
    CHECK(guard.valid());
    CHECK(guard.accept(row_with_partition("tenant-a")));

    engine.revoke("tenant-a", "reader", {"tenant-a"});
    CHECK(!guard.valid());
    CHECK(!guard.accept(row_with_partition("tenant-a")));
    expect_denied(engine.authorize(plan_for("tenant-a"), identity), netsec::DenyCode::NoGrant);

    engine.grant("tenant-a", "reader", {"tenant-a"});
    CHECK(!guard.valid());
    auto fresh = engine.authorize(plan_for("tenant-a"), identity);
    expect_allowed(fresh);
    auto fresh_guard = engine.guard(fresh);
    CHECK(fresh_guard.valid());
    CHECK(fresh_guard.accept(row_with_partition("tenant-a")));

    engine.set_grant("tenant-a", "reader", {"tenant-b"});
    expect_denied(engine.authorize(plan_for("tenant-a"), identity),
                  netsec::DenyCode::UnauthorizedPartition);
    CHECK(!fresh_guard.valid());
    expect_allowed(engine.authorize(plan_for("tenant-b"), identity));

    engine.remove_principal("tenant-a", "reader");
    expect_denied(engine.authorize(plan_for("tenant-b"), identity), netsec::DenyCode::NoGrant);
}

void test_role_change() {
    netsec::AuthzEngine engine;
    engine.grant("tenant-a", "reader", {"tenant-a"});
    engine.grant("tenant-a", "publisher", {"tenant-a"});

    auto reader_result = engine.authorize(plan_for("tenant-a"), {"tenant-a", "reader"});
    expect_allowed(reader_result);
    auto reader_guard = engine.guard(reader_result);
    CHECK(reader_guard.accept(row_with_partition("tenant-a")));

    auto policy = engine.default_policy();
    policy.allow_full_table_scan = true;
    engine.set_role_policy("publisher", policy);
    CHECK(!reader_guard.valid());

    expect_denied(engine.authorize(plan_for("tenant-a", netsec::ScanType::FullTable),
                                   {"tenant-a", "reader"}),
                  netsec::DenyCode::FullTableScanDenied);
    expect_allowed(engine.authorize(plan_for("tenant-a", netsec::ScanType::FullTable),
                                    {"tenant-a", "publisher"}));
}

void test_scope_analysis() {
    const auto pk = "tenant_id";
    auto scope = netsec::analyze_partition_scope(
        netsec::Condition::eq("tenant_id", netsec::Value("a")), pk);
    CHECK(scope.valid);
    CHECK(scope.satisfiable);
    CHECK(scope.bounded);
    CHECK(scope.values.size() == 1);

    scope = netsec::analyze_partition_scope(
        netsec::Condition::any(
            {netsec::Condition::eq("tenant_id", netsec::Value("a")),
             netsec::Condition::eq("category", netsec::Value("x"))}),
        pk);
    CHECK(scope.valid);
    CHECK(!scope.bounded);

    scope = netsec::analyze_partition_scope(
        netsec::Condition::all(
            {netsec::Condition::eq("tenant_id", netsec::Value("a")),
             netsec::Condition::eq("tenant_id", netsec::Value("b"))}),
        pk);
    CHECK(!scope.satisfiable);

    scope = netsec::analyze_partition_scope(
        netsec::Condition::negate(
            netsec::Condition::eq("tenant_id", netsec::Value("a"))),
        pk);
    CHECK(scope.valid);
    CHECK(!scope.bounded);
}

void test_json_plan() {
    std::string error;
    const std::string json = R"json({
        "table": "events",
        "operation": "select",
        "scan": "single_partition",
        "partition_key": "tenant_id",
        "partition": "tenant-a",
        "allow_filtering": true,
        "where": {"and": [
            {"eq": ["tenant_id", "tenant-a"]},
            {"eq": ["category", "operations"]}
        ]}
    })json";
    auto plan = netsec::parse_plan_json(json, &error);
    CHECK(plan.has_value());
    CHECK(error.empty());
    CHECK(plan->scan_type == netsec::ScanType::SinglePartition);
    CHECK(plan->scan_partition.has_value());
    CHECK(plan->where != nullptr);

    netsec::AuthzEngine engine;
    engine.grant("tenant-a", "reader", {"tenant-a"});
    auto result = engine.authorize(*plan, {"tenant-a", "reader"});
    expect_allowed(result);

    const std::string attack = R"json({
        "table": "events",
        "scan": "full_table",
        "allow_filtering": true,
        "where": {"eq": ["category", "operations"]}
    })json";
    auto attack_plan = netsec::parse_plan_json(attack, &error);
    CHECK(attack_plan.has_value());
    expect_denied(engine.authorize(*attack_plan, {"tenant-a", "reader"}),
                  netsec::DenyCode::MissingPartitionEquality);

    auto broken = netsec::parse_plan_json("{", &error);
    CHECK(!broken.has_value());
    CHECK(!error.empty());

    const std::string serialized = netsec::plan_to_json(*plan);
    auto round_trip = netsec::parse_query_plan(serialized, &error);
    CHECK(round_trip.has_value());
    expect_allowed(engine.authorize(*round_trip, {"tenant-a", "reader"}));

    const std::string predicates = R"json({
        "table": "events",
        "predicates": [
            {"column": "tenant_id", "op": "eq", "values": ["tenant-a"]},
            {"column": "category", "op": "eq", "values": ["operations"]}
        ]
    })json";
    auto predicate_plan = netsec::parse_plan_json(predicates, &error);
    CHECK(predicate_plan.has_value());
    expect_allowed(engine.authorize(*predicate_plan, {"tenant-a", "reader"}));

    netsec::QueryPlan aggregate;
    aggregate.table = "events";
    aggregate.operation = netsec::QueryOperation::Aggregate;
    aggregate.where = netsec::Condition::eq("tenant_id", netsec::Value("tenant-a"));
    aggregate.group_by = {"tenant_id"};
    aggregate.aggregates = {{"count", "*", "n", false}};
    const std::string aggregate_json = netsec::plan_to_json(aggregate);
    auto aggregate_plan = netsec::parse_plan_json(aggregate_json, &error);
    CHECK(aggregate_plan.has_value());
    CHECK(aggregate_plan->operation == netsec::QueryOperation::Aggregate);
    CHECK(aggregate_plan->aggregates.size() == 1);
    expect_allowed(engine.authorize(*aggregate_plan, {"tenant-a", "reader"}));
}

void test_qualified_columns() {
    netsec::AuthzEngine engine;
    engine.grant("tenant-a", "reader", {"tenant-a"});
    netsec::QueryPlan plan;
    plan.table = "events";
    plan.where = netsec::Condition::eq("events.tenant_id", netsec::Value("tenant-a"));
    auto result = engine.authorize(plan, {"tenant-a", "reader"});
    expect_allowed(result);
    CHECK(result.plan.identity.tenant == "tenant-a");
    CHECK(result.plan.identity.role == "reader");
}

void test_partition_key_is_server_configured() {
    netsec::AuthzEngine engine;
    engine.grant("tenant-a", "reader", {"tenant-a"});

    netsec::QueryPlan tampered;
    tampered.table = "events";
    tampered.partition_key_column = "category";
    tampered.where = netsec::Condition::eq("category", netsec::Value("tenant-a"));
    tampered.scan_partition = netsec::Value("tenant-a");
    expect_denied(engine.authorize(tampered, {"tenant-a", "reader"}),
                  netsec::DenyCode::InvalidPlan);

    engine.set_partition_key_column("org_id");
    netsec::QueryPlan configured;
    configured.table = "events";
    configured.partition_key_column = "org_id";
    configured.where = netsec::Condition::eq("org_id", netsec::Value("tenant-a"));
    configured.scan_partition = netsec::Value("tenant-a");
    auto result = engine.authorize(configured, {"tenant-a", "reader"});
    expect_allowed(result);
    CHECK(result.plan.query.partition_key_column == "org_id");

    netsec::QueryPlan still_uses_tenant = configured;
    still_uses_tenant.partition_key_column = "tenant_id";
    still_uses_tenant.where = netsec::Condition::eq("tenant_id", netsec::Value("tenant-a"));
    expect_denied(engine.authorize(still_uses_tenant, {"tenant-a", "reader"}),
                  netsec::DenyCode::InvalidPlan);

    engine.set_table_partition_key("audit", "workspace_id");
    netsec::QueryPlan audit;
    audit.table = "audit";
    audit.partition_key_column = "workspace_id";
    audit.where = netsec::Condition::eq("workspace_id", netsec::Value("tenant-a"));
    audit.scan_partition = netsec::Value("tenant-a");
    expect_allowed(engine.authorize(audit, {"tenant-a", "reader"}));
}

}  // namespace

int main() {
    test_full_table_filtering();
    test_secondary_index();
    test_aggregation_leakage();
    test_legitimate_partition_query();
    test_filter_cannot_expand_scope();
    test_cross_partition_policy();
    test_filtering_policy();
    test_authorization_change();
    test_role_change();
    test_scope_analysis();
    test_json_plan();
    test_qualified_columns();
    test_partition_key_is_server_configured();

    if (failures != 0) {
        std::cerr << failures << " check(s) failed\n";
        return 1;
    }
    std::cout << "all authorization checks passed\n";
    return 0;
}
