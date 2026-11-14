#include "netsec.hpp"

#include <algorithm>
#include <array>
#include <cerrno>
#include <cctype>
#include <cstring>
#include <dirent.h>
#include <fcntl.h>
#include <limits>
#include <optional>
#include <sstream>
#include <sys/stat.h>
#include <sys/types.h>
#include <unistd.h>
#include <utility>

namespace netsec {
namespace {

constexpr std::size_t kMaxCommandNameLength = 128;
constexpr std::size_t kMaxEnvironmentNameLength = 128;
constexpr std::size_t kMaxFieldParameterLength = 128;
constexpr std::size_t kMaxMatchValueLength = 512;
constexpr std::size_t kMaxTemplateLength = 1024;
constexpr std::size_t kMaxExpandedValueLength = 1024;

constexpr std::array<std::string_view, 8> kActions = {
    "add", "remove", "change", "bind", "unbind", "move", "online", "offline",
};

constexpr std::array<std::string_view, 9> kScalarFields = {
    "ACTION", "SUBSYSTEM", "KERNEL", "DEVPATH", "DEVNAME",
    "DRIVER", "SYSNAME", "SYSPATH", "SEQNUM",
};

bool isAsciiAlnum(char c) {
    const auto uc = static_cast<unsigned char>(c);
    return std::isalnum(uc) != 0;
}

std::string_view trim(std::string_view value) {
    while (!value.empty() &&
           (value.front() == ' ' || value.front() == '\t' ||
            value.front() == '\r' || value.front() == '\n')) {
        value.remove_prefix(1);
    }
    while (!value.empty() &&
           (value.back() == ' ' || value.back() == '\t' ||
            value.back() == '\r' || value.back() == '\n')) {
        value.remove_suffix(1);
    }
    return value;
}

bool containsControl(std::string_view value) {
    for (const char c : value) {
        const auto uc = static_cast<unsigned char>(c);
        if (uc < 0x20U || uc == 0x7fU) {
            return true;
        }
    }
    return false;
}

bool isScalarField(std::string_view field) {
    return std::find(kScalarFields.begin(), kScalarFields.end(), field) !=
           kScalarFields.end();
}

bool isKnownAction(std::string_view action) {
    return std::find(kActions.begin(), kActions.end(), action) != kActions.end();
}

bool isCommandName(std::string_view name) {
    if (name.empty() || name.size() > kMaxCommandNameLength) {
        return false;
    }
    if (!(isAsciiAlnum(name.front()) || name.front() == '_')) {
        return false;
    }
    return std::all_of(name.begin(), name.end(), [](char c) {
        return isAsciiAlnum(c) || c == '_' || c == '-' || c == '.' || c == ':';
    });
}

bool isEnvironmentName(std::string_view name) {
    if (name.empty() || name.size() > kMaxEnvironmentNameLength) {
        return false;
    }
    if (!(name.front() == '_' ||
          (name.front() >= 'A' && name.front() <= 'Z'))) {
        return false;
    }
    return std::all_of(name.begin(), name.end(), [](char c) {
        return (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_';
    });
}

bool isDangerousEnvironmentName(std::string_view name) {
    static constexpr std::array<std::string_view, 21> kDenied = {
        "PATH",          "IFS",           "ENV",          "BASH_ENV",
        "SHELL",         "HOME",          "PWD",          "TMPDIR",
        "LD_PRELOAD",    "LD_LIBRARY_PATH", "LD_AUDIT",   "DYLD_INSERT_LIBRARIES",
        "GCONV_PATH",    "LOCPATH",       "NLSPATH",      "PYTHONPATH",
        "PYTHONHOME",    "PERL5LIB",      "RUBYOPT",      "NODE_OPTIONS",
        "SSH_AUTH_SOCK",
    };
    if (std::find(kDenied.begin(), kDenied.end(), name) != kDenied.end()) {
        return true;
    }
    return name.starts_with("LD_") || name.starts_with("DYLD_");
}

bool isFieldParameter(std::string_view parameter) {
    if (parameter.empty() || parameter.size() > kMaxFieldParameterLength) {
        return false;
    }
    return std::all_of(parameter.begin(), parameter.end(), [](char c) {
        return isAsciiAlnum(c) || c == '_' || c == '-' || c == '.' || c == ':';
    });
}

bool isSafeLiteral(char c) {
    return isAsciiAlnum(c) || c == '.' || c == '_' || c == '-' || c == '/' ||
           c == ':' || c == '@' || c == '+' || c == '=' || c == ',' ||
           c == '%' || c == '#' || c == '!' || c == '~';
}

bool isSafeEventValue(std::string_view value) {
    if (value.size() > kMaxExpandedValueLength) {
        return false;
    }
    for (const char c : value) {
        if (!isSafeLiteral(c)) {
            return false;
        }
    }
    return true;
}

bool isSafeExecutablePath(std::string_view value) {
    if (value.empty() || value.front() != '/' || value.size() > 4096 ||
        containsControl(value)) {
        return false;
    }
    for (const char c : value) {
        if (!isSafeLiteral(c)) {
            return false;
        }
    }
    const std::filesystem::path path{std::string(value)};
    if (!path.is_absolute()) {
        return false;
    }
    for (const auto& component : path) {
        if (component == "..") {
            return false;
        }
    }
    return path.lexically_normal().generic_string() == value;
}

struct TemplatePart {
    bool token = false;
    std::string value;
};

struct ParsedTemplate {
    bool ok = false;
    std::vector<TemplatePart> parts;
    std::string error;
};

bool isScalarToken(std::string_view token) {
    return std::find(kScalarFields.begin(), kScalarFields.end(), token) !=
           kScalarFields.end();
}

ParsedTemplate parseTemplate(std::string_view value,
                             const RuleFilePolicy& policy) {
    ParsedTemplate result;
    if (value.size() > kMaxTemplateLength) {
        result.error = "template is too long";
        return result;
    }

    std::string literal;
    for (std::size_t i = 0; i < value.size();) {
        if (value[i] == '}') {
            result.error = "unmatched '}' in template";
            return result;
        }
        if (value[i] != '{') {
            if (!isSafeLiteral(value[i])) {
                result.error = "unsafe character in template literal";
                return result;
            }
            literal.push_back(value[i]);
            ++i;
            continue;
        }

        if (!literal.empty()) {
            result.parts.push_back(TemplatePart{false, std::move(literal)});
            literal.clear();
        }

        const auto close = value.find('}', i + 1);
        if (close == std::string_view::npos) {
            result.error = "unclosed '{' in template";
            return result;
        }
        const auto token = value.substr(i + 1, close - i - 1);
        if (token.empty() || token.find('{') != std::string_view::npos) {
            result.error = "invalid template placeholder";
            return result;
        }

        bool valid = isScalarToken(token);
        if (token.starts_with("ATTR:")) {
            valid = isFieldParameter(token.substr(5));
        } else if (token.starts_with("ENV:")) {
            const auto name = token.substr(4);
            valid = policy.allowed_event_environment.contains(std::string(name));
        }
        if (!valid) {
            result.error = "placeholder is not in the controlled set: {" +
                           std::string(token) + "}";
            return result;
        }
        result.parts.push_back(TemplatePart{true, std::string(token)});
        i = close + 1;
    }

    if (!literal.empty()) {
        result.parts.push_back(TemplatePart{false, std::move(literal)});
    }
    result.ok = true;
    return result;
}

void addError(std::vector<RuleError>& errors, RuleErrorCode code,
              std::string path, std::size_t line, std::string message) {
    if (errors.size() >= 256) {
        return;
    }
    errors.push_back(
        RuleError{code, std::move(path), line, std::move(message)});
}

std::vector<RuleError> validateCommands(
    const std::vector<CommandPolicy>& commands,
    const RuleFilePolicy& file_policy) {
    std::vector<RuleError> errors;
    std::set<std::string> names;
    const std::string source = "<command-policy>";

    for (const auto& command : commands) {
        if (!isCommandName(command.name)) {
            addError(errors, RuleErrorCode::conflicting_policy, source, 0,
                     "invalid command policy name");
        } else if (!names.insert(command.name).second) {
            addError(errors, RuleErrorCode::conflicting_policy, source, 0,
                     "duplicate command policy name: " + command.name);
        }

        const auto executable = command.executable.generic_string();
        if (!isSafeExecutablePath(executable)) {
            addError(errors, RuleErrorCode::invalid_value, source, 0,
                     "command policy executable must be a normalised absolute "
                     "path without shell metacharacters: " +
                         executable);
        }
        if (command.argument_template.size() > 128) {
            addError(errors, RuleErrorCode::limit_exceeded, source, 0,
                     "too many command policy arguments");
        }
        for (const auto& argument : command.argument_template) {
            if (argument.empty()) {
                addError(errors, RuleErrorCode::invalid_value, source, 0,
                         "command policy argument template cannot be empty");
                continue;
            }
            const auto parsed = parseTemplate(argument, file_policy);
            if (!parsed.ok) {
                addError(errors, RuleErrorCode::invalid_value, source, 0,
                         "invalid command argument template: " + parsed.error);
            }
        }

        std::set<std::string> environment_names;
        for (const auto& [name, value] : command.environment_template) {
            if (!isEnvironmentName(name)) {
                addError(errors, RuleErrorCode::environment_not_allowed, source,
                         0, "invalid environment variable name: " + name);
            } else if (isDangerousEnvironmentName(name)) {
                addError(errors, RuleErrorCode::environment_not_allowed, source,
                         0,
                         "environment variable is reserved: " + name);
            } else if (!environment_names.insert(name).second) {
                addError(errors, RuleErrorCode::conflicting_policy, source, 0,
                         "duplicate environment variable: " + name);
            }
            const auto parsed = parseTemplate(value, file_policy);
            if (!parsed.ok) {
                addError(errors, RuleErrorCode::environment_not_allowed, source,
                         0,
                         "invalid environment template for " + name + ": " +
                             parsed.error);
            }
        }
    }
    return errors;
}

bool globMatch(std::string_view pattern, std::string_view value) {
    const std::size_t width = value.size() + 1;
    std::vector<signed char> memo((pattern.size() + 1) * width, -1);

    const auto match = [&](const auto& self, std::size_t pi,
                           std::size_t vi) -> bool {
        auto& cached = memo[pi * width + vi];
        if (cached != -1) {
            return cached != 0;
        }

        bool answer = false;
        if (pi == pattern.size()) {
            answer = vi == value.size();
        } else if (pattern[pi] == '*') {
            answer = self(self, pi + 1, vi) ||
                     (vi < value.size() && self(self, pi, vi + 1));
        } else if (vi < value.size() && pattern[pi] == '?') {
            answer = self(self, pi + 1, vi + 1);
        } else if (vi < value.size() && pattern[pi] == '[') {
            std::size_t end = pi + 1;
            bool negate = false;
            if (end < pattern.size() &&
                (pattern[end] == '!' || pattern[end] == '^')) {
                negate = true;
                ++end;
            }
            bool matched = false;
            bool closed = false;
            while (end < pattern.size()) {
                if (pattern[end] == ']') {
                    closed = true;
                    break;
                }
                char first = pattern[end];
                if (first == '\\') {
                    return false;
                }
                if (end + 2 < pattern.size() && pattern[end + 1] == '-' &&
                    pattern[end + 2] != ']') {
                    const char last = pattern[end + 2];
                    if (value[vi] >= first && value[vi] <= last) {
                        matched = true;
                    }
                    end += 3;
                } else {
                    if (value[vi] == first) {
                        matched = true;
                    }
                    ++end;
                }
            }
            if (!closed) {
                answer = false;
            } else {
                if (negate) {
                    matched = !matched;
                }
                answer = matched && self(self, end + 1, vi + 1);
            }
        } else if (vi < value.size() && pattern[pi] == value[vi]) {
            answer = self(self, pi + 1, vi + 1);
        }

        cached = answer ? 1 : 0;
        return answer;
    };

    return match(match, 0, 0);
}

bool isValidMatchValue(std::string_view value, std::string_view field,
                       std::string& error) {
    if (value.empty()) {
        error = "match value cannot be empty";
        return false;
    }
    if (value.size() > kMaxMatchValueLength) {
        error = "match value is too long";
        return false;
    }
    bool in_class = false;
    for (const char c : value) {
        const auto uc = static_cast<unsigned char>(c);
        if (uc < 0x20U || uc == 0x7fU || std::isspace(uc) != 0) {
            error = "whitespace and control characters are not allowed";
            return false;
        }
        if (c == '"' || c == '\'' || c == '\\' || c == '`' || c == '$' ||
            c == '{' || c == '}' || c == ';' || c == '|' || c == '&' ||
            c == '<' || c == '>' || c == '(' || c == ')' || c == ',' ||
            c == '#') {
            error = "unsafe character in match value";
            return false;
        }
        if (c == '[') {
            if (in_class) {
                error = "nested character class is not allowed";
                return false;
            }
            in_class = true;
        } else if (c == ']') {
            if (!in_class) {
                error = "unmatched ']' in match value";
                return false;
            }
            in_class = false;
        }
    }
    if (in_class) {
        error = "unclosed character class in match value";
        return false;
    }

    if (field == "ACTION" && value.find_first_of("*?[") == std::string_view::npos &&
        !isKnownAction(value)) {
        error = "unknown ACTION value";
        return false;
    }
    if (field == "SEQNUM" &&
        std::any_of(value.begin(), value.end(), [](char c) {
            return !(c >= '0' && c <= '9') && c != '*' && c != '?' &&
                   c != '[' && c != ']' && c != '!' && c != '^' && c != '-';
        })) {
        error = "SEQNUM can only contain digits or a glob";
        return false;
    }
    return true;
}

struct ParsedField {
    bool ok = false;
    RuleErrorCode code = RuleErrorCode::none;
    std::string field;
    std::string error;
};

ParsedField parseField(std::string_view lhs, const RuleFilePolicy& policy) {
    ParsedField result;
    if (lhs.empty() || lhs.find_first_of(" \t\r\n") != std::string_view::npos) {
        result.code = RuleErrorCode::invalid_syntax;
        result.error = "invalid field name";
        return result;
    }

    const auto open = lhs.find('{');
    if (open == std::string_view::npos) {
        if (lhs == "RUN") {
            result.ok = true;
            result.field = "RUN";
            return result;
        }
        if (!isScalarField(lhs)) {
            result.code = RuleErrorCode::unknown_keyword;
            result.error = "keyword is not allowed: " + std::string(lhs);
            return result;
        }
        result.ok = true;
        result.field = std::string(lhs);
        return result;
    }

    if (lhs.back() != '}' || lhs.find('{', open + 1) != std::string_view::npos) {
        result.code = RuleErrorCode::invalid_syntax;
        result.error = "malformed parameterised field";
        return result;
    }
    const auto keyword = lhs.substr(0, open);
    const auto parameter = lhs.substr(open + 1, lhs.size() - open - 2);
    if (!isFieldParameter(parameter)) {
        result.code = RuleErrorCode::invalid_field;
        result.error = "invalid field parameter";
        return result;
    }

    if (keyword == "ATTR") {
        result.ok = true;
        result.field = "ATTR:" + std::string(parameter);
        return result;
    }
    if (keyword == "ENV") {
        if (!policy.allowed_event_environment.contains(std::string(parameter))) {
            result.code = RuleErrorCode::environment_not_allowed;
            result.error = "ENV key is not in the controlled set: " +
                           std::string(parameter);
            return result;
        }
        result.ok = true;
        result.field = "ENV:" + std::string(parameter);
        return result;
    }

    result.code = RuleErrorCode::unknown_keyword;
    result.error = "parameterised keyword is not allowed: " +
                   std::string(keyword);
    return result;
}

std::vector<std::string_view> splitClauses(std::string_view line, bool& ok,
                                           std::string& error) {
    std::vector<std::string_view> clauses;
    std::size_t start = 0;
    bool quoted = false;
    for (std::size_t i = 0; i < line.size(); ++i) {
        if (line[i] == '"') {
            quoted = !quoted;
        } else if (line[i] == '\\') {
            ok = false;
            error = "backslash escapes are not allowed in rules";
            return {};
        } else if (line[i] == ',' && !quoted) {
            const auto clause = trim(line.substr(start, i - start));
            if (clause.empty()) {
                ok = false;
                error = "empty clause";
                return {};
            }
            clauses.push_back(clause);
            start = i + 1;
        }
    }
    if (quoted) {
        ok = false;
        error = "unterminated quoted value";
        return {};
    }
    const auto last = trim(line.substr(start));
    if (last.empty()) {
        ok = false;
        error = "empty clause";
        return {};
    }
    clauses.push_back(last);
    ok = true;
    return clauses;
}

struct ParsedClause {
    bool ok = false;
    std::string lhs;
    std::string op;
    std::string value;
    std::string error;
};

ParsedClause parseClause(std::string_view clause) {
    ParsedClause result;
    const auto equals = clause.find('=');
    if (equals == std::string_view::npos) {
        result.error = "clause has no '=' operator";
        return result;
    }

    std::size_t op_begin = equals;
    std::size_t rhs_begin = equals + 1;
    if (equals > 0 && clause[equals - 1] == '+') {
        result.op = "+=";
        op_begin = equals - 1;
    } else if (equals > 0 && clause[equals - 1] == '!') {
        result.op = "!=";
        op_begin = equals - 1;
    } else if (equals + 1 < clause.size() && clause[equals + 1] == '=') {
        result.op = "==";
        rhs_begin = equals + 2;
    } else {
        result.op = "=";
    }

    result.lhs = std::string(trim(clause.substr(0, op_begin)));
    auto rhs = trim(clause.substr(rhs_begin));
    if (result.lhs.empty()) {
        result.error = "missing left-hand side";
        return result;
    }
    if (rhs.size() < 2 || rhs.front() != '"' || rhs.back() != '"') {
        result.error = "right-hand side must be a double-quoted value";
        return result;
    }
    rhs.remove_prefix(1);
    rhs.remove_suffix(1);
    if (rhs.find('"') != std::string_view::npos ||
        containsControl(rhs)) {
        result.error = "invalid character in quoted value";
        return result;
    }
    result.value = std::string(rhs);
    result.ok = true;
    return result;
}

struct RunResolution {
    bool ok = false;
    RuleErrorCode code = RuleErrorCode::none;
    std::string command;
    std::string error;
};

RunResolution resolveRun(const std::string& value,
                         const std::vector<CommandPolicy>& commands) {
    RunResolution result;
    if (value.empty()) {
        result.code = RuleErrorCode::invalid_value;
        result.error = "RUN value cannot be empty";
        return result;
    }

    // Preferred form: RUN+="command-policy-name". The rule cannot influence
    // the executable or argument vector at all.
    for (const auto& command : commands) {
        if (command.name == value) {
            result.ok = true;
            result.command = command.name;
            return result;
        }
    }

    // Compatibility form: RUN+="/fixed/path [template...]". It is accepted
    // only when it is byte-for-byte the host-owned policy template.
    std::vector<std::string> tokens;
    std::size_t position = 0;
    while (position < value.size()) {
        while (position < value.size() &&
               (value[position] == ' ' || value[position] == '\t')) {
            ++position;
        }
        if (position == value.size()) {
            break;
        }
        const auto begin = position;
        while (position < value.size() && value[position] != ' ' &&
               value[position] != '\t') {
            ++position;
        }
        tokens.push_back(value.substr(begin, position - begin));
    }
    if (tokens.empty()) {
        result.code = RuleErrorCode::invalid_value;
        result.error = "RUN value cannot be empty";
        return result;
    }

    std::vector<std::string> matches;
    if (tokens.size() == 1) {
        for (const auto& command : commands) {
            if (command.executable.generic_string() == tokens.front()) {
                matches.push_back(command.name);
            }
        }
    } else {
        for (const auto& command : commands) {
            if (command.executable.generic_string() != tokens.front()) {
                continue;
            }
            if (command.argument_template.size() + 1 != tokens.size()) {
                continue;
            }
            bool exact = true;
            for (std::size_t i = 0; i < command.argument_template.size(); ++i) {
                if (command.argument_template[i] != tokens[i + 1]) {
                    exact = false;
                    break;
                }
            }
            if (exact) {
                matches.push_back(command.name);
            }
        }
    }

    if (matches.size() == 1) {
        result.ok = true;
        result.command = matches.front();
        return result;
    }
    if (matches.size() > 1) {
        result.code = RuleErrorCode::conflicting_policy;
        result.error = "RUN value is ambiguous between command policies";
        return result;
    }
    result.code = RuleErrorCode::command_not_allowed;
    result.error =
        "RUN value does not match a fixed command policy path and template";
    return result;
}

bool ownerAllowed(OwnerId owner, const RuleFilePolicy& policy,
                  bool file_owner) {
    if (owner == 0) {
        return !file_owner || policy.accept_root_owner;
    }
    return policy.trusted_uid != 0 && owner == policy.trusted_uid;
}

bool canonicalSecureDirectory(const std::filesystem::path& directory,
                              const RuleFilePolicy& policy,
                              std::filesystem::path& canonical,
                              std::vector<RuleError>& errors) {
    const auto source = directory.generic_string();
    std::error_code ec;
    canonical = std::filesystem::weakly_canonical(
        std::filesystem::absolute(directory, ec), ec);
    if (ec) {
        addError(errors, RuleErrorCode::insecure_path, source, 0,
                 "cannot canonicalise rule directory: " + ec.message());
        return false;
    }
    if (!std::filesystem::is_directory(canonical, ec) || ec) {
        addError(errors, RuleErrorCode::insecure_path, source, 0,
                 "rule directory does not exist or is not a directory");
        return false;
    }

    if (!policy.require_secure_directories) {
        return true;
    }

    const auto check = [&](const std::filesystem::path& path, bool final) {
        struct stat st {};
        if (::lstat(path.c_str(), &st) != 0) {
            addError(errors, RuleErrorCode::insecure_path,
                     path.generic_string(), 0,
                     "cannot inspect directory: " +
                         std::string(std::strerror(errno)));
            return false;
        }
        if (!S_ISDIR(st.st_mode)) {
            addError(errors, RuleErrorCode::insecure_path,
                     path.generic_string(), 0,
                     "path component is not a directory");
            return false;
        }
        if (!ownerAllowed(static_cast<OwnerId>(st.st_uid), policy, false)) {
            addError(errors, RuleErrorCode::insecure_owner,
                     path.generic_string(), 0,
                     "rule directory chain has an untrusted owner");
            return false;
        }
        const bool writable = (st.st_mode & (S_IWGRP | S_IWOTH)) != 0;
        const bool protected_by_sticky = (st.st_mode & S_ISVTX) != 0;
        if (writable && (final || !protected_by_sticky)) {
            addError(errors, RuleErrorCode::insecure_permissions,
                     path.generic_string(), 0,
                     "rule directory chain is group- or world-writable");
            return false;
        }
        return true;
    };

    if (!check("/", false)) {
        return false;
    }
    std::filesystem::path cursor = "/";
    for (const auto& component : canonical.relative_path()) {
        cursor /= component;
        if (!check(cursor, cursor == canonical)) {
            return false;
        }
    }
    return true;
}

int openSecureDirectory(const std::filesystem::path& canonical,
                        const RuleFilePolicy& policy,
                        std::vector<RuleError>& errors) {
    int fd = ::open(canonical.c_str(), O_RDONLY | O_CLOEXEC | O_NOFOLLOW);
    if (fd < 0) {
        addError(errors, RuleErrorCode::io_error, canonical.generic_string(), 0,
                 "cannot open rule directory: " +
                     std::string(std::strerror(errno)));
        return -1;
    }
    if (policy.require_secure_directories) {
        struct stat st {};
        if (::fstat(fd, &st) != 0) {
            addError(errors, RuleErrorCode::io_error,
                     canonical.generic_string(), 0,
                     "cannot stat rule directory: " +
                         std::string(std::strerror(errno)));
            ::close(fd);
            return -1;
        }
        if (!S_ISDIR(st.st_mode) ||
            !ownerAllowed(static_cast<OwnerId>(st.st_uid), policy, false) ||
            (st.st_mode & (S_IWGRP | S_IWOTH)) != 0) {
            addError(errors, RuleErrorCode::insecure_path,
                     canonical.generic_string(), 0,
                     "rule directory changed to an insecure object");
            ::close(fd);
            return -1;
        }
    }
    return fd;
}

std::optional<std::string> readRuleFile(int directory_fd,
                                        const std::string& name,
                                        const std::filesystem::path& source,
                                        const RuleFilePolicy& policy,
                                        std::vector<RuleError>& errors,
                                        std::size_t& total_bytes) {
    int fd = ::openat(directory_fd, name.c_str(),
                      O_RDONLY | O_CLOEXEC | O_NOFOLLOW);
    if (fd < 0) {
        addError(errors, RuleErrorCode::insecure_path, source.generic_string(),
                 0, "cannot securely open rule file: " +
                         std::string(std::strerror(errno)));
        return std::nullopt;
    }

    struct stat st {};
    if (::fstat(fd, &st) != 0) {
        addError(errors, RuleErrorCode::io_error, source.generic_string(), 0,
                 "cannot stat rule file: " +
                     std::string(std::strerror(errno)));
        ::close(fd);
        return std::nullopt;
    }
    if (!S_ISREG(st.st_mode)) {
        addError(errors, RuleErrorCode::insecure_path, source.generic_string(),
                 0, "rule entry is not a regular file");
        ::close(fd);
        return std::nullopt;
    }
    if (!ownerAllowed(static_cast<OwnerId>(st.st_uid), policy, true)) {
        addError(errors, RuleErrorCode::insecure_owner, source.generic_string(),
                 0, "rule file is not owned by a privileged owner");
        ::close(fd);
        return std::nullopt;
    }
    if ((st.st_mode & (S_IWUSR | S_IWGRP | S_IWOTH)) != 0) {
        addError(errors, RuleErrorCode::insecure_permissions,
                 source.generic_string(), 0,
                 "rule file must not be writable");
        ::close(fd);
        return std::nullopt;
    }
    if (st.st_size < 0 ||
        static_cast<std::uintmax_t>(st.st_size) > policy.max_file_size) {
        addError(errors, RuleErrorCode::file_too_large, source.generic_string(),
                 0, "rule file exceeds the size limit");
        ::close(fd);
        return std::nullopt;
    }
    const auto declared_size = static_cast<std::size_t>(st.st_size);
    if (declared_size > policy.max_total_size - total_bytes) {
        addError(errors, RuleErrorCode::file_too_large, source.generic_string(),
                 0, "rule bundle exceeds the total size limit");
        ::close(fd);
        return std::nullopt;
    }

    std::string content;
    content.resize(declared_size);
    std::size_t offset = 0;
    while (offset < content.size()) {
        const auto got = ::read(fd, content.data() + offset,
                                content.size() - offset);
        if (got < 0) {
            if (errno == EINTR) {
                continue;
            }
            addError(errors, RuleErrorCode::io_error, source.generic_string(),
                     0, "cannot read rule file: " +
                         std::string(std::strerror(errno)));
            ::close(fd);
            return std::nullopt;
        }
        if (got == 0) {
            break;
        }
        offset += static_cast<std::size_t>(got);
    }
    content.resize(offset);
    ::close(fd);
    total_bytes += content.size();
    return content;
}

struct TemplateExpansion {
    bool ok = false;
    std::string value;
    std::string error;
};

TemplateExpansion expandTemplate(std::string_view raw,
                                 const DeviceEvent& event,
                                 const RuleFilePolicy& policy) {
    TemplateExpansion result;
    const auto parsed = parseTemplate(raw, policy);
    if (!parsed.ok) {
        result.error = parsed.error;
        return result;
    }

    for (const auto& part : parsed.parts) {
        if (!part.token) {
            result.value += part.value;
            continue;
        }

        std::optional<std::string> value;
        if (part.value == "ACTION") {
            value = event.action;
        } else if (part.value == "SUBSYSTEM") {
            value = event.subsystem;
        } else if (part.value == "KERNEL") {
            value = event.kernel;
        } else if (part.value == "DEVPATH") {
            value = event.devpath;
        } else if (part.value == "DEVNAME") {
            value = event.devname;
        } else if (part.value == "DRIVER") {
            value = event.driver;
        } else if (part.value == "SYSNAME") {
            value = event.sysname;
        } else if (part.value == "SYSPATH") {
            value = event.syspath;
        } else if (part.value == "SEQNUM") {
            value = std::to_string(event.seqnum);
        } else if (part.value.starts_with("ATTR:")) {
            const auto found = event.attributes.find(part.value.substr(5));
            if (found != event.attributes.end()) {
                value = found->second;
            }
        } else if (part.value.starts_with("ENV:")) {
            const auto name = part.value.substr(4);
            if (!policy.allowed_event_environment.contains(name)) {
                result.error = "environment source is not allowed: " + name;
                return result;
            }
            const auto found = event.environment.find(name);
            if (found != event.environment.end()) {
                value = found->second;
            }
        }

        if (!value.has_value()) {
            result.error = "event does not provide placeholder {" +
                           part.value + "}";
            return result;
        }
        if (!isSafeEventValue(*value)) {
            result.error = "unsafe value for placeholder {" + part.value + "}";
            return result;
        }
        result.value += *value;
        if (result.value.size() > kMaxExpandedValueLength) {
            result.error = "expanded value is too long";
            return result;
        }
    }

    if (parsed.parts.size() == 1 && parsed.parts.front().token &&
        result.value.starts_with('-')) {
        result.error = "an event value cannot become an option argument";
        return result;
    }
    if (!isSafeEventValue(result.value)) {
        result.error = "expanded value contains unsafe characters";
        return result;
    }
    result.ok = true;
    return result;
}

bool validateEvent(const DeviceEvent& event,
                   const RuleFilePolicy& policy,
                   std::vector<RuleError>& errors) {
    const auto reject = [&](std::string message) {
        addError(errors, RuleErrorCode::event_rejected, "<device-event>", 0,
                 std::move(message));
        return false;
    };

    if (!isKnownAction(event.action)) {
        return reject("ACTION is not a recognised udev action");
    }
    const auto scalar_ok = [](const std::string& value) {
        return value.empty() || isSafeEventValue(value);
    };
    if (!scalar_ok(event.subsystem) || !scalar_ok(event.kernel) ||
        !scalar_ok(event.devpath) || !scalar_ok(event.devname) ||
        !scalar_ok(event.driver) || !scalar_ok(event.sysname) ||
        !scalar_ok(event.syspath)) {
        return reject("device event contains an unsafe field");
    }
    for (const auto& name : policy.allowed_event_environment) {
        const auto found = event.environment.find(name);
        if (found != event.environment.end() &&
            !isSafeEventValue(found->second)) {
            return reject("device event environment contains an unsafe value "
                          "for " +
                          name);
        }
    }
    return true;
}

std::optional<std::string> conditionValue(const MatchCondition& condition,
                                          const DeviceEvent& event) {
    if (condition.field == "ACTION") {
        return event.action;
    }
    if (condition.field == "SUBSYSTEM") {
        return event.subsystem;
    }
    if (condition.field == "KERNEL") {
        return event.kernel;
    }
    if (condition.field == "DEVPATH") {
        return event.devpath;
    }
    if (condition.field == "DEVNAME") {
        return event.devname;
    }
    if (condition.field == "DRIVER") {
        return event.driver;
    }
    if (condition.field == "SYSNAME") {
        return event.sysname;
    }
    if (condition.field == "SYSPATH") {
        return event.syspath;
    }
    if (condition.field == "SEQNUM") {
        return std::to_string(event.seqnum);
    }
    if (condition.field.starts_with("ATTR:")) {
        const auto found = event.attributes.find(condition.field.substr(5));
        if (found != event.attributes.end()) {
            return found->second;
        }
        return std::nullopt;
    }
    if (condition.field.starts_with("ENV:")) {
        const auto found = event.environment.find(condition.field.substr(4));
        if (found != event.environment.end()) {
            return found->second;
        }
        return std::nullopt;
    }
    return std::nullopt;
}

bool ruleMatches(const Rule& rule, const DeviceEvent& event) {
    for (const auto& condition : rule.conditions) {
        const auto actual = conditionValue(condition, event);
        if (!actual.has_value()) {
            return false;
        }
        if (actual->size() > kMaxExpandedValueLength) {
            return false;
        }
        const bool equal = globMatch(condition.value, *actual);
        if (condition.negated ? equal : !equal) {
            return false;
        }
    }
    return true;
}

}  // namespace

std::string_view toString(RuleErrorCode code) noexcept {
    switch (code) {
        case RuleErrorCode::none:
            return "none";
        case RuleErrorCode::io_error:
            return "io_error";
        case RuleErrorCode::insecure_path:
            return "insecure_path";
        case RuleErrorCode::insecure_owner:
            return "insecure_owner";
        case RuleErrorCode::insecure_permissions:
            return "insecure_permissions";
        case RuleErrorCode::file_too_large:
            return "file_too_large";
        case RuleErrorCode::limit_exceeded:
            return "limit_exceeded";
        case RuleErrorCode::invalid_syntax:
            return "invalid_syntax";
        case RuleErrorCode::unknown_keyword:
            return "unknown_keyword";
        case RuleErrorCode::invalid_field:
            return "invalid_field";
        case RuleErrorCode::invalid_value:
            return "invalid_value";
        case RuleErrorCode::command_not_allowed:
            return "command_not_allowed";
        case RuleErrorCode::environment_not_allowed:
            return "environment_not_allowed";
        case RuleErrorCode::conflicting_policy:
            return "conflicting_policy";
        case RuleErrorCode::event_rejected:
            return "event_rejected";
    }
    return "unknown";
}

const CommandPolicy* RuleSet::findCommand(std::string_view name) const noexcept {
    const auto found = std::find_if(
        commands.begin(), commands.end(),
        [name](const CommandPolicy& command) { return command.name == name; });
    return found == commands.end() ? nullptr : &*found;
}

RuleParser::RuleParser(std::vector<CommandPolicy> commands,
                       RuleFilePolicy file_policy)
    : commands_(std::move(commands)), file_policy_(std::move(file_policy)) {}

ParseResult RuleParser::parse(std::string_view text,
                              std::string source_name) const {
    ParseResult result;
    result.errors = validateCommands(commands_, file_policy_);
    if (!result.errors.empty()) {
        return result;
    }

    std::size_t line_number = 0;
    while (!text.empty()) {
        ++line_number;
        const auto newline = text.find('\n');
        auto line = text.substr(0, newline);
        if (newline == std::string_view::npos) {
            text = {};
        } else {
            text.remove_prefix(newline + 1);
        }
        if (!line.empty() && line.back() == '\r') {
            line.remove_suffix(1);
        }

        if (line.size() > file_policy_.max_line_length) {
            addError(result.errors, RuleErrorCode::limit_exceeded, source_name,
                     line_number, "rule line exceeds the length limit");
            continue;
        }
        const auto stripped = trim(line);
        if (stripped.empty() || stripped.front() == '#') {
            continue;
        }
        if (result.rules.size() >= file_policy_.max_rules) {
            addError(result.errors, RuleErrorCode::limit_exceeded, source_name,
                     line_number, "rule bundle exceeds the rule limit");
            break;
        }

        bool split_ok = false;
        std::string split_error;
        const auto clauses = splitClauses(stripped, split_ok, split_error);
        if (!split_ok) {
            addError(result.errors, RuleErrorCode::invalid_syntax, source_name,
                     line_number, std::move(split_error));
            continue;
        }

        Rule rule;
        rule.source = source_name;
        rule.line = line_number;
        bool line_ok = true;
        std::optional<std::string> run_value;

        for (const auto clause : clauses) {
            const auto parsed = parseClause(clause);
            if (!parsed.ok) {
                addError(result.errors, RuleErrorCode::invalid_syntax,
                         source_name, line_number, parsed.error);
                line_ok = false;
                continue;
            }
            const auto field = parseField(parsed.lhs, file_policy_);
            if (!field.ok) {
                addError(result.errors, field.code, source_name, line_number,
                         field.error);
                line_ok = false;
                continue;
            }

            if (field.field == "RUN") {
                if (parsed.op != "+=") {
                    addError(result.errors, RuleErrorCode::invalid_syntax,
                             source_name, line_number,
                             "RUN must use the += operator");
                    line_ok = false;
                    continue;
                }
                if (run_value.has_value()) {
                    addError(result.errors, RuleErrorCode::invalid_syntax,
                             source_name, line_number,
                             "a rule may contain only one RUN clause");
                    line_ok = false;
                    continue;
                }
                run_value = parsed.value;
                continue;
            }

            if (parsed.lhs.starts_with("ENV{") &&
                (parsed.op == "=" || parsed.op == "+=")) {
                addError(result.errors,
                         RuleErrorCode::environment_not_allowed, source_name,
                         line_number,
                         "rules cannot assign environment values; use a "
                         "CommandPolicy environment template");
                line_ok = false;
                continue;
            }
            if (parsed.op != "==" && parsed.op != "!=") {
                addError(result.errors, RuleErrorCode::invalid_syntax,
                         source_name, line_number,
                         "match clauses must use == or !=");
                line_ok = false;
                continue;
            }

            std::string value_error;
            if (!isValidMatchValue(parsed.value, field.field, value_error)) {
                addError(result.errors, RuleErrorCode::invalid_value,
                         source_name, line_number, std::move(value_error));
                line_ok = false;
                continue;
            }
            rule.conditions.push_back(
                MatchCondition{field.field, parsed.op == "!=", parsed.value});
        }

        if (rule.conditions.size() > file_policy_.max_conditions_per_rule) {
            addError(result.errors, RuleErrorCode::limit_exceeded, source_name,
                     line_number, "too many match conditions in one rule");
            line_ok = false;
        }
        if (!run_value.has_value()) {
            addError(result.errors, RuleErrorCode::invalid_syntax, source_name,
                     line_number, "a rule must contain exactly one RUN clause");
            line_ok = false;
        } else {
            const auto resolved = resolveRun(*run_value, commands_);
            if (!resolved.ok) {
                addError(result.errors, resolved.code, source_name, line_number,
                         resolved.error);
                line_ok = false;
            } else {
                rule.command_name = resolved.command;
            }
        }

        if (line_ok) {
            result.rules.push_back(std::move(rule));
        }
    }

    result.ok = result.errors.empty();
    return result;
}

RuleFileLoader::RuleFileLoader(std::filesystem::path directory,
                               std::vector<CommandPolicy> commands,
                               RuleFilePolicy file_policy)
    : directory_(std::move(directory)),
      commands_(std::move(commands)),
      parser_(commands_, file_policy),
      file_policy_(std::move(file_policy)) {}

LoadResult RuleFileLoader::load(Generation generation) const {
    LoadResult result;
    std::vector<RuleError> errors;

    const auto policy_probe = parser_.parse("", "<command-policy>");
    if (!policy_probe.ok) {
        result.errors = policy_probe.errors;
        return result;
    }

    std::filesystem::path canonical;
    if (!canonicalSecureDirectory(directory_, file_policy_, canonical, errors)) {
        result.errors = std::move(errors);
        return result;
    }
    const int directory_fd =
        openSecureDirectory(canonical, file_policy_, errors);
    if (directory_fd < 0) {
        result.errors = std::move(errors);
        return result;
    }

    DIR* raw_directory = ::fdopendir(directory_fd);
    if (raw_directory == nullptr) {
        addError(errors, RuleErrorCode::io_error, canonical.generic_string(), 0,
                 "cannot enumerate rule directory: " +
                     std::string(std::strerror(errno)));
        ::close(directory_fd);
        result.errors = std::move(errors);
        return result;
    }
    const auto close_directory = [](DIR* directory) {
        if (directory != nullptr) {
            ::closedir(directory);
        }
    };
    std::unique_ptr<DIR, decltype(close_directory)> listing(raw_directory,
                                                           close_directory);
    const int list_fd = ::dirfd(listing.get());

    std::vector<std::string> names;
    errno = 0;
    while (const auto* entry = ::readdir(listing.get())) {
        const std::string name = entry->d_name;
        if (name == "." || name == "..") {
            continue;
        }
        if (name.size() >= 6 && name.compare(name.size() - 6, 6, ".rules") == 0) {
            names.push_back(name);
        }
    }
    if (errno != 0) {
        addError(errors, RuleErrorCode::io_error, canonical.generic_string(), 0,
                 "cannot enumerate rule directory: " +
                     std::string(std::strerror(errno)));
        result.errors = std::move(errors);
        return result;
    }
    std::sort(names.begin(), names.end());

    auto rule_set = std::make_shared<RuleSet>();
    rule_set->generation = generation;
    rule_set->source_directory = canonical;
    rule_set->commands = commands_;
    std::size_t total_bytes = 0;

    for (const auto& name : names) {
        const auto source = canonical / name;
        auto content = readRuleFile(list_fd, name, source, file_policy_, errors,
                                    total_bytes);
        if (!content.has_value()) {
            continue;
        }
        const auto parsed = parser_.parse(*content, source.generic_string());
        if (!parsed.ok) {
            errors.insert(errors.end(), parsed.errors.begin(),
                          parsed.errors.end());
            continue;
        }
        rule_set->rules.insert(rule_set->rules.end(), parsed.rules.begin(),
                               parsed.rules.end());
    }

    if (rule_set->rules.size() > file_policy_.max_rules) {
        addError(errors, RuleErrorCode::limit_exceeded, canonical.generic_string(),
                 0, "rule bundle exceeds the rule limit");
    }
    if (!errors.empty()) {
        result.errors = std::move(errors);
        return result;
    }

    result.ok = true;
    result.generation = generation;
    result.rule_count = rule_set->rules.size();
    result.snapshot = std::move(rule_set);
    return result;
}

RuleEngine::RuleEngine(std::filesystem::path rule_directory,
                       std::vector<CommandPolicy> commands,
                       RuleFilePolicy file_policy)
    : loader_(std::move(rule_directory), std::move(commands), file_policy),
      file_policy_(std::move(file_policy)) {}

LoadResult RuleEngine::load() {
    std::lock_guard reload_lock(reload_mutex_);
    Generation next = 1;
    {
        std::lock_guard state_lock(state_mutex_);
        if (active_) {
            if (active_->generation == std::numeric_limits<Generation>::max()) {
                LoadResult result;
                addError(result.errors, RuleErrorCode::limit_exceeded,
                         "<rule-engine>", 0,
                         "rule generation counter is exhausted");
                return result;
            }
            next = active_->generation + 1;
        }
    }

    auto result = loader_.load(next);
    if (!result.ok || !result.snapshot) {
        return result;
    }

    std::lock_guard state_lock(state_mutex_);
    active_ = result.snapshot;
    return result;
}

LoadResult RuleEngine::reload() {
    return load();
}

Evaluation RuleEngine::evaluate(const DeviceEvent& event) const {
    std::shared_ptr<const RuleSet> snapshot;
    {
        std::lock_guard state_lock(state_mutex_);
        snapshot = active_;
    }
    if (!snapshot) {
        Evaluation result;
        result.event_id = event.event_id;
        addError(result.errors, RuleErrorCode::event_rejected,
                 "<rule-engine>", 0,
                 "no generation has been loaded; refusing to execute");
        return result;
    }
    return evaluateWith(event, *snapshot);
}

Evaluation RuleEngine::evaluate(const DeviceEvent& event,
                                const RuleSet& bound_snapshot) const {
    return evaluateWith(event, bound_snapshot);
}

Evaluation RuleEngine::evaluateWith(const DeviceEvent& event,
                                    const RuleSet& rules) const {
    Evaluation result;
    result.generation = rules.generation;
    result.event_id = event.event_id;

    if (rules.generation == kNoGeneration) {
        addError(result.errors, RuleErrorCode::event_rejected, "<rule-set>", 0,
                 "event cannot be bound to an ungenerated rule set");
        return result;
    }
    if (!validateEvent(event, file_policy_, result.errors)) {
        return result;
    }

    for (const auto& rule : rules.rules) {
        if (!ruleMatches(rule, event)) {
            continue;
        }
        const auto* command = rules.findCommand(rule.command_name);
        if (command == nullptr) {
            addError(result.errors, RuleErrorCode::command_not_allowed,
                     rule.source, rule.line,
                     "rule references a missing command policy");
            continue;
        }

        std::vector<std::string> argv;
        argv.push_back(command->executable.generic_string());
        bool expansion_ok = true;
        for (const auto& argument : command->argument_template) {
            auto expanded = expandTemplate(argument, event, file_policy_);
            if (!expanded.ok) {
                addError(result.errors, RuleErrorCode::event_rejected,
                         rule.source, rule.line, std::move(expanded.error));
                expansion_ok = false;
                break;
            }
            argv.push_back(std::move(expanded.value));
        }
        if (!expansion_ok) {
            continue;
        }

        std::map<std::string, std::string> environment;
        for (const auto& [name, value] : command->environment_template) {
            auto expanded = expandTemplate(value, event, file_policy_);
            if (!expanded.ok) {
                addError(result.errors, RuleErrorCode::event_rejected,
                         rule.source, rule.line, std::move(expanded.error));
                expansion_ok = false;
                break;
            }
            environment.emplace(name, std::move(expanded.value));
        }
        if (!expansion_ok) {
            continue;
        }

        CommandInvocation invocation;
        invocation.generation = rules.generation;
        invocation.event_id = event.event_id;
        invocation.rule_source = rule.source;
        invocation.rule_line = rule.line;
        invocation.command_name = rule.command_name;
        invocation.executable = command->executable.generic_string();
        invocation.argv = std::move(argv);
        invocation.environment = std::move(environment);
        result.commands.push_back(std::move(invocation));
    }

    // Reject the event as a unit rather than running a safe-looking prefix of
    // rules after one rule failed to expand untrusted event data.
    if (!result.errors.empty()) {
        result.commands.clear();
    }
    result.ok = result.errors.empty();
    return result;
}

Generation RuleEngine::activeGeneration() const {
    std::lock_guard state_lock(state_mutex_);
    return active_ ? active_->generation : kNoGeneration;
}

std::size_t RuleEngine::ruleCount() const {
    std::lock_guard state_lock(state_mutex_);
    return active_ ? active_->rules.size() : 0;
}

std::shared_ptr<const RuleSet> RuleEngine::snapshot() const {
    std::lock_guard state_lock(state_mutex_);
    return active_;
}

std::string LoadResult::summary() const {
    if (ok) {
        return "loaded " + std::to_string(rule_count) + " rule(s) as generation " +
               std::to_string(generation);
    }
    std::ostringstream stream;
    stream << "rule load failed";
    if (!errors.empty()) {
        stream << " (" << toString(errors.front().code) << "): "
               << errors.front().message;
        if (!errors.front().path.empty()) {
            stream << " [" << errors.front().path;
            if (errors.front().line != 0) {
                stream << ':' << errors.front().line;
            }
            stream << ']';
        }
    }
    return stream.str();
}

}  // namespace netsec
