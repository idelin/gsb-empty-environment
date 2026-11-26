#include "pki/validator.hpp"

#include "pki/names.hpp"

#include <algorithm>
#include <chrono>
#include <limits>
#include <set>
#include <sstream>

namespace pki {

namespace {

constexpr const char* OID_EMAIL_ADDRESS = "1.2.840.113549.1.9.1";

struct PolicyNodeState {
    std::size_t id = 0;
    std::size_t depth = 0;
    std::string valid_policy;
    std::vector<std::string> expected_policy_set;
    std::vector<std::size_t> parent_ids;
};

struct NameConstraintState {
    std::vector<x509::GeneralName> permitted;
    std::vector<x509::GeneralName> excluded;
    std::vector<x509::GeneralNameKind> denied_kinds;
};

bool is_denied(const NameConstraintState& state, x509::GeneralNameKind kind) {
    return std::find(state.denied_kinds.begin(), state.denied_kinds.end(), kind) !=
           state.denied_kinds.end();
}

void deny_kind(NameConstraintState& state, x509::GeneralNameKind kind) {
    if (!is_denied(state, kind)) {
        state.denied_kinds.push_back(kind);
    }
}

void apply_permitted(NameConstraintState& state,
                     const std::vector<x509::GeneralName>& incoming) {
    std::set<x509::GeneralNameKind> kinds;
    for (const x509::GeneralName& rule : incoming) {
        kinds.insert(rule.kind);
    }

    for (x509::GeneralNameKind kind : kinds) {
        if (is_denied(state, kind)) {
            continue;
        }

        std::vector<x509::GeneralName> added;
        for (const x509::GeneralName& rule : incoming) {
            if (rule.kind == kind) {
                added.push_back(rule);
            }
        }

        std::vector<x509::GeneralName> current;
        for (const x509::GeneralName& rule : state.permitted) {
            if (rule.kind == kind) {
                current.push_back(rule);
            }
        }

        state.permitted.erase(
            std::remove_if(state.permitted.begin(), state.permitted.end(),
                           [kind](const x509::GeneralName& rule) { return rule.kind == kind; }),
            state.permitted.end());

        if (current.empty()) {
            state.permitted.insert(state.permitted.end(), added.begin(), added.end());
            continue;
        }

        std::vector<x509::GeneralName> intersection;
        for (const x509::GeneralName& left : current) {
            for (const x509::GeneralName& right : added) {
                std::optional<x509::GeneralName> common =
                    names::intersect_general_constraints(left, right);
                if (common.has_value()) {
                    bool duplicate = false;
                    for (const x509::GeneralName& existing : intersection) {
                        if (existing.kind == common->kind && existing.text == common->text &&
                            existing.ip == common->ip &&
                            existing.directory_name == common->directory_name) {
                            duplicate = true;
                            break;
                        }
                    }
                    if (!duplicate) {
                        intersection.push_back(std::move(*common));
                    }
                }
            }
        }

        if (intersection.empty()) {
            deny_kind(state, kind);
        } else {
            state.permitted.insert(state.permitted.end(), intersection.begin(),
                                  intersection.end());
        }
    }
}

void apply_name_constraints(NameConstraintState& state,
                            const x509::NameConstraints& constraints) {
    apply_permitted(state, constraints.permitted);
    state.excluded.insert(state.excluded.end(), constraints.excluded.begin(),
                          constraints.excluded.end());
}

std::vector<x509::GeneralName> collect_names(const x509::Certificate& certificate) {
    std::vector<x509::GeneralName> result = certificate.subject_alt_names;

    bool has_email_san = false;
    bool has_dns_san = false;
    for (const x509::GeneralName& name : result) {
        has_email_san = has_email_san || name.kind == x509::GeneralNameKind::Rfc822;
        has_dns_san = has_dns_san || name.kind == x509::GeneralNameKind::Dns;
    }

    if (!has_email_san) {
        for (const x509::AttributeTypeAndValue& attribute : certificate.subject.attributes) {
            if (attribute.oid == OID_EMAIL_ADDRESS && !attribute.value.empty()) {
                x509::GeneralName email;
                email.kind = x509::GeneralNameKind::Rfc822;
                email.text = attribute.value;
                result.push_back(std::move(email));
                has_email_san = true;
                break;
            }
        }
    }

    if (!has_dns_san && certificate.find_extension(x509::OID_SUBJECT_ALT_NAME) == nullptr) {
        for (const std::string& common_name : certificate.subject.common_names()) {
            if (names::looks_like_dns_name(common_name)) {
                x509::GeneralName dns;
                dns.kind = x509::GeneralNameKind::Dns;
                dns.text = common_name;
                result.push_back(std::move(dns));
            }
        }
    }

    return result;
}

VerifyResult check_names(const x509::Certificate& certificate,
                         const NameConstraintState& state,
                         std::size_t leaf_index) {
    std::vector<x509::GeneralName> certificate_names = collect_names(certificate);

    if (names::is_excluded(certificate_names, state.excluded)) {
        return VerifyResult::failure(ErrorCode::ExcludedName,
                                     "certificate name matches an excluded subtree", leaf_index);
    }

    for (const x509::GeneralName& name : certificate_names) {
        if (is_denied(state, name.kind)) {
            return VerifyResult::failure(
                ErrorCode::PermittedNameViolation,
                "permitted-subtree intersection leaves no allowed names of this type", leaf_index);
        }
    }

    if (!names::is_permitted(certificate_names, state.permitted)) {
        return VerifyResult::failure(ErrorCode::PermittedNameViolation,
                                     "certificate name is outside permitted subtrees", leaf_index);
    }
    return {};
}

const PolicyNodeState* find_node(const std::vector<PolicyNodeState>& nodes, std::size_t id) {
    for (const PolicyNodeState& node : nodes) {
        if (node.id == id) {
            return &node;
        }
    }
    return nullptr;
}

bool expected_contains(const PolicyNodeState& node, const std::string& policy) {
    return std::find(node.expected_policy_set.begin(), node.expected_policy_set.end(), policy) !=
               node.expected_policy_set.end() ||
           std::find(node.expected_policy_set.begin(), node.expected_policy_set.end(),
                     x509::OID_ANY_POLICY) != node.expected_policy_set.end();
}

void add_unique_parent(std::vector<std::size_t>& parents, std::size_t parent) {
    if (std::find(parents.begin(), parents.end(), parent) == parents.end()) {
        parents.push_back(parent);
    }
}

bool same_policy_children(const PolicyNodeState& left, const PolicyNodeState& right) {
    return left.depth == right.depth && left.valid_policy == right.valid_policy &&
           left.parent_ids == right.parent_ids;
}

void add_policy_child(std::vector<PolicyNodeState>& nodes,
                      std::size_t depth,
                      const std::string& valid_policy,
                      const std::vector<std::size_t>& parent_ids) {
    PolicyNodeState child;
    child.depth = depth;
    child.valid_policy = valid_policy;
    child.expected_policy_set = {valid_policy};
    child.parent_ids = parent_ids;

    for (const PolicyNodeState& existing : nodes) {
        if (same_policy_children(existing, child)) {
            return;
        }
    }
    nodes.push_back(std::move(child));
}

std::vector<std::size_t> nodes_at_depth(const std::vector<PolicyNodeState>& nodes,
                                        std::size_t depth) {
    std::vector<std::size_t> ids;
    for (const PolicyNodeState& node : nodes) {
        if (node.depth == depth) {
            ids.push_back(node.id);
        }
    }
    return ids;
}

bool certificate_asserts_any_policy(const x509::Certificate& certificate) {
    return std::find(certificate.certificate_policies.begin(),
                     certificate.certificate_policies.end(),
                     x509::OID_ANY_POLICY) != certificate.certificate_policies.end();
}

void process_policy_mappings(const x509::Certificate& certificate,
                             std::size_t depth,
                             std::vector<PolicyNodeState>& nodes,
                             std::uint64_t mapping_count) {
    if (mapping_count == 0 || certificate.policy_mappings.empty()) {
        return;
    }

    std::set<std::string> issuer_policies;
    for (const x509::PolicyMapping& mapping : certificate.policy_mappings) {
        issuer_policies.insert(mapping.issuer_domain_policy);
    }

    for (const std::string& issuer_policy : issuer_policies) {
        std::vector<std::string> subject_policies;
        for (const x509::PolicyMapping& mapping : certificate.policy_mappings) {
            if (mapping.issuer_domain_policy == issuer_policy &&
                std::find(subject_policies.begin(), subject_policies.end(),
                          mapping.subject_domain_policy) == subject_policies.end()) {
                subject_policies.push_back(mapping.subject_domain_policy);
            }
        }

        bool found_explicit = false;
        for (PolicyNodeState& node : nodes) {
            if (node.depth == depth && node.valid_policy == issuer_policy) {
                node.expected_policy_set = subject_policies;
                found_explicit = true;
            }
        }

        if (!found_explicit) {
            for (const PolicyNodeState& any_node : nodes) {
                if (any_node.depth == depth &&
                    any_node.valid_policy == x509::OID_ANY_POLICY) {
                    std::vector<std::size_t> parents;
                    for (std::size_t parent_id : any_node.parent_ids) {
                        add_unique_parent(parents, parent_id);
                    }
                    PolicyNodeState mapped;
                    mapped.id = nodes.empty() ? 1 : nodes.back().id + 1;
                    mapped.depth = depth;
                    mapped.valid_policy = issuer_policy;
                    mapped.expected_policy_set = subject_policies;
                    mapped.parent_ids = parents;
                    nodes.push_back(std::move(mapped));
                    break;
                }
            }
        }
    }
}

bool user_policy_accepted(const PolicyNodeState& node,
                          const std::vector<PolicyNodeState>& all_nodes,
                          const std::vector<std::string>& user_policies) {
    if (std::find(user_policies.begin(), user_policies.end(), node.valid_policy) !=
        user_policies.end()) {
        return true;
    }

    std::vector<std::size_t> frontier = node.parent_ids;
    std::set<std::size_t> visited;
    while (!frontier.empty()) {
        const std::size_t id = frontier.back();
        frontier.pop_back();
        if (!visited.insert(id).second) {
            continue;
        }
        const PolicyNodeState* ancestor = find_node(all_nodes, id);
        if (ancestor == nullptr) {
            continue;
        }
        if (std::find(user_policies.begin(), user_policies.end(),
                      ancestor->valid_policy) != user_policies.end()) {
            return true;
        }
        for (std::size_t parent : ancestor->parent_ids) {
            frontier.push_back(parent);
        }
    }
    return false;
}

} // namespace

VerifyResult VerifyResult::failure(ErrorCode code, std::string message,
                                   std::size_t certificate_index) {
    VerifyResult result;
    result.code = code;
    result.message = std::move(message);
    result.certificate_index = certificate_index;
    return result;
}

std::int64_t current_unix_time() {
    return std::chrono::duration_cast<std::chrono::seconds>(
               std::chrono::system_clock::now().time_since_epoch())
        .count();
}

const char* error_code_name(ErrorCode code) {
    switch (code) {
        case ErrorCode::Ok: return "ok";
        case ErrorCode::InvalidInput: return "invalid_input";
        case ErrorCode::InvalidCertificateEncoding: return "invalid_certificate_encoding";
        case ErrorCode::UnknownCriticalExtension: return "unknown_critical_extension";
        case ErrorCode::BadSignature: return "bad_signature";
        case ErrorCode::NotYetValid: return "not_yet_valid";
        case ErrorCode::Expired: return "expired";
        case ErrorCode::NameChaining: return "name_chaining";
        case ErrorCode::NotACa: return "not_a_ca";
        case ErrorCode::KeyUsage: return "key_usage";
        case ErrorCode::PathLengthExceeded: return "path_length_exceeded";
        case ErrorCode::ExcludedName: return "excluded_name";
        case ErrorCode::PermittedNameViolation: return "permitted_name_violation";
        case ErrorCode::UnrecognizedCriticalExtension:
            return "unrecognized_critical_extension";
        case ErrorCode::PolicyMappingDenied: return "policy_mapping_denied";
        case ErrorCode::ExplicitPolicyRequired: return "explicit_policy_required";
        case ErrorCode::NoValidPolicy: return "no_valid_policy";
        case ErrorCode::DuplicateCertificate: return "duplicate_certificate";
        case ErrorCode::MissingTrustAnchor: return "missing_trust_anchor";
    }
    return "unknown";
}

VerifyResult verify_chain_including_anchor(
    const std::vector<x509::Certificate>& leaf_first_including_anchor,
    const VerificationOptions& options) {
    if (leaf_first_including_anchor.empty()) {
        return VerifyResult::failure(ErrorCode::MissingTrustAnchor,
                                     "certificate chain is empty");
    }
    x509::Certificate anchor = leaf_first_including_anchor.back();
    std::vector<x509::Certificate> leaf_first;
    leaf_first.reserve(leaf_first_including_anchor.size() - 1);
    for (std::size_t i = 0; i + 1 < leaf_first_including_anchor.size(); ++i) {
        leaf_first.push_back(leaf_first_including_anchor[i]);
    }
    return verify_path(leaf_first, anchor, options);
}

VerifyResult verify_der_path(const std::vector<der::Bytes>& leaf_first_der,
                             const der::Bytes& trust_anchor_der,
                             const VerificationOptions& options) {
    x509::ParseResult anchor_result = x509::parse_certificate(trust_anchor_der);
    if (!anchor_result.ok) {
        return VerifyResult::failure(ErrorCode::InvalidCertificateEncoding,
                                     "trust anchor: " + anchor_result.error);
    }

    std::vector<x509::Certificate> certificates;
    certificates.reserve(leaf_first_der.size());
    for (std::size_t i = 0; i < leaf_first_der.size(); ++i) {
        x509::ParseResult result = x509::parse_certificate(leaf_first_der[i]);
        if (!result.ok) {
            return VerifyResult::failure(ErrorCode::InvalidCertificateEncoding,
                                         "certificate " + std::to_string(i) + ": " + result.error,
                                         i);
        }
        certificates.push_back(std::move(result.certificate));
    }

    return verify_path(certificates, anchor_result.certificate, options);
}

VerifyResult verify_path(const std::vector<x509::Certificate>& leaf_first,
                         const x509::Certificate& trust_anchor,
                         const VerificationOptions& options) {
    if (options.signature_verifier == nullptr) {
        return VerifyResult::failure(ErrorCode::InvalidInput,
                                     "signature verifier callback is required");
    }
    if (leaf_first.empty()) {
        return VerifyResult::failure(ErrorCode::InvalidInput, "certification path is empty");
    }

    const std::int64_t now =
        options.verification_time >= 0 ? options.verification_time : current_unix_time();

    for (const x509::Certificate& certificate : leaf_first) {
        if (certificate.der.empty()) {
            return VerifyResult::failure(ErrorCode::InvalidCertificateEncoding,
                                         "parsed certificate contains no DER bytes");
        }
    }

    std::vector<const x509::Certificate*> all_for_duplicates;
    all_for_duplicates.push_back(&trust_anchor);
    for (const x509::Certificate& certificate : leaf_first) {
        all_for_duplicates.push_back(&certificate);
    }
    for (std::size_t i = 0; i < all_for_duplicates.size(); ++i) {
        for (std::size_t j = i + 1; j < all_for_duplicates.size(); ++j) {
            if (all_for_duplicates[i]->der == all_for_duplicates[j]->der) {
                return VerifyResult::failure(ErrorCode::DuplicateCertificate,
                                             "certificate appears more than once", j);
            }
        }
    }

    if (!trust_anchor.unknown_critical_extensions.empty()) {
        return VerifyResult::failure(ErrorCode::UnrecognizedCriticalExtension,
                                     "trust anchor has an unknown critical extension");
    }

    NameConstraintState names_state;
    if (trust_anchor.name_constraints.has_value()) {
        apply_name_constraints(names_state, *trust_anchor.name_constraints);
    }

    bool has_path_length = false;
    std::uint64_t path_length = 0;
    if (trust_anchor.basic_constraints.has_value() &&
        trust_anchor.basic_constraints->path_len_constraint.has_value()) {
        has_path_length = true;
        path_length = *trust_anchor.basic_constraints->path_len_constraint;
    }

    const std::size_t n = leaf_first.size();
    const std::uint64_t initial_counter = static_cast<std::uint64_t>(n) + 1;
    std::uint64_t explicit_policy =
        options.initial_explicit_policy ? 0 : initial_counter;
    std::uint64_t policy_mapping =
        options.initial_policy_mapping_inhibit ? 0 : initial_counter;
    std::uint64_t inhibit_any_policy =
        options.initial_any_policy_inhibit ? 0 : initial_counter;

    std::vector<PolicyNodeState> policy_nodes;
    policy_nodes.push_back(PolicyNodeState{0, 0, x509::OID_ANY_POLICY,
                                           {x509::OID_ANY_POLICY}, {}});
    bool policy_tree_null = false;

    for (std::size_t ordered_index = 0; ordered_index < n; ++ordered_index) {
        const x509::Certificate& certificate = leaf_first[n - 1 - ordered_index];
        const std::size_t leaf_index = n - 1 - ordered_index;
        const bool final_certificate = ordered_index + 1 == n;
        const x509::Certificate& issuer =
            ordered_index == 0 ? trust_anchor : leaf_first[n - ordered_index];

        if (!certificate.unknown_critical_extensions.empty()) {
            return VerifyResult::failure(
                ErrorCode::UnrecognizedCriticalExtension,
                "unrecognized critical extension: " +
                    certificate.unknown_critical_extensions.front(),
                leaf_index);
        }

        if (certificate.issuer_raw != issuer.subject_raw) {
            return VerifyResult::failure(ErrorCode::NameChaining,
                                        "certificate issuer does not match issuer subject",
                                        leaf_index);
        }

        SignatureInput signature_input{issuer, certificate,
                                       der::ByteView(certificate.tbs_der.data(),
                                                     certificate.tbs_der.size()),
                                       der::ByteView(certificate.signature.data(),
                                                     certificate.signature.size()),
                                       certificate.signature_algorithm.oid};
        if (!options.signature_verifier->verify(signature_input)) {
            return VerifyResult::failure(ErrorCode::BadSignature,
                                         "signature verification failed", leaf_index);
        }

        if (options.check_validity) {
            if (now < certificate.not_before.unix_seconds) {
                return VerifyResult::failure(ErrorCode::NotYetValid,
                                             "certificate is not yet valid", leaf_index);
            }
            if (now > certificate.not_after.unix_seconds) {
                return VerifyResult::failure(ErrorCode::Expired,
                                             "certificate has expired", leaf_index);
            }
        }

        if (!final_certificate) {
            if (options.check_basic_constraints &&
                (!certificate.basic_constraints.has_value() ||
                 !certificate.basic_constraints->ca)) {
                return VerifyResult::failure(ErrorCode::NotACa,
                                             "issuer certificate does not assert cA=TRUE",
                                             leaf_index);
            }
            if (options.check_key_usage && certificate.key_usage.has_value() &&
                !certificate.key_usage->key_cert_sign) {
                return VerifyResult::failure(ErrorCode::KeyUsage,
                                             "CA certificate lacks keyCertSign", leaf_index);
            }

            if (!certificate.is_self_issued()) {
                if (has_path_length) {
                    if (path_length == 0) {
                        return VerifyResult::failure(
                            ErrorCode::PathLengthExceeded,
                            "pathLenConstraint prohibits another non-self-issued CA", leaf_index);
                    }
                    --path_length;
                }
            }
            if (certificate.basic_constraints.has_value() &&
                certificate.basic_constraints->path_len_constraint.has_value()) {
                const std::uint64_t constraint =
                    *certificate.basic_constraints->path_len_constraint;
                if (!has_path_length || constraint < path_length) {
                    has_path_length = true;
                    path_length = constraint;
                }
            }
        }

        const bool skip_name_check =
            certificate.is_self_issued() && !final_certificate;
        if (options.check_names && !skip_name_check) {
            VerifyResult name_result = check_names(certificate, names_state, leaf_index);
            if (!name_result.ok()) {
                return name_result;
            }
        }

        const std::size_t depth = ordered_index + 1;
        if (!policy_tree_null) {
            std::vector<std::size_t> parent_ids = nodes_at_depth(policy_nodes, depth - 1);
            std::vector<PolicyNodeState> children;

            for (const std::string& policy : certificate.certificate_policies) {
                if (policy == x509::OID_ANY_POLICY) {
                    continue;
                }
                std::vector<std::size_t> matching_parents;
                for (std::size_t parent_id : parent_ids) {
                    const PolicyNodeState* parent = find_node(policy_nodes, parent_id);
                    if (parent != nullptr && expected_contains(*parent, policy)) {
                        matching_parents.push_back(parent_id);
                    }
                }
                if (!matching_parents.empty()) {
                    add_policy_child(children, depth, policy, matching_parents);
                }
            }

            const bool any_allowed =
                inhibit_any_policy > 0 ||
                (!final_certificate && certificate.is_self_issued());
            if (certificate_asserts_any_policy(certificate) && any_allowed) {
                for (std::size_t parent_id : parent_ids) {
                    const PolicyNodeState* parent = find_node(policy_nodes, parent_id);
                    if (parent == nullptr) {
                        continue;
                    }
                    for (const std::string& expected : parent->expected_policy_set) {
                        std::vector<std::size_t> one_parent{parent_id};
                        add_policy_child(children, depth, expected, one_parent);
                    }
                }
            }

    if (children.empty()) {
                policy_tree_null = true;
            } else {
                for (PolicyNodeState& child : children) {
                    child.id = policy_nodes.size();
                    policy_nodes.push_back(std::move(child));
                }
                process_policy_mappings(certificate, depth, policy_nodes, policy_mapping);
            }
        }

        if (!certificate.is_self_issued()) {
            if (explicit_policy > 0) {
                --explicit_policy;
            }
            if (policy_mapping > 0) {
                --policy_mapping;
            }
            if (inhibit_any_policy > 0) {
                --inhibit_any_policy;
            }
        }

        if (certificate.policy_constraints.has_value()) {
            const auto& constraints = *certificate.policy_constraints;
            if (constraints.require_explicit_policy.has_value()) {
                explicit_policy =
                    std::min(explicit_policy, *constraints.require_explicit_policy);
            }
            if (constraints.inhibit_policy_mapping.has_value()) {
                policy_mapping =
                    std::min(policy_mapping, *constraints.inhibit_policy_mapping);
            }
        }
        if (certificate.inhibit_any_policy.has_value()) {
            inhibit_any_policy =
                std::min(inhibit_any_policy, *certificate.inhibit_any_policy);
        }

        if (explicit_policy == 0) {
            const std::vector<std::size_t> current_nodes =
                nodes_at_depth(policy_nodes, depth);
            if (policy_tree_null || current_nodes.empty()) {
                return VerifyResult::failure(
                    ErrorCode::ExplicitPolicyRequired,
                    "explicit certificate policy is required but no valid policy remains",
                    leaf_index);
            }
        }

        if (!final_certificate && certificate.name_constraints.has_value()) {
            apply_name_constraints(names_state, *certificate.name_constraints);
        }
    }

    VerifyResult result;
    result.code = ErrorCode::Ok;
    for (const PolicyNodeState& node : policy_nodes) {
        PolicyNode public_node;
        public_node.depth = node.depth;
        public_node.valid_policy = node.valid_policy;
        public_node.expected_policy_set = node.expected_policy_set;
        public_node.parent_indices = node.parent_ids;
        result.policy_tree.push_back(std::move(public_node));

        if (node.depth == n) {
            result.valid_policies.push_back(node.valid_policy);
        }
    }

    if (!options.user_initial_policies.empty()) {
        if (std::find(options.user_initial_policies.begin(),
                      options.user_initial_policies.end(),
                      x509::OID_ANY_POLICY) != options.user_initial_policies.end()) {
            return result;
        }

        bool accepted = false;
        for (const PolicyNodeState& node : policy_nodes) {
            if (node.depth != n) {
                continue;
            }
            if (user_policy_accepted(node, policy_nodes, options.user_initial_policies)) {
                accepted = true;
                break;
            }
        }
        if (!accepted) {
            return VerifyResult::failure(ErrorCode::NoValidPolicy,
                                         "no policy intersects the user initial policy set");
        }
    }

    return result;
}

} // namespace pki
