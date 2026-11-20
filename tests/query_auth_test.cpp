#include "widetable/query_auth.hpp"

#include <iostream>
#include <string>
#include <utility>
#include <vector>

namespace auth = wide::query_auth;

namespace {

int failures = 0;

#define CHECK(expression)                                                     \
    do {                                                                      \
        if (!(expression)) {                                                  \
            std::cerr << "FAIL " << __LINE__ << ": " #expression << '\n';      \
            ++failures;                                                       \
        }                                                                     \
    } while (false)

#define CHECK_CODE(decision, expected)                                        \
    do {                                                                      \
        auto&& value = (decision);                                             \
        if (value.code != (expected)) {                                       \
            std::cerr << "FAIL " << __LINE__ << ": expected "                 \
                      << auth::to_string(expected) << ", got "                \
                      << auth::to_string(value.code) << " ("                  \
                      << value.message << ")\n";                              \
            ++failures;                                                       \
        }                                                                     \
    } while (false)

auth::PartitionKey key(
    std::initializer_list<std::pair<std::string, auth::Value>> entries) {
    auth::PartitionKey result;
    for (const auto& [column, value] : entries) {
        result.emplace(column, value);
    }
    return result;
}

auth::PartitionKey events_key(const std::string& tenant, std::int64_t bucket) {
    return key({{"tenant_id", auth::Value(tenant)},
                {"bucket", auth::Value(bucket)}});
}

auth::TablePolicy events_policy() {
    auth::TablePolicy table;
    table.partition_key_columns = {"tenant_id", "bucket"};
    table.tenant_column = "tenant_id";
    table.authorized_partitions = {
        events_key("tenant-a", 1),
        events_key("tenant-a", 2),
    };
    return table;
}

auth::TablePolicy docs_policy(bool allow_disjunction) {
    auth::TablePolicy table;
    table.partition_key_columns = {"tenant_id"};
    // No implicit tenant injection: this table is used to prove that a key
    // equality hidden under OR/NOT is not a mandatory constraint.
    table.tenant_column = "";
    table.allow_disjunctive_filters = allow_disjunction;
    table.authorized_partitions = {key({{"tenant_id", auth::Value("tenant-a")}})};
    return table;
}

auth::TablePolicy metrics_policy() {
    auth::TablePolicy table;
    table.partition_key_columns = {"tenant_id"};
    table.tenant_column = ""; // The partition key itself is the tenant.
    table.authorized_partitions = {key({{"tenant_id", auth::Value("tenant-a")}})};
    table.allow_aggregation = true;
    table.allow_full_scan = true;
    table.allow_cross_partition_scan = true;
    table.allow_token_range = true;
    return table;
}

void add_grant(auth::AuthPolicy& policy,
               const std::string& tenant,
               const std::string& role,
               const std::string& table_name,
               auth::TablePolicy table) {
    auth::RoleGrant grant;
    grant.tenant_id = tenant;
    grant.role = role;
    grant.tables.emplace(table_name, std::move(table));
    policy.grants.push_back(std::move(grant));
}

auth::AuthPolicy make_policy() {
    auth::AuthPolicy policy;
    add_grant(policy, "tenant-a", "reader", "events", events_policy());
    add_grant(policy, "tenant-a", "reader", "docs",
              docs_policy(/*allow_disjunction=*/false));

    auto index_reader = events_policy();
    index_reader.allow_secondary_index = true;
    index_reader.allow_secondary_index_backlookup = true;
    add_grant(policy, "tenant-a", "index_reader", "events",
              std::move(index_reader));

    auto index_no_backlookup = events_policy();
    index_no_backlookup.allow_secondary_index = true;
    index_no_backlookup.allow_secondary_index_backlookup = false;
    add_grant(policy, "tenant-a", "index_no_backlookup", "events",
              std::move(index_no_backlookup));

    auto analyst = events_policy();
    analyst.allow_aggregation = true;
    analyst.allow_full_scan = true;
    analyst.allow_cross_partition_scan = true;
    analyst.max_partitions_per_query = 8;

    auto analyst_cross = analyst;
    analyst_cross.allow_cross_partition_aggregation = true;
    add_grant(policy, "tenant-a", "analyst", "events", std::move(analyst));
    add_grant(policy, "tenant-a", "analyst_cross", "events",
              std::move(analyst_cross));

    auto analyst_single = events_policy();
    analyst_single.allow_aggregation = true;
    analyst_single.require_all_partition_key_components = true;
    add_grant(policy, "tenant-a", "analyst_single", "events",
              std::move(analyst_single));

    auto admin = events_policy();
    admin.allow_full_scan = true;
    admin.allow_token_range = true;
    admin.allow_cross_partition_scan = true;
    add_grant(policy, "tenant-a", "admin", "events", std::move(admin));

    auto docs_disjunction = docs_policy(/*allow_disjunction=*/true);
    add_grant(policy, "tenant-a", "docs_disjunction", "docs",
              std::move(docs_disjunction));

    add_grant(policy, "tenant-a", "metric_analyst", "metrics",
              metrics_policy());
    auto metrics_reader = metrics_policy();
    metrics_reader.allow_aggregation = false;
    add_grant(policy, "tenant-a", "metric_reader", "metrics",
              std::move(metrics_reader));
    return policy;
}

auth::QueryPlan select_plan(auth::Condition where,
                            auth::AccessPath path = auth::AccessPath::Partition,
                            std::string table = "events") {
    auth::QueryPlan plan;
    plan.table = std::move(table);
    plan.access_path = path;
    plan.where = std::move(where);
    return plan;
}

auth::QueryPlan aggregate_plan(auth::Condition where,
                               auth::AccessPath path,
                               std::vector<std::string> group_by = {}) {
    auth::QueryPlan plan;
    plan.kind = auth::StatementKind::Aggregate;
    plan.table = "events";
    plan.access_path = path;
    plan.where = std::move(where);
    plan.aggregates.push_back(
        {auth::AggregateFunction::Count, "*", "n"});
    plan.group_by = std::move(group_by);
    return plan;
}

auth::ResultRow row(auth::PartitionKey partition,
                    auth::RowValues values = {},
                    std::optional<std::string> tenant = std::nullopt) {
    auth::ResultRow result;
    result.source_partition = std::move(partition);
    result.values = std::move(values);
    result.tenant_id = std::move(tenant);
    return result;
}

void test_full_table_filter_attack() {
    auth::QueryAuthorizer authorizer(make_policy());
    const auth::SecurityContext reader{"tenant-a", "reader"};

    // Low-cardinality filter with ALLOW FILTERING but no partition equality.
    auto attack = select_plan(
        auth::Condition::all(
            {auth::Condition::eq("region", auth::Value("us"))}),
        auth::AccessPath::FullScan);
    attack.allow_filtering = true;
    const auto denied = authorizer.authorize(attack, reader);
    CHECK(!denied.allowed());
    CHECK(denied.code != auth::DenialCode::Ok);

    // Even a role that is allowed to scan may not bypass the mandatory
    // partition-key equality.  This table has no implicit tenant predicate,
    // so the low-cardinality filter is the only predicate.
    auto pure_filter = select_plan(
        auth::Condition::all(
            {auth::Condition::eq("region", auth::Value("us"))}),
        auth::AccessPath::FullScan, "metrics");
    pure_filter.allow_filtering = true;
    CHECK_CODE(
        authorizer.authorize(pure_filter, {"tenant-a", "metric_reader"}),
        auth::DenialCode::MissingPartitionEquality);

    // A complete partition equality does not make a full scan acceptable for
    // a role without the full-scan capability.
    auto scan_with_key = select_plan(
        auth::Condition::all({auth::Condition::eq(
                                  "tenant_id", auth::Value("tenant-a")),
                              auth::Condition::eq("bucket", auth::Value(1)),
                              auth::Condition::eq("region",
                                                  auth::Value("us"))}),
        auth::AccessPath::FullScan);
    scan_with_key.allow_filtering = true;
    const auto scan_denied = authorizer.authorize(scan_with_key, reader);
    CHECK_CODE(scan_denied, auth::DenialCode::FullScanDenied);

    auto range_scan = scan_with_key;
    range_scan.access_path = auth::AccessPath::TokenRange;
    const auto range_denied = authorizer.authorize(range_scan, reader);
    CHECK_CODE(range_denied, auth::DenialCode::TokenRangeDenied);

    // IN is not an equality and cannot be used to enumerate partitions.
    auto in_attack = select_plan(
        auth::Condition::all({auth::Condition::in(
            "bucket", {auth::Value(1), auth::Value(2)})}),
        auth::AccessPath::FullScan);
    const auto in_denied = authorizer.authorize(in_attack, reader);
    CHECK(!in_denied.allowed());
}

void test_secondary_index() {
    auth::QueryAuthorizer authorizer(make_policy());

    auto plan = select_plan(
        auth::Condition::all(
            {auth::Condition::eq("tenant_id", auth::Value("tenant-a")),
             auth::Condition::eq("bucket", auth::Value(1)),
             auth::Condition::eq("email", auth::Value("a@example.test"))}),
        auth::AccessPath::SecondaryIndex);
    plan.index = auth::IndexSpec{"by_email", {"email"}, true};

    const auto reader_denied =
        authorizer.authorize(plan, {"tenant-a", "reader"});
    CHECK_CODE(reader_denied, auth::DenialCode::SecondaryIndexDenied);

    const auto backlookup_denied =
        authorizer.authorize(plan, {"tenant-a", "index_no_backlookup"});
    CHECK_CODE(backlookup_denied, auth::DenialCode::IndexBacklookupDenied);

    // An unbound index column would turn the lookup into an index-wide scan.
    auto unbound = plan;
    unbound.index = auth::IndexSpec{"by_email", {"email", "region"}, true};
    const auto index_not_bound =
        authorizer.authorize(unbound, {"tenant-a", "index_reader"});
    CHECK_CODE(index_not_bound, auth::DenialCode::IndexNotBound);

    const auto allowed =
        authorizer.authorize(plan, {"tenant-a", "index_reader"});
    CHECK_CODE(allowed, auth::DenialCode::Ok);
    CHECK(allowed.authorized.tenant_id == "tenant-a");
    CHECK(allowed.authorized.effective_plan.role == "index_reader");

    auto rows = std::vector<auth::ResultRow>{
        row(events_key("tenant-a", 1), {{"amount", auth::Value(5)}}),
        row(events_key("tenant-a", 2), {{"amount", auth::Value(6)}}),
        row(events_key("tenant-b", 1), {{"amount", auth::Value(7)}}),
    };
    const auto review = authorizer.recheck(allowed.authorized, std::move(rows));
    CHECK(review.rows.size() == 1);
    CHECK(review.rejected_rows == 2);
    CHECK(review.rows.front().source_partition == events_key("tenant-a", 1));
}

void test_aggregation_leakage() {
    auth::QueryAuthorizer authorizer(make_policy());

    // No partition equality: aggregation cannot be used as an oracle.
    auto metric_plan = aggregate_plan(
        auth::Condition::all(
            {auth::Condition::eq("region", auth::Value("us"))}),
        auth::AccessPath::FullScan);
    metric_plan.table = "metrics";
    const auto missing_key =
        authorizer.authorize(metric_plan, {"tenant-a", "metric_analyst"});
    CHECK_CODE(missing_key, auth::DenialCode::MissingPartitionEquality);

    // Role lacking the aggregation capability is rejected even with a key.
    const auto role_denied = authorizer.authorize(
        aggregate_plan(
            auth::Condition::all(
                {auth::Condition::eq("tenant_id", auth::Value("tenant-a")),
                 auth::Condition::eq("bucket", auth::Value(1))}),
            auth::AccessPath::Partition),
        {"tenant-a", "reader"});
    CHECK_CODE(role_denied, auth::DenialCode::AggregationDenied);

    // A tenant-only predicate would aggregate two authorized partitions.
    const auto cross_denied = authorizer.authorize(
        aggregate_plan(
            auth::Condition::all(
                {auth::Condition::eq("tenant_id", auth::Value("tenant-a"))}),
            auth::AccessPath::FullScan),
        {"tenant-a", "analyst"});
    CHECK_CODE(cross_denied,
               auth::DenialCode::CrossPartitionAggregationDenied);

    // Even when cross-partition aggregation is granted, the result must be
    // scoped by the complete partition key.
    const auto unscoped = authorizer.authorize(
        aggregate_plan(
            auth::Condition::all(
                {auth::Condition::eq("tenant_id", auth::Value("tenant-a"))}),
            auth::AccessPath::FullScan),
        {"tenant-a", "analyst_cross"});
    CHECK_CODE(unscoped, auth::DenialCode::AggregationScopeUnknown);

    const auto grouped = authorizer.authorize(
        aggregate_plan(
            auth::Condition::all(
                {auth::Condition::eq("tenant_id", auth::Value("tenant-a"))}),
            auth::AccessPath::FullScan,
            {"tenant_id", "bucket"}),
        {"tenant-a", "analyst_cross"});
    CHECK_CODE(grouped, auth::DenialCode::Ok);

    auto grouped_rows = std::vector<auth::ResultRow>{
        row(events_key("tenant-a", 1), {{"n", auth::Value(10)}},
            std::string("tenant-a")),
        row(events_key("tenant-a", 2), {{"n", auth::Value(20)}},
            std::string("tenant-a")),
        row(events_key("tenant-b", 1), {{"n", auth::Value(99)}},
            std::string("tenant-b")),
    };
    const auto grouped_review =
        authorizer.recheck(grouped.authorized, std::move(grouped_rows));
    CHECK(grouped_review.rows.size() == 2);
    CHECK(grouped_review.rejected_rows == 1);

    // A single-partition COUNT has a scope known from the plan, so an
    // ungrouped aggregate row is safe and must be accepted.
    const auto single = authorizer.authorize(
        aggregate_plan(
            auth::Condition::all(
                {auth::Condition::eq("tenant_id", auth::Value("tenant-a")),
                 auth::Condition::eq("bucket", auth::Value(1))}),
            auth::AccessPath::Partition),
        {"tenant-a", "analyst_single"});
    CHECK_CODE(single, auth::DenialCode::Ok);
    auto count_rows = std::vector<auth::ResultRow>{
        row({}, {{"n", auth::Value(42)}})};
    const auto count_review =
        authorizer.recheck(single.authorized, std::move(count_rows));
    CHECK(count_review.rows.size() == 1);
}

void test_legal_partition_query_and_result_recheck() {
    auth::QueryAuthorizer authorizer(make_policy());
    const auto decision = authorizer.authorize(
        select_plan(auth::Condition::all(
            {auth::Condition::eq("tenant_id", auth::Value("tenant-a")),
             auth::Condition::eq("bucket", auth::Value(1))})),
        {"tenant-a", "reader"});
    CHECK_CODE(decision, auth::DenialCode::Ok);
    CHECK(decision.authorized.effective_plan.tenant_id == "tenant-a");
    CHECK(decision.authorized.effective_plan.role == "reader");
    CHECK(decision.authorized.authorized_partitions.size() == 1);

    auto rows = std::vector<auth::ResultRow>{
        // Authorized partition, tenant provenance supplied separately.
        row(events_key("tenant-a", 1), {{"amount", auth::Value(10)}},
            std::string("tenant-a")),
        // Same tenant, different partition: outside this query's scope.
        row(events_key("tenant-a", 2), {{"amount", auth::Value(20)}},
            std::string("tenant-a")),
        // Different tenant: must never be returned.
        row(events_key("tenant-b", 1), {{"amount", auth::Value(30)}},
            std::string("tenant-b")),
        // Projection need not contain the key; provenance is enough.
        row(events_key("tenant-a", 1), {{"amount", auth::Value(11)}}),
        // Projected key is inconsistent with storage provenance.
        row(events_key("tenant-a", 1),
            {{"tenant_id", auth::Value("tenant-a")},
             {"bucket", auth::Value(2)}},
            std::string("tenant-a")),
        // Tenant provenance conflicts with the authorized tenant.
        row(events_key("tenant-a", 1), {{"amount", auth::Value(12)}},
            std::string("tenant-b")),
    };

    const auto review = authorizer.recheck(decision.authorized, std::move(rows));
    CHECK(review.rows.size() == 2);
    CHECK(review.rejected_rows == 4);
    CHECK(review.violation);
    for (const auto& returned : review.rows) {
        const auto partition = returned.source_partition;
        CHECK(partition.at("tenant_id").data == auth::Value("tenant-a").data);
        CHECK(partition.at("bucket").data == auth::Value(1).data);
    }
}

void test_full_scan_result_is_filtered() {
    auth::QueryAuthorizer authorizer(make_policy());
    auto plan = select_plan(
        auth::Condition::all(
            {auth::Condition::eq("tenant_id", auth::Value("tenant-a")),
             auth::Condition::eq("bucket", auth::Value(1)),
             auth::Condition::eq("region", auth::Value("us"))}),
        auth::AccessPath::FullScan);
    plan.allow_filtering = true;
    const auto decision = authorizer.authorize(plan, {"tenant-a", "admin"});
    CHECK_CODE(decision, auth::DenialCode::Ok);

    auto rows = std::vector<auth::ResultRow>{
        row(events_key("tenant-a", 1), {{"amount", auth::Value(1)}},
            std::string("tenant-a")),
        row(events_key("tenant-a", 2), {{"amount", auth::Value(2)}},
            std::string("tenant-a")),
        row(events_key("tenant-b", 1), {{"amount", auth::Value(3)}},
            std::string("tenant-b")),
    };
    const auto review = authorizer.recheck(decision.authorized, std::move(rows));
    CHECK(review.rows.size() == 1);
    CHECK(review.rejected_rows == 2);
    CHECK(review.rows.front().source_partition == events_key("tenant-a", 1));
}

void test_authorization_change() {
    auth::AuthPolicy policy = make_policy();
    auth::QueryAuthorizer authorizer(policy);
    const auth::SecurityContext reader{"tenant-a", "reader"};
    auto plan = select_plan(auth::Condition::all(
        {auth::Condition::eq("tenant_id", auth::Value("tenant-a")),
         auth::Condition::eq("bucket", auth::Value(1))}));

    const auto before = authorizer.authorize(plan, reader);
    CHECK_CODE(before, auth::DenialCode::Ok);
    auto initial_review = authorizer.recheck(
        before.authorized,
        {row(events_key("tenant-a", 1), {{"amount", auth::Value(1)}})});
    CHECK(initial_review.rows.size() == 1);
    CHECK(initial_review.ok());

    // Revoke bucket 1 while keeping the role and bucket 2.
    auth::AuthPolicy changed = make_policy();
    for (auto& grant : changed.grants) {
        if (grant.tenant_id == "tenant-a" && grant.role == "reader") {
            auto item = grant.tables.find("events");
            if (item != grant.tables.end()) {
                item->second.authorized_partitions = {
                    events_key("tenant-a", 2)};
            }
        }
    }
    authorizer.set_policy(std::move(changed));

    const auto revoked = authorizer.authorize(plan, reader);
    CHECK_CODE(revoked, auth::DenialCode::UnauthorizedPartition);

    // Tokens issued before the policy change cannot be replayed.
    auto stale_review = authorizer.recheck(
        before.authorized,
        {row(events_key("tenant-a", 1), {{"amount", auth::Value(1)}})});
    CHECK_CODE(stale_review, auth::DenialCode::StaleAuthorization);
    CHECK(stale_review.rows.empty());

    // Rebuilding the authorizer with a different policy must reject old
    // tokens as well; authorization is bound to policy content, not only to
    // an in-memory counter.
    auth::AuthPolicy replacement_policy = make_policy();
    for (auto& grant : replacement_policy.grants) {
        if (grant.tenant_id == "tenant-a" && grant.role == "reader") {
            auto item = grant.tables.find("events");
            if (item != grant.tables.end()) {
                item->second.authorized_partitions = {
                    events_key("tenant-a", 2)};
            }
        }
    }
    auth::QueryAuthorizer replacement(std::move(replacement_policy));
    const auto replacement_review = replacement.recheck(
        before.authorized,
        {row(events_key("tenant-a", 1), {{"amount", auth::Value(1)}})});
    CHECK_CODE(replacement_review, auth::DenialCode::StaleAuthorization);
    CHECK(replacement_review.rows.empty());

    auto bucket_two = select_plan(auth::Condition::all(
        {auth::Condition::eq("tenant_id", auth::Value("tenant-a")),
         auth::Condition::eq("bucket", auth::Value(2))}));
    CHECK_CODE(authorizer.authorize(bucket_two, reader), auth::DenialCode::Ok);

    // Removing the grant denies new queries immediately.
    auth::AuthPolicy no_reader = make_policy();
    no_reader.grants.erase(
        std::remove_if(no_reader.grants.begin(), no_reader.grants.end(),
                       [](const auth::RoleGrant& grant) {
                           return grant.role == "reader";
                       }),
        no_reader.grants.end());
    authorizer.set_policy(std::move(no_reader));
    CHECK_CODE(authorizer.authorize(plan, reader),
               auth::DenialCode::UnknownRole);
}

void test_filter_cannot_broaden_scope() {
    auth::QueryAuthorizer authorizer(make_policy());

    // A key equality inside OR is not a mandatory constraint.
    auto or_plan = select_plan(
        auth::Condition::any(
            {auth::Condition::eq("tenant_id", auth::Value("tenant-a")),
             auth::Condition::eq("region", auth::Value("us"))}),
        auth::AccessPath::Partition,
        "docs");
    const auto or_denied =
        authorizer.authorize(or_plan, {"tenant-a", "docs_disjunction"});
    CHECK_CODE(or_denied, auth::DenialCode::MissingPartitionEquality);

    auto not_plan = select_plan(
        auth::Condition::negate(auth::Condition::eq(
            "tenant_id", auth::Value("tenant-b"))),
        auth::AccessPath::Partition,
        "docs");
    const auto not_denied =
        authorizer.authorize(not_plan, {"tenant-a", "docs_disjunction"});
    CHECK_CODE(not_denied, auth::DenialCode::MissingPartitionEquality);

    // A disjunction under a mandatory partition equality can only narrow the
    // already-authorized partition and is accepted.
    auto narrow = select_plan(
        auth::Condition::all(
            {auth::Condition::eq("tenant_id", auth::Value("tenant-a")),
             auth::Condition::any(
                 {auth::Condition::eq("region", auth::Value("us")),
                  auth::Condition::eq("region", auth::Value("eu"))})}),
        auth::AccessPath::Partition,
        "docs");
    CHECK_CODE(
        authorizer.authorize(narrow, {"tenant-a", "docs_disjunction"}),
        auth::DenialCode::Ok);

    // Conflicting equalities are not a valid partition binding.
    auto conflicting = select_plan(
        auth::Condition::all(
            {auth::Condition::eq("tenant_id", auth::Value("tenant-a")),
             auth::Condition::eq("tenant_id", auth::Value("tenant-b"))}),
        auth::AccessPath::Partition,
        "docs");
    CHECK_CODE(
        authorizer.authorize(conflicting, {"tenant-a", "docs_disjunction"}),
        auth::DenialCode::ConflictingPartitionConditions);

    // Default policy rejects disjunctions outright.
    auto default_or = or_plan;
    default_or.where = auth::Condition::any(
        {auth::Condition::eq("tenant_id", auth::Value("tenant-a")),
         auth::Condition::eq("region", auth::Value("us"))});
    CHECK_CODE(authorizer.authorize(default_or, {"tenant-a", "reader"}),
               auth::DenialCode::DisjunctiveFilterDenied);
}

void test_json_plan_parsing_and_identity_injection() {
    auth::QueryAuthorizer authorizer(make_policy());

    const std::string text = R"json(
        {
          "statement_id": "q-1",
          "kind": "select",
          "table": "events",
          "access_path": "partition",
          "where": {
            "and": [
              {"column": "bucket", "op": "eq", "value": 1}
            ]
          },
          "tenant_id": "tenant-a",
          "role": "reader"
        }
    )json";
    std::string error;
    const auto parsed = auth::try_parse_query_plan(text, &error);
    CHECK(parsed.has_value());
    if (!parsed.has_value()) {
        std::cerr << "parse error: " << error << '\n';
        return;
    }
    const auto decision = authorizer.authorize(*parsed, {"tenant-a", "reader"});
    CHECK_CODE(decision, auth::DenialCode::Ok);
    // The tenant predicate is injected at the plan layer.
    CHECK(decision.authorized.effective_plan.tenant_id == "tenant-a");
    CHECK(decision.authorized.effective_plan.role == "reader");

