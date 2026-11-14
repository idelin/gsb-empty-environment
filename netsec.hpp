#pragma once

#include <cstddef>
#include <cstdint>
#include <filesystem>
#include <map>
#include <memory>
#include <mutex>
#include <set>
#include <string>
#include <string_view>
#include <vector>

namespace netsec {

using Generation = std::uint64_t;
using OwnerId = std::uint32_t;

inline constexpr Generation kNoGeneration = 0;

enum class RuleErrorCode {
    none = 0,
    io_error,
    insecure_path,
    insecure_owner,
    insecure_permissions,
    file_too_large,
    limit_exceeded,
    invalid_syntax,
    unknown_keyword,
    invalid_field,
    invalid_value,
    command_not_allowed,
    environment_not_allowed,
    conflicting_policy,
    event_rejected,
};

std::string_view toString(RuleErrorCode code) noexcept;

struct RuleError {
    RuleErrorCode code = RuleErrorCode::none;
    std::string path;
    std::size_t line = 0;
    std::string message;

    [[nodiscard]] explicit operator bool() const noexcept {
        return code != RuleErrorCode::none;
    }
};

// A command that device-manager configuration explicitly authorises. Rules
// may select this entry, but they cannot change its executable or its argv
// template. A template value is a literal and/or a placeholder such as
// "{ACTION}", "{KERNEL}", "{ATTR:idVendor}", or "{ENV:SERIAL}".
struct CommandPolicy {
    std::string name;
    std::filesystem::path executable;
    std::vector<std::string> argument_template;
    std::vector<std::pair<std::string, std::string>> environment_template;
};

// Policy for reading the rule directory. The defaults are suitable for a
// root daemon: files and their parent directories must be root-owned unless
// trusted_uid is explicitly set to another privileged service account.
struct RuleFilePolicy {
    OwnerId trusted_uid = 0;
    bool accept_root_owner = true;
    bool require_secure_directories = true;
    std::size_t max_file_size = 1024U * 1024U;
    std::size_t max_total_size = 8U * 1024U * 1024U;
    std::size_t max_rules = 4096;
    std::size_t max_line_length = 8192;
    std::size_t max_conditions_per_rule = 64;

    // ENV{...} conditions and {ENV:...} substitutions are limited to these
    // event-provided keys. Values are still treated as untrusted event data.
    std::set<std::string> allowed_event_environment = {
        "DEVTYPE",
        "ID_BUS",
        "ID_MODEL_ID",
        "ID_SERIAL_SHORT",
        "ID_VENDOR_ID",
        "SERIAL",
    };
};

struct MatchCondition {
    // Normalised field name: ACTION, SUBSYSTEM, KERNEL, DEVPATH, DEVNAME,
    // DRIVER, SYSNAME, SYSPATH, SEQNUM, ATTR:<name>, or ENV:<name>.
    std::string field;
    bool negated = false;
    std::string value;
};

struct Rule {
    std::string source;
    std::size_t line = 0;
    std::vector<MatchCondition> conditions;
    // Name of an entry in RuleSet::commands. No raw command line is retained.
    std::string command_name;
};

struct RuleSet {
    Generation generation = kNoGeneration;
    std::filesystem::path source_directory;
    std::vector<CommandPolicy> commands;
    std::vector<Rule> rules;

    [[nodiscard]] const CommandPolicy* findCommand(std::string_view name) const noexcept;
};

struct DeviceEvent {
    std::string action;
    std::string subsystem;
    std::string kernel;
    std::string devpath;
    std::string devname;
    std::string driver;
    std::string sysname;
    std::string syspath;
    std::uint64_t seqnum = 0;
    std::uint64_t event_id = 0;
    std::map<std::string, std::string> attributes;
    std::map<std::string, std::string> environment;
};

struct CommandInvocation {
    // Generation of the immutable rule snapshot that selected this command.
    Generation generation = kNoGeneration;
    std::uint64_t event_id = 0;
    std::string rule_source;
    std::size_t rule_line = 0;
    std::string command_name;
    std::string executable;
    // argv[0] is always executable. The remaining entries come from the
    // host-owned CommandPolicy template, never from rule text.
    std::vector<std::string> argv;
    // A clean environment. It is not inherited from the daemon.
    std::map<std::string, std::string> environment;
};

struct Evaluation {
    bool ok = false;
    Generation generation = kNoGeneration;
    std::uint64_t event_id = 0;
    std::vector<CommandInvocation> commands;
    std::vector<RuleError> errors;
};

struct ParseResult {
    bool ok = false;
    std::vector<Rule> rules;
    std::vector<RuleError> errors;

