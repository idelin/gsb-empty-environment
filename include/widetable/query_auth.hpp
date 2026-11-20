#pragma once

// Wide-table query authorization.
//
// This component is deliberately independent of any database transport or
// storage engine.  A service hands it the parsed query plan and the identity
// established by authentication, then executes only an AuthorizedQuery.  Rows
// coming back from storage are passed through recheck() before being returned
// to the caller.
//
// The security model is fail-closed:
//   * the policy, not the request, owns the partition-key definition;
//   * an equality predicate on an authorized partition key is mandatory;
//   * the equality must be in a conjunctive (mandatory) part of the filter;
//   * full scans, token-range scans, secondary-index backlookups and
//     aggregations are capabilities granted explicitly by policy;
//   * tenant and role are overwritten from the authenticated session and can
//     never be selected by the caller;
//   * every returned row is checked against the authorized partition set.

#include <algorithm>
#include <atomic>
#include <cctype>
#include <charconv>
#include <cstddef>
#include <cstdint>
#include <functional>
#include <limits>
#include <map>
#include <memory>
#include <mutex>
#include <optional>
#include <shared_mutex>
#include <sstream>
#include <stdexcept>
#include <string>
#include <string_view>
#include <utility>
#include <variant>
#include <vector>

namespace wide::query_auth {

class PlanParseError : public std::runtime_error {
public:
    using std::runtime_error::runtime_error;
};

// ---------------------------------------------------------------------------
// Values and partition keys
// ---------------------------------------------------------------------------

struct Value {
    using Storage =
        std::variant<std::monostate, std::int64_t, double, bool, std::string>;

    Storage data;

    Value() = default;
    Value(std::nullptr_t) : data(std::monostate{}) {}
    Value(int value) : data(static_cast<std::int64_t>(value)) {}
    Value(std::int64_t value) : data(value) {}
    Value(double value) : data(value) {}
    Value(bool value) : data(value) {}
    Value(const char* value) : data(std::string(value)) {}
    Value(std::string value) : data(std::move(value)) {}

    [[nodiscard]] bool is_null() const noexcept {
        return std::holds_alternative<std::monostate>(data);
    }

    template <typename T>
    [[nodiscard]] const T* get_if() const noexcept {
        return std::get_if<T>(&data);
    }
};

[[nodiscard]] inline bool value_equal(const Value& lhs, const Value& rhs) {
    if (lhs.data.index() != rhs.data.index()) {
        return false;
    }
    if (lhs.is_null()) {
        return true;
    }
    if (const auto* value = lhs.get_if<std::int64_t>()) {
        return *value == *rhs.get_if<std::int64_t>();
    }
    if (const auto* value = lhs.get_if<double>()) {
        return *value == *rhs.get_if<double>();
    }
    if (const auto* value = lhs.get_if<bool>()) {
        return *value == *rhs.get_if<bool>();
    }
    return *lhs.get_if<std::string>() == *rhs.get_if<std::string>();
}

inline bool operator==(const Value& lhs, const Value& rhs) {
    return value_equal(lhs, rhs);
}

inline bool operator!=(const Value& lhs, const Value& rhs) {
    return !value_equal(lhs, rhs);
}

[[nodiscard]] inline std::string value_to_string(const Value& value) {
    if (value.is_null()) {
        return "null";
    }
    if (const auto* item = value.get_if<std::int64_t>()) {
        return std::to_string(*item);
    }
    if (const auto* item = value.get_if<double>()) {
        std::ostringstream output;
        output << *item;
        return output.str();
    }
    if (const auto* item = value.get_if<bool>()) {
        return *item ? "true" : "false";
    }
    return *value.get_if<std::string>();
}

// A composite partition key.  Keys are column names and values are compared
// with strict type equality.
using PartitionKey = std::map<std::string, Value, std::less<>>;
using RowValues = std::map<std::string, Value, std::less<>>;

[[nodiscard]] inline bool partition_matches(const PartitionKey& candidate,
                                            const PartitionKey& requested) {
    for (const auto& [column, value] : requested) {
        const auto item = candidate.find(column);
        if (item == candidate.end() || !value_equal(item->second, value)) {
            return false;
        }
    }
    return true;
}

[[nodiscard]] inline bool partition_is_empty(const PartitionKey& key) {
    return key.empty();
}

[[nodiscard]] inline std::string partition_to_string(const PartitionKey& key) {
    std::ostringstream output;
    output << '{';
    bool first = true;
    for (const auto& [column, value] : key) {
        if (!first) {
            output << ", ";
        }
        first = false;
        output << column << '=' << value_to_string(value);
    }
    output << '}';
    return output.str();
}

// ---------------------------------------------------------------------------
// Query-plan model
// ---------------------------------------------------------------------------

enum class CompareOp {
    Eq,
    Ne,
    Lt,
    Le,
    Gt,
    Ge,
    In,
    Contains,
    IsNull,
    IsNotNull,
};

enum class ConditionKind { Predicate, And, Or, Not };

struct Condition {
    ConditionKind kind = ConditionKind::Predicate;
    std::string column;
    CompareOp op = CompareOp::Eq;
    std::vector<Value> values;
    std::vector<Condition> children;

    Condition() = default;

    static Condition predicate(std::string column,
                               CompareOp op,
                               std::vector<Value> values = {}) {
        Condition result;
        result.kind = ConditionKind::Predicate;
        result.column = std::move(column);
        result.op = op;
        result.values = std::move(values);
        return result;
    }

    static Condition eq(std::string column, Value value) {
        return predicate(std::move(column), CompareOp::Eq, {std::move(value)});
    }

    static Condition ne(std::string column, Value value) {
        return predicate(std::move(column), CompareOp::Ne, {std::move(value)});
    }

    static Condition in(std::string column, std::vector<Value> values) {
        return predicate(std::move(column), CompareOp::In, std::move(values));
    }

    static Condition all(std::vector<Condition> children) {
        Condition result;
        result.kind = ConditionKind::And;
        result.children = std::move(children);
        return result;
    }

    static Condition any(std::vector<Condition> children) {
        Condition result;
        result.kind = ConditionKind::Or;
        result.children = std::move(children);
        return result;
    }

    static Condition negate(Condition child) {
        Condition result;
        result.kind = ConditionKind::Not;
        result.children.push_back(std::move(child));
        return result;
    }
};

enum class StatementKind { Select, Aggregate };
enum class AccessPath { Partition, SecondaryIndex, FullScan, TokenRange };
enum class AggregateFunction { Count, Sum, Avg, Min, Max };

struct AggregateSpec {
    AggregateFunction function = AggregateFunction::Count;
    std::string column;
    std::string alias;
};

struct IndexSpec {
    std::string name;
    std::vector<std::string> columns;
    bool requires_backlookup = true;
};

struct QueryPlan {
    std::string statement_id;
    StatementKind kind = StatementKind::Select;
    std::string table;
    AccessPath access_path = AccessPath::Partition;
    std::vector<std::string> partition_key_columns;
    Condition where = Condition::all({});
    std::vector<std::string> selected_columns;
    std::vector<AggregateSpec> aggregates;
    std::vector<std::string> group_by;
    std::optional<IndexSpec> index;
    std::optional<std::uint64_t> limit;
    bool allow_filtering = false;

    // These fields are parsed for mismatch detection only.  They are always
    // overwritten from SecurityContext before the plan is authorized.
    std::string tenant_id;
    std::string role;
};

// ---------------------------------------------------------------------------
// Policy
// ---------------------------------------------------------------------------

enum class ResultReviewMode {
    // Produce only rows whose partition is in the authorized set and record a
    // violation.  This keeps legitimate rows of an otherwise over-broad
    // administrator scan from being exposed when the policy explicitly allows
    // that scan.
    DropUnauthorized,
    // If any returned row violates the scope, return no rows at all.
    FailClosed,
};

struct TablePolicy {
    // Authoritative partition-key definition.  A plan may repeat this list,
    // but it may never redefine it.
    std::vector<std::string> partition_key_columns;

