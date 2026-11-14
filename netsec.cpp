#include "netsec.hpp"

#include <algorithm>
#include <filesystem>
#include <map>
#include <mutex>
#include <optional>
#include <string>
#include <system_error>
#include <utility>
#include <vector>

#include <sys/stat.h>
#include <unistd.h>

namespace netsec {
namespace {

bool pattern_matches(std::string_view value, std::string_view pattern) {
  if (!pattern.empty() && pattern.back() == '*') {
    pattern.remove_suffix(1);
    return value.size() >= pattern.size() &&
           value.substr(0, pattern.size()) == pattern;
  }
  return value == pattern;
}

bool is_dangerous_environment(std::string_view name) {
  // These variables influence the loader, a shell, or a language runtime
  // before the new domain's policy has had a chance to constrain the process.
  static constexpr std::string_view kExact[] = {
      "BASH_ENV",   "ENV",           "GCONV_PATH",      "GLIBC_TUNABLES",
      "LD_AUDIT",   "LD_DEBUG",      "LD_LIBRARY_PATH", "LD_PRELOAD",
      "LD_PROFILE", "MALLOC_CHECK_", "MALLOC_CONF",     "NODE_OPTIONS",
      "PERL5LIB",   "PERLLIB",       "PYTHONHOME",      "PYTHONPATH",
      "RUBYLIB",    "ZDOTDIR",
  };
  for (std::string_view candidate : kExact) {
    if (name == candidate) {
      return true;
    }
  }
  return name.starts_with("DYLD_");
}

struct DescriptorDecision {
  bool permitted = true;
  FileAccess access = FileAccess::None;
};

DescriptorDecision resolve_descriptor(const DomainPolicy &domain,
                                      const InheritedDescriptor &descriptor) {
  int best_score = -1;
  bool have_match = false;
  bool best_deny = false;
  FileAccess best_access = FileAccess::None;

  for (const InheritedDescriptorRule &rule : domain.descriptor_rules) {
    bool matches = true;
    int score = 0;

    if (rule.fd) {
      if (*rule.fd != descriptor.fd) {
        matches = false;
      } else {
        score += 8;
      }
    }
    if (!rule.path.empty()) {
      if (rule.path != descriptor.path) {
        matches = false;
      } else {
        score += 4;
      }
    }
    if (!rule.label.empty()) {
      if (rule.label != descriptor.label) {
        matches = false;
      } else {
        score += 2;
      }
    }
    if (!matches) {
      continue;
    }

    if (rule.fd || !rule.path.empty() || !rule.label.empty()) {
      ++score;
    } else {
      score = 1;
    }

    if (!have_match || score > best_score) {
      have_match = true;
      best_score = score;
      best_deny = rule.deny;
      best_access = rule.access;
    } else if (score == best_score) {
      // Equally specific rules compose conservatively: a deny wins, and
      // otherwise only the access common to all matching rules survives.
      best_deny = best_deny || rule.deny;
      best_access &= rule.access;
    }
  }

  if (!have_match) {
    return {true, domain.default_inherited_descriptor_access};
  }
  if (best_deny) {
    return {false, FileAccess::None};
  }
  return {true, best_access};
}

std::optional<DenyCode> widening_code(const DomainPolicy &source,
                                      const DomainPolicy &target) {
  // File rules are compared per label.  Repeating a label in one domain
  // grants the union, so the comparison uses the same union semantics.
  std::map<std::string, FileAccess> target_files;
  for (const FileRule &rule : target.file_rules) {
    target_files[rule.label] |= rule.access;
  }
  for (const auto &[label, access] : target_files) {
    if (access == FileAccess::None) {
      continue;
    }
    const std::optional<FileAccess> source_access = source.file_access(label);
    if (!source_access || !contains(*source_access, access)) {
      return DenyCode::TargetWidensFileRules;
    }
  }

  if (!contains(source.network_access, target.network_access)) {
    return DenyCode::TargetWidensNetworkRules;
  }

  for (const std::string &capability : target.capabilities) {
    if (!source.allows_capability(capability)) {
      return DenyCode::TargetWidensCapabilities;
    }
  }

  return std::nullopt;
}

std::string quoted(const std::string &value) {
  std::string result;
  result.reserve(value.size() + 2);
  result.push_back('\'');
  result.append(value);
  result.push_back('\'');
  return result;
}

bool in_supplementary_group(gid_t group) {
  if (group == ::getegid()) {
    return true;
  }
  const int count = ::getgroups(0, nullptr);
  if (count <= 0) {
    return false;
  }
  std::vector<gid_t> groups(static_cast<std::size_t>(count));
  if (::getgroups(count, groups.data()) != count) {
    return false;
  }
  return std::find(groups.begin(), groups.end(), group) != groups.end();
}

bool mode_writable_for_principal(const struct stat &metadata) {
  const uid_t effective_uid = ::geteuid();
  if ((metadata.st_mode & S_IWOTH) != 0) {
    return true;
  }
  if ((metadata.st_mode & S_IWUSR) != 0 && metadata.st_uid == effective_uid) {
    return true;
  }
  return (metadata.st_mode & S_IWGRP) != 0 &&
         in_supplementary_group(metadata.st_gid);
}

bool writable_for_principal(const char *path, const struct stat &metadata,
                            bool check_access) {
  if (mode_writable_for_principal(metadata)) {
    return true;
  }
  return check_access && ::access(path, W_OK) == 0;
}

} // namespace

std::optional<FileAccess>
DomainPolicy::file_access(std::string_view label) const {
  std::optional<FileAccess> result;
  for (const FileRule &rule : file_rules) {
    if (rule.label == label) {
      if (!result) {
        result = FileAccess::None;
      }
      *result |= rule.access;
    }
  }
  return result;
}

bool DomainPolicy::allows_file(std::string_view label,
                               FileAccess access) const {
  const std::optional<FileAccess> allowed = file_access(label);
  return allowed && contains(*allowed, access);
}

bool DomainPolicy::allows_capability(std::string_view capability) const {
  return capabilities.find(std::string(capability)) != capabilities.end();
}

DomainPolicy &DomainPolicy::add_file_rule(std::string label,
                                          FileAccess access) {
  for (FileRule &rule : file_rules) {
    if (rule.label == label) {
      rule.access |= access;
      return *this;
    }
  }
  file_rules.push_back({std::move(label), access});
  return *this;
}

std::string_view to_string(DenyCode code) noexcept {
  switch (code) {
  case DenyCode::None:
    return "none";
  case DenyCode::SourceDomainUnknown:
    return "source domain is not declared";
  case DenyCode::TargetDomainUnknown:
    return "target domain is not declared";
  case DenyCode::EmptyExecutablePath:
    return "executable path is empty";
  case DenyCode::EmptyFileLabel:
    return "executable file label is empty";
  case DenyCode::ExecutableMissing:
    return "executable does not exist";
  case DenyCode::ExecutableNotRegular:
    return "executable is not a regular file";
  case DenyCode::InvalidFileMetadata:
    return "executable metadata is invalid";
  case DenyCode::SymlinkEntryPoint:
    return "symlink cannot be a transition entry point";
  case DenyCode::WritableEntryPoint:
    return "writable path cannot be a transition entry point";
  case DenyCode::MultipleHardLinks:
    return "multiply linked file cannot be a transition entry point";
  case DenyCode::PermissiveFileLabel:
    return "permissive file label cannot enter a transition";
  case DenyCode::PermissiveTargetDomain:
    return "permissive target domain is not a legal transition destination";
  case DenyCode::TransitionRuleMissing:
    return "no transition rule declares this source, label, and target";
  case DenyCode::TransitionRuleDisabled:
    return "transition rule is disabled";
  case DenyCode::TargetWidensFileRules:
    return "target domain widens file rules";
  case DenyCode::TargetWidensNetworkRules:
    return "target domain widens network rules";
  case DenyCode::TargetWidensCapabilities:
    return "target domain widens capability rules";
  case DenyCode::CurrentCapabilitiesExceedSource:
    return "current capabilities exceed the source domain";
  case DenyCode::DescriptorNotPermitted:
    return "inherited descriptor is not permitted after transition";
  case DenyCode::EnvironmentNotPermitted:
    return "inherited environment is not permitted after transition";
  case DenyCode::RequiredEnvironmentMissing:
    return "required environment is missing after transition";
  case DenyCode::NamespaceNotPermitted:
    return "inherited namespace is not permitted after transition";
  case DenyCode::RequiredNamespaceMissing:
    return "required namespace is missing after transition";
  }
  return "unknown denial";
}

TransitionPolicy::TransitionPolicy() {
  // These labels conventionally mean that no confinement applies.  They are
  // data, not special cases, and can be removed with clear_permissive_labels()
  // or unmark_permissive_label() when importing a policy with different names.
  permissive_labels_ = {"*", "any", "permissive", "unconfined"};
}

TransitionPolicy &TransitionPolicy::add_domain(DomainPolicy domain) {
  std::unique_lock lock(mutex_);
  domains_[domain.name] = std::move(domain);
  ++generation_;
  return *this;
}

TransitionPolicy &TransitionPolicy::upsert_domain(DomainPolicy domain) {
  return add_domain(std::move(domain));
}

TransitionPolicy &TransitionPolicy::update_domain(DomainPolicy domain) {
  return add_domain(std::move(domain));
}

bool TransitionPolicy::remove_domain(std::string_view name) {
  std::unique_lock lock(mutex_);
  const bool removed = domains_.erase(std::string(name)) != 0;
  if (removed) {
    ++generation_;
  }
  return removed;
}

void TransitionPolicy::apply_update(const PolicyUpdate &update) {
  std::unique_lock lock(mutex_);
  bool changed = false;

  for (const std::string &name : update.remove_domains) {
    if (domains_.erase(name) != 0) {
      changed = true;
    }
  }
  for (const DomainPolicy &domain : update.upsert_domains) {
    domains_[domain.name] = domain;
    changed = true;
  }

  const auto same_key = [](const TransitionRule &lhs,
                           const TransitionRule &rhs) {
    return lhs.source_domain == rhs.source_domain &&
           lhs.executable_label == rhs.executable_label &&
           lhs.target_domain == rhs.target_domain;
  };

  for (const TransitionRule &remove : update.remove_transitions) {
    const auto it = std::find_if(
        transitions_.begin(), transitions_.end(),
        [&](const TransitionRule &rule) { return same_key(rule, remove); });
    if (it != transitions_.end()) {
      transitions_.erase(it);
      changed = true;
    }
  }
  for (const TransitionRule &upsert : update.upsert_transitions) {
    bool replaced = false;
    for (TransitionRule &rule : transitions_) {
      if (same_key(rule, upsert)) {
        rule = upsert;
        replaced = true;
        changed = true;
        break;
      }
    }
    if (!replaced) {
      transitions_.push_back(upsert);
      changed = true;
    }
  }

  for (const std::string &label : update.remove_permissive_labels) {
    if (permissive_labels_.erase(label) != 0) {
      changed = true;
    }
  }
  for (const std::string &label : update.add_permissive_labels) {
    if (permissive_labels_.insert(label).second) {
      changed = true;
    }
  }

  if (changed) {
    ++generation_;
  }
}

TransitionPolicy &TransitionPolicy::add_transition(TransitionRule rule) {
  std::unique_lock lock(mutex_);
  for (TransitionRule &existing : transitions_) {
    if (existing.source_domain == rule.source_domain &&
        existing.executable_label == rule.executable_label &&
        existing.target_domain == rule.target_domain) {
      existing = std::move(rule);
      ++generation_;
      return *this;
    }
  }
  transitions_.push_back(std::move(rule));
  ++generation_;
  return *this;
}

TransitionPolicy &TransitionPolicy::upsert_transition(TransitionRule rule) {
  return add_transition(std::move(rule));
}

bool TransitionPolicy::update_transition(const TransitionRule &rule) {
  std::unique_lock lock(mutex_);
  for (TransitionRule &existing : transitions_) {
    if (existing.source_domain == rule.source_domain &&
        existing.executable_label == rule.executable_label &&
        existing.target_domain == rule.target_domain) {
      existing = rule;
      ++generation_;
      return true;
    }
  }
  return false;
}

bool TransitionPolicy::remove_transition(std::string_view source_domain,
                                         std::string_view executable_label,
                                         std::string_view target_domain) {
  std::unique_lock lock(mutex_);
  const auto it =
      std::find_if(transitions_.begin(), transitions_.end(),
                   [&](const TransitionRule &rule) {
                     return rule.source_domain == source_domain &&
                            rule.executable_label == executable_label &&
                            rule.target_domain == target_domain;
                   });
  if (it == transitions_.end()) {
    return false;
  }
  transitions_.erase(it);
  ++generation_;
  return true;
}

TransitionPolicy &TransitionPolicy::mark_permissive_label(std::string label) {
  std::unique_lock lock(mutex_);
  if (permissive_labels_.insert(std::move(label)).second) {
    ++generation_;
  }
  return *this;
}

bool TransitionPolicy::unmark_permissive_label(std::string_view label) {
  std::unique_lock lock(mutex_);
  const bool removed = permissive_labels_.erase(std::string(label)) != 0;
  if (removed) {
    ++generation_;
  }
  return removed;
}

bool TransitionPolicy::is_permissive_label(std::string_view label) const {
  std::shared_lock lock(mutex_);
  return permissive_labels_.find(std::string(label)) !=
         permissive_labels_.end();
}

void TransitionPolicy::clear_permissive_labels() {
  std::unique_lock lock(mutex_);
  if (!permissive_labels_.empty()) {
    permissive_labels_.clear();
    ++generation_;
  }
}

std::optional<DomainPolicy>
TransitionPolicy::find_domain(std::string_view name) const {
  std::shared_lock lock(mutex_);
  const auto it = domains_.find(std::string(name));
  if (it == domains_.end()) {
    return std::nullopt;
  }
  return it->second;
}

std::optional<TransitionRule>
TransitionPolicy::find_transition(std::string_view source_domain,
                                  std::string_view executable_label,
                                  std::string_view target_domain) const {
  std::shared_lock lock(mutex_);
  for (const TransitionRule &rule : transitions_) {
    if (rule.source_domain == source_domain &&
        rule.executable_label == executable_label &&
        rule.target_domain == target_domain) {
      return rule;
    }
  }
  return std::nullopt;
}

std::uint64_t TransitionPolicy::generation() const {
  std::shared_lock lock(mutex_);
  return generation_;
}

bool TransitionPolicy::is_current(const TransitionResult &result) const {
  std::shared_lock lock(mutex_);
  return result.policy_generation == generation_;
}

TransitionPolicy::Snapshot TransitionPolicy::snapshot() const {
  std::shared_lock lock(mutex_);
  return {domains_, transitions_, permissive_labels_, generation_};
}

TransitionResult
TransitionPolicy::evaluate_snapshot(const Snapshot &snapshot,
                                    const ExecRequest &request) {
  TransitionResult result;
  result.source_domain = request.current_domain;
  result.target_domain = request.target_domain;
  result.file_label = request.executable.label;
  result.policy_generation = snapshot.generation;

  const auto deny = [&](DenyCode code, std::string detail) {
    result.allowed = false;
    result.code = code;
    result.reason = std::string(to_string(code));
    if (!detail.empty()) {
      result.reason += ": ";
      result.reason += std::move(detail);
    }
    result.effective_capabilities.clear();
    result.effective_network_access = NetworkAccess::None;
    return result;
  };

  const auto source_it = snapshot.domains.find(request.current_domain);
  if (source_it == snapshot.domains.end()) {
    return deny(DenyCode::SourceDomainUnknown, quoted(request.current_domain));
  }
  const auto target_it = snapshot.domains.find(request.target_domain);
  if (target_it == snapshot.domains.end()) {
    return deny(DenyCode::TargetDomainUnknown, quoted(request.target_domain));
  }

  const DomainPolicy &source = source_it->second;
  const DomainPolicy &target = target_it->second;
  const ExecutableInfo &file = request.executable;
  const bool is_transition = request.current_domain != request.target_domain;

  if (file.path.empty()) {
    return deny(DenyCode::EmptyExecutablePath, {});
  }
  if (file.label.empty()) {
    return deny(DenyCode::EmptyFileLabel, {});
  }

  if (is_transition) {
    const auto rule_it =
        std::find_if(snapshot.transitions.begin(), snapshot.transitions.end(),
                     [&](const TransitionRule &rule) {
                       return rule.source_domain == request.current_domain &&
                              rule.executable_label == file.label &&
                              rule.target_domain == request.target_domain;
                     });
    if (rule_it == snapshot.transitions.end()) {
      return deny(DenyCode::TransitionRuleMissing,
                  quoted(request.current_domain) + " + " + quoted(file.label) +
                      " -> " + quoted(request.target_domain));
    }
    if (!rule_it->enabled) {
      return deny(DenyCode::TransitionRuleDisabled,
                  quoted(request.current_domain) + " + " + quoted(file.label) +
                      " -> " + quoted(request.target_domain));
    }
  } else {
    // A same-domain exec has no transition rule requirement, but an explicit
    // disabled rule still takes precedence.
    const auto rule_it =
        std::find_if(snapshot.transitions.begin(), snapshot.transitions.end(),
                     [&](const TransitionRule &rule) {
                       return rule.source_domain == request.current_domain &&
                              rule.executable_label == file.label &&
                              rule.target_domain == request.target_domain;
                     });
    if (rule_it != snapshot.transitions.end() && !rule_it->enabled) {
      return deny(DenyCode::TransitionRuleDisabled,
                  quoted(request.current_domain) + " + " + quoted(file.label) +
                      " -> " + quoted(request.target_domain));
    }
  }

  if (is_transition && target.permissive) {
    return deny(DenyCode::PermissiveTargetDomain,
                quoted(request.target_domain));
  }

  if (const std::optional<DenyCode> widened = widening_code(source, target)) {
    return deny(*widened, quoted(request.current_domain) + " -> " +
                              quoted(request.target_domain));
  }

  // Basic executable metadata is checked for every exec.
  if (!file.exists) {
    return deny(DenyCode::ExecutableMissing, quoted(file.path));
  }
  if (!file.regular_file) {
    return deny(DenyCode::ExecutableNotRegular, quoted(file.path));
  }
  if (file.hard_link_count == 0) {
    return deny(DenyCode::InvalidFileMetadata, "hard link count is unknown");
  }

  // Entry-point restrictions apply to a real domain transition.  A
  // same-domain exec does not cross a label boundary and is not a transition
  // entry.
  if (is_transition) {
    if (file.symlink) {
      return deny(DenyCode::SymlinkEntryPoint, quoted(file.path));
    }
    if (file.writable()) {
      return deny(DenyCode::WritableEntryPoint, quoted(file.path));
    }
    if (file.hard_link_count > 1) {
      return deny(DenyCode::MultipleHardLinks, quoted(file.path));
    }
    if (file.label_permissive || snapshot.permissive_labels.find(file.label) !=
                                     snapshot.permissive_labels.end()) {
      return deny(DenyCode::PermissiveFileLabel, quoted(file.label));
    }
  }

  std::set<std::string> current_capabilities;
  if (request.current_capabilities) {
    current_capabilities = *request.current_capabilities;
    for (const std::string &capability : current_capabilities) {
      if (!source.capabilities.contains(capability)) {
        return deny(DenyCode::CurrentCapabilitiesExceedSource,
                    quoted(capability));
      }
    }
  } else {
    current_capabilities = source.capabilities;
  }

  // Re-evaluate every descriptor that survives exec.  Failure is a denial,
  // not a silent close.
  for (const InheritedDescriptor &descriptor : request.inherited_descriptors) {
    if (descriptor.close_on_exec) {
      continue;
    }
    if (descriptor.fd < 0) {
      return deny(DenyCode::DescriptorNotPermitted, "invalid descriptor");
    }
    const DescriptorDecision decision = resolve_descriptor(target, descriptor);
    if (!decision.permitted) {
      return deny(DenyCode::DescriptorNotPermitted,
                  "fd " + std::to_string(descriptor.fd) + " is denied");
    }
    if (!contains(decision.access, descriptor.access)) {
      return deny(DenyCode::DescriptorNotPermitted,
                  "fd " + std::to_string(descriptor.fd) +
                      " requests access not permitted by " +
                      quoted(request.target_domain));
    }
  }

  for (const auto &[name, value] : request.environment) {
    (void)value;
    if (is_dangerous_environment(name)) {
      return deny(DenyCode::EnvironmentNotPermitted,
                  "unsafe variable " + quoted(name));
    }
    if (target.environment_policy == EnvironmentPolicy::DenyAll) {
      return deny(DenyCode::EnvironmentNotPermitted,
                  "environment is denied by " + quoted(request.target_domain));
    }
    if (target.environment_policy == EnvironmentPolicy::AllowList) {
      bool permitted = false;
      for (const std::string &allowed : target.allowed_environment) {
        if (pattern_matches(name, allowed)) {
          permitted = true;
          break;
        }
      }
      if (!permitted) {
        return deny(DenyCode::EnvironmentNotPermitted, quoted(name));
      }
    }
    for (const std::string &forbidden : target.forbidden_environment) {
      if (pattern_matches(name, forbidden)) {
        return deny(DenyCode::EnvironmentNotPermitted, quoted(name));
      }
    }
  }

  for (const std::string &required : target.required_environment) {
    if (request.environment.find(required) == request.environment.end()) {
      return deny(DenyCode::RequiredEnvironmentMissing, quoted(required));
    }
  }

  if (target.namespace_policy == NamespacePolicy::AllowList) {
    for (const std::string &name : request.namespaces) {
      bool permitted = false;
      for (const std::string &allowed : target.allowed_namespaces) {
        if (pattern_matches(name, allowed)) {
          permitted = true;
          break;
        }
      }
      if (!permitted) {
        return deny(DenyCode::NamespaceNotPermitted, quoted(name));
      }
    }
  }

  for (const std::string &required : target.required_namespaces) {
    if (request.namespaces.find(required) == request.namespaces.end()) {
      return deny(DenyCode::RequiredNamespaceMissing, quoted(required));
    }
  }

  result.allowed = true;
  result.code = DenyCode::None;
  result.reason = "transition permitted";
  result.effective_network_access = target.network_access;
  for (const std::string &capability : target.capabilities) {
    if (current_capabilities.contains(capability)) {
      result.effective_capabilities.insert(capability);
    }
  }
  return result;
}

TransitionResult TransitionPolicy::evaluate(const ExecRequest &request) const {
  return evaluate_snapshot(snapshot(), request);
}

std::vector<PolicyIssue> TransitionPolicy::validate() const {
  const Snapshot state = snapshot();
  std::vector<PolicyIssue> issues;

  for (const TransitionRule &rule : state.transitions) {
    PolicyIssue issue;
    issue.source_domain = rule.source_domain;
    issue.executable_label = rule.executable_label;
    issue.target_domain = rule.target_domain;

    const auto source_it = state.domains.find(rule.source_domain);
    if (source_it == state.domains.end()) {
      issue.code = DenyCode::SourceDomainUnknown;
      issue.detail = "source domain is not declared";
      issues.push_back(std::move(issue));
      continue;
    }
    const auto target_it = state.domains.find(rule.target_domain);
    if (target_it == state.domains.end()) {
      issue.code = DenyCode::TargetDomainUnknown;
      issue.detail = "target domain is not declared";
      issues.push_back(std::move(issue));
      continue;
    }

    if (!rule.enabled) {
      issue.code = DenyCode::TransitionRuleDisabled;
      issue.detail = "transition rule is disabled";
      issues.push_back(std::move(issue));
      continue;
    }
    if (target_it->second.permissive) {
      issue.code = DenyCode::PermissiveTargetDomain;
      issue.detail = "target domain is permissive";
      issues.push_back(std::move(issue));
      continue;
    }
    if (state.permissive_labels.find(rule.executable_label) !=
        state.permissive_labels.end()) {
      issue.code = DenyCode::PermissiveFileLabel;
      issue.detail = "executable label is permissive";
      issues.push_back(std::move(issue));
      continue;
    }
    if (const std::optional<DenyCode> widened =
            widening_code(source_it->second, target_it->second)) {
      issue.code = *widened;
      issue.detail = std::string(to_string(*widened));
      issues.push_back(std::move(issue));
    }
  }

  return issues;
}

TransitionResult evaluate_exec(const TransitionPolicy &policy,
                               const ExecRequest &request) {
  return policy.evaluate(request);
}

TransitionResult authorize_exec(const TransitionPolicy &policy,
                                const ExecRequest &request) {
  return policy.evaluate(request);
}

ExecutableInfo inspect_executable(std::string path, std::string label,
                                  const InspectOptions &options) {
  ExecutableInfo info;
  info.path = std::move(path);
  info.label = std::move(label);

  struct stat link_stat{};
  if (::lstat(info.path.c_str(), &link_stat) != 0) {
    info.exists = false;
    info.regular_file = false;
    // Fail closed: an unreadable path is treated as an unsafe entry point.
    info.path_writable = true;
    return info;
  }
  if (S_ISLNK(link_stat.st_mode)) {
    info.symlink = true;
  }

  struct stat file_stat{};
  if (::stat(info.path.c_str(), &file_stat) != 0) {
    info.exists = false;
    info.regular_file = false;
    info.path_writable = true;
    return info;
  }

  info.exists = true;
  info.regular_file = S_ISREG(file_stat.st_mode) != 0;
  info.hard_link_count = static_cast<std::uint64_t>(file_stat.st_nlink);

  info.file_writable = writable_for_principal(info.path.c_str(), file_stat,
                                              options.check_access_permission);

  std::error_code error;
  const std::filesystem::path absolute =
      std::filesystem::canonical(std::filesystem::path(info.path), error);
  if (error) {
    info.path_writable = true;
    return info;
  }

  const auto directory_is_writable = [&](const std::filesystem::path &dir) {
    struct stat dir_stat{};
    if (::stat(dir.c_str(), &dir_stat) != 0 || !S_ISDIR(dir_stat.st_mode)) {
      return true;
    }
    return writable_for_principal(dir.c_str(), dir_stat,
                                  options.check_access_permission);
  };

  const auto chain_is_writable =
      [&](const std::filesystem::path &path_to_check) {
        std::filesystem::path directory = path_to_check.parent_path();
        if (directory.empty()) {
          directory = "/";
        }
        if (directory_is_writable(directory)) {
          return true;
        }
        if (!options.check_all_path_components) {
          return false;
        }
        while (true) {
          const std::filesystem::path parent = directory.parent_path();
          if (parent.empty() || parent == directory) {
            break;
          }
          directory = parent;
          if (directory_is_writable(directory)) {
            return true;
          }
        }
        return false;
      };

  // Check both the lexical path and the canonical path.  The lexical walk
  // catches a writable directory that contains a symlinked path component,
  // even when the symlink points into a root-owned directory.
  std::error_code lexical_error;
  const std::filesystem::path lexical = std::filesystem::absolute(
      std::filesystem::path(info.path), lexical_error);
  if (lexical_error) {
    info.path_writable = true;
  } else if (chain_is_writable(lexical)) {
    info.path_writable = true;
  }

  if (!info.path_writable && chain_is_writable(absolute)) {
    info.path_writable = true;
  }

  return info;
}

} // namespace netsec
