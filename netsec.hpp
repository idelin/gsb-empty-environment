#pragma once

#include <cstddef>
#include <cstdint>
#include <map>
#include <memory>
#include <optional>
#include <string>
#include <string_view>
#include <variant>
#include <vector>

namespace netsec {

namespace detail {
struct AuthState;
}

class Value {
public:
    using Storage = std::variant<std::monostate, std::string, std::int64_t, double, bool>;

    Value() = default;
    Value(std::string value);
    Value(const char* value);
    Value(std::int64_t value);
    Value(int value);
    Value(double value);
    Value(bool value);

    static Value null();

    bool is_null() const noexcept;
    const Storage& storage() const noexcept;
    std::string canonical() const;
    std::string to_string() const;

    friend bool operator==(const Value& lhs, const Value& rhs);
    friend bool operator!=(const Value& lhs, const Value& rhs) { return !(lhs == rhs); }

private:
    Storage value_;
};

enum class PredicateOp {
    Eq,
    In,
    Lt,
    Le,
    Gt,
    Ge,
    Ne,
};

struct Predicate {
    std::string column;
    PredicateOp op = PredicateOp::Eq;
    std::vector<Value> values;
};

struct Condition;
using ConditionPtr = std::shared_ptr<Condition>;

struct Condition {
    enum class Kind {
        True,
        False,
        Compare,
        And,
        Or,
        Not,
    };

    Kind kind = Kind::True;
    Predicate predicate;
    std::vector<ConditionPtr> children;

    static ConditionPtr truth(bool value);
    static ConditionPtr compare(std::string column, PredicateOp op, std::vector<Value> values);
    static ConditionPtr eq(std::string column, Value value);
    static ConditionPtr in(std::string column, std::vector<Value> values);
    static ConditionPtr ne(std::string column, Value value);
    static ConditionPtr lt(std::string column, Value value);
    static ConditionPtr le(std::string column, Value value);
    static ConditionPtr gt(std::string column, Value value);
    static ConditionPtr ge(std::string column, Value value);
    static ConditionPtr all(std::vector<ConditionPtr> children);
    static ConditionPtr any(std::vector<ConditionPtr> children);
    static ConditionPtr negate(ConditionPtr child);
};

enum class QueryOperation {
    Select,
    Aggregate,
};

enum class ScanType {
    SinglePartition,
    MultiPartition,
    FullTable,
    SecondaryIndex,
};

struct AggregateSpec {
    std::string function;
    std::string column;
    std::string alias;
    bool distinct = false;
};

struct QueryPlan {
    std::string table;
    QueryOperation operation = QueryOperation::Select;
    ScanType scan_type = ScanType::SinglePartition;
    std::string partition_key_column = "tenant_id";
    std::optional<Value> scan_partition;
    std::vector<Value> requested_partitions;
    std::optional<std::string> index_name;
    ConditionPtr where;
    std::vector<std::string> group_by;
    std::vector<AggregateSpec> aggregates;
    bool allow_filtering = false;
    std::uint64_t limit = 0;
    bool distinct = false;
};

struct AuthContext {
    std::string tenant;
    std::string role;
};

struct RolePolicy {
    bool allow_full_table_scan = false;
    bool allow_cross_partition_scan = false;
    bool allow_secondary_index = false;
    bool allow_aggregation = true;
    bool allow_cross_partition_aggregation = false;
    bool allow_filtering = true;
    std::size_t max_partitions_per_query = 1;
};

enum class DenyCode {
    None,
    InvalidPlan,
    MissingIdentity,
    NoGrant,
    MissingPartitionEquality,
    UnauthorizedPartition,
    FullTableScanDenied,
    CrossPartitionDenied,
    SecondaryIndexDenied,
    AggregationDenied,
    PredicateCannotBeBounded,
    FilteringDenied,
    AuthorizationChanged,
};

struct BoundPlan {
    QueryPlan query;
    AuthContext identity;
    std::vector<std::string> authorized_partitions;
    ConditionPtr injected_partition_filter;
    std::uint64_t authorization_epoch = 0;
    bool requires_result_recheck = true;
};

struct AuthorizationResult {
    bool allowed = false;
    DenyCode code = DenyCode::None;
    std::string reason;
    BoundPlan plan;