    // Exact partitions this role may read.  Every entry is a complete
    // partition key for this table.
    std::vector<PartitionKey> authorized_partitions;

    // Optional tenant column.  When set, an equality predicate for the
    // authenticated tenant is injected into the effective plan and result rows
    // must carry matching tenant provenance.
    std::string tenant_column;

    bool allow_full_scan = false;
    bool allow_token_range = false;
    bool allow_cross_partition_scan = false;
    bool allow_secondary_index = false;
    bool allow_secondary_index_backlookup = false;
    bool require_full_index_binding = true;
    bool allow_aggregation = false;
    bool allow_cross_partition_aggregation = false;
    bool allow_disjunctive_filters = false;
    bool require_all_partition_key_components = false;
    bool require_tenant_provenance = true;
    std::size_t max_partitions_per_query = 1;
    std::optional<std::uint64_t> max_limit;
    ResultReviewMode review_mode = ResultReviewMode::DropUnauthorized;
};

struct RoleGrant {
    std::string tenant_id;
    std::string role;
    std::map<std::string, TablePolicy, std::less<>> tables;
};

struct AuthPolicy {
    std::vector<RoleGrant> grants;

    [[nodiscard]] bool has_grant(std::string_view tenant,
                                 std::string_view role) const {
        return std::any_of(grants.begin(), grants.end(),
                           [&](const RoleGrant& grant) {
                               return grant.tenant_id == tenant &&
                                      grant.role == role;
                           });
    }

    // A role may be described by several grants (for example one grant per
    // table).  Capabilities are looked up per table so splitting a grant does
    // not accidentally hide later tables.
    [[nodiscard]] const TablePolicy* find_table(
        std::string_view tenant,
        std::string_view role,
        std::string_view table) const {
        for (const auto& grant : grants) {
            if (grant.tenant_id != tenant || grant.role != role) {
                continue;
            }
            const auto item = grant.tables.find(table);
            if (item != grant.tables.end()) {
                return &item->second;
            }
        }
        return nullptr;
    }

};

namespace detail {
[[nodiscard]] inline std::uint64_t policy_fingerprint(
    const AuthPolicy& policy);
} // namespace detail

struct SecurityContext {
    std::string tenant_id;
    std::string role;
};

// ---------------------------------------------------------------------------
// Authorization result
// ---------------------------------------------------------------------------

enum class DenialCode {
    Ok,
    Unauthenticated,
    UnknownRole,
    TableNotAllowed,
    IdentityMismatch,
    InvalidPlan,
    MalformedCondition,
    ConflictingPartitionConditions,
    MissingPartitionEquality,
    PartialPartitionKey,
    UnauthorizedPartition,
    DisjunctiveFilterDenied,
    FullScanDenied,
    TokenRangeDenied,
    CrossPartitionDenied,
    SecondaryIndexDenied,
    IndexBacklookupDenied,
    IndexNotBound,
    AggregationDenied,
    AggregationScopeUnknown,
    CrossPartitionAggregationDenied,
    LimitExceeded,
    StaleAuthorization,
    ResultScopeViolation,
    MissingResultProvenance,
};

[[nodiscard]] inline const char* to_string(DenialCode code) noexcept {
    switch (code) {
    case DenialCode::Ok:
        return "ok";
    case DenialCode::Unauthenticated:
        return "unauthenticated";
    case DenialCode::UnknownRole:
        return "unknown_role";
    case DenialCode::TableNotAllowed:
        return "table_not_allowed";
    case DenialCode::IdentityMismatch:
        return "identity_mismatch";
    case DenialCode::InvalidPlan:
        return "invalid_plan";
    case DenialCode::MalformedCondition:
        return "malformed_condition";
    case DenialCode::ConflictingPartitionConditions:
        return "conflicting_partition_conditions";
    case DenialCode::MissingPartitionEquality:
        return "missing_partition_equality";
    case DenialCode::PartialPartitionKey:
        return "partial_partition_key";
    case DenialCode::UnauthorizedPartition:
        return "unauthorized_partition";
    case DenialCode::DisjunctiveFilterDenied:
        return "disjunctive_filter_denied";
    case DenialCode::FullScanDenied:
        return "full_scan_denied";
    case DenialCode::TokenRangeDenied:
        return "token_range_denied";
    case DenialCode::CrossPartitionDenied:
        return "cross_partition_denied";
    case DenialCode::SecondaryIndexDenied:
        return "secondary_index_denied";
    case DenialCode::IndexBacklookupDenied:
        return "index_backlookup_denied";
    case DenialCode::IndexNotBound:
        return "index_not_bound";
    case DenialCode::AggregationDenied:
        return "aggregation_denied";
    case DenialCode::AggregationScopeUnknown:
        return "aggregation_scope_unknown";
    case DenialCode::CrossPartitionAggregationDenied:
        return "cross_partition_aggregation_denied";
    case DenialCode::LimitExceeded:
        return "limit_exceeded";
    case DenialCode::StaleAuthorization:
        return "stale_authorization";
    case DenialCode::ResultScopeViolation:
        return "result_scope_violation";
    case DenialCode::MissingResultProvenance:
        return "missing_result_provenance";
    }
    return "unknown";
}

struct AuthorizedQuery {
    QueryPlan effective_plan;
    std::string tenant_id;
    std::string role;
    std::string table;
    std::vector<std::string> partition_key_columns;
    std::string tenant_column;
    std::vector<PartitionKey> authorized_partitions;
    ResultReviewMode review_mode = ResultReviewMode::DropUnauthorized;
    bool require_tenant_provenance = true;
    bool aggregation_single_scope = false;
    std::uint64_t policy_generation = 0;
    std::uint64_t policy_fingerprint = 0;
    std::uint64_t nonce = 0;
    bool valid = false;
};

struct AuthorizationDecision {
    DenialCode code = DenialCode::Ok;
    std::string message;
    AuthorizedQuery authorized;

    [[nodiscard]] bool allowed() const noexcept {
        return code == DenialCode::Ok && authorized.valid;
    }

    explicit operator bool() const noexcept { return allowed(); }
};

struct ResultRow {
    RowValues values;

    // Provenance supplied by the storage layer.  It is intentionally separate
    // from the projected values, so a query that does not project the
    // partition key can still be checked safely.
    PartitionKey source_partition;
    std::optional<std::string> tenant_id;
};

struct ResultReview {
    std::vector<ResultRow> rows;
    std::size_t rejected_rows = 0;
    bool violation = false;
    DenialCode code = DenialCode::Ok;
    std::string message;

    [[nodiscard]] bool ok() const noexcept {
        return !violation && code == DenialCode::Ok;
    }
};

// ---------------------------------------------------------------------------
// Query authorizer
// ---------------------------------------------------------------------------

class QueryAuthorizer {
public:
    explicit QueryAuthorizer(AuthPolicy policy)
        : policy_(std::move(policy)),
          generation_(1),
          policy_fingerprint_(detail::policy_fingerprint(policy_)) {}

    [[nodiscard]] AuthorizationDecision authorize(
        const QueryPlan& plan,
        const SecurityContext& session) const;

    // Replacing the policy invalidates every previously issued token.  This is
    // what makes revocation effective for in-flight/queued queries.
    void set_policy(AuthPolicy policy) {
        std::unique_lock lock(mutex_);
        policy_ = std::move(policy);
        ++generation_;
        policy_fingerprint_ = detail::policy_fingerprint(policy_);
    }

    [[nodiscard]] std::uint64_t generation() const {
        std::shared_lock lock(mutex_);
        return generation_;
    }

    [[nodiscard]] AuthPolicy policy_snapshot() const {
        std::shared_lock lock(mutex_);
        return policy_;
    }