    // A caller-supplied tenant is never trusted.
    auto forged = *parsed;
    forged.tenant_id = "tenant-b";
    CHECK_CODE(authorizer.authorize(forged, {"tenant-a", "reader"}),
               auth::DenialCode::IdentityMismatch);

    const std::string key_object = R"json(
        {
          "kind": "select",
          "table": "events",
          "partition_key": {"tenant_id": "tenant-a", "bucket": 1}
        }
    )json";
    const auto key_plan = auth::try_parse_query_plan(key_object, &error);
    CHECK(key_plan.has_value());
    if (key_plan.has_value()) {
        CHECK_CODE(authorizer.authorize(*key_plan, {"tenant-a", "reader"}),
                   auth::DenialCode::Ok);
    }

    CHECK(!auth::try_parse_query_plan("{not json", &error).has_value());
    CHECK(!error.empty());
}

void test_tenant_cannot_be_selected_by_plan() {
    auth::QueryAuthorizer authorizer(make_policy());
    auto plan = select_plan(auth::Condition::all(
        {auth::Condition::eq("tenant_id", auth::Value("tenant-b")),
         auth::Condition::eq("bucket", auth::Value(1))}));
    CHECK_CODE(authorizer.authorize(plan, {"tenant-a", "reader"}),
               auth::DenialCode::IdentityMismatch);
}

} // namespace

int main() {
    test_full_table_filter_attack();
    test_secondary_index();
    test_aggregation_leakage();
    test_legal_partition_query_and_result_recheck();
    test_full_scan_result_is_filtered();
    test_authorization_change();
    test_filter_cannot_broaden_scope();
    test_json_plan_parsing_and_identity_injection();
    test_tenant_cannot_be_selected_by_plan();

    if (failures != 0) {
        std::cerr << failures << " check(s) failed\n";
        return 1;
    }
    std::cout << "all query authorization tests passed\n";
    return 0;
}
