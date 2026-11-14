#include "netsec.hpp"

#include <algorithm>
#include <charconv>
#include <cmath>
#include <cctype>
#include <cstdint>
#include <cstdlib>
#include <iomanip>
#include <limits>
#include <map>
#include <mutex>
#include <set>
#include <sstream>
#include <stdexcept>
#include <string>
#include <tuple>
#include <utility>

namespace netsec {

namespace detail {

struct Principal {
    std::string tenant;
    std::string role;

    friend bool operator<(const Principal& lhs, const Principal& rhs) {
        return std::tie(lhs.tenant, lhs.role) < std::tie(rhs.tenant, rhs.role);
    }
};

struct AuthState {
    mutable std::mutex mutex;
    RolePolicy default_policy;
    std::string default_partition_key_column = "tenant_id";
    std::map<std::string, std::string, std::less<>> table_partition_key_columns;
    std::map<std::string, RolePolicy, std::less<>> role_policies;
    std::map<Principal, std::set<std::string>> grants;
    std::uint64_t epoch = 1;

    void bump_epoch() {
        ++epoch;
        if (epoch == 0) {
            epoch = 1;
        }
    }
};

inline std::string lower_ascii(std::string text) {
    std::transform(text.begin(), text.end(), text.begin(), [](unsigned char ch) {
        return static_cast<char>(std::tolower(ch));
    });
    return text;
}

inline bool same_column(std::string_view lhs, std::string_view rhs) {
    if (lhs == rhs) {
        return true;
    }
    const auto strip = [](std::string_view name) {
        const auto dot = name.rfind('.');
        return dot == std::string_view::npos ? name : name.substr(dot + 1);
    };
    return lhs == strip(rhs) || rhs == strip(lhs);
}

inline std::optional<std::string> partition_text(const Value& value) {
    if (value.is_null()) {
        return std::nullopt;
    }
    if (const auto* number = std::get_if<double>(&value.storage());
        number != nullptr && !std::isfinite(*number)) {
        return std::nullopt;
    }
    return value.to_string();
}

inline std::string value_double_text(double value) {
    if (value == 0.0) {
        value = 0.0;
    }
    char buffer[64];
    const auto result = std::to_chars(buffer, buffer + sizeof(buffer), value,
                                      std::chars_format::general,
                                      std::numeric_limits<double>::max_digits10);
    if (result.ec != std::errc{}) {
        std::ostringstream stream;
        stream << std::setprecision(std::numeric_limits<double>::max_digits10) << value;
        return stream.str();
    }
    return std::string(buffer, result.ptr);
}

struct Scope {
    bool valid = true;
    bool satisfiable = true;
    bool bounded = false;
    std::map<std::string, Value> values;

    static Scope universal() {
        Scope scope;
        scope.bounded = false;
        return scope;
    }

    static Scope unsatisfiable() {
        Scope scope;
        scope.satisfiable = false;
        scope.bounded = true;
        return scope;
    }