    [[nodiscard]] ResultReview recheck(const AuthorizedQuery& authorized,
                                       std::vector<ResultRow> rows) const;

private:
    mutable std::shared_mutex mutex_;
    AuthPolicy policy_;
    std::uint64_t generation_;
    std::uint64_t policy_fingerprint_;
    mutable std::atomic<std::uint64_t> nonce_{1};
};

// ---------------------------------------------------------------------------
// Implementation details
// ---------------------------------------------------------------------------

namespace detail {

inline void hash_bytes(std::uint64_t& hash, std::string_view bytes) {
    constexpr std::uint64_t prime = 1099511628211ULL;
    for (const char ch : bytes) {
        hash ^= static_cast<unsigned char>(ch);
        hash *= prime;
    }
    hash ^= 0xFFU;
    hash *= prime;
}

inline void hash_u64(std::uint64_t& hash, std::uint64_t value) {
    constexpr std::uint64_t prime = 1099511628211ULL;
    for (int i = 0; i < 8; ++i) {
        hash ^= static_cast<unsigned char>(value & 0xFFU);
        hash *= prime;
        value >>= 8;
    }
}

inline void hash_value(std::uint64_t& hash, const Value& value) {
    hash_u64(hash, value.data.index());
    hash_bytes(hash, value_to_string(value));
}

inline std::uint64_t policy_fingerprint(const AuthPolicy& policy) {
    std::uint64_t hash = 14695981039346656037ULL;
    hash_u64(hash, policy.grants.size());
    for (const auto& grant : policy.grants) {
        hash_bytes(hash, grant.tenant_id);
        hash_bytes(hash, grant.role);
        hash_u64(hash, grant.tables.size());
        for (const auto& [table_name, table] : grant.tables) {
            hash_bytes(hash, table_name);
            hash_u64(hash, table.partition_key_columns.size());
            for (const auto& column : table.partition_key_columns) {
                hash_bytes(hash, column);
            }
            hash_u64(hash, table.authorized_partitions.size());
            for (const auto& partition : table.authorized_partitions) {
                hash_u64(hash, partition.size());
                for (const auto& [column, value] : partition) {
                    hash_bytes(hash, column);
                    hash_value(hash, value);
                }
            }
            hash_bytes(hash, table.tenant_column);
            hash_u64(hash, table.allow_full_scan);
            hash_u64(hash, table.allow_token_range);
            hash_u64(hash, table.allow_cross_partition_scan);
            hash_u64(hash, table.allow_secondary_index);
            hash_u64(hash, table.allow_secondary_index_backlookup);
            hash_u64(hash, table.require_full_index_binding);
            hash_u64(hash, table.allow_aggregation);
            hash_u64(hash, table.allow_cross_partition_aggregation);
            hash_u64(hash, table.allow_disjunctive_filters);
            hash_u64(hash, table.require_all_partition_key_components);
            hash_u64(hash, table.require_tenant_provenance);
            hash_u64(hash, table.max_partitions_per_query);
            hash_u64(hash, table.max_limit.has_value());
            if (table.max_limit.has_value()) {
                hash_u64(hash, *table.max_limit);
            }
            hash_u64(hash,
                     static_cast<std::uint64_t>(table.review_mode));
        }
    }
    return hash;
}

inline std::string lower_ascii(std::string_view input) {
    std::string result;
    result.reserve(input.size());
    for (const char ch : input) {
        result.push_back(static_cast<char>(
            std::tolower(static_cast<unsigned char>(ch))));
    }
    return result;
}

inline std::string join_columns(const std::vector<std::string>& columns) {
    std::ostringstream output;
    for (std::size_t i = 0; i < columns.size(); ++i) {
        if (i != 0) {
            output << ", ";
        }
        output << columns[i];
    }
    return output.str();
}

inline bool contains_column(const std::vector<std::string>& columns,
                            std::string_view column) {
    return std::any_of(columns.begin(), columns.end(),
                       [&](const std::string& entry) {
                           return entry == column;
                       });
}

inline void require_condition_shape(const Condition& condition,
                                    std::size_t depth,
                                    bool allow_disjunction) {
    if (depth > 64) {
        throw PlanParseError("condition tree is too deep");
    }

    switch (condition.kind) {
    case ConditionKind::Predicate:
        if (condition.column.empty()) {
            throw PlanParseError("predicate has no column");
        }
        if (condition.op == CompareOp::IsNull ||
            condition.op == CompareOp::IsNotNull) {
            if (!condition.values.empty()) {
                throw PlanParseError(
                    "is_null/not_null predicates must not carry values");
            }
            return;
        }
        if (condition.op == CompareOp::In) {
            if (condition.values.empty()) {
                throw PlanParseError("IN predicate needs at least one value");
            }
        } else if (condition.values.size() != 1) {
            throw PlanParseError(
                "predicate needs exactly one value");
        }
        for (const auto& value : condition.values) {
            if (value.is_null()) {
                throw PlanParseError(
                    "NULL is not accepted in authorization predicates");
            }
        }
        return;

    case ConditionKind::And:
        for (const auto& child : condition.children) {
            require_condition_shape(child, depth + 1, allow_disjunction);
        }
        return;

    case ConditionKind::Or:
        if (!allow_disjunction) {
            throw PlanParseError(
                "disjunctive filters are disabled by policy");
        }
        if (condition.children.empty()) {
            throw PlanParseError("empty OR predicate");
        }
        for (const auto& child : condition.children) {
            require_condition_shape(child, depth + 1, allow_disjunction);
        }
        return;

    case ConditionKind::Not:
        if (!allow_disjunction) {
            throw PlanParseError("negated filters are disabled by policy");
        }
        if (condition.children.size() != 1) {
            throw PlanParseError("NOT needs exactly one child");
        }
        require_condition_shape(condition.children.front(), depth + 1,
                                allow_disjunction);
        return;
    }
}

inline void collect_mandatory(const Condition& condition,
                              std::vector<const Condition*>& output) {
    if (condition.kind == ConditionKind::Predicate) {
        output.push_back(&condition);
        return;
    }
    if (condition.kind == ConditionKind::And) {
        for (const auto& child : condition.children) {
            collect_mandatory(child, output);
        }
    }
    // Predicates below OR/NOT are not mandatory: a match on one branch does
    // not constrain the read range.
}

inline bool has_eq(const std::vector<const Condition*>& mandatory,
                   std::string_view column,
                   const Value** value) {
    for (const Condition* condition : mandatory) {
        if (condition->kind == ConditionKind::Predicate &&
            condition->op == CompareOp::Eq &&
            condition->column == column && condition->values.size() == 1) {
            if (value != nullptr) {
                *value = &condition->values.front();
            }
            return true;
        }
    }
    return false;
}

inline std::vector<const Value*> eq_values(
    const std::vector<const Condition*>& mandatory,
    std::string_view column) {
    std::vector<const Value*> result;
    for (const Condition* condition : mandatory) {
        if (condition->kind == ConditionKind::Predicate &&
            condition->op == CompareOp::Eq &&
            condition->column == column && condition->values.size() == 1) {
            result.push_back(&condition->values.front());
        }
    }
    return result;
}

inline bool has_any_eq_on(const std::vector<const Condition*>& mandatory,
                          const std::vector<std::string>& columns) {
    return std::any_of(columns.begin(), columns.end(),
                       [&](const std::string& column) {
                           return has_eq(mandatory, column, nullptr);
                       });
}

inline bool all_eq_on(const std::vector<const Condition*>& mandatory,
                      const std::vector<std::string>& columns) {
    return std::all_of(columns.begin(), columns.end(),
                       [&](const std::string& column) {
                           return has_eq(mandatory, column, nullptr);
                       });
}

inline bool add_tenant_injection(QueryPlan& plan,
                                 std::string_view tenant_column,
                                 std::string_view tenant_id,
                                 std::vector<const Condition*> mandatory,
                                 std::string& error) {
    const auto existing = eq_values(mandatory, tenant_column);
    for (const Value* value : existing) {
        if (!value_equal(*value, Value(std::string(tenant_id)))) {
            error =
                "plan tenant predicate does not match authenticated tenant";
            return false;
        }
    }

    if (!existing.empty()) {
        return true;
    }

    Condition injected = Condition::eq(std::string(tenant_column),
                                       Value(std::string(tenant_id)));
    if (plan.where.kind == ConditionKind::And) {
        plan.where.children.push_back(std::move(injected));
    } else {
        Condition previous = std::move(plan.where);
        plan.where = Condition::all({std::move(previous), std::move(injected)});
    }
    return true;
}

inline std::vector<PartitionKey> matching_partitions(
    const TablePolicy& table,
    const PartitionKey& bound_values) {
    std::vector<PartitionKey> matches;
    for (const auto& partition : table.authorized_partitions) {
        if (partition_matches(partition, bound_values)) {
            const bool duplicate =
                std::any_of(matches.begin(), matches.end(),
                            [&](const PartitionKey& existing) {
                                return partition_matches(existing, partition) &&
                                       partition_matches(partition, existing);
                            });
            if (!duplicate) {
                matches.push_back(partition);
            }
        }
    }
    return matches;
}

inline bool table_partition_is_valid(const TablePolicy& table,
                                     const PartitionKey& partition) {
    if (table.partition_key_columns.empty() || partition.empty()) {
        return false;
    }
    for (const auto& column : table.partition_key_columns) {
        if (partition.find(column) == partition.end()) {
            return false;
        }
    }
    return true;
}

inline bool row_value(const RowValues& values,
                      std::string_view column,
                      const Value** output) {
    const auto item = values.find(column);
    if (item == values.end()) {
        return false;
    }
    *output = &item->second;
    return true;
}

inline bool partition_value(const PartitionKey& partition,
                            std::string_view column,
                            const Value** output) {
    const auto item = partition.find(column);
    if (item == partition.end()) {
        return false;
    }
    *output = &item->second;
    return true;
}

inline bool authorized_partition_contains(
    const std::vector<PartitionKey>& partitions,
    const PartitionKey& key,
    const std::vector<std::string>& partition_columns) {
    for (const auto& candidate : partitions) {
        bool match = true;
        for (const auto& column : partition_columns) {
            const Value* left = nullptr;
            const Value* right = nullptr;
            if (!partition_value(candidate, column, &left) ||
                !partition_value(key, column, &right) ||
                !value_equal(*left, *right)) {
                match = false;
                break;
            }
        }
        if (match) {
            return true;
        }
    }
    return false;
}

struct RowCheck {
    bool allowed = false;
    DenialCode code = DenialCode::Ok;
    std::string message;
};

inline RowCheck check_row(const AuthorizedQuery& authorized,
                          const ResultRow& row) {
    PartitionKey observed = row.source_partition;
    bool missing_key = false;

    for (const auto& column : authorized.partition_key_columns) {
        const Value* value = nullptr;
        const Value* projected = nullptr;
        if (partition_value(observed, column, &value) &&
            row_value(row.values, column, &projected) &&
            !value_equal(*value, *projected)) {
            return {false, DenialCode::ResultScopeViolation,
                    "result row has inconsistent partition-key provenance"};
        }
        if (!partition_value(observed, column, &value)) {
            if (row_value(row.values, column, &value)) {
                observed[column] = *value;
            }
        }
        if (partition_value(observed, column, &value) == false) {
            missing_key = true;
        }
    }

    if (missing_key) {
        // An ungrouped aggregate over exactly one authorized partition has a
        // scope known from the plan even though the result row contains only
        // the aggregate value.
        if (authorized.aggregation_single_scope &&
            authorized.authorized_partitions.size() == 1) {
            observed = authorized.authorized_partitions.front();
        } else {
            return {false, DenialCode::MissingResultProvenance,
                    "result row has no complete partition-key provenance"};
        }
    }

    if (!authorized_partition_contains(authorized.authorized_partitions,
                                       observed,
                                       authorized.partition_key_columns)) {
        return {false, DenialCode::ResultScopeViolation,
                "result row belongs to an unauthorized partition " +
                    partition_to_string(observed)};
    }

    if (!authorized.tenant_column.empty() &&
        authorized.require_tenant_provenance) {
        std::vector<std::string> evidence;
        if (row.tenant_id.has_value()) {
            evidence.push_back(*row.tenant_id);
        }

        const Value* value = nullptr;
        if (row_value(row.values, authorized.tenant_column, &value)) {
            if (value->is_null()) {
                return {false, DenialCode::MissingResultProvenance,
                        "result row has a NULL tenant"};
            }
            evidence.push_back(value_to_string(*value));
        }

        if (partition_value(observed, authorized.tenant_column, &value)) {
            if (value->is_null()) {
                return {false, DenialCode::MissingResultProvenance,
                        "result row has a NULL tenant in its partition key"};
            }
            evidence.push_back(value_to_string(*value));
        }

        if (evidence.empty()) {
            // Even a single-partition aggregate must carry tenant provenance
            // when the policy separates tenant from the partition key.
            return {false, DenialCode::MissingResultProvenance,
                    "result row has no tenant provenance"};
        }

        const std::string& tenant = evidence.front();
        if (std::any_of(evidence.begin(), evidence.end(),
                        [&](const std::string& item) {
                            return item != tenant;
                        })) {
            return {false, DenialCode::ResultScopeViolation,
                    "result row has inconsistent tenant provenance"};
        }
        if (tenant != authorized.tenant_id) {
            return {false, DenialCode::ResultScopeViolation,
                    "result row belongs to an unauthorized tenant"};
        }
    }

    return {true, DenialCode::Ok, {}};
}

// ------------------------------ JSON parser --------------------------------

struct Json {
    enum class Type { Null, Bool, Int, Double, String, Array, Object };