    [[nodiscard]] explicit operator bool() const noexcept {
        return ok;
    }
};

struct LoadResult {
    bool ok = false;
    Generation generation = kNoGeneration;
    std::size_t rule_count = 0;
    std::shared_ptr<const RuleSet> snapshot;
    std::vector<RuleError> errors;

    [[nodiscard]] explicit operator bool() const noexcept {
        return ok;
    }

    [[nodiscard]] std::string summary() const;
};

// Parses the restricted udev-like dialect described in README.md. It performs
// no file I/O, which makes it useful for validating generated rule bundles.
class RuleParser {
public:
    explicit RuleParser(std::vector<CommandPolicy> commands,
                        RuleFilePolicy file_policy = {});

    [[nodiscard]] ParseResult parse(std::string_view text,
                                    std::string source_name) const;

private:
    std::vector<CommandPolicy> commands_;
    RuleFilePolicy file_policy_;
};

// Loads all *.rules files in a directory. A single unsafe, unreadable, or
// malformed file rejects the whole bundle; no later file can shadow it.
class RuleFileLoader {
public:
    RuleFileLoader(std::filesystem::path directory,
                   std::vector<CommandPolicy> commands,
                   RuleFilePolicy file_policy = {});

    [[nodiscard]] LoadResult load(Generation generation = 1) const;

private:
    std::filesystem::path directory_;
    std::vector<CommandPolicy> commands_;
    RuleParser parser_;
    RuleFilePolicy file_policy_;
};

// Thread-safe rule runtime. Every evaluation captures exactly one immutable
// snapshot, so an event is evaluated entirely against one generation. Reloads
// publish a fully validated snapshot with one atomic pointer swap; a failed
// reload leaves the previous snapshot and generation untouched.
class RuleEngine {
public:
    RuleEngine(std::filesystem::path rule_directory,
               std::vector<CommandPolicy> commands,
               RuleFilePolicy file_policy = {});

    [[nodiscard]] LoadResult load();
    [[nodiscard]] LoadResult reload();

    // Aliases with explicit names for callers that prefer them.
    [[nodiscard]] LoadResult loadRules() { return load(); }
    [[nodiscard]] LoadResult reloadRules() { return reload(); }

    [[nodiscard]] Evaluation evaluate(const DeviceEvent& event) const;

    // Bind an event to a snapshot captured by snapshot(). This is useful when
    // the caller queues events and wants them evaluated against the generation
    // that was current when the event was accepted.
    [[nodiscard]] Evaluation evaluate(const DeviceEvent& event,
                                      const RuleSet& bound_snapshot) const;

    [[nodiscard]] Evaluation onEvent(const DeviceEvent& event) const {
        return evaluate(event);
    }

    [[nodiscard]] Generation activeGeneration() const;
    [[nodiscard]] std::size_t ruleCount() const;
    [[nodiscard]] std::shared_ptr<const RuleSet> snapshot() const;

private:
    [[nodiscard]] Evaluation evaluateWith(const DeviceEvent& event,
                                          const RuleSet& rules) const;

    RuleFileLoader loader_;
    RuleFilePolicy file_policy_;
    mutable std::mutex reload_mutex_;
    mutable std::mutex state_mutex_;
    std::shared_ptr<const RuleSet> active_;
};

using RuleManager = RuleEngine;
using UdevRuleLoader = RuleFileLoader;

}  // namespace netsec