    static Scope invalid() {
        Scope scope;
        scope.valid = false;
        scope.satisfiable = false;
        scope.bounded = true;
        return scope;
    }
};

constexpr std::size_t kMaxScopeValues = 4096;
constexpr std::size_t kMaxConditionDepth = 128;

Scope analyze(const ConditionPtr& condition, std::string_view partition_key, std::size_t depth) {
    if (!condition) {
        return Scope::universal();
    }
    if (depth > kMaxConditionDepth) {
        return Scope::invalid();
    }

    switch (condition->kind) {
    case Condition::Kind::True:
        return Scope::universal();
    case Condition::Kind::False:
        return Scope::unsatisfiable();
    case Condition::Kind::Compare: {
        const Predicate& predicate = condition->predicate;
        if (!same_column(predicate.column, partition_key)) {
            return Scope::universal();
        }
        switch (predicate.op) {
        case PredicateOp::Eq: {
            if (predicate.values.size() != 1) {
                return Scope::invalid();
            }
            if (predicate.values.front().is_null()) {
                return Scope::unsatisfiable();
            }
            Scope scope;
            scope.bounded = true;
            const auto& value = predicate.values.front();
            scope.values.emplace(value.canonical(), value);
            return scope;
        }
        case PredicateOp::In: {
            Scope scope;
            scope.bounded = true;
            for (const auto& value : predicate.values) {
                if (value.is_null()) {
                    continue;
                }
                scope.values.emplace(value.canonical(), value);
                if (scope.values.size() > kMaxScopeValues) {
                    return Scope::invalid();
                }
            }
            if (scope.values.empty()) {
                scope.satisfiable = false;
            }
            return scope;
        }
        default:
            return Scope::universal();
        }
    }
    case Condition::Kind::And: {
        Scope result = Scope::universal();
        bool have_finite = false;
        for (const auto& child : condition->children) {
            const Scope part = analyze(child, partition_key, depth + 1);
            if (!part.valid) {
                return part;
            }
            if (!part.satisfiable) {
                return Scope::unsatisfiable();
            }
            if (!part.bounded) {
                continue;
            }
            if (!have_finite) {
                result = part;
                have_finite = true;
                continue;
            }
            std::map<std::string, Value> intersection;
            for (const auto& [key, value] : result.values) {
                if (part.values.find(key) != part.values.end()) {
                    intersection.emplace(key, value);
                }
            }
            result.values = std::move(intersection);
            if (result.values.empty()) {
                return Scope::unsatisfiable();
            }
        }
        result.satisfiable = true;
        result.bounded = have_finite;
        result.valid = true;
        return result;
    }
    case Condition::Kind::Or: {
        Scope result;
        bool have_satisfiable = false;
        bool have_universal = false;
        for (const auto& child : condition->children) {
            const Scope part = analyze(child, partition_key, depth + 1);
            if (!part.valid) {
                return part;
            }
            if (!part.satisfiable) {
                continue;
            }
            have_satisfiable = true;
            if (!part.bounded) {
                have_universal = true;
                continue;
            }
            for (const auto& [key, value] : part.values) {
                result.values.emplace(key, value);
                if (result.values.size() > kMaxScopeValues) {
                    return Scope::invalid();
                }
            }
        }
        if (!have_satisfiable) {
            return Scope::unsatisfiable();
        }
        if (have_universal) {
            return Scope::universal();
        }
        result.satisfiable = true;
        result.bounded = true;
        result.valid = true;
        return result;
    }
    case Condition::Kind::Not: {
        if (condition->children.size() != 1) {
            return Scope::invalid();
        }
        const Scope child = analyze(condition->children.front(), partition_key, depth + 1);
        if (!child.valid) {
            return child;
        }
        return Scope::universal();
    }
    }
    return Scope::invalid();
}

inline RolePolicy lookup_policy(const AuthState& state, std::string_view role) {
    const auto exact = state.role_policies.find(role);
    if (exact != state.role_policies.end()) {
        return exact->second;
    }
    const auto wildcard = state.role_policies.find("*");
    if (wildcard != state.role_policies.end()) {
        return wildcard->second;
    }
    return state.default_policy;
}

inline std::set<std::string> lookup_grants(const AuthState& state, const AuthContext& identity) {
    std::set<std::string> result;
    const auto exact = state.grants.find({identity.tenant, identity.role});
    if (exact != state.grants.end()) {
        result.insert(exact->second.begin(), exact->second.end());
    }
    const auto wildcard = state.grants.find({identity.tenant, "*"});
    if (wildcard != state.grants.end()) {
        result.insert(wildcard->second.begin(), wildcard->second.end());
    }
    return result;
}

inline ConditionPtr and_conditions(ConditionPtr left, ConditionPtr right) {
    if (!left) {
        return right;
    }
    if (!right) {
        return left;
    }
    auto condition = std::make_shared<Condition>();
    condition->kind = Condition::Kind::And;
    condition->children.push_back(std::move(left));
    condition->children.push_back(std::move(right));
    return condition;
}

inline bool valid_aggregate_function(std::string_view function) {
    const std::string name = lower_ascii(std::string(function));
    return name == "count" || name == "sum" || name == "min" || name == "max" || name == "avg";
}

}  // namespace detail

Value::Value(std::string value) : value_(std::move(value)) {}
Value::Value(const char* value) : value_(value == nullptr ? Storage{} : Storage{std::string(value)}) {}
Value::Value(std::int64_t value) : value_(value) {}
Value::Value(int value) : value_(static_cast<std::int64_t>(value)) {}
Value::Value(double value) : value_(value) {}
Value::Value(bool value) : value_(value) {}

Value Value::null() {
    return Value{};
}

bool Value::is_null() const noexcept {
    return std::holds_alternative<std::monostate>(value_);
}

const Value::Storage& Value::storage() const noexcept {
    return value_;
}

std::string Value::canonical() const {
    return std::visit(
        [](const auto& value) -> std::string {
            using T = std::decay_t<decltype(value)>;
            if constexpr (std::is_same_v<T, std::monostate>) {
                return "n";
            } else if constexpr (std::is_same_v<T, std::string>) {
                return "s" + std::to_string(value.size()) + ":" + value;
            } else if constexpr (std::is_same_v<T, std::int64_t>) {
                return "i:" + std::to_string(value);
            } else if constexpr (std::is_same_v<T, double>) {
                return "d:" + detail::value_double_text(value);
            } else {
                return value ? "b:1" : "b:0";
            }
        },
        value_);
}

std::string Value::to_string() const {
    return std::visit(
        [](const auto& value) -> std::string {
            using T = std::decay_t<decltype(value)>;
            if constexpr (std::is_same_v<T, std::monostate>) {
                return "NULL";
            } else if constexpr (std::is_same_v<T, std::string>) {
                return value;
            } else if constexpr (std::is_same_v<T, std::int64_t>) {
                return std::to_string(value);
            } else if constexpr (std::is_same_v<T, double>) {
                return detail::value_double_text(value);
            } else {
                return value ? "true" : "false";
            }
        },
        value_);
}

bool operator==(const Value& lhs, const Value& rhs) {
    return lhs.canonical() == rhs.canonical();
}

ConditionPtr Condition::truth(bool value) {
    auto condition = std::make_shared<Condition>();
    condition->kind = value ? Kind::True : Kind::False;
    return condition;
}

ConditionPtr Condition::compare(std::string column, PredicateOp op, std::vector<Value> values) {
    auto condition = std::make_shared<Condition>();
    condition->kind = Kind::Compare;
    condition->predicate = Predicate{std::move(column), op, std::move(values)};
    return condition;
}

ConditionPtr Condition::eq(std::string column, Value value) {
    return compare(std::move(column), PredicateOp::Eq, {std::move(value)});
}

ConditionPtr Condition::in(std::string column, std::vector<Value> values) {
    return compare(std::move(column), PredicateOp::In, std::move(values));
}

ConditionPtr Condition::ne(std::string column, Value value) {
    return compare(std::move(column), PredicateOp::Ne, {std::move(value)});
}

ConditionPtr Condition::lt(std::string column, Value value) {
    return compare(std::move(column), PredicateOp::Lt, {std::move(value)});
}

ConditionPtr Condition::le(std::string column, Value value) {
    return compare(std::move(column), PredicateOp::Le, {std::move(value)});
}

ConditionPtr Condition::gt(std::string column, Value value) {
    return compare(std::move(column), PredicateOp::Gt, {std::move(value)});
}

ConditionPtr Condition::ge(std::string column, Value value) {
    return compare(std::move(column), PredicateOp::Ge, {std::move(value)});
}

ConditionPtr Condition::all(std::vector<ConditionPtr> children) {
    auto condition = std::make_shared<Condition>();
    condition->kind = Kind::And;
    condition->children = std::move(children);
    return condition;
}

ConditionPtr Condition::any(std::vector<ConditionPtr> children) {
    auto condition = std::make_shared<Condition>();
    condition->kind = Kind::Or;
    condition->children = std::move(children);
    return condition;
}

ConditionPtr Condition::negate(ConditionPtr child) {
    auto condition = std::make_shared<Condition>();
    condition->kind = Kind::Not;
    condition->children.push_back(std::move(child));
    return condition;
}

PartitionScope analyze_partition_scope(const ConditionPtr& condition,
                                       std::string_view partition_key_column) {
    const detail::Scope scope = detail::analyze(condition, partition_key_column, 0);
    PartitionScope result;
    result.valid = scope.valid;
    result.satisfiable = scope.satisfiable;
    result.bounded = scope.bounded;
    for (const auto& [key, value] : scope.values) {
        (void)key;
        result.values.push_back(value);
    }
    return result;
}

const Value* DbRow::find(std::string_view column) const {
    const auto it = columns.find(column);
    return it == columns.end() ? nullptr : &it->second;
}

std::optional<Value> DbRow::get(std::string_view column) const {
    const Value* value = find(column);
    if (value == nullptr) {
        return std::nullopt;
    }
    return *value;
}

AuthzEngine::AuthzEngine() : state_(std::make_shared<detail::AuthState>()) {}

AuthzEngine::AuthzEngine(RolePolicy default_policy) : AuthzEngine() {
    set_default_policy(default_policy);
}

void AuthzEngine::set_default_policy(RolePolicy policy) {
    std::lock_guard lock(state_->mutex);
    state_->default_policy = policy;
    state_->bump_epoch();
}

RolePolicy AuthzEngine::default_policy() const {
    std::lock_guard lock(state_->mutex);
    return state_->default_policy;
}

void AuthzEngine::set_partition_key_column(std::string column) {
    std::lock_guard lock(state_->mutex);
    state_->default_partition_key_column = std::move(column);
    state_->bump_epoch();
}

void AuthzEngine::set_table_partition_key(std::string table, std::string column) {
    std::lock_guard lock(state_->mutex);
    state_->table_partition_key_columns.insert_or_assign(std::move(table), std::move(column));
    state_->bump_epoch();
}

std::string AuthzEngine::partition_key_column(std::string_view table) const {
    std::lock_guard lock(state_->mutex);
    const auto it = state_->table_partition_key_columns.find(table);
    if (it != state_->table_partition_key_columns.end()) {
        return it->second;
    }
    return state_->default_partition_key_column;
}

void AuthzEngine::set_role_policy(std::string role, RolePolicy policy) {
    std::lock_guard lock(state_->mutex);
    state_->role_policies.insert_or_assign(std::move(role), policy);
    state_->bump_epoch();
}

void AuthzEngine::clear_role_policy(std::string_view role) {
    std::lock_guard lock(state_->mutex);
    const auto it = state_->role_policies.find(role);
    if (it != state_->role_policies.end()) {
        state_->role_policies.erase(it);
        state_->bump_epoch();
    }
}

RolePolicy AuthzEngine::role_policy(std::string_view role) const {
    std::lock_guard lock(state_->mutex);
    return detail::lookup_policy(*state_, role);
}

void AuthzEngine::grant(std::string tenant, std::string role,
                        std::vector<std::string> partitions) {
    std::lock_guard lock(state_->mutex);
    auto& grant_set = state_->grants[{std::move(tenant), std::move(role)}];
    for (auto& partition : partitions) {
        grant_set.insert(std::move(partition));
    }
    state_->bump_epoch();
}

void AuthzEngine::set_grant(std::string tenant, std::string role,
                            std::vector<std::string> partitions) {
    std::lock_guard lock(state_->mutex);
    std::set<std::string> replacement;
    for (auto& partition : partitions) {
        replacement.insert(std::move(partition));
    }
    const detail::Principal principal{std::move(tenant), std::move(role)};
    if (replacement.empty()) {
        state_->grants.erase(principal);
    } else {
        state_->grants.insert_or_assign(principal, std::move(replacement));
    }
    state_->bump_epoch();
}

void AuthzEngine::revoke(std::string_view tenant, std::string_view role,
                         std::vector<std::string> partitions) {
    std::lock_guard lock(state_->mutex);
    const detail::Principal principal{std::string(tenant), std::string(role)};
    const auto it = state_->grants.find(principal);
    if (it == state_->grants.end()) {
        return;
    }
    for (const auto& partition : partitions) {
        it->second.erase(partition);
    }
    if (it->second.empty()) {
        state_->grants.erase(it);
    }
    state_->bump_epoch();
}

void AuthzEngine::remove_principal(std::string_view tenant, std::string_view role) {
    std::lock_guard lock(state_->mutex);
    state_->grants.erase({std::string(tenant), std::string(role)});
    state_->bump_epoch();
}

void AuthzEngine::clear() {
    std::lock_guard lock(state_->mutex);
    state_->grants.clear();
    state_->role_policies.clear();
    state_->table_partition_key_columns.clear();
    state_->default_partition_key_column = "tenant_id";
    state_->bump_epoch();
}

bool AuthzEngine::is_granted(std::string_view tenant, std::string_view role,
                             std::string_view partition) const {
    std::lock_guard lock(state_->mutex);
    const auto grants = detail::lookup_grants(*state_, AuthContext{std::string(tenant), std::string(role)});
    return grants.find(std::string(partition)) != grants.end();
}

std::vector<std::string> AuthzEngine::partitions_for(std::string_view tenant,
                                                     std::string_view role) const {
    std::lock_guard lock(state_->mutex);
    const auto grants = detail::lookup_grants(*state_, AuthContext{std::string(tenant), std::string(role)});
    return std::vector<std::string>(grants.begin(), grants.end());
}

std::uint64_t AuthzEngine::authorization_epoch() const {
    std::lock_guard lock(state_->mutex);
    return state_->epoch;
}

AuthorizationResult AuthzEngine::authorize(const QueryPlan& plan,
                                           const AuthContext& identity) const {
    const auto deny = [](DenyCode code, std::string reason) {
        AuthorizationResult result;
        result.allowed = false;
        result.code = code;
        result.reason = std::move(reason);
        return result;
    };

    if (identity.tenant.empty() || identity.role.empty()) {
        return deny(DenyCode::MissingIdentity, "tenant and role must be supplied by the service");
    }
    if (plan.operation == QueryOperation::Select && !plan.aggregates.empty()) {
        return deny(DenyCode::InvalidPlan, "select plan carries aggregate specifications");
    }

    RolePolicy policy;
    std::set<std::string> grants;
    std::string partition_key;
    std::uint64_t epoch = 0;
    {
        std::lock_guard lock(state_->mutex);
        policy = detail::lookup_policy(*state_, identity.role);
        grants = detail::lookup_grants(*state_, identity);
        const auto table_key = state_->table_partition_key_columns.find(plan.table);
        partition_key = table_key == state_->table_partition_key_columns.end()
                            ? state_->default_partition_key_column
                            : table_key->second;
        epoch = state_->epoch;
    }
    if (partition_key.empty()) {
        return deny(DenyCode::InvalidPlan, "authoritative partition key column is not configured");
    }
    if (!plan.partition_key_column.empty() &&
        !detail::same_column(plan.partition_key_column, partition_key)) {
        return deny(DenyCode::InvalidPlan,
                    "plan partition key does not match the configured table schema");
    }
    if (grants.empty()) {
        return deny(DenyCode::NoGrant, "principal has no authorized partition");
    }

    const detail::Scope scope = detail::analyze(plan.where, partition_key, 0);
    if (!scope.valid) {
        return deny(DenyCode::InvalidPlan, "query condition is malformed or too deeply nested");
    }
    if (!scope.satisfiable) {
        return deny(DenyCode::PredicateCannotBeBounded,
                    "query condition is unsatisfiable and cannot be authorized safely");
    }
    if (!scope.bounded || scope.values.empty()) {
        return deny(DenyCode::MissingPartitionEquality,
                    "query needs an equality or IN condition on the authorized partition key");
    }

    std::vector<std::string> predicate_partitions;
    predicate_partitions.reserve(scope.values.size());
    for (const auto& [key, value] : scope.values) {
        (void)key;
        const auto partition = detail::partition_text(value);
        if (!partition.has_value()) {
            return deny(DenyCode::PredicateCannotBeBounded,
                        "partition key value cannot be represented as a partition id");
        }
        if (grants.find(*partition) == grants.end()) {
            return deny(DenyCode::UnauthorizedPartition,
                        "query references partition outside the authorization grant: " + *partition);
        }
        predicate_partitions.push_back(*partition);
    }

    std::vector<std::string> scan_partitions;
    ScanType enforced_scan = plan.scan_type;

    switch (plan.scan_type) {
    case ScanType::SinglePartition: {
        if (plan.scan_partition.has_value()) {
            const auto requested = detail::partition_text(*plan.scan_partition);
            if (!requested.has_value()) {
                return deny(DenyCode::InvalidPlan, "single-partition scan has a null partition key");
            }
            if (std::find(predicate_partitions.begin(), predicate_partitions.end(), *requested) ==
                predicate_partitions.end()) {
                return deny(DenyCode::PredicateCannotBeBounded,
                            "scan partition is not constrained by the query predicates");
            }
            scan_partitions.push_back(*requested);
        } else {
            if (predicate_partitions.size() != 1) {
                return deny(DenyCode::PredicateCannotBeBounded,
                            "single-partition scan without a partition id requires one predicate value");
            }
            scan_partitions.push_back(predicate_partitions.front());
        }
        break;
    }
    case ScanType::MultiPartition: {
        if (plan.requested_partitions.empty()) {
            return deny(DenyCode::CrossPartitionDenied,
                        "multi-partition scan must enumerate its partitions");
        }
        std::set<std::string> unique;
        for (const auto& value : plan.requested_partitions) {
            const auto requested = detail::partition_text(value);
            if (!requested.has_value() ||
                std::find(predicate_partitions.begin(), predicate_partitions.end(), *requested) ==
                    predicate_partitions.end()) {
                return deny(DenyCode::PredicateCannotBeBounded,
                            "scan partitions widen the range allowed by the query predicates");
            }
            unique.insert(*requested);
        }
        scan_partitions.assign(unique.begin(), unique.end());
        break;
    }
    case ScanType::FullTable: {
        if (!policy.allow_full_table_scan) {
            return deny(DenyCode::FullTableScanDenied,
                        "full-table scans are denied by policy");
        }
        scan_partitions = predicate_partitions;
        enforced_scan = scan_partitions.size() == 1 ? ScanType::SinglePartition
                                                    : ScanType::MultiPartition;
        break;
    }
    case ScanType::SecondaryIndex: {
        if (!policy.allow_secondary_index) {
            return deny(DenyCode::SecondaryIndexDenied,
                        "secondary-index lookups are denied by policy");
        }
        if (!plan.index_name.has_value() || plan.index_name->empty()) {
            return deny(DenyCode::InvalidPlan, "secondary-index scan has no index name");
        }
        if (plan.scan_partition.has_value()) {
            const auto requested = detail::partition_text(*plan.scan_partition);
            if (!requested.has_value() ||
                std::find(predicate_partitions.begin(), predicate_partitions.end(), *requested) ==
                    predicate_partitions.end()) {
                return deny(DenyCode::PredicateCannotBeBounded,
                            "index partition is not constrained by the query predicates");
            }
            scan_partitions.push_back(*requested);
        } else if (!plan.requested_partitions.empty()) {
            std::set<std::string> unique;
            for (const auto& value : plan.requested_partitions) {
                const auto requested = detail::partition_text(value);
                if (!requested.has_value() ||
                    std::find(predicate_partitions.begin(), predicate_partitions.end(), *requested) ==
                        predicate_partitions.end()) {
                    return deny(DenyCode::PredicateCannotBeBounded,
                                "index partition set widens the predicate range");
                }
                unique.insert(*requested);
            }
            scan_partitions.assign(unique.begin(), unique.end());
        } else {
            scan_partitions = predicate_partitions;
        }
        break;
    }
    }

    if (scan_partitions.empty()) {
        return deny(DenyCode::PredicateCannotBeBounded,
                    "authorized scan has no concrete partition");
    }
    if (scan_partitions.size() > policy.max_partitions_per_query) {
        return deny(DenyCode::CrossPartitionDenied,
                    "query exceeds the policy partition budget");
    }
    if (scan_partitions.size() > 1 && !policy.allow_cross_partition_scan) {
        return deny(DenyCode::CrossPartitionDenied,
                    "cross-partition scans are denied by policy");
    }
    if (plan.allow_filtering && !policy.allow_filtering) {
        return deny(DenyCode::FilteringDenied,
                    "ALLOW FILTERING is denied by policy");
    }

    if (plan.operation == QueryOperation::Aggregate) {
        if (!policy.allow_aggregation) {
            return deny(DenyCode::AggregationDenied,
                        "aggregations are denied by policy");
        }
        if (plan.aggregates.empty()) {
            return deny(DenyCode::InvalidPlan, "aggregate plan has no aggregate function");
        }
        for (const auto& aggregate : plan.aggregates) {
            if (!detail::valid_aggregate_function(aggregate.function)) {
                return deny(DenyCode::InvalidPlan,
                            "unsupported aggregate function: " + aggregate.function);
            }
            if (aggregate.column.empty()) {
                return deny(DenyCode::InvalidPlan, "aggregate function has no column");
            }
        }
        if (scan_partitions.size() > 1) {
            if (!policy.allow_cross_partition_aggregation) {
                return deny(DenyCode::AggregationDenied,
                            "cross-partition aggregation is denied by policy");
            }
            const bool groups_by_partition = std::any_of(
                plan.group_by.begin(), plan.group_by.end(), [&](const std::string& column) {
                    return detail::same_column(column, partition_key);
                });
            if (!groups_by_partition) {
                return deny(DenyCode::AggregationDenied,
                            "cross-partition aggregation must include the partition key in GROUP BY");
            }
        }
    }

    BoundPlan bound;
    bound.query = plan;
    bound.query.partition_key_column = partition_key;
    bound.identity = identity;
    bound.authorized_partitions = scan_partitions;
    bound.authorization_epoch = epoch;
    bound.requires_result_recheck = true;

    std::vector<Value> partition_values;
    partition_values.reserve(scan_partitions.size());
    for (const auto& partition : scan_partitions) {
        partition_values.emplace_back(partition);
    }
    if (scan_partitions.size() == 1) {
        bound.injected_partition_filter =
            Condition::eq(partition_key, partition_values.front());
    } else {
        bound.injected_partition_filter =
            Condition::in(partition_key, std::move(partition_values));
    }
    bound.query.where = detail::and_conditions(bound.injected_partition_filter, plan.where);
    bound.query.scan_type = enforced_scan;
    bound.query.scan_partition.reset();
    bound.query.requested_partitions.clear();
    if (enforced_scan == ScanType::SinglePartition) {
        bound.query.scan_partition = Value(scan_partitions.front());
    } else if (enforced_scan == ScanType::MultiPartition ||
               enforced_scan == ScanType::SecondaryIndex) {
        for (const auto& partition : scan_partitions) {
            bound.query.requested_partitions.emplace_back(partition);
        }
    }
    if (enforced_scan != ScanType::SecondaryIndex) {
        bound.query.index_name.reset();
    }

    {
        std::lock_guard lock(state_->mutex);
        if (state_->epoch != epoch) {
            return deny(DenyCode::AuthorizationChanged,
                        "authorization changed while the query was being authorized");
        }
    }

    AuthorizationResult result;
    result.allowed = true;
    result.code = DenyCode::None;
    result.reason = "authorized";
    result.plan = std::move(bound);
    return result;
}

bool ResultGuard::valid() const {
    if (!state_ || epoch_ == 0 || partitions_.empty()) {
        return false;
    }
    std::lock_guard lock(state_->mutex);
    if (state_->epoch != epoch_) {
        return false;
    }
    const auto grants = detail::lookup_grants(*state_, identity_);
    for (const auto& partition : partitions_) {
        if (grants.find(partition) == grants.end()) {
            return false;
        }
    }
    return true;
}

bool ResultGuard::accept(const DbRow& row) const {
    if (!state_ || epoch_ == 0 || partitions_.empty()) {
        return false;
    }
    std::lock_guard lock(state_->mutex);
    if (state_->epoch != epoch_) {
        return false;
    }
    const auto grants = detail::lookup_grants(*state_, identity_);
    for (const auto& partition : partitions_) {
        if (grants.find(partition) == grants.end()) {
            return false;
        }
    }

    std::optional<Value> partition = row.partition;
    if (!partition.has_value()) {
        partition = row.get(partition_key_column_);
        if (!partition.has_value()) {
            for (const auto& [column, value] : row.columns) {
                if (detail::same_column(column, partition_key_column_)) {
                    partition = value;
                    break;
                }
            }
        }
    }
    if (!partition.has_value()) {
        return allow_missing_partition_evidence_;
    }
    const auto partition_id = detail::partition_text(*partition);
    if (!partition_id.has_value()) {
        return false;
    }
    const std::string canonical = Value(*partition_id).canonical();
    return std::find(partition_canonicals_.begin(), partition_canonicals_.end(), canonical) !=
           partition_canonicals_.end();
}

std::vector<DbRow> ResultGuard::filter(const std::vector<DbRow>& rows) const {
    std::vector<DbRow> accepted;
    accepted.reserve(rows.size());
    for (const auto& row : rows) {
        if (accept(row)) {
            accepted.push_back(row);
        }
    }
    return accepted;
}

std::vector<DbRow> ResultGuard::filter(std::vector<DbRow>&& rows) const {
    std::vector<DbRow> accepted;
    accepted.reserve(rows.size());
    for (auto& row : rows) {
        if (accept(row)) {
            accepted.push_back(std::move(row));
        }
    }
    return accepted;
}

const AuthContext& ResultGuard::identity() const noexcept {
    return identity_;
}

const std::vector<std::string>& ResultGuard::partitions() const noexcept {
    return partitions_;
}

std::uint64_t ResultGuard::authorization_epoch() const noexcept {
    return epoch_;
}

ResultGuard AuthzEngine::guard(const AuthorizationResult& result) const {
    ResultGuard guard;
    if (!result.allowed) {
        return guard;
    }
    guard.state_ = state_;
    guard.identity_ = result.plan.identity;
    guard.partition_key_column_ = result.plan.query.partition_key_column;
    guard.partitions_ = result.plan.authorized_partitions;
    guard.partition_canonicals_.reserve(guard.partitions_.size());
    for (const auto& partition : guard.partitions_) {
        guard.partition_canonicals_.push_back(Value(partition).canonical());
    }
    guard.epoch_ = result.plan.authorization_epoch;
    guard.allow_missing_partition_evidence_ =
        result.plan.query.operation == QueryOperation::Aggregate &&
        result.plan.authorized_partitions.size() == 1;
    return guard;
}

std::optional<ResultGuard> AuthzEngine::make_guard(const AuthorizationResult& result) const {
    if (!result.allowed) {
        return std::nullopt;
    }
    return guard(result);
}

namespace detail {

struct JsonValue {
    using Array = std::vector<JsonValue>;
    using Object = std::map<std::string, JsonValue, std::less<>>;
    using Storage =
        std::variant<std::nullptr_t, bool, double, std::string, std::shared_ptr<Array>,
                     std::shared_ptr<Object>>;