    Type type = Type::Null;
    bool boolean = false;
    std::int64_t integer = 0;
    double number = 0.0;
    std::string string;
    std::vector<Json> array;
    std::vector<std::pair<std::string, Json>> object;

    [[nodiscard]] const Json* find(std::string_view key) const {
        for (const auto& [name, value] : object) {
            if (name == key) {
                return &value;
            }
        }
        return nullptr;
    }

    [[nodiscard]] bool is_object() const noexcept {
        return type == Type::Object;
    }
    [[nodiscard]] bool is_array() const noexcept {
        return type == Type::Array;
    }
};

class JsonParser {
public:
    explicit JsonParser(std::string_view input) : input_(input) {}

    [[nodiscard]] Json parse() {
        skip_whitespace();
        Json value = parse_value(0);
        skip_whitespace();
        if (position_ != input_.size()) {
            fail("trailing characters after JSON value");
        }
        return value;
    }

private:
    [[noreturn]] void fail(const std::string& message) const {
        throw PlanParseError("JSON parse error at byte " +
                             std::to_string(position_) + ": " + message);
    }

    void skip_whitespace() {
        while (position_ < input_.size() &&
               std::isspace(static_cast<unsigned char>(input_[position_]))) {
            ++position_;
        }
    }

    bool consume(char expected) {
        if (position_ < input_.size() && input_[position_] == expected) {
            ++position_;
            return true;
        }
        return false;
    }

    Json parse_value(std::size_t depth) {
        if (depth > 64) {
            fail("nesting is too deep");
        }
        skip_whitespace();
        if (position_ >= input_.size()) {
            fail("unexpected end of input");
        }

        const char ch = input_[position_];
        if (ch == '{') {
            return parse_object(depth + 1);
        }
        if (ch == '[') {
            return parse_array(depth + 1);
        }
        if (ch == '"') {
            Json result;
            result.type = Json::Type::String;
            result.string = parse_string();
            return result;
        }
        if (ch == 't' || ch == 'f') {
            return parse_bool();
        }
        if (ch == 'n') {
            parse_literal("null");
            Json result;
            result.type = Json::Type::Null;
            return result;
        }
        return parse_number();
    }

    void parse_literal(std::string_view literal) {
        if (input_.substr(position_, literal.size()) != literal) {
            fail("invalid literal");
        }
        position_ += literal.size();
    }

    Json parse_bool() {
        Json result;
        result.type = Json::Type::Bool;
        if (input_[position_] == 't') {
            parse_literal("true");
            result.boolean = true;
        } else {
            parse_literal("false");
            result.boolean = false;
        }
        return result;
    }

