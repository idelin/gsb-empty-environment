#include "netsec.hpp"

#include <algorithm>
#include <array>
#include <cctype>
#include <optional>
#include <set>
#include <utility>

namespace netsec {
namespace {

constexpr char kBackslash = static_cast<char>(0x5C);  // 0x5C == backslash

char asciiLower(char ch) noexcept {
    return static_cast<char>(std::tolower(static_cast<unsigned char>(ch)));
}

std::string toLower(std::string value) {
    std::transform(value.begin(), value.end(), value.begin(), asciiLower);
    return value;
}

std::string trim(std::string value) {
    const auto isSpace = [](unsigned char ch) { return std::isspace(ch) != 0; };
    while (!value.empty() && isSpace(static_cast<unsigned char>(value.front()))) {
        value.erase(value.begin());
    }
    while (!value.empty() && isSpace(static_cast<unsigned char>(value.back()))) {
        value.pop_back();
    }
    return value;
}

bool isDriveLetter(char ch) noexcept {
    return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z');
}

// Splits "C:\a\b" or "\\server\share\dir" into a root prefix and components.
struct PathParts {
    std::string root;
    std::vector<std::string> components;
};

std::optional<PathParts> splitWindowsPath(std::string_view path) {
    PathParts parts;
    std::size_t pos = 0;

    if (path.size() >= 2 && isDriveLetter(path[0]) && path[1] == ':') {
        parts.root = std::string(path.substr(0, 2));  // e.g. "C:"
        pos = 2;
        if (pos >= path.size() || (path[pos] != kBackslash && path[pos] != '/')) {
            return std::nullopt;  // drive-relative paths are not absolute
        }
        ++pos;
    } else if (path.size() >= 2 && path[0] == kBackslash &&
               path[1] == kBackslash) {
        // UNC: \\server\share[\rest]
        std::size_t firstSep = path.find_first_of("\\/", 2);
        if (firstSep == std::string_view::npos) {
            return std::nullopt;
        }
        std::size_t secondSep = path.find_first_of("\\/", firstSep + 1);
        std::size_t rootEnd =
            secondSep == std::string_view::npos ? path.size() : secondSep;
        parts.root = std::string(path.substr(0, rootEnd));  // \\server\share
        pos = rootEnd;
        if (secondSep != std::string_view::npos) {
            ++pos;
        }
    } else {
        return std::nullopt;
    }

    while (pos < path.size()) {
        std::size_t next = path.find_first_of("\\/", pos);
        std::string_view segment = path.substr(
            pos, next == std::string_view::npos ? std::string_view::npos
                                                : next - pos);
        if (!segment.empty() && segment != ".") {
            parts.components.emplace_back(segment);
        }
        if (next == std::string_view::npos) {
            break;
        }
        pos = next + 1;
    }
    return parts;
}

// Re-assembles normalized parts (lowercase, backslashes, resolved "."/"..").
std::string joinWindowsPath(const PathParts& parts) {
    std::string result = toLower(parts.root);
    bool driveRoot = parts.root.size() == 2 && parts.root[1] == ':';

    std::vector<std::string> resolved;
    for (const std::string& component : parts.components) {
        if (component == "..") {
            if (!resolved.empty()) {
                resolved.pop_back();
            }
            continue;
        }
        resolved.push_back(component);
    }

    for (const std::string& component : resolved) {
        // Drive roots ("c:") need an initial separator; UNC roots already
        // end with the separator after the share.
        if (driveRoot || result.back() != kBackslash) {
            result += kBackslash;
        }
        result += toLower(component);
    }
    if (driveRoot && resolved.empty()) {
        result += kBackslash;  // bare "C:\" root
    }
    return result;
}

bool isTokenChar(char ch) noexcept {
    return ch != ' ' && ch != static_cast<char>(0x09);  // 0x09 == '\t'
}

bool isValidAccountNameCharacter(char ch) noexcept {
    return std::isalnum(static_cast<unsigned char>(ch)) != 0 ||
           ch == '.' || ch == '-' || ch == '_' || ch == '$';
}

bool isWellKnownAccount(std::string_view account) {
    static constexpr std::array<std::string_view, 12> known = {
        "localsystem",
        "nt authority\\system",
        "nt authority\\localservice",
        "nt authority\\networkservice",
        "local service",
        "network service",
        ".\\localsystem",
        ".\\localservice",
        ".\\networkservice",
        "system",
        "localservice",
        "networkservice",
    };
    std::string lower = toLower(std::string(account));
    return std::find(known.begin(), known.end(), std::string_view(lower)) !=
           known.end();
}

bool validUpn(const std::string& account) {
    std::size_t at = account.find('@');
    if (at == std::string::npos || at == 0 ||
        account.find('@', at + 1) != std::string::npos) {
        return false;
    }
    std::string user = account.substr(0, at);
    std::string domain = account.substr(at + 1);
    if (user.empty() || domain.empty() || domain.find('.') == std::string::npos) {
        return false;
    }
    if (std::any_of(user.begin(), user.end(), [](char ch) {
            return !isValidAccountNameCharacter(ch);
        })) {
        return false;
    }
    std::string label;
    for (char ch : domain) {
        if (ch == '.') {
            if (label.empty()) {
                return false;
            }
            label.clear();
            continue;
        }
        if (!std::isalnum(static_cast<unsigned char>(ch)) && ch != '-') {
            return false;
        }
        label.push_back(ch);
    }
    return !label.empty();
}

bool validDownLevelName(const std::string& account) {
    std::size_t slash = account.find('\\');
    if (slash == std::string::npos || slash == 0 ||
        account.find('\\', slash + 1) != std::string::npos) {
        return false;
    }
    std::string domain = account.substr(0, slash);
    std::string user = account.substr(slash + 1);
    if (domain.empty() || user.empty()) {
        return false;
    }
    const auto validDomainChar = [](char ch) {
        return std::isalnum(static_cast<unsigned char>(ch)) != 0 ||
               ch == '.' || ch == '-';
    };
    if (std::any_of(domain.begin(), domain.end(),
                    [&](char ch) { return !validDomainChar(ch); })) {
        return false;
    }
    if (std::any_of(user.begin(), user.end(), [](char ch) {
            return !isValidAccountNameCharacter(ch);
        })) {
        return false;
    }
    return true;
}

void addViolation(ValidationResult& result, Violation code, std::string message) {
    result.violations.push_back(ViolationEntry{code, std::move(message)});
}

// Validates the executable portion of a command line (service image or a
// RunCommand recovery action). Parsing output is only populated for the
// top-level image so the caller can keep the parsed representation.
void validateExecutableCommandLine(std::string_view commandLine,
                                   const ValidationOptions& options,
                                   bool populateParsed,
                                   ValidationResult& result) {
    ParsedImagePath parsed;
    if (!parseImageCommandLine(commandLine, parsed)) {
        if (commandLine.empty() || trim(std::string(commandLine)).empty()) {
            addViolation(result, Violation::EmptyImagePath,
                         "service image path must not be empty");
        } else {
            std::size_t firstQuote =
                commandLine.find('"');  // leading whitespace already tolerated
            bool hasClosingQuote =
                firstQuote != std::string_view::npos &&
                commandLine.find('"', firstQuote + 1) !=
                    std::string_view::npos;
            if (!hasClosingQuote) {
                addViolation(result, Violation::UnterminatedQuote,
                             "service image path has an unterminated quote");
            } else {
                addViolation(result, Violation::InvalidImagePath,
                             "service image path does not contain an "
                             "executable");
            }
        }
        return;
    }

    if (populateParsed) {
        result.parsed = parsed;
    }

    if (!parsed.quoted) {
        addViolation(result, Violation::UnquotedImagePath,
                     "executable path must be quoted and kept separate from "
                     "arguments: " +
                         parsed.executablePath);
    }
    if (containsUnresolvedVariable(parsed.executablePath)) {
        addViolation(result, Violation::UnresolvedVariable,
                     "executable path contains an unresolved variable: " +
                         parsed.executablePath);
    }
    if (!isAbsoluteWindowsPath(parsed.executablePath)) {
        addViolation(result, Violation::RelativeImagePath,
                     "executable path must be absolute and drive/UNC rooted: " +
                         parsed.executablePath);
        return;  // containment checks require an absolute path
    }

    char lastPathChar = parsed.executablePath.back();
    std::optional<PathParts> parts = splitWindowsPath(parsed.executablePath);
    if (!parts || parts->components.empty() ||
        parts->components.back() == ".." ||
        lastPathChar == kBackslash || lastPathChar == '/') {
        addViolation(result, Violation::ImagePathIsDirectory,
                     "executable path must name a file, not a directory: " +
                         parsed.executablePath);
        return;
    }

    std::string normalized = normalizeWindowsPath(parsed.executablePath);
    std::vector<std::string> roots = defaultUserWritableRoots();
    for (const std::string& extra : options.additionalWritableRoots) {
        roots.push_back(extra);
    }
    for (const std::string& root : roots) {
        std::string normalizedRoot = normalizeWindowsPath(root);
        if (!normalizedRoot.empty() &&
            isPathWithinRoot(normalized, normalizedRoot)) {
            addViolation(result, Violation::ImagePathInUserWritableDirectory,
                         "executable path resides in a user-writable directory ('" +
                             root + "'): " + parsed.executablePath);
            break;
        }
    }
}

}  // namespace

bool ValidationResult::has(Violation code) const noexcept {
    return std::any_of(violations.begin(), violations.end(),
                       [code](const ViolationEntry& entry) {
                           return entry.code == code;
                       });
}

ServiceSidType serviceSidTypeFromInt(int raw) noexcept {
    switch (raw) {
        case 0:
            return ServiceSidType::None;
        case 1:
            return ServiceSidType::Unrestricted;
        case 3:
            return ServiceSidType::Restricted;
        default:
            return ServiceSidType::Unknown;
    }
}

std::vector<std::string> defaultUserWritableRoots() {
    return {
        "C:\\Users",
        "C:\\ProgramData",
        "C:\\Windows\\Temp",
        "C:\\Windows\\Tasks",
        "C:\\Windows\\Debug",
        "C:\\Temp",
        "C:\\Tmp",
        "C:\\PerfLogs",
    };
}

bool parseImageCommandLine(std::string_view commandLine, ParsedImagePath& out) {
    out = ParsedImagePath{};
    std::size_t cursor = 0;
    while (cursor < commandLine.size() &&
           (commandLine[cursor] == ' ' ||
            commandLine[cursor] == static_cast<char>(0x09))) {
        ++cursor;
    }
    if (cursor >= commandLine.size()) {
        return false;
    }

    if (commandLine[cursor] == '"') {
        std::size_t closing = commandLine.find('"', cursor + 1);
        if (closing == std::string_view::npos) {
            out.quoted = true;
            return false;
        }
        std::string_view executable =
            commandLine.substr(cursor + 1, closing - cursor - 1);
        if (executable.empty()) {
            out.quoted = true;
            return false;
        }
        out.quoted = true;
        out.executablePath = std::string(executable);
        std::size_t argStart = closing + 1;
        while (argStart < commandLine.size() &&
               (commandLine[argStart] == ' ' ||
                commandLine[argStart] == static_cast<char>(0x09))) {
            ++argStart;
        }
        if (argStart < commandLine.size()) {
            out.arguments =
                std::string(trim(std::string(commandLine.substr(argStart))));
        }
        return true;
    }

    std::size_t firstSpace = cursor;
    while (firstSpace < commandLine.size() &&
           isTokenChar(commandLine[firstSpace])) {
        ++firstSpace;
    }
    if (firstSpace >= commandLine.size()) {
        firstSpace = std::string_view::npos;
    }
    out.executablePath = std::string(commandLine.substr(
        cursor, firstSpace == std::string_view::npos
                    ? std::string_view::npos
                    : firstSpace - cursor));
    if (out.executablePath.empty()) {
        return false;
    }
    if (firstSpace != std::string_view::npos) {
        out.arguments =
            std::string(trim(std::string(commandLine.substr(firstSpace + 1))));
    }
    return true;
}

bool containsUnresolvedVariable(std::string_view path) noexcept {
    std::size_t open = path.find('%');
    if (open == std::string_view::npos) {
        return false;
    }
    std::size_t close = path.find('%', open + 1);
    if (close == std::string_view::npos) {
        return true;  // dangling marker is itself unresolved
    }
    return close != open + 1;  // empty "%%" is treated as a literal pair
}

bool isAbsoluteWindowsPath(std::string_view path) noexcept {
    if (path.size() >= 2 && isDriveLetter(path[0]) && path[1] == ':') {
        return path.size() >= 3 &&
               (path[2] == kBackslash || path[2] == '/');
    }
    return path.size() >= 3 && path[0] == kBackslash &&
           path[1] == kBackslash && isTokenChar(path[2]);
}

std::string normalizeWindowsPath(std::string_view path) {
    std::optional<PathParts> parts = splitWindowsPath(path);
    if (!parts) {
        return {};
    }
    return joinWindowsPath(*parts);
}

bool isPathWithinRoot(std::string_view normalizedPath,
                      std::string_view normalizedRoot) noexcept {
    if (normalizedPath.size() < normalizedRoot.size()) {
        return false;
    }
    if (normalizedPath.substr(0, normalizedRoot.size()) != normalizedRoot) {
        return false;
    }
    if (normalizedPath.size() == normalizedRoot.size()) {
        return true;  // the root itself
    }
    return normalizedPath[normalizedRoot.size()] == kBackslash;
}

std::vector<std::string> unquotedSearchCandidates(std::string_view firstToken) {
    std::vector<std::string> candidates;
    const auto push = [&](std::string candidate) {
        if (std::find(candidates.begin(), candidates.end(), candidate) ==
            candidates.end()) {
            candidates.push_back(std::move(candidate));
        }
    };

    std::size_t pos = 0;
    while (pos < firstToken.size()) {
        std::size_t next = firstToken.find(kBackslash, pos);
        std::string prefix =
            std::string(firstToken.substr(0, next == std::string_view::npos
                                                  ? std::string_view::npos
                                                  : next));
        if (!prefix.empty()) {
            push(prefix);
            if (prefix.find('.') == std::string::npos) {
                push(prefix + ".exe");
            }
        }
        if (next == std::string_view::npos) {
            break;
        }
        pos = next + 1;
    }
    return candidates;
}

std::string resolveLaunchedProgram(std::string_view commandLine,
                                   const FileExistsFn& exists) {
    ParsedImagePath parsed;
    if (!parseImageCommandLine(commandLine, parsed)) {
        return {};
    }

    if (parsed.quoted) {
        return exists(parsed.executablePath) ? parsed.executablePath
                                             : std::string{};
    }

    for (const std::string& candidate :
         unquotedSearchCandidates(parsed.executablePath)) {
        if (exists(candidate)) {
            return candidate;
        }
    }
    return {};
}

ValidationResult validateServiceConfig(const ServiceConfig& config,
                                       const ValidationOptions& options) {
    ValidationResult result;

    validateExecutableCommandLine(config.imageCommandLine, options,
                                  /*populateParsed=*/true, result);

    std::string account = trim(config.accountName);
    if (account.empty()) {
        addViolation(result, Violation::InvalidServiceAccount,
                     "service account must not be empty");
    } else if (!isWellKnownAccount(account) &&
               !validDownLevelName(account) && !validUpn(account)) {
        addViolation(result, Violation::InvalidServiceAccount,
                     "service account must be a well-known principal, "
                     "DOMAIN\\user or a UPN: " +
                         account);
    }

    std::set<std::string> seenDependencies;
    for (const std::string& rawDependency : config.dependencies) {
        std::string dependency = trim(rawDependency);
        if (dependency.empty() || dependency.size() > 256 ||
            dependency.find('/') != std::string::npos ||
            dependency.find('\\') != std::string::npos) {
            addViolation(result, Violation::InvalidServiceDependency,
                         "service dependency name is invalid: '" +
                             rawDependency + "'");
            continue;
        }
        std::string key = toLower(dependency);
        if (!seenDependencies.insert(key).second) {
            addViolation(result, Violation::DuplicateServiceDependency,
                         "duplicate service dependency: " + dependency);
        }
    }

    if (config.sidType == ServiceSidType::Unknown) {
        addViolation(result, Violation::InvalidServiceSidType,
                     "service SID type must be None, Restricted or Unrestricted");
    }

    bool hasConcreteAction = false;
    for (const RecoveryAction& action : config.recoveryActions) {
        switch (action.type) {
            case RecoveryActionType::None:
            case RecoveryActionType::Restart:
            case RecoveryActionType::Reboot:
                hasConcreteAction =
                    hasConcreteAction || action.type != RecoveryActionType::None;
                if (!trim(action.command).empty()) {
                    addViolation(result, Violation::InvalidRecoveryCommand,
                                 "only RunCommand recovery actions carry a "
                                 "command line");
                }
                break;
            case RecoveryActionType::RunCommand: {
                hasConcreteAction = true;
                ValidationResult commandResult;
                validateExecutableCommandLine(action.command, options,
                                              /*populateParsed=*/false,
                                              commandResult);
                if (!commandResult.ok()) {
                    for (const ViolationEntry& violation :
                         commandResult.violations) {
                        addViolation(result, Violation::InvalidRecoveryCommand,
                                     "recovery command: " + violation.message);
                    }
                }
                break;
            }
            case RecoveryActionType::Unknown:
                addViolation(result, Violation::InvalidRecoveryAction,
                             "recovery action type must be None, Restart, "
                             "Reboot or RunCommand");
                break;
        }
    }
    if (options.requireRecoveryActions && !hasConcreteAction) {
        addViolation(result, Violation::InvalidRecoveryAction,
                     "at least one concrete recovery action is required");
    }

    return result;
}

const char* toString(Violation code) noexcept {
    switch (code) {
        case Violation::None:
            return "None";
        case Violation::EmptyImagePath:
            return "EmptyImagePath";
        case Violation::UnterminatedQuote:
            return "UnterminatedQuote";
        case Violation::UnquotedImagePath:
            return "UnquotedImagePath";
        case Violation::RelativeImagePath:
            return "RelativeImagePath";
        case Violation::UnresolvedVariable:
            return "UnresolvedVariable";
        case Violation::ImagePathInUserWritableDirectory:
            return "ImagePathInUserWritableDirectory";
        case Violation::ImagePathIsDirectory:
            return "ImagePathIsDirectory";
        case Violation::InvalidImagePath:
            return "InvalidImagePath";
        case Violation::InvalidServiceName:
            return "InvalidServiceName";
        case Violation::InvalidServiceAccount:
            return "InvalidServiceAccount";
        case Violation::InvalidServiceDependency:
            return "InvalidServiceDependency";
        case Violation::DuplicateServiceDependency:
            return "DuplicateServiceDependency";
        case Violation::InvalidServiceSidType:
            return "InvalidServiceSidType";
        case Violation::InvalidRecoveryAction:
            return "InvalidRecoveryAction";
        case Violation::InvalidRecoveryCommand:
            return "InvalidRecoveryCommand";
    }
    return "Unknown";
}

const char* toString(ServiceSidType type) noexcept {
    switch (type) {
        case ServiceSidType::None:
            return "None";
        case ServiceSidType::Restricted:
            return "Restricted";
        case ServiceSidType::Unrestricted:
            return "Unrestricted";
        case ServiceSidType::Unknown:
            return "Unknown";
    }
    return "Unknown";
}

const char* toString(RecoveryActionType type) noexcept {
    switch (type) {
        case RecoveryActionType::None:
            return "None";
        case RecoveryActionType::Restart:
            return "Restart";
        case RecoveryActionType::Reboot:
            return "Reboot";
        case RecoveryActionType::RunCommand:
            return "RunCommand";
        case RecoveryActionType::Unknown:
            return "Unknown";
    }
    return "Unknown";
}

bool MemoryServiceStore::exists(std::string_view serviceName) const {
    return services_.find(std::string(serviceName)) != services_.end();
}

bool MemoryServiceStore::write(const std::string& serviceName,
                               const ServiceConfig& config) {
    services_[serviceName] = config;
    ++writeCount_;
    lastWrittenName_ = serviceName;
    return true;
}

const ServiceConfig* MemoryServiceStore::get(
    std::string_view serviceName) const {
    auto it = services_.find(std::string(serviceName));
    return it == services_.end() ? nullptr : &it->second;
}

void MemoryServiceStore::clear() {
    services_.clear();
    writeCount_ = 0;
    lastWrittenName_.clear();
}

namespace {

bool validServiceName(std::string_view name) {
    if (name.empty() || name.size() > 256) {
        return false;
    }
    for (char ch : name) {
        if (ch == '/' || ch == '\\' || ch == '"' || ch == '[' ||
            ch == ']' || ch == ':' || ch == '|' || ch == '<' ||
            ch == '>' || ch == '+' || ch == '=' || ch == ';' ||
            ch == ',' || ch == '*' || ch == '?') {
            return false;
        }
    }
    return true;
}

}  // namespace

ServiceManager::ServiceManager(IServiceStore& store, ValidationOptions options)
    : store_(store), options_(std::move(options)) {}

OperationResult ServiceManager::validateAndWrite(
    std::string_view serviceName, const ServiceConfig& config, bool isUpdate) {
    OperationResult operation;

    if (!validServiceName(serviceName)) {
        operation.status = StoreStatus::InvalidName;
        addViolation(operation.validation, Violation::InvalidServiceName,
                     "service name is invalid: " + std::string(serviceName));
        return operation;
    }

    bool present = store_.exists(serviceName);
    if (isUpdate && !present) {
        operation.status = StoreStatus::NotFound;
        return operation;
    }
    if (!isUpdate && present) {
        operation.status = StoreStatus::AlreadyExists;
        return operation;
    }

    operation.validation = validateServiceConfig(config, options_);
    if (!operation.validation.ok()) {
        operation.status = StoreStatus::ValidationRejected;
        return operation;  // the store is never touched on failure
    }

    if (!store_.write(std::string(serviceName), config)) {
        operation.status = StoreStatus::StoreFailure;
        return operation;
    }
    operation.status = StoreStatus::Ok;
    return operation;
}

OperationResult ServiceManager::install(std::string_view serviceName,
                                        const ServiceConfig& config) {
    return validateAndWrite(serviceName, config, /*isUpdate=*/false);
}

OperationResult ServiceManager::update(std::string_view serviceName,
                                       const ServiceConfig& config) {
    return validateAndWrite(serviceName, config, /*isUpdate=*/true);
}

}  // namespace netsec