    Storage data;

    bool is_object() const { return std::holds_alternative<std::shared_ptr<Object>>(data); }
    bool is_array() const { return std::holds_alternative<std::shared_ptr<Array>>(data); }

    const Object* object() const {
        const auto* value = std::get_if<std::shared_ptr<Object>>(&data);
        return value == nullptr ? nullptr : value->get();
    }

    const Array* array() const {
        const auto* value = std::get_if<std::shared_ptr<Array>>(&data);
        return value == nullptr ? nullptr : value->get();
    }
};

class JsonParser {
public:
    explicit JsonParser(std::string_view input) : input_(input) {}

    bool parse(JsonValue& output) {
        skip_space();
        if (!parse_value(output, 0)) {
            return false;
        }
        skip_space();
        if (position_ != input_.size()) {
            return fail("unexpected trailing data");
        }
        return true;
    }

    const std::string& error() const { return error_; }
    std::size_t position() const { return position_; }

private:
    bool fail(std::string message) {
        if (error_.empty()) {
            error_ = std::move(message) + " at byte " + std::to_string(position_);
        }
        return false;
    }

    void skip_space() {
        while (position_ < input_.size()) {
            const char ch = input_[position_];
            if (ch != ' ' && ch != '\t' && ch != '\n' && ch != '\r') {
                break;
            }
            ++position_;
        }
    }