    static void append_utf8(std::string& output, std::uint32_t code_point) {
        if (code_point <= 0x7F) {
            output.push_back(static_cast<char>(code_point));
        } else if (code_point <= 0x7FF) {
            output.push_back(static_cast<char>(0xC0 | (code_point >> 6)));
            output.push_back(static_cast<char>(0x80 | (code_point & 0x3F)));
        } else if (code_point <= 0xFFFF) {
            output.push_back(static_cast<char>(0xE0 | (code_point >> 12)));
            output.push_back(
                static_cast<char>(0x80 | ((code_point >> 6) & 0x3F)));
            output.push_back(static_cast<char>(0x80 | (code_point & 0x3F)));
        } else {
            output.push_back(static_cast<char>(0xF0 | (code_point >> 18)));
            output.push_back(
                static_cast<char>(0x80 | ((code_point >> 12) & 0x3F)));
            output.push_back(
                static_cast<char>(0x80 | ((code_point >> 6) & 0x3F)));
            output.push_back(static_cast<char>(0x80 | (code_point & 0x3F)));
        }
    }

    std::uint32_t parse_hex4() {
        if (position_ + 4 > input_.size()) {
            fail("truncated \\u escape");
        }
        std::uint32_t value = 0;
        for (int i = 0; i < 4; ++i) {
            const char ch = input_[position_++];
            value <<= 4;
            if (ch >= '0' && ch <= '9') {
                value += static_cast<std::uint32_t>(ch - '0');
            } else if (ch >= 'a' && ch <= 'f') {
                value += static_cast<std::uint32_t>(ch - 'a' + 10);
            } else if (ch >= 'A' && ch <= 'F') {
                value += static_cast<std::uint32_t>(ch - 'A' + 10);
            } else {
                fail("invalid hex digit in \\u escape");
            }
        }
        return value;
    }

    std::string parse_string() {
        if (!consume('"')) {
            fail("expected string");
        }
        std::string output;
        while (position_ < input_.size()) {
            const char ch = input_[position_++];
            if (ch == '"') {
                return output;
            }
            if (static_cast<unsigned char>(ch) < 0x20) {
                fail("control character in string");
            }
            if (ch != '\\') {
                output.push_back(ch);
                continue;
            }
            if (position_ >= input_.size()) {
                fail("truncated escape");
            }
            const char escape = input_[position_++];
            switch (escape) {
            case '"':
            case '\\':
            case '/':
                output.push_back(escape);
                break;
            case 'b':
                output.push_back('\b');
                break;
            case 'f':
                output.push_back('\f');
                break;
            case 'n':
                output.push_back('\n');
                break;
            case 'r':
                output.push_back('\r');
                break;
            case 't':
                output.push_back('\t');
                break;
            case 'u': {
                std::uint32_t code_point = parse_hex4();
                if (code_point >= 0xD800 && code_point <= 0xDBFF) {
                    if (!consume('\\') || !consume('u')) {
                        fail("missing low surrogate");
                    }
                    const std::uint32_t low = parse_hex4();
                    if (low < 0xDC00 || low > 0xDFFF) {
                        fail("invalid low surrogate");
                    }
                    code_point =
                        0x10000 + ((code_point - 0xD800) << 10) +
                        (low - 0xDC00);
                }
                append_utf8(output, code_point);
                break;
            }
            default:
                fail("invalid escape");
            }
        }
        fail("unterminated string");
    }

    Json parse_number() {
        const std::size_t start = position_;
        if (position_ < input_.size() && input_[position_] == '-') {
            ++position_;
        }
        if (position_ >= input_.size() ||
            !std::isdigit(static_cast<unsigned char>(input_[position_]))) {
            fail("invalid number");
        }
        while (position_ < input_.size() &&
               std::isdigit(static_cast<unsigned char>(input_[position_]))) {
            ++position_;
        }
        bool is_double = false;
        if (position_ < input_.size() && input_[position_] == '.') {
            is_double = true;
            ++position_;
            if (position_ >= input_.size() ||
                !std::isdigit(
                    static_cast<unsigned char>(input_[position_]))) {
                fail("invalid fraction");
            }
            while (position_ < input_.size() &&
                   std::isdigit(
                       static_cast<unsigned char>(input_[position_]))) {
                ++position_;
            }
        }
        if (position_ < input_.size() &&
            (input_[position_] == 'e' || input_[position_] == 'E')) {
            is_double = true;
            ++position_;
            if (position_ < input_.size() &&
                (input_[position_] == '+' || input_[position_] == '-')) {
                ++position_;
            }
            if (position_ >= input_.size() ||
                !std::isdigit(
                    static_cast<unsigned char>(input_[position_]))) {
                fail("invalid exponent");
            }
            while (position_ < input_.size() &&
                   std::isdigit(
                       static_cast<unsigned char>(input_[position_]))) {
                ++position_;
            }
        }

        Json result;
        const std::string_view token = input_.substr(start, position_ - start);
        if (is_double) {
            result.type = Json::Type::Double;
            try {
                result.number = std::stod(std::string(token));
            } catch (const std::exception&) {
                fail("number out of range");
            }
        } else {
            result.type = Json::Type::Int;
            const auto [pointer, error] = std::from_chars(
                token.data(), token.data() + token.size(), result.integer);
            if (error != std::errc{} || pointer != token.data() + token.size()) {
                fail("integer out of range");
            }
        }
        return result;
    }

    Json parse_array(std::size_t depth) {
        consume('[');
        Json result;
        result.type = Json::Type::Array;
        skip_whitespace();
        if (consume(']')) {
            return result;
        }
        while (true) {
            result.array.push_back(parse_value(depth));
            skip_whitespace();
            if (consume(']')) {
                return result;
            }
            if (!consume(',')) {
                fail("expected ',' or ']'");
            }
        }
    }

    Json parse_object(std::size_t depth) {
        consume('{');
        Json result;
        result.type = Json::Type::Object;
        skip_whitespace();
        if (consume('}')) {
            return result;
        }
        while (true) {
            skip_whitespace();
            if (position_ >= input_.size() || input_[position_] != '"') {
                fail("expected object key");
            }
            std::string key = parse_string();
            if (result.find(key) != nullptr) {
                fail("duplicate object key '" + key + "'");
            }
            skip_whitespace();
            if (!consume(':')) {
                fail("expected ':'");
            }
            result.object.emplace_back(std::move(key), parse_value(depth));
            skip_whitespace();
            if (consume('}')) {
                return result;
            }
            if (!consume(',')) {
                fail("expected ',' or '}'");
            }
        }
    }

