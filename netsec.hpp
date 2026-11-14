#pragma once

#include <cstdint>
#include <functional>
#include <map>
#include <string>
#include <string_view>
#include <vector>

namespace netsec {

// Service SID placement, mirroring the Windows SERVICE_SID_TYPE_* values.
enum class ServiceSidType {
    Unknown = 0,
    None,
    Restricted,
    Unrestricted,
};

// Failure actions, mirroring the Windows SC_ACTION_* values.
enum class RecoveryActionType {
    Unknown = 0,
    None,
    Restart,
    Reboot,
    RunCommand,
};

// Stable violation codes so callers can react programmatically.
enum class Violation {
    None = 0,
    EmptyImagePath,
    UnterminatedQuote,
    UnquotedImagePath,
    RelativeImagePath,
    UnresolvedVariable,
    ImagePathInUserWritableDirectory,
    ImagePathIsDirectory,
    InvalidImagePath,
    InvalidServiceName,
    InvalidServiceAccount,
    InvalidServiceDependency,
    DuplicateServiceDependency,
    InvalidServiceSidType,
    InvalidRecoveryAction,
    InvalidRecoveryCommand,
};

struct RecoveryAction {
    RecoveryActionType type{RecoveryActionType::Unknown};
    // Full command line (quoted image path + arguments); used only for RunCommand.
    std::string command;
};

// Full service configuration as it would be persisted by the SCM.
struct ServiceConfig {
    // ImagePath registry value: quoted executable path followed by arguments.
    std::string imageCommandLine;
    // Run-as account, e.g. "LocalSystem", "NT AUTHORITY\\NetworkService",
    // ".\\svcAcct", "DOMAIN\\gMSA$" or "svc@corp.example".
    std::string accountName;
    std::vector<std::string> dependencies;
    ServiceSidType sidType{ServiceSidType::Unknown};
    std::vector<RecoveryAction> recoveryActions;
    // Seconds before failure counters reset; 0 means never reset.
    std::uint32_t resetPeriodSeconds{0};
};

struct ParsedImagePath {
    std::string executablePath;
    std::string arguments;
    bool quoted{false};
};

struct ViolationEntry {
    Violation code{Violation::None};
    std::string message;
};

struct ValidationResult {
    std::vector<ViolationEntry> violations;
    ParsedImagePath parsed;

    bool ok() const noexcept { return violations.empty(); }
    bool has(Violation code) const noexcept;
};

struct ValidationOptions {
    // Extra directories treated as user-writable in addition to the defaults.
    std::vector<std::string> additionalWritableRoots;
    // When true, a service must declare at least one concrete recovery action.
    bool requireRecoveryActions{false};
};

// Maps SERVICE_SID_TYPE_* raw integers; out-of-range values become Unknown.
ServiceSidType serviceSidTypeFromInt(int raw) noexcept;

// Default set of directories considered writable by unprivileged users.
std::vector<std::string> defaultUserWritableRoots();

// Parses an ImagePath value into executable path and arguments.
// Returns false only for an empty value, a missing closing quote or an empty
// quoted segment; an unquoted value still parses so the validator can report it.
bool parseImageCommandLine(std::string_view commandLine, ParsedImagePath& out);

// Host-independent Windows path helpers.
bool containsUnresolvedVariable(std::string_view path) noexcept;
bool isAbsoluteWindowsPath(std::string_view path) noexcept;
std::string normalizeWindowsPath(std::string_view path);
bool isPathWithinRoot(std::string_view normalizedPath,
                      std::string_view normalizedRoot) noexcept;

// Ordered list of paths CreateProcess probes for an unquoted first token,
// e.g. "C:\\Program Files\\App\\s.exe" yields "C:\\Program",
// "C:\\Program.exe", "C:\\Program Files\\App", ... ending at the full token.
std::vector<std::string> unquotedSearchCandidates(std::string_view firstToken);

using FileExistsFn = std::function<bool(const std::string& path)>;

// Simulates which program the SCM would actually launch. Quoted command lines
// launch the quoted path; unquoted ones follow Windows prefix probing and can
// therefore be hijacked by an earlier "Program.exe". Returns "" if no probed
// path exists. The predicate models the on-disk state for host-independent
// testing.
std::string resolveLaunchedProgram(std::string_view commandLine,
                                   const FileExistsFn& exists);

// Single validation entry point shared by install and update flows.
ValidationResult validateServiceConfig(const ServiceConfig& config,
                                       const ValidationOptions& options = {});

const char* toString(Violation code) noexcept;
const char* toString(ServiceSidType type) noexcept;
const char* toString(RecoveryActionType type) noexcept;

// Persistence boundary. A production implementation forwards to the SCM/registry;
// write() is only ever invoked after validation has succeeded.
class IServiceStore {
public:
    virtual ~IServiceStore() = default;
    virtual bool exists(std::string_view serviceName) const = 0;
    virtual bool write(const std::string& serviceName,
                       const ServiceConfig& config) = 0;
};

// In-memory store, useful as a reference implementation and for tests.
class MemoryServiceStore final : public IServiceStore {
public:
    bool exists(std::string_view serviceName) const override;
    bool write(const std::string& serviceName,
               const ServiceConfig& config) override;

    const ServiceConfig* get(std::string_view serviceName) const;
    std::size_t writeCount() const noexcept { return writeCount_; }
    const std::string& lastWrittenName() const noexcept { return lastWrittenName_; }
    void clear();

private:
    std::map<std::string, ServiceConfig> services_;
    std::size_t writeCount_{0};
    std::string lastWrittenName_;
};

enum class StoreStatus {
    Ok = 0,
    ValidationRejected,
    AlreadyExists,
    NotFound,
    InvalidName,
    StoreFailure,
};

struct OperationResult {
    StoreStatus status{StoreStatus::StoreFailure};
    ValidationResult validation;

    bool ok() const noexcept { return status == StoreStatus::Ok; }
};

// Service management facade. install() and update() run the identical
// validation and refuse to touch the store when it fails.
class ServiceManager {
public:
    explicit ServiceManager(IServiceStore& store,
                            ValidationOptions options = {});

    OperationResult install(std::string_view serviceName,
                            const ServiceConfig& config);
    OperationResult update(std::string_view serviceName,
                           const ServiceConfig& config);

    const ValidationOptions& options() const noexcept { return options_; }

private:
    OperationResult validateAndWrite(std::string_view serviceName,
                                     const ServiceConfig& config,
                                     bool isUpdate);

    IServiceStore& store_;
    ValidationOptions options_;
};

}  // namespace netsec