    bool consume(char expected) {
        if (position_ >= input_.size() || input_[position_] != expected) {
            return fail(std::string("expected '") + expected + "'");
        }
        ++position_;
        return true;
    }

    bool parse_value(JsonValue& output, std::size_t depth) {
        if (depth > 128) {
            return fail("JSON nesting is too deep");
        }
        skip_space();
        if (position_ >= input_.size()) {
            return fail("unexpected end of input");
        }
        switch (input_[position_]) {
        case '{':
            return parse_object(output, depth + 1);
        case '[':
            return parse_array(output, depth + 1);
        case '"': {
            std::string value;
            if (!parse_string(value)) {
                return false;
            }
            output.data = std::move(value);
            return true;
        }
        case 't':
            if (!consume_literal("true")) {
                return false;
            }
            output.data = true;
            return true;
        case 'f':
            if (!consume_literal("false")) {
                return false;
            }
            output.data = false;
            return true;
        case 'n':
            if (!consume_literal("null")) {
                return false;
            }
            output.data = nullptr;
            return true;
        default:
            if (input_[position_] == '-' ||
                (input_[position_] >= '0' && input_[position_] <= '9')) {
                return parse_number(output);
            }
            return fail("unexpected character");
        }
    }

    bool parse_object(JsonValue& output, std::size_t depth) {
        if (!consume('{')) {
            return false;
        }
        auto object = std::make_shared<JsonValue::Object>();
        skip_space();
        if (position_ < input_.size() && input_[position_] == '}') {
            ++position_;
            output.data = std::move(object);
            return true;
        }
        while (true) {
            skip_space();
            std::string key;
            if (!parse_string(key)) {
                return false;
            }
            skip_space();
            if (!consume(':')) {
                return false;
            }
            JsonValue value;
            if (!parse_value(value, depth + 1)) {
                return false;
            }
            if (object->find(key) != object->end()) {
                return fail("duplicate object key: " + key);
            }
            object->emplace(std::move(key), std::move(value));
            skip_space();
            if (position_ < input_.size() && input_[position_] == ',') {
                ++position_;
                continue;
            }
            if (!consume('}')) {
                return false;
            }
            break;
        }
        output.data = std::move(object);
        return true;
    }