    explicit operator bool() const noexcept { return allowed; }
};

struct PartitionScope {
    bool valid = true;
    bool satisfiable = true;
    bool bounded = false;
    std::vector<Value> values;
};

PartitionScope analyze_partition_scope(const ConditionPtr& condition,
                                       std::string_view partition_key_column);

struct DbRow {
    std::map<std::string, Value, std::less<>> columns;
    std::optional<Value> partition;

    const Value* find(std::string_view column) const;
    std::optional<Value> get(std::string_view column) const;
};

class AuthzEngine;

class ResultGuard {
public:
    ResultGuard() = default;

    bool valid() const;
    bool accept(const DbRow& row) const;
    bool check(const DbRow& row) const { return accept(row); }
    std::vector<DbRow> filter(const std::vector<DbRow>& rows) const;
    std::vector<DbRow> filter(std::vector<DbRow>&& rows) const;

    const AuthContext& identity() const noexcept;
    const std::vector<std::string>& partitions() const noexcept;
    std::uint64_t authorization_epoch() const noexcept;

private:
    friend class AuthzEngine;

    std::shared_ptr<detail::AuthState> state_;
    AuthContext identity_;
    std::string partition_key_column_;
    std::vector<std::string> partitions_;
    std::vector<std::string> partition_canonicals_;
    std::uint64_t epoch_ = 0;
    bool allow_missing_partition_evidence_ = false;
};

class AuthzEngine {
public:
    AuthzEngine();
    explicit AuthzEngine(RolePolicy default_policy);

    AuthzEngine(const AuthzEngine&) = default;
    AuthzEngine& operator=(const AuthzEngine&) = default;
    AuthzEngine(AuthzEngine&&) noexcept = default;
    AuthzEngine& operator=(AuthzEngine&&) noexcept = default;

    void set_default_policy(RolePolicy policy);
    RolePolicy default_policy() const;

    void set_partition_key_column(std::string column);
    void set_table_partition_key(std::string table, std::string column);
    std::string partition_key_column(std::string_view table) const;

    void set_role_policy(std::string role, RolePolicy policy);
    void clear_role_policy(std::string_view role);
    RolePolicy role_policy(std::string_view role) const;

    void grant(std::string tenant, std::string role, std::vector<std::string> partitions);
    void set_grant(std::string tenant, std::string role, std::vector<std::string> partitions);
    void revoke(std::string_view tenant, std::string_view role, std::vector<std::string> partitions);
    void remove_principal(std::string_view tenant, std::string_view role);
    void clear();

    bool is_granted(std::string_view tenant, std::string_view role,
                    std::string_view partition) const;
    std::vector<std::string> partitions_for(std::string_view tenant,
                                            std::string_view role) const;
    std::uint64_t authorization_epoch() const;

    AuthorizationResult authorize(const QueryPlan& plan, const AuthContext& identity) const;
    AuthorizationResult authorize(const QueryPlan& plan, std::string_view tenant,
                                  std::string_view role) const {
        return authorize(plan, AuthContext{std::string(tenant), std::string(role)});
    }

    ResultGuard guard(const AuthorizationResult& result) const;
    std::optional<ResultGuard> make_guard(const AuthorizationResult& result) const;

private:
    std::shared_ptr<detail::AuthState> state_;
};

using QueryAuthorizer = AuthzEngine;
using AuthDecision = AuthorizationResult;
using AccessPolicy = RolePolicy;

std::optional<QueryPlan> parse_plan_json(std::string_view json, std::string* error = nullptr);
std::optional<QueryPlan> parse_query_plan(std::string_view json, std::string* error = nullptr);
std::string plan_to_json(const QueryPlan& plan);

}  // namespace netsec