    std::string_view input_;
    std::size_t position_ = 0;
};

inline const Json& require_member(const Json& object,
                                  std::string_view key,
                                  std::string_view context) {
    if (!object.is_object()) {
        throw PlanParseError(std::string(context) + " must be an object");
    }
    const Json* value = object.find(key);
    if (value == nullptr) {
        throw PlanParseError(std::string(context) + " is missing '" +
                             std::string(key) + "'");
    }
    return *value;
}

inline std::string json_string(const Json& value,
                               std::string_view context) {
    if (value.type != Json::Type::String) {
        throw PlanParseError(std::string(context) + " must be a string");
    }
    return value.string;
}

inline bool json_bool(const Json& value, std::string_view context) {
    if (value.type != Json::Type::Bool) {
        throw PlanParseError(std::string(context) + " must be a boolean");
    }
    return value.boolean;
}

inline std::vector<std::string> json_string_array(const Json& value,
                                                  std::string_view context) {
    if (!value.is_array()) {
        throw PlanParseError(std::string(context) + " must be an array");
    }
    std::vector<std::string> result;
    result.reserve(value.array.size());
    for (const auto& item : value.array) {
        result.push_back(json_string(item, context));
    }
    return result;
}

inline Value json_value(const Json& value, std::string_view context) {
    switch (value.type) {
    case Json::Type::Null:
        return Value(nullptr);
    case Json::Type::Bool:
        return Value(value.boolean);
    case Json::Type::Int:
        return Value(value.integer);
    case Json::Type::Double:
        return Value(value.number);
    case Json::Type::String:
        return Value(value.string);
    case Json::Type::Array:
        throw PlanParseError(std::string(context) +
                             " must be a scalar; use 'values' for IN");
    case Json::Type::Object:
        throw PlanParseError(std::string(context) + " must be a scalar");
    }
    throw PlanParseError("unreachable JSON value");
}

inline CompareOp parse_operator(std::string_view input) {
    const std::string op = lower_ascii(input);
    if (op == "eq" || op == "=" || op == "==") {
        return CompareOp::Eq;
    }
    if (op == "ne" || op == "!=" || op == "<>") {
        return CompareOp::Ne;
    }
    if (op == "lt" || op == "<") {
        return CompareOp::Lt;
    }
    if (op == "le" || op == "lte" || op == "<=") {
        return CompareOp::Le;
    }
    if (op == "gt" || op == ">") {
        return CompareOp::Gt;
    }
    if (op == "ge" || op == "gte" || op == ">=") {
        return CompareOp::Ge;
    }
    if (op == "in") {
        return CompareOp::In;
    }
    if (op == "contains") {
        return CompareOp::Contains;
    }
    if (op == "is_null" || op == "isnull") {
        return CompareOp::IsNull;
    }
    if (op == "is_not_null" || op == "not_null" || op == "isnotnull") {
        return CompareOp::IsNotNull;
    }
    throw PlanParseError("unknown comparison operator '" +
                         std::string(input) + "'");
}

inline Condition parse_condition(const Json& value, std::size_t depth) {
    if (depth > 64) {
        throw PlanParseError("condition nesting is too deep");
    }
    if (value.is_array()) {
        std::vector<Condition> children;
        for (const auto& item : value.array) {
            children.push_back(parse_condition(item, depth + 1));
        }
        return Condition::all(std::move(children));
    }
    if (!value.is_object()) {
        throw PlanParseError("condition must be an object or array");
    }

    if (const Json* children = value.find("and")) {
        if (!children->is_array()) {
            throw PlanParseError("'and' must be an array");
        }
        std::vector<Condition> result;
        for (const auto& item : children->array) {
            result.push_back(parse_condition(item, depth + 1));
        }
        return Condition::all(std::move(result));
    }
    if (const Json* children = value.find("or")) {
        if (!children->is_array()) {
            throw PlanParseError("'or' must be an array");
        }
        std::vector<Condition> result;
        for (const auto& item : children->array) {
            result.push_back(parse_condition(item, depth + 1));
        }
        return Condition::any(std::move(result));
    }
    if (const Json* child = value.find("not")) {
        return Condition::negate(parse_condition(*child, depth + 1));
    }

    const Json* column = value.find("column");
    if (column == nullptr) {
        column = value.find("field");
    }
    if (column != nullptr) {
        Condition result;
        result.kind = ConditionKind::Predicate;
        result.column = json_string(*column, "condition column");

        if (const Json* op = value.find("op")) {
            result.op = parse_operator(json_string(*op, "condition op"));
        } else {
            result.op = CompareOp::Eq;
        }

        if (const Json* values = value.find("values")) {
            if (!values->is_array()) {
                throw PlanParseError("'values' must be an array");
            }
            for (const auto& item : values->array) {
                result.values.push_back(json_value(item, "condition value"));
            }
        }
        if (const Json* item = value.find("value")) {
            if (item->is_array()) {
                if (result.op == CompareOp::Eq) {
                    result.op = CompareOp::In;
                }
                for (const auto& entry : item->array) {
                    result.values.push_back(
                        json_value(entry, "condition value"));
                }
            } else {
                result.values.push_back(
                    json_value(*item, "condition value"));
            }
        }
        return result;
    }

    // Shorthand: {"tenant_id":"a","bucket":7} means AND of equalities.
    if (value.object.empty()) {
        return Condition::all({});
    }
    std::vector<Condition> children;
    children.reserve(value.object.size());
    for (const auto& [name, item] : value.object) {
        if (item.is_array()) {
            std::vector<Value> values;
            for (const auto& entry : item.array) {
                values.push_back(json_value(entry, "condition value"));
            }
            children.push_back(Condition::in(name, std::move(values)));
        } else {
            children.push_back(
                Condition::eq(name, json_value(item, "condition value")));
        }
    }
    return Condition::all(std::move(children));
}

inline StatementKind parse_statement_kind(std::string_view input) {
    const std::string kind = lower_ascii(input);
    if (kind == "select" || kind == "read") {
        return StatementKind::Select;
    }
    if (kind == "aggregate" || kind == "aggregation") {
        return StatementKind::Aggregate;
    }
    throw PlanParseError("unknown statement kind '" + std::string(input) + "'");
}

inline AccessPath parse_access_path(std::string_view input) {
    const std::string path = lower_ascii(input);
    if (path == "partition" || path == "direct" ||
        path == "partition_lookup") {
        return AccessPath::Partition;
    }
    if (path == "secondary_index" || path == "index" ||
        path == "index_lookup") {
        return AccessPath::SecondaryIndex;
    }
    if (path == "full_scan" || path == "full_table_scan" ||
        path == "scan" || path == "allow_filtering") {
        return AccessPath::FullScan;
    }
    if (path == "token_range" || path == "range_scan" ||
        path == "cross_partition") {
        return AccessPath::TokenRange;
    }
    throw PlanParseError("unknown access path '" + std::string(input) + "'");
}

inline AggregateFunction parse_aggregate_function(std::string_view input) {
    const std::string function = lower_ascii(input);
    if (function == "count") {
        return AggregateFunction::Count;
    }
    if (function == "sum") {
        return AggregateFunction::Sum;
    }
    if (function == "avg") {
        return AggregateFunction::Avg;
    }
    if (function == "min") {
        return AggregateFunction::Min;
    }
    if (function == "max") {
        return AggregateFunction::Max;
    }
    throw PlanParseError("unknown aggregate function '" +
                         std::string(input) + "'");
}

} // namespace detail

// ---------------------------------------------------------------------------
// Plan parsing
// ---------------------------------------------------------------------------

[[nodiscard]] inline QueryPlan parse_query_plan(std::string_view json_text) {
    const detail::Json root = detail::JsonParser(json_text).parse();
    if (!root.is_object()) {
        throw PlanParseError("query plan must be a JSON object");
    }

    QueryPlan plan;

    if (const auto* id = root.find("statement_id")) {
        plan.statement_id = detail::json_string(*id, "statement_id");
    } else if (const auto* alternate_id = root.find("id")) {
        plan.statement_id = detail::json_string(*alternate_id, "id");
    }

    if (const auto* kind = root.find("kind")) {
        plan.kind =
            detail::parse_statement_kind(detail::json_string(*kind, "kind"));
    }

    if (const auto* table = root.find("table")) {
        plan.table = detail::json_string(*table, "table");
    } else if (const auto* alternate_table = root.find("from")) {
        plan.table = detail::json_string(*alternate_table, "from");
    }

    if (const auto* path = root.find("access_path")) {
        plan.access_path = detail::parse_access_path(
            detail::json_string(*path, "access_path"));
    } else if (const auto* alternate_path = root.find("access")) {
        plan.access_path =
            detail::parse_access_path(
                detail::json_string(*alternate_path, "access"));
    }

    std::vector<Condition> partition_conditions;
    if (const auto* key = root.find("partition_key")) {
        if (key->is_array()) {
            plan.partition_key_columns =
                detail::json_string_array(*key, "partition_key");
        } else if (key->is_object()) {
            for (const auto& [column, value] : key->object) {
                plan.partition_key_columns.push_back(column);
                partition_conditions.push_back(Condition::eq(
                    column, detail::json_value(value, "partition_key value")));
            }
        } else {
            throw PlanParseError(
                "partition_key must be an array or object");
        }
    }
    if (const auto* key = root.find("partition_key_columns")) {
        plan.partition_key_columns =
            detail::json_string_array(*key, "partition_key_columns");
    }

    if (const auto* where = root.find("where")) {
        plan.where = detail::parse_condition(*where, 0);
    } else if (const auto* filter = root.find("filter")) {
        plan.where = detail::parse_condition(*filter, 0);
    }
    if (!partition_conditions.empty()) {
        Condition key_condition = Condition::all(std::move(partition_conditions));
        if (plan.where.kind == ConditionKind::And &&
            plan.where.children.empty()) {
            plan.where = std::move(key_condition);
        } else {
            plan.where =
                Condition::all({std::move(plan.where), std::move(key_condition)});
        }
    }

    if (const auto* selected = root.find("selected_columns")) {
        plan.selected_columns =
            detail::json_string_array(*selected, "selected_columns");
    } else if (const auto* alternate_selection = root.find("select")) {
        if (alternate_selection->is_array()) {
            plan.selected_columns =
                detail::json_string_array(*alternate_selection, "select");
        }
    }

    if (const auto* index = root.find("index")) {
        if (!index->is_object()) {
            throw PlanParseError("index must be an object");
        }
        IndexSpec spec;
        spec.name = detail::json_string(
            detail::require_member(*index, "name", "index"), "index.name");
        spec.columns = detail::json_string_array(
            detail::require_member(*index, "columns", "index"),
            "index.columns");
        if (const auto* backlookup = index->find("requires_backlookup")) {
            spec.requires_backlookup =
                detail::json_bool(*backlookup, "index.requires_backlookup");
        } else if (const auto* alternate_backlookup =
                       index->find("backlookup")) {
            spec.requires_backlookup =
                detail::json_bool(*alternate_backlookup, "index.backlookup");
        }
        plan.index = std::move(spec);
    }

    if (const auto* aggregates = root.find("aggregates")) {
        if (!aggregates->is_array()) {
            throw PlanParseError("aggregates must be an array");
        }
        for (const auto& item : aggregates->array) {
            if (!item.is_object()) {
                throw PlanParseError("aggregate must be an object");
            }
            AggregateSpec spec;
            const auto* function = item.find("function");
            if (function == nullptr) {
                function = item.find("fn");
            }
            if (function == nullptr) {
                throw PlanParseError(
                    "aggregate is missing 'function' or 'fn'");
            }
            spec.function = detail::parse_aggregate_function(
                detail::json_string(*function, "aggregate.function"));
            const auto* column = item.find("column");
            if (column != nullptr) {
                spec.column =
                    detail::json_string(*column, "aggregate.column");
            } else {
                spec.column = "*";
            }
            if (const auto* alias = item.find("alias")) {
                spec.alias = detail::json_string(*alias, "aggregate.alias");
            }
            plan.aggregates.push_back(std::move(spec));
        }
    }

    if (const auto* group_by = root.find("group_by")) {
        plan.group_by = detail::json_string_array(*group_by, "group_by");
    }

    if (const auto* limit = root.find("limit")) {
        if (limit->type != detail::Json::Type::Int || limit->integer < 0) {
            throw PlanParseError("limit must be a non-negative integer");
        }
        plan.limit = static_cast<std::uint64_t>(limit->integer);
    }

    if (const auto* allow_filtering = root.find("allow_filtering")) {
        plan.allow_filtering =
            detail::json_bool(*allow_filtering, "allow_filtering");
    }

    if (const auto* tenant = root.find("tenant_id")) {
        plan.tenant_id = detail::json_string(*tenant, "tenant_id");
    }
    if (const auto* role = root.find("role")) {
        plan.role = detail::json_string(*role, "role");
    }

    return plan;
}

[[nodiscard]] inline std::optional<QueryPlan> try_parse_query_plan(
    std::string_view json_text,
    std::string* error = nullptr) {
    try {
        return parse_query_plan(json_text);
    } catch (const std::exception& exception) {
        if (error != nullptr) {
            *error = exception.what();
        }
        return std::nullopt;
    }
}

// ---------------------------------------------------------------------------
// Authorization
// ---------------------------------------------------------------------------

inline AuthorizationDecision QueryAuthorizer::authorize(
    const QueryPlan& input,
    const SecurityContext& session) const {
    auto deny = [](DenialCode code, std::string message) {
        AuthorizationDecision decision;
        decision.code = code;
        decision.message = std::move(message);
        return decision;
    };

    if (session.tenant_id.empty() || session.role.empty()) {
        return deny(DenialCode::Unauthenticated,
                    "tenant and role must be established by authentication");
    }

    std::shared_lock lock(mutex_);
    const std::uint64_t generation = generation_;
    const std::uint64_t fingerprint = policy_fingerprint_;
    if (!policy_.has_grant(session.tenant_id, session.role)) {
        return deny(DenialCode::UnknownRole,
                    "no grant for the authenticated tenant and role");
    }

    const TablePolicy* table_pointer =
        policy_.find_table(session.tenant_id, session.role, input.table);
    if (table_pointer == nullptr) {
        return deny(DenialCode::TableNotAllowed,
                    "table is not granted to this role");
    }
    const TablePolicy& table = *table_pointer;

    if (table.partition_key_columns.empty()) {
        return deny(DenialCode::InvalidPlan,
                    "table policy has no partition key");
    }
    for (const auto& partition : table.authorized_partitions) {
        if (!detail::table_partition_is_valid(table, partition)) {
            return deny(DenialCode::InvalidPlan,
                        "table policy contains an incomplete authorized "
                        "partition");
        }
    }

    QueryPlan plan = input;

    // Identity is never selected by the caller.  A mismatching claim is an
    // attack signal and is rejected rather than silently replaced.
    if (!plan.tenant_id.empty() &&
        plan.tenant_id != session.tenant_id) {
        return deny(DenialCode::IdentityMismatch,
                    "plan tenant does not match authenticated tenant");
    }
    if (!plan.role.empty() && plan.role != session.role) {
        return deny(DenialCode::IdentityMismatch,
                    "plan role does not match authenticated role");
    }
    plan.tenant_id = session.tenant_id;
    plan.role = session.role;

    if (!plan.partition_key_columns.empty() &&
        plan.partition_key_columns != table.partition_key_columns) {
        return deny(DenialCode::InvalidPlan,
                    "plan attempted to redefine the partition key");
    }
    plan.partition_key_columns = table.partition_key_columns;

    if (plan.table.empty()) {
        return deny(DenialCode::InvalidPlan, "plan has no table");
    }

    // -----------------------------------------------------------------------
    // Validate filter shape.  OR/NOT are rejected by default because an
    // equality hidden inside an OR is not a mandatory read constraint.
    // -----------------------------------------------------------------------
    try {
        detail::require_condition_shape(plan.where, 0,
                                        table.allow_disjunctive_filters);
    } catch (const PlanParseError& exception) {
        const std::string message = exception.what();
        if (message.find("disjunctive") != std::string::npos ||
            message.find("negated") != std::string::npos ||
            message.find("empty OR") != std::string::npos) {
            return deny(DenialCode::DisjunctiveFilterDenied, message);
        }
        return deny(DenialCode::MalformedCondition, message);
    }

    // Inject the authenticated tenant as a mandatory equality, preserving the
    // rule that filters may only narrow the read range.
    if (!table.tenant_column.empty()) {
        std::vector<const Condition*> mandatory;
        detail::collect_mandatory(plan.where, mandatory);
        std::string error;
        if (!detail::add_tenant_injection(plan, table.tenant_column,
                                         session.tenant_id, mandatory,
                                         error)) {
            return deny(DenialCode::IdentityMismatch, std::move(error));
        }
    }

    std::vector<const Condition*> mandatory;
    detail::collect_mandatory(plan.where, mandatory);

    // -----------------------------------------------------------------------
    // Mandatory partition-key equality.
    // -----------------------------------------------------------------------
    PartitionKey bound_values;
    std::size_t bound_components = 0;
    for (const auto& column : table.partition_key_columns) {
        const auto values = detail::eq_values(mandatory, column);
        if (values.empty()) {
            continue;
        }
        ++bound_components;
        for (const Value* value : values) {
            if (!value_equal(*value, *values.front())) {
                return deny(DenialCode::ConflictingPartitionConditions,
                            "partition key has conflicting equality values");
            }
        }
        bound_values[column] = *values.front();
    }

    if (bound_components == 0) {
        return deny(DenialCode::MissingPartitionEquality,
                    "query needs an equality on the authorized partition key");
    }
    if (table.require_all_partition_key_components &&
        bound_components != table.partition_key_columns.size()) {
        return deny(DenialCode::PartialPartitionKey,
                    "query must bind every partition-key component");
    }

    // A "partition" access path promises single-partition routing, which is
    // impossible without the complete key.
    if (plan.access_path == AccessPath::Partition &&
        bound_components != table.partition_key_columns.size()) {
        return deny(DenialCode::PartialPartitionKey,
                    "partition lookup requires the complete partition key");
    }

    std::vector<PartitionKey> candidates =
        detail::matching_partitions(table, bound_values);
    if (candidates.empty()) {
        return deny(DenialCode::UnauthorizedPartition,
                    "partition equality does not match an authorized "
                    "partition: " +
                        partition_to_string(bound_values));
    }
    if (candidates.size() > table.max_partitions_per_query) {
        return deny(DenialCode::CrossPartitionDenied,
                    "query would touch " +
                        std::to_string(candidates.size()) +
                        " partitions, exceeding the policy limit");
    }
    if (candidates.size() > 1 && !table.allow_cross_partition_scan) {
        return deny(DenialCode::CrossPartitionDenied,
                    "cross-partition reads are not granted to this role");
    }

    // -----------------------------------------------------------------------
    // Access-path capabilities.
    // -----------------------------------------------------------------------
    switch (plan.access_path) {
    case AccessPath::Partition:
        if (plan.index.has_value()) {
            return deny(DenialCode::InvalidPlan,
                        "partition lookup must not carry index metadata");
        }
        break;

    case AccessPath::SecondaryIndex: {
        if (!table.allow_secondary_index) {
            return deny(DenialCode::SecondaryIndexDenied,
                        "secondary index access is not granted");
        }
        if (!plan.index.has_value()) {
            return deny(DenialCode::InvalidPlan,
                        "secondary index path has no index specification");
        }
        const IndexSpec& index = *plan.index;
        if (index.name.empty() || index.columns.empty()) {
            return deny(DenialCode::InvalidPlan,
                        "secondary index needs a name and columns");
        }
        if (index.requires_backlookup &&
            !table.allow_secondary_index_backlookup) {
            return deny(DenialCode::IndexBacklookupDenied,
                        "secondary index backlookup is not granted");
        }
        if (table.require_full_index_binding &&
            !detail::all_eq_on(mandatory, index.columns)) {
            return deny(DenialCode::IndexNotBound,
                        "secondary index lookup must bind every index column "
                        "with equality");
        }
        break;
    }

    case AccessPath::FullScan:
        if (!table.allow_full_scan ||
            !table.allow_cross_partition_scan) {
            return deny(DenialCode::FullScanDenied,
                        "full-table scan is not granted");
        }
        break;

    case AccessPath::TokenRange:
        if (!table.allow_token_range ||
            !table.allow_cross_partition_scan) {
            return deny(DenialCode::TokenRangeDenied,
                        "token-range scan is not granted");
        }
        break;
    }

    // A plan that carries index metadata but does not use the index is
    // ambiguous; fail closed rather than guessing.
    if (plan.access_path != AccessPath::SecondaryIndex &&
        plan.index.has_value()) {
        return deny(DenialCode::InvalidPlan,
                    "index metadata is present on a non-index plan");
    }

    // -----------------------------------------------------------------------
    // Aggregation capability and scope.
    // -----------------------------------------------------------------------
    if (plan.kind == StatementKind::Select) {
        if (!plan.aggregates.empty()) {
            return deny(DenialCode::InvalidPlan,
                        "select plan carries aggregate functions");
        }
        if (!plan.group_by.empty()) {
            return deny(DenialCode::InvalidPlan,
                        "select plan carries a group-by clause");
        }
    } else {
        if (plan.aggregates.empty()) {
            return deny(DenialCode::InvalidPlan,
                        "aggregate plan has no aggregate functions");
        }
        if (!table.allow_aggregation) {
            return deny(DenialCode::AggregationDenied,
                        "aggregation is not granted to this role");
        }
        for (const auto& aggregate : plan.aggregates) {
            if (aggregate.function != AggregateFunction::Count &&
                aggregate.column.empty()) {
                return deny(DenialCode::InvalidPlan,
                            "aggregate function has no input column");
            }
        }

        if (candidates.size() > 1) {
            if (!table.allow_cross_partition_aggregation) {
                return deny(DenialCode::CrossPartitionAggregationDenied,
                            "cross-partition aggregation is not granted");
            }
            if (!std::all_of(
                    table.partition_key_columns.begin(),
                    table.partition_key_columns.end(),
                    [&](const std::string& column) {
                        return detail::contains_column(plan.group_by, column);
                    })) {
                return deny(
                    DenialCode::AggregationScopeUnknown,
                    "cross-partition aggregate must group by the complete "
                    "partition key");
            }
        }
    }

    if (plan.limit.has_value() && table.max_limit.has_value() &&
        *plan.limit > *table.max_limit) {
        return deny(DenialCode::LimitExceeded,
                    "query limit exceeds the policy maximum");
    }

    // -----------------------------------------------------------------------
    // Issue a token bound to the exact policy generation and partition set.
    // -----------------------------------------------------------------------
    AuthorizedQuery authorized;
    authorized.effective_plan = plan;
    authorized.tenant_id = session.tenant_id;
    authorized.role = session.role;
    authorized.table = plan.table;
    authorized.partition_key_columns = table.partition_key_columns;
    authorized.tenant_column = table.tenant_column;
    authorized.authorized_partitions = std::move(candidates);
    authorized.review_mode = table.review_mode;
    authorized.require_tenant_provenance = table.require_tenant_provenance;
    authorized.aggregation_single_scope =
        plan.kind == StatementKind::Aggregate &&
        authorized.authorized_partitions.size() == 1;
    authorized.policy_generation = generation;
    authorized.policy_fingerprint = fingerprint;
    authorized.nonce = nonce_.fetch_add(1, std::memory_order_relaxed);
    authorized.valid = true;

    AuthorizationDecision decision;
    decision.code = DenialCode::Ok;
    decision.authorized = std::move(authorized);
    return decision;
}

// ---------------------------------------------------------------------------
// Result recheck
// ---------------------------------------------------------------------------

inline ResultReview QueryAuthorizer::recheck(
    const AuthorizedQuery& authorized,
    std::vector<ResultRow> rows) const {
    ResultReview review;
    if (!authorized.valid) {
        review.violation = true;
        review.code = DenialCode::InvalidPlan;
        review.message = "authorization token is not valid";
        return review;
    }

    // Hold the read lock for the whole review: a concurrent revocation must
    // either happen before this check (making the token stale) or after the
    // caller has already received the reviewed rows.
    std::shared_lock lock(mutex_);
    if (authorized.policy_generation != generation_ ||
        authorized.policy_fingerprint != policy_fingerprint_) {
        review.violation = true;
        review.code = DenialCode::StaleAuthorization;
        review.message =
            "authorization was issued under a previous policy revision";
        // Never return rows from a stale authorization, even if they happen
        // to look authorized.
        return review;
    }

    const bool fail_closed =
        authorized.review_mode == ResultReviewMode::FailClosed;

    for (auto& row : rows) {
        const detail::RowCheck check = detail::check_row(authorized, row);
        if (check.allowed) {
            review.rows.push_back(std::move(row));
            continue;
        }

        review.violation = true;
        ++review.rejected_rows;
        if (review.code == DenialCode::Ok) {
            review.code = check.code;
            review.message = check.message;
        }
    }

    if (review.violation && fail_closed) {
        review.rows.clear();
    }
    return review;
}

} // namespace wide::query_auth