    bool parse_array(JsonValue& output, std::size_t depth) {
        if (!consume('[')) {
            return false;
        }
        auto array = std::make_shared<JsonValue::Array>();
        skip_space();
        if (position_ < input_.size() && input_[position_] == ']') {
            ++position_;
            output.data = std::move(array);
            return true;
        }
        while (true) {
            JsonValue value;
            if (!parse_value(value, depth + 1)) {
                return false;
            }
            array->push_back(std::move(value));
            skip_space();
            if (position_ < input_.size() && input_[position_] == ',') {
                ++position_;
                continue;
            }
            if (!consume(']')) {
                return false;
            }
            break;
        }
        output.data = std::move(array);
        return true;
    }

    static void append_utf8(std::string& output, std::uint32_t codepoint) {
        if (codepoint <= 0x7f) {
            output.push_back(static_cast<char>(codepoint));
        } else if (codepoint <= 0x7ff) {
            output.push_back(static_cast<char>(0xc0 | (codepoint >> 6)));
            output.push_back(static_cast<char>(0x80 | (codepoint & 0x3f)));
        } else if (codepoint <= 0xffff) {
            output.push_back(static_cast<char>(0xe0 | (codepoint >> 12)));
            output.push_back(static_cast<char>(0x80 | ((codepoint >> 6) & 0x3f)));
            output.push_back(static_cast<char>(0x80 | (codepoint & 0x3f)));
        } else {
            output.push_back(static_cast<char>(0xf0 | (codepoint >> 18)));
            output.push_back(static_cast<char>(0x80 | ((codepoint >> 12) & 0x3f)));
            output.push_back(static_cast<char>(0x80 | ((codepoint >> 6) & 0x3f)));
            output.push_back(static_cast<char>(0x80 | (codepoint & 0x3f)));
        }
    }

    bool parse_hex4(std::uint32_t& value) {
        value = 0;
        for (int index = 0; index < 4; ++index) {
            if (position_ >= input_.size()) {
                return fail("truncated unicode escape");
            }
            const char ch = input_[position_++];
            value <<= 4;
            if (ch >= '0' && ch <= '9') {
                value |= static_cast<std::uint32_t>(ch - '0');
            } else if (ch >= 'a' && ch <= 'f') {
                value |= static_cast<std::uint32_t>(ch - 'a' + 10);
            } else if (ch >= 'A' && ch <= 'F') {
                value |= static_cast<std::uint32_t>(ch - 'A' + 10);
            } else {
                return fail("invalid unicode escape");
            }
        }
        return true;
    }

    bool parse_string(std::string& output) {
        if (!consume('"')) {
            return false;
        }
        output.clear();
        while (position_ < input_.size()) {
            const unsigned char ch = static_cast<unsigned char>(input_[position_++]);
            if (ch == '"') {
                return true;
            }
            if (ch == '\\') {
                if (position_ >= input_.size()) {
                    return fail("truncated escape sequence");
                }
                const char escape = input_[position_++];
                switch (escape) {
                case '"':
                    output.push_back('"');
                    break;
                case '\\':
                    output.push_back('\\');
                    break;
                case '/':
                    output.push_back('/');
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
                    std::uint32_t codepoint = 0;
                    if (!parse_hex4(codepoint)) {
                        return false;
                    }
                    if (codepoint >= 0xd800 && codepoint <= 0xdbff &&
                        position_ + 6 <= input_.size() && input_[position_] == '\\' &&
                        input_[position_ + 1] == 'u') {
                        position_ += 2;
                        std::uint32_t low = 0;
                        if (!parse_hex4(low)) {
                            return false;
                        }
                        if (low < 0xdc00 || low > 0xdfff) {
                            return fail("invalid unicode surrogate pair");
                        }
                        codepoint = 0x10000 + ((codepoint - 0xd800) << 10) + (low - 0xdc00);
                    }
                    append_utf8(output, codepoint);
                    break;
                }
                default:
                    return fail("invalid escape sequence");
                }
                continue;
            }
            if (ch < 0x20) {
                return fail("control character in string");
            }
            output.push_back(static_cast<char>(ch));
        }
        return fail("unterminated string");
    }

    bool consume_literal(std::string_view literal) {
        if (input_.substr(position_, literal.size()) != literal) {
            return fail("invalid literal");
        }
        position_ += literal.size();
        return true;
    }

    bool parse_number(JsonValue& output) {
        const std::size_t start = position_;
        if (input_[position_] == '-') {
            ++position_;
        }
        if (position_ >= input_.size()) {
            return fail("truncated number");
        }
        if (input_[position_] == '0') {
            ++position_;
        } else if (input_[position_] >= '1' && input_[position_] <= '9') {
            while (position_ < input_.size() && std::isdigit(static_cast<unsigned char>(input_[position_]))) {
                ++position_;
            }
        } else {
            return fail("invalid number");
        }
        if (position_ < input_.size() && input_[position_] == '.') {
            ++position_;
            if (position_ >= input_.size() ||
                !std::isdigit(static_cast<unsigned char>(input_[position_]))) {
                return fail("invalid fraction");
            }
            while (position_ < input_.size() &&
                   std::isdigit(static_cast<unsigned char>(input_[position_]))) {
                ++position_;
            }
        }
        if (position_ < input_.size() &&
            (input_[position_] == 'e' || input_[position_] == 'E')) {
            ++position_;
            if (position_ < input_.size() &&
                (input_[position_] == '+' || input_[position_] == '-')) {
                ++position_;
            }
            if (position_ >= input_.size() ||
                !std::isdigit(static_cast<unsigned char>(input_[position_]))) {
                return fail("invalid exponent");
            }
            while (position_ < input_.size() &&
                   std::isdigit(static_cast<unsigned char>(input_[position_]))) {
                ++position_;
            }
        }
        double value = 0.0;
        const auto result =
            std::from_chars(input_.data() + start, input_.data() + position_, value);
        if (result.ec != std::errc{} || result.ptr != input_.data() + position_) {
            return fail("number is out of range");
        }
        output.data = value;
        return true;
    }

    std::string_view input_;
    std::size_t position_ = 0;
    std::string error_;
};

inline const JsonValue* json_field(const JsonValue::Object& object, std::string_view name) {
    const auto it = object.find(name);
    return it == object.end() ? nullptr : &it->second;
}

inline const JsonValue* json_field(const JsonValue::Object& object,
                                   std::initializer_list<std::string_view> names) {
    for (const auto name : names) {
        if (const JsonValue* value = json_field(object, name); value != nullptr) {
            return value;
        }
    }
    return nullptr;
}

inline const std::string* json_string(const JsonValue* value) {
    return value == nullptr ? nullptr : std::get_if<std::string>(&value->data);
}

inline const bool* json_bool(const JsonValue* value) {
    return value == nullptr ? nullptr : std::get_if<bool>(&value->data);
}

inline const double* json_number(const JsonValue* value) {
    return value == nullptr ? nullptr : std::get_if<double>(&value->data);
}

inline bool json_scalar_to_value(const JsonValue& json, Value& value) {
    if (std::get_if<std::nullptr_t>(&json.data) != nullptr) {
        value = Value::null();
        return true;
    }
    if (const auto* text = std::get_if<std::string>(&json.data)) {
        value = Value(*text);
        return true;
    }
    if (const auto* boolean = std::get_if<bool>(&json.data)) {
        value = Value(*boolean);
        return true;
    }
    if (const auto* number = std::get_if<double>(&json.data)) {
        if (std::isfinite(*number) && std::trunc(*number) == *number &&
            *number >= static_cast<double>(std::numeric_limits<std::int64_t>::min()) &&
            *number <= static_cast<double>(std::numeric_limits<std::int64_t>::max())) {
            value = Value(static_cast<std::int64_t>(*number));
        } else {
            value = Value(*number);
        }
        return true;
    }
    return false;
}

inline std::optional<PredicateOp> predicate_op_from_string(std::string_view text) {
    const std::string name = lower_ascii(std::string(text));
    if (name == "eq" || name == "=" || name == "==") {
        return PredicateOp::Eq;
    }
    if (name == "in") {
        return PredicateOp::In;
    }
    if (name == "lt" || name == "<") {
        return PredicateOp::Lt;
    }
    if (name == "le" || name == "lte" || name == "<=") {
        return PredicateOp::Le;
    }
    if (name == "gt" || name == ">") {
        return PredicateOp::Gt;
    }
    if (name == "ge" || name == "gte" || name == ">=") {
        return PredicateOp::Ge;
    }
    if (name == "ne" || name == "neq" || name == "!=" || name == "<>") {
        return PredicateOp::Ne;
    }
    return std::nullopt;
}

inline std::string trim_text(std::string_view text) {
    std::size_t begin = 0;
    std::size_t end = text.size();
    while (begin < end && std::isspace(static_cast<unsigned char>(text[begin]))) {
        ++begin;
    }
    while (end > begin && std::isspace(static_cast<unsigned char>(text[end - 1]))) {
        --end;
    }
    return std::string(text.substr(begin, end - begin));
}

inline ConditionPtr parse_condition(const JsonValue& json, std::string& error,
                                    std::size_t depth) {
    if (depth > 128) {
        error = "condition nesting is too deep";
        return nullptr;
    }
    if (const auto* boolean = std::get_if<bool>(&json.data)) {
        return Condition::truth(*boolean);
    }
    if (const auto* text = std::get_if<std::string>(&json.data)) {
        const std::string lowered = lower_ascii(*text);
        if (lowered == "true") {
            return Condition::truth(true);
        }
        if (lowered == "false") {
            return Condition::truth(false);
        }
        error = "condition string must be true or false";
        return nullptr;
    }
    if (const auto* array = json.array()) {
        std::vector<ConditionPtr> children;
        for (const auto& item : *array) {
            auto child = parse_condition(item, error, depth + 1);
            if (!child) {
                return nullptr;
            }
            children.push_back(std::move(child));
        }
        return Condition::all(std::move(children));
    }
    const auto* object = json.object();
    if (object == nullptr) {
        error = "condition must be an object, array, or boolean";
        return nullptr;
    }

    if (const JsonValue* nested = json_field(*object, "and")) {
        const auto* array = nested->array();
        if (array == nullptr) {
            error = "and expects an array";
            return nullptr;
        }
        std::vector<ConditionPtr> children;
        for (const auto& item : *array) {
            auto child = parse_condition(item, error, depth + 1);
            if (!child) {
                return nullptr;
            }
            children.push_back(std::move(child));
        }
        return Condition::all(std::move(children));
    }
    if (const JsonValue* nested = json_field(*object, "or")) {
        const auto* array = nested->array();
        if (array == nullptr) {
            error = "or expects an array";
            return nullptr;
        }
        std::vector<ConditionPtr> children;
        for (const auto& item : *array) {
            auto child = parse_condition(item, error, depth + 1);
            if (!child) {
                return nullptr;
            }
            children.push_back(std::move(child));
        }
        return Condition::any(std::move(children));
    }
    if (const JsonValue* nested = json_field(*object, "not")) {
        auto child = parse_condition(*nested, error, depth + 1);
        if (!child) {
            return nullptr;
        }
        return Condition::negate(std::move(child));
    }
    if (const JsonValue* column = json_field(*object, "column")) {
        const auto* column_name = json_string(column);
        if (column_name == nullptr) {
            error = "predicate column must be a string";
            return nullptr;
        }
        std::string op_name = "eq";
        if (const JsonValue* op = json_field(*object, "op")) {
            const auto* text = json_string(op);
            if (text == nullptr) {
                error = "predicate op must be a string";
                return nullptr;
            }
            op_name = *text;
        }
        const auto op = predicate_op_from_string(op_name);
        if (!op.has_value()) {
            error = "unsupported predicate op: " + op_name;
            return nullptr;
        }
        std::vector<Value> values;
        if (const JsonValue* list = json_field(*object, {"values", "value"})) {
            if (const auto* array = list->array()) {
                for (const auto& item : *array) {
                    Value value;
                    if (!json_scalar_to_value(item, value)) {
                        error = "predicate values must be scalars";
                        return nullptr;
                    }
                    values.push_back(std::move(value));
                }
            } else {
                Value value;
                if (!json_scalar_to_value(*list, value)) {
                    error = "predicate value must be a scalar";
                    return nullptr;
                }
                values.push_back(std::move(value));
            }
        }
        return Condition::compare(*column_name, *op, std::move(values));
    }

    const std::pair<std::string_view, PredicateOp> comparisons[] = {
        {"eq", PredicateOp::Eq}, {"ne", PredicateOp::Ne}, {"lt", PredicateOp::Lt},
        {"le", PredicateOp::Le}, {"gt", PredicateOp::Gt}, {"ge", PredicateOp::Ge},
        {"in", PredicateOp::In},
    };
    for (const auto& [name, op] : comparisons) {
        const JsonValue* nested = json_field(*object, name);
        if (nested == nullptr) {
            continue;
        }
        const auto* array = nested->array();
        if (array == nullptr || array->size() != 2) {
            error = std::string(name) + " expects [column, value]";
            return nullptr;
        }
        const auto* column_name = json_string(&(*array)[0]);
        if (column_name == nullptr) {
            error = std::string(name) + " column must be a string";
            return nullptr;
        }
        std::vector<Value> values;
        if (op == PredicateOp::In) {
            const auto* list = (*array)[1].array();
            if (list == nullptr) {
                error = "in expects an array of values";
                return nullptr;
            }
            for (const auto& item : *list) {
                Value value;
                if (!json_scalar_to_value(item, value)) {
                    error = "in values must be scalars";
                    return nullptr;
                }
                values.push_back(std::move(value));
            }
        } else {
            Value value;
            if (!json_scalar_to_value((*array)[1], value)) {
                error = std::string(name) + " value must be a scalar";
                return nullptr;
            }
            values.push_back(std::move(value));
        }
        return Condition::compare(*column_name, op, std::move(values));
    }

    std::vector<ConditionPtr> implicit_children;
    for (const auto& [column, value] : *object) {
        if (const auto* list = value.array()) {
            std::vector<Value> values;
            for (const auto& item : *list) {
                Value scalar;
                if (!json_scalar_to_value(item, scalar)) {
                    error = "implicit predicate values must be scalars";
                    return nullptr;
                }
                values.push_back(std::move(scalar));
            }
            implicit_children.push_back(Condition::in(column, std::move(values)));
            continue;
        }
        Value scalar;
        if (!json_scalar_to_value(value, scalar)) {
            error = "implicit predicate value must be a scalar or array";
            return nullptr;
        }
        implicit_children.push_back(Condition::eq(column, std::move(scalar)));
    }
    if (!implicit_children.empty()) {
        return Condition::all(std::move(implicit_children));
    }

    error = "unknown condition operator";
    return nullptr;
}

inline std::optional<ScanType> scan_type_from_string(std::string_view text) {
    const std::string name = lower_ascii(trim_text(text));
    if (name == "single_partition" || name == "single" || name == "partition") {
        return ScanType::SinglePartition;
    }
    if (name == "multi_partition" || name == "multi" || name == "partitions") {
        return ScanType::MultiPartition;
    }
    if (name == "full_table" || name == "full" || name == "table" ||
        name == "table_scan" || name == "scan") {
        return ScanType::FullTable;
    }
    if (name == "secondary_index" || name == "index" || name == "secondary") {
        return ScanType::SecondaryIndex;
    }
    return std::nullopt;
}

inline std::optional<QueryOperation> operation_from_string(std::string_view text) {
    const std::string name = lower_ascii(trim_text(text));
    if (name == "select" || name == "read" || name == "scan") {
        return QueryOperation::Select;
    }
    if (name == "aggregate" || name == "aggregation" || name == "agg" || name == "group") {
        return QueryOperation::Aggregate;
    }
    return std::nullopt;
}

inline bool json_string_array(const JsonValue* value, std::vector<std::string>& output,
                              std::string& error, std::string_view field_name) {
    if (value == nullptr) {
        return true;
    }
    const auto* array = value->array();
    if (array == nullptr) {
        error = std::string(field_name) + " must be an array of strings";
        return false;
    }
    for (const auto& item : *array) {
        const auto* text = json_string(&item);
        if (text == nullptr) {
            error = std::string(field_name) + " must contain only strings";
            return false;
        }
        output.push_back(*text);
    }
    return true;
}

inline bool json_value_array(const JsonValue* value, std::vector<Value>& output,
                             std::string& error, std::string_view field_name) {
    if (value == nullptr) {
        return true;
    }
    const auto* array = value->array();
    if (array == nullptr) {
        error = std::string(field_name) + " must be an array";
        return false;
    }
    for (const auto& item : *array) {
        Value scalar;
        if (!json_scalar_to_value(item, scalar)) {
            error = std::string(field_name) + " must contain only scalars";
            return false;
        }
        output.push_back(std::move(scalar));
    }
    return true;
}

inline AggregateSpec parse_aggregate_string(std::string_view text) {
    AggregateSpec spec;
    const std::string trimmed = trim_text(text);
    const auto open = trimmed.find('(');
    if (open == std::string::npos) {
        spec.function = lower_ascii(trimmed);
        spec.column = "*";
        return spec;
    }
    const auto close = trimmed.rfind(')');
    spec.function = lower_ascii(trim_text(std::string_view(trimmed).substr(0, open)));
    std::string argument;
    if (close != std::string::npos && close > open) {
        argument = trim_text(std::string_view(trimmed).substr(open + 1, close - open - 1));
    }
    if (lower_ascii(argument).rfind("distinct ", 0) == 0) {
        spec.distinct = true;
        argument = trim_text(std::string_view(argument).substr(9));
    }
    spec.column = argument.empty() ? "*" : argument;
    return spec;
}

inline std::string escape_json(std::string_view text) {
    std::string output;
    output.reserve(text.size() + 2);
    for (const char raw : text) {
        const unsigned char ch = static_cast<unsigned char>(raw);
        switch (ch) {
        case '"':
            output += "\\\"";
            break;
        case '\\':
            output += "\\\\";
            break;
        case '\b':
            output += "\\b";
            break;
        case '\f':
            output += "\\f";
            break;
        case '\n':
            output += "\\n";
            break;
        case '\r':
            output += "\\r";
            break;
        case '\t':
            output += "\\t";
            break;
        default:
            if (ch < 0x20) {
                std::ostringstream stream;
                stream << "\\u" << std::hex << std::setw(4) << std::setfill('0')
                       << static_cast<unsigned int>(ch);
                output += stream.str();
            } else {
                output.push_back(raw);
            }
        }
    }
    return output;
}

inline std::string value_to_json(const Value& value) {
    return std::visit(
        [](const auto& item) -> std::string {
            using T = std::decay_t<decltype(item)>;
            if constexpr (std::is_same_v<T, std::monostate>) {
                return "null";
            } else if constexpr (std::is_same_v<T, std::string>) {
                return "\"" + escape_json(item) + "\"";
            } else if constexpr (std::is_same_v<T, std::int64_t>) {
                return std::to_string(item);
            } else if constexpr (std::is_same_v<T, double>) {
                return std::isfinite(item) ? value_double_text(item) : "null";
            } else {
                return item ? "true" : "false";
            }
        },
        value.storage());
}

inline std::string condition_to_json(const ConditionPtr& condition) {
    if (!condition) {
        return "true";
    }
    switch (condition->kind) {
    case Condition::Kind::True:
        return "true";
    case Condition::Kind::False:
        return "false";
    case Condition::Kind::Compare: {
        std::string name;
        switch (condition->predicate.op) {
        case PredicateOp::Eq:
            name = "eq";
            break;
        case PredicateOp::In:
            name = "in";
            break;
        case PredicateOp::Lt:
            name = "lt";
            break;
        case PredicateOp::Le:
            name = "le";
            break;
        case PredicateOp::Gt:
            name = "gt";
            break;
        case PredicateOp::Ge:
            name = "ge";
            break;
        case PredicateOp::Ne:
            name = "ne";
            break;
        }
        std::string values;
        if (condition->predicate.op == PredicateOp::In) {
            values = "[";
            for (std::size_t index = 0; index < condition->predicate.values.size(); ++index) {
                if (index != 0) {
                    values += ",";
                }
                values += value_to_json(condition->predicate.values[index]);
            }
            values += "]";
        } else {
            values = condition->predicate.values.empty()
                         ? "null"
                         : value_to_json(condition->predicate.values.front());
        }
        return "{\"" + name + "\":[\"" + escape_json(condition->predicate.column) + "\"," +
               values + "]}";
    }
    case Condition::Kind::And:
    case Condition::Kind::Or: {
        const bool is_and = condition->kind == Condition::Kind::And;
        std::string output = is_and ? "{\"and\":[" : "{\"or\":[";
        for (std::size_t index = 0; index < condition->children.size(); ++index) {
            if (index != 0) {
                output += ",";
            }
            output += condition_to_json(condition->children[index]);
        }
        output += "]}";
        return output;
    }
    case Condition::Kind::Not:
        return "{\"not\":" +
               (condition->children.empty() ? std::string("false")
                                            : condition_to_json(condition->children.front())) +
               "}";
    }
    return "false";
}

inline const char* scan_type_name(ScanType scan) {
    switch (scan) {
    case ScanType::SinglePartition:
        return "single_partition";
    case ScanType::MultiPartition:
        return "multi_partition";
    case ScanType::FullTable:
        return "full_table";
    case ScanType::SecondaryIndex:
        return "secondary_index";
    }
    return "single_partition";
}

}  // namespace detail

std::optional<QueryPlan> parse_plan_json(std::string_view json, std::string* error) {
    detail::JsonValue root;
    detail::JsonParser parser(json);
    if (!parser.parse(root)) {
        if (error != nullptr) {
            *error = parser.error();
        }
        return std::nullopt;
    }
    const auto* object = root.object();
    if (object == nullptr) {
        if (error != nullptr) {
            *error = "plan must be a JSON object";
        }
        return std::nullopt;
    }

    std::string local_error;
    QueryPlan plan;

    if (const auto* text = detail::json_string(detail::json_field(*object, "table"))) {
        plan.table = *text;
    }
    if (const auto* text = detail::json_string(
            detail::json_field(*object, {"operation", "op", "type"}))) {
        const auto operation = detail::operation_from_string(*text);
        if (!operation.has_value()) {
            local_error = "unknown operation: " + *text;
        } else {
            plan.operation = *operation;
        }
    }
    if (!local_error.empty()) {
        if (error != nullptr) {
            *error = local_error;
        }
        return std::nullopt;
    }
    if (const auto* text = detail::json_string(
            detail::json_field(*object, {"scan", "scan_type", "scanType"}))) {
        const auto scan = detail::scan_type_from_string(*text);
        if (!scan.has_value()) {
            if (error != nullptr) {
                *error = "unknown scan type: " + *text;
            }
            return std::nullopt;
        }
        plan.scan_type = *scan;
    }
    if (const auto* text = detail::json_string(detail::json_field(
            *object, {"partition_key", "partition_key_column", "tenant_key",
                      "partitionKey", "partitionKeyColumn"}))) {
        plan.partition_key_column = *text;
    }
    if (const auto* value = detail::json_field(*object, {"partition", "scan_partition"})) {
        Value scalar;
        if (!detail::json_scalar_to_value(*value, scalar)) {
            if (error != nullptr) {
                *error = "partition must be a scalar";
            }
            return std::nullopt;
        }
        plan.scan_partition = std::move(scalar);
    }
    if (!detail::json_value_array(
            detail::json_field(*object, {"partitions", "requested_partitions",
                                         "scan_partitions", "requestedPartitions"}),
            plan.requested_partitions, local_error, "partitions")) {
        if (error != nullptr) {
            *error = local_error;
        }
        return std::nullopt;
    }
    if (const auto* text = detail::json_string(
            detail::json_field(*object, {"index", "index_name", "indexName"}))) {
        plan.index_name = *text;
    }
    if (const auto* value =
            detail::json_field(*object, {"where", "condition", "filter", "predicate"})) {
        plan.where = detail::parse_condition(*value, local_error, 0);
        if (!plan.where) {
            if (error != nullptr) {
                *error = local_error;
            }
            return std::nullopt;
        }
    }
    if (const auto* value = detail::json_field(*object, {"predicates", "filters"})) {
        const auto* array = value->array();
        if (array == nullptr) {
            if (error != nullptr) {
                *error = "predicates must be an array";
            }
            return std::nullopt;
        }
        std::vector<ConditionPtr> children;
        for (const auto& item : *array) {
            auto child = detail::parse_condition(item, local_error, 0);
            if (!child) {
                if (error != nullptr) {
                    *error = local_error;
                }
                return std::nullopt;
            }
            children.push_back(std::move(child));
        }
        plan.where = detail::and_conditions(plan.where, Condition::all(std::move(children)));
    }
    if (!detail::json_string_array(detail::json_field(*object, {"group_by", "groupby", "groupBy"}),
                                   plan.group_by, local_error, "group_by")) {
        if (error != nullptr) {
            *error = local_error;
        }
        return std::nullopt;
    }
    if (const auto* value = detail::json_field(*object, {"aggregates", "aggregations"})) {
        const auto* array = value->array();
        if (array == nullptr) {
            if (error != nullptr) {
                *error = "aggregates must be an array";
            }
            return std::nullopt;
        }
        for (const auto& item : *array) {
            if (const auto* text = detail::json_string(&item)) {
                plan.aggregates.push_back(detail::parse_aggregate_string(*text));
                continue;
            }
            const auto* aggregate = item.object();
            if (aggregate == nullptr) {
                if (error != nullptr) {
                    *error = "aggregate must be a string or object";
                }
                return std::nullopt;
            }
            AggregateSpec spec;
            if (const auto* function = detail::json_string(
                    detail::json_field(*aggregate, {"function", "fn"}))) {
                spec.function = *function;
            }
            if (const auto* column =
                    detail::json_string(detail::json_field(*aggregate, "column"))) {
                spec.column = *column;
            }
            if (const auto* alias =
                    detail::json_string(detail::json_field(*aggregate, "alias"))) {
                spec.alias = *alias;
            }
            if (const auto* distinct = detail::json_bool(
                    detail::json_field(*aggregate, "distinct"))) {
                spec.distinct = *distinct;
            }
            if (spec.function.empty()) {
                if (error != nullptr) {
                    *error = "aggregate function is missing";
                }
                return std::nullopt;
            }
            if (spec.column.empty()) {
                spec.column = "*";
            }
            plan.aggregates.push_back(std::move(spec));
        }
    }
    if (const auto* value = detail::json_bool(
            detail::json_field(*object, {"allow_filtering", "allowFiltering"}))) {
        plan.allow_filtering = *value;
    }
    if (const auto* number = detail::json_number(detail::json_field(*object, "limit"))) {
        if (*number < 0 || !std::isfinite(*number) ||
            *number > static_cast<double>(std::numeric_limits<std::uint64_t>::max())) {
            if (error != nullptr) {
                *error = "limit must be a non-negative integer";
            }
            return std::nullopt;
        }
        plan.limit = static_cast<std::uint64_t>(*number);
    }
    if (const auto* value = detail::json_bool(detail::json_field(*object, "distinct"))) {
        plan.distinct = *value;
    }
    return plan;
}

std::optional<QueryPlan> parse_query_plan(std::string_view json, std::string* error) {
    return parse_plan_json(json, error);
}

std::string plan_to_json(const QueryPlan& plan) {
    std::ostringstream output;
    output << "{";
    output << "\"table\":\"" << detail::escape_json(plan.table) << "\",";
    output << "\"operation\":\""
           << (plan.operation == QueryOperation::Aggregate ? "aggregate" : "select") << "\",";
    output << "\"scan\":\"" << detail::scan_type_name(plan.scan_type) << "\",";
    output << "\"partition_key\":\"" << detail::escape_json(plan.partition_key_column) << "\"";
    if (plan.scan_partition.has_value()) {
        output << ",\"partition\":" << detail::value_to_json(*plan.scan_partition);
    }
    if (!plan.requested_partitions.empty()) {
        output << ",\"partitions\":[";
        for (std::size_t index = 0; index < plan.requested_partitions.size(); ++index) {
            if (index != 0) {
                output << ",";
            }
            output << detail::value_to_json(plan.requested_partitions[index]);
        }
        output << "]";
    }
    if (plan.index_name.has_value()) {
        output << ",\"index\":\"" << detail::escape_json(*plan.index_name) << "\"";
    }
    output << ",\"where\":" << detail::condition_to_json(plan.where);
    if (!plan.group_by.empty()) {
        output << ",\"group_by\":[";
        for (std::size_t index = 0; index < plan.group_by.size(); ++index) {
            if (index != 0) {
                output << ",";
            }
            output << "\"" << detail::escape_json(plan.group_by[index]) << "\"";
        }
        output << "]";
    }
    if (!plan.aggregates.empty()) {
        output << ",\"aggregates\":[";
        for (std::size_t index = 0; index < plan.aggregates.size(); ++index) {
            const auto& aggregate = plan.aggregates[index];
            if (index != 0) {
                output << ",";
            }
            output << "{\"function\":\"" << detail::escape_json(aggregate.function)
                   << "\",\"column\":\"" << detail::escape_json(aggregate.column) << "\"";
            if (!aggregate.alias.empty()) {
                output << ",\"alias\":\"" << detail::escape_json(aggregate.alias) << "\"";
            }
            if (aggregate.distinct) {
                output << ",\"distinct\":true";
            }
            output << "}";
        }
        output << "]";
    }
    output << ",\"allow_filtering\":" << (plan.allow_filtering ? "true" : "false");
    output << ",\"limit\":" << plan.limit;
    output << ",\"distinct\":" << (plan.distinct ? "true" : "false");
    output << "}";
    return output.str();
}

}  // namespace netsec
