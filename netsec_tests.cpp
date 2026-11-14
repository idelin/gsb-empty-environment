#include "netsec.hpp"

#include <algorithm>
#include <iostream>
#include <memory>
#include <string>
#include <vector>

using namespace netsec;

namespace {

int g_failures = 0;
int g_checks = 0;

void check(bool condition, const std::string& label) {
    ++g_checks;
    if (!condition) {
        ++g_failures;
        std::cerr << "FAIL: " << label << '\n';
    }
}

void checkHas(const ValidationResult& result, Violation code,
              const std::string& label) {
    check(result.has(code), label + " [" + toString(code) + "]");
}

FileExistsFn diskContaining(std::vector<std::string> present) {
    auto files = std::make_shared<std::vector<std::string>>(std::move(present));
    return [files](const std::string& path) {
        for (const std::string& candidate : *files) {
            if (candidate == path) {
                return true;
            }
        }
        return false;
    };
}

ServiceConfig baseConfig() {
    ServiceConfig config;
    config.imageCommandLine =
        "\"C:\\Program Files\\Vendor\\service.exe\" --serve";
    config.accountName = "LocalSystem";
    config.sidType = ServiceSidType::Unrestricted;
    config.dependencies = {"RpcSs", "Dnscache"};
    config.recoveryActions = {
        RecoveryAction{RecoveryActionType::Restart, ""},
        RecoveryAction{RecoveryActionType::Restart, ""},
    };
    config.resetPeriodSeconds = 86400;
    return config;
}

void test_unquotedPathRejected() {
    ServiceConfig config = baseConfig();
    config.imageCommandLine = "C:\\Program Files\\Vendor\\service.exe --serve";

    ValidationResult result = validateServiceConfig(config);
    check(!result.ok(), "unquoted spaced path is rejected");
    checkHas(result, Violation::UnquotedImagePath,
             "unquoted spaced path reports UnquotedImagePath");

    ParsedImagePath parsed;
    check(parseImageCommandLine(config.imageCommandLine, parsed),
          "unquoted line still parses for diagnostics");
    check(parsed.executablePath == "C:\\Program",
          "unquoted first token ends at the first space (the ambiguity root "
          "cause)");
    check(parsed.arguments == "Files\\Vendor\\service.exe --serve",
          "remainder after the first space is treated as arguments");
    check(!parsed.quoted, "parsed line flagged as unquoted");

    ServiceConfig noSpaces = baseConfig();
    noSpaces.imageCommandLine = "C:\\Windows\\System32\\svchost.exe -k netsvcs";
    ValidationResult noSpaceResult = validateServiceConfig(noSpaces);
    checkHas(noSpaceResult, Violation::UnquotedImagePath,
             "unquoted path without spaces is still rejected");
}

void test_sameNamedProgramPreemption() {
    // A single-token unquoted path: spaces would split it even earlier, but
    // backslash prefix probing alone already creates the hijack surface.
    const std::string declared = "C:\\ProgramFiles\\Vendor\\service.exe";
    const std::string hijacker = "C:\\ProgramFiles.exe";
    const std::string unquoted = declared + " --serve";
    const std::string quoted =
        "\"C:\\Program Files\\Vendor\\service.exe\" --serve";

    std::vector<std::string> candidates =
        unquotedSearchCandidates(declared);
    check(std::find(candidates.begin(), candidates.end(),
                    "C:\\ProgramFiles") != candidates.end(),
          "prefix without extension is among the unquoted probes");
    check(std::find(candidates.begin(), candidates.end(), hijacker) !=
              candidates.end(),
          "the hijackable same-named .exe is among the unquoted probes");
    check(candidates.back() == declared,
          "last unquoted probe is the declared program");
    check(std::find(candidates.begin(), candidates.end(), hijacker) <
              std::find(candidates.begin(), candidates.end(), declared),
          "the hijacker is probed before the declared program");

    FileExistsFn hijackedDisk = diskContaining({hijacker, declared});
    check(resolveLaunchedProgram(unquoted, hijackedDisk) == hijacker,
          "unquoted line launches the attacker's same-named program first");

    FileExistsFn cleanDisk = diskContaining({declared});
    check(resolveLaunchedProgram(unquoted, cleanDisk) == declared,
          "without a hijacker the unquoted line reaches the declared program");

    const std::string quotedDeclared =
        "C:\\Program Files\\Vendor\\service.exe";
    FileExistsFn spacedDisk =
        diskContaining({"C:\\Program.exe", quotedDeclared});
    check(resolveLaunchedProgram(quoted, spacedDisk) == quotedDeclared,
          "quoted line ignores C:\\Program.exe and launches declared "
          "program");

    ServiceConfig config = baseConfig();
    config.imageCommandLine = unquoted;
    check(!validateServiceConfig(config).ok(),
          "vulnerable unquoted configuration is refused at validation");

    // The canonical "C:\Program Files" layout: an unquoted image there is
    // both rejected and demonstrably resolves to C:\Program.exe first.
    const std::string classicUnquoted =
        "C:\\Program Files\\Vendor\\service.exe --serve";
    FileExistsFn classicDisk = diskContaining(
        {"C:\\Program.exe", "C:\\Program Files\\Vendor\\service.exe"});
    check(resolveLaunchedProgram(classicUnquoted, classicDisk) ==
              "C:\\Program.exe",
          "classic unquoted 'Program Files' path is hijacked by "
          "C:\\Program.exe");
}

void test_userWritableDirectoriesRejected() {
    const std::vector<std::string> writableImages = {
        "\"C:\\Users\\Public\\service.exe\"",
        "\"C:\\ProgramData\\Drop\\service.exe\"",
        "\"C:\\Windows\\Temp\\service.exe\"",
        "\"C:\\Temp\\service.exe\"",
    };
    for (const std::string& image : writableImages) {
        ServiceConfig config = baseConfig();
        config.imageCommandLine = image;
        checkHas(validateServiceConfig(config),
                 Violation::ImagePathInUserWritableDirectory,
                 "user-writable image rejected: " + image);
    }

    ValidationOptions options;
    options.additionalWritableRoots = {"D:\\Deploy\\Staging"};
    ServiceConfig customRoot = baseConfig();
    customRoot.imageCommandLine =
        "\"D:\\Deploy\\Staging\\service.exe\"";
    checkHas(validateServiceConfig(customRoot, options),
             Violation::ImagePathInUserWritableDirectory,
             "configurable additional writable root enforced");

    ServiceConfig safe = baseConfig();
    check(validateServiceConfig(safe).ok(),
          "image under Program Files outside writable roots is allowed");

    ServiceConfig parent = baseConfig();
    parent.imageCommandLine =
        "\"C:\\Program Files\\Vendor\\..\\..\\Users\\Public\\x.exe\"";
    checkHas(validateServiceConfig(parent),
             Violation::ImagePathInUserWritableDirectory,
             "traversal escaping into a writable root is rejected");
}

void test_unresolvedVariablesAndRelativePaths() {
    const std::vector<std::string> variableImages = {
        "\"%ProgramFiles%\\Vendor\\service.exe\"",
        "\"C:\\Apps\\%APPNAME%\\service.exe\"",
        "\"%SystemRoot%\\System32\\service.exe\"",
        "\"C:\\broken%\\service.exe\"",
    };
    for (const std::string& image : variableImages) {
        ServiceConfig config = baseConfig();
        config.imageCommandLine = image;
        checkHas(validateServiceConfig(config), Violation::UnresolvedVariable,
                 "variable-bearing image rejected: " + image);
    }

    const std::vector<std::string> relativeImages = {
        "\"service.exe\"",
        "\"..\\bin\\service.exe\"",
        "\"C:service.exe\"",
        "\"System32\\service.exe\"",
    };
    for (const std::string& image : relativeImages) {
        ServiceConfig config = baseConfig();
        config.imageCommandLine = image;
        checkHas(validateServiceConfig(config), Violation::RelativeImagePath,
                 "relative image rejected: " + image);
    }

    ServiceConfig directory = baseConfig();
    directory.imageCommandLine = "\"C:\\Program Files\\Vendor\\\"";
    checkHas(validateServiceConfig(directory),
             Violation::ImagePathIsDirectory,
             "path ending in a separator is not a file");

    ServiceConfig unterminated = baseConfig();
    unterminated.imageCommandLine =
        "\"C:\\Program Files\\Vendor\\service.exe --serve";
    checkHas(validateServiceConfig(unterminated),
             Violation::UnterminatedQuote, "unterminated quote rejected");

    check(!containsUnresolvedVariable("C:\\Apps\\100%%\\service.exe"),
          "empty %% pair is treated as a literal, not a variable");
    check(containsUnresolvedVariable("C:\\Apps\\%"),
          "dangling percent marker is unresolved");
    check(normalizeWindowsPath("C:\\Program Files\\..\\X\\.\\Y") ==
              "c:\\x\\y",
          "path normalization resolves dots and lowercases");
    check(isPathWithinRoot("c:\\users\\alice\\x.exe", "c:\\users"),
          "containment matches a nested path");
    check(!isPathWithinRoot("c:\\users-backup\\x.exe", "c:\\users"),
          "containment does not match a sibling prefix");

    ServiceConfig uncWritable = baseConfig();
    uncWritable.imageCommandLine =
        "\"\\\\localhost\\C$\\Users\\Public\\x.exe\"";
    ValidationOptions uncOptions;
    uncOptions.additionalWritableRoots = {"\\\\localhost\\C$\\Users"};
    checkHas(validateServiceConfig(uncWritable, uncOptions),
             Violation::ImagePathInUserWritableDirectory,
             "UNC path inside a configured writable root is rejected");
}

void test_legalQuotedPathAccepted() {
    ServiceConfig config = baseConfig();
    ValidationResult result = validateServiceConfig(config);
    check(result.ok(), "legal quoted configuration passes");
    check(result.parsed.quoted, "parsed representation is quoted");
    check(result.parsed.executablePath ==
              "C:\\Program Files\\Vendor\\service.exe",
          "executable path extracted without quotes");
    check(result.parsed.arguments == "--serve",
          "arguments kept separate from the path");

    ServiceConfig noArgs = baseConfig();
    noArgs.imageCommandLine =
        "\"C:\\Program Files\\Vendor\\service.exe\"";
    ValidationResult noArgsResult = validateServiceConfig(noArgs);
    check(noArgsResult.ok(), "quoted path without arguments passes");
    check(noArgsResult.parsed.arguments.empty(),
          "missing arguments parse as empty");

    ServiceConfig unc = baseConfig();
    unc.imageCommandLine =
        "\"\\\\corp-dc\\SvcShare\\service.exe\" --flag value";
    check(validateServiceConfig(unc).ok(), "quoted UNC image path passes");

    ServiceConfig forwardSlash = baseConfig();
    forwardSlash.imageCommandLine =
        "\"C:/Program Files/Vendor/service.exe\" --serve";
    check(validateServiceConfig(forwardSlash).ok(),
          "forward-slash absolute path normalizes and passes");
}

void test_accountDependencyRecoverySid() {
    ServiceConfig config = baseConfig();
    config.accountName = "DOMAIN\\gMSA-Acct$";
    check(validateServiceConfig(config).ok(), "domain gMSA account accepted");

    config.accountName = "svc@corp.example";
    check(validateServiceConfig(config).ok(), "UPN account accepted");

    config.accountName = "NT AUTHORITY\\NetworkService";
    check(validateServiceConfig(config).ok(),
          "well-known network service accepted");

    const std::vector<std::string> badAccounts = {
        "", "bareName", "DOMAIN\\", "user@", "a@b", "DOMAIN\\bad/name",
    };
    for (const std::string& account : badAccounts) {
        ServiceConfig bad = baseConfig();
        bad.accountName = account;
        checkHas(validateServiceConfig(bad), Violation::InvalidServiceAccount,
                 "invalid account rejected: '" + account + "'");
    }

    ServiceConfig duplicateDep = baseConfig();
    duplicateDep.dependencies = {"RpcSs", "rpcss"};
    checkHas(validateServiceConfig(duplicateDep),
             Violation::DuplicateServiceDependency,
             "case-insensitive duplicate dependency rejected");

    ServiceConfig badDep = baseConfig();
    badDep.dependencies = {"Bad/Dep"};
    checkHas(validateServiceConfig(badDep),
             Violation::InvalidServiceDependency,
             "malformed dependency rejected");

    ServiceConfig badSid = baseConfig();
    badSid.sidType = ServiceSidType::Unknown;
    checkHas(validateServiceConfig(badSid), Violation::InvalidServiceSidType,
             "unknown SID type rejected");
    check(serviceSidTypeFromInt(3) == ServiceSidType::Restricted,
          "raw SID type 3 maps to Restricted");
    check(serviceSidTypeFromInt(99) == ServiceSidType::Unknown,
          "out-of-range raw SID type maps to Unknown");

    ServiceConfig unknownAction = baseConfig();
    unknownAction.recoveryActions = {
        RecoveryAction{RecoveryActionType::Unknown, ""}};
    checkHas(validateServiceConfig(unknownAction),
             Violation::InvalidRecoveryAction,
             "unknown recovery action rejected");

    ServiceConfig runCommand = baseConfig();
    runCommand.recoveryActions = {
        RecoveryAction{RecoveryActionType::RunCommand,
                       "\"C:\\Program Files\\Vendor\\repair.exe\" --fix"}};
    check(validateServiceConfig(runCommand).ok(),
          "quoted RunCommand recovery action accepted");

    ServiceConfig badRunCommand = baseConfig();
    badRunCommand.recoveryActions = {
        RecoveryAction{RecoveryActionType::RunCommand,
                       "C:\\Users\\Public\\repair.exe"}};
    checkHas(validateServiceConfig(badRunCommand),
             Violation::InvalidRecoveryCommand,
             "unquoted/writable RunCommand rejected");

    ServiceConfig strayCommand = baseConfig();
    strayCommand.recoveryActions = {
        RecoveryAction{RecoveryActionType::Restart, "whoami"}};
    checkHas(validateServiceConfig(strayCommand),
             Violation::InvalidRecoveryCommand,
             "command on a non-RunCommand action rejected");
}

void test_installAndUpdateShareValidationAndNeverWriteOnFailure() {
    MemoryServiceStore store;
    ServiceManager manager(store);

    OperationResult installed = manager.install("VendorSvc", baseConfig());
    check(installed.ok(), "valid install succeeds");
    check(store.writeCount() == 1, "install performs exactly one write");
    check(store.exists("VendorSvc"), "service now exists in store");

    OperationResult duplicate = manager.install("VendorSvc", baseConfig());
    check(duplicate.status == StoreStatus::AlreadyExists,
          "duplicate install is rejected");
    check(store.writeCount() == 1, "duplicate install performs no write");

    ServiceConfig bad = baseConfig();
    bad.imageCommandLine = "C:\\Program Files\\Vendor\\service.exe";
    OperationResult rejectedInstall = manager.install("BadSvc", bad);
    check(rejectedInstall.status == StoreStatus::ValidationRejected,
          "invalid install is validation-rejected");
    checkHas(rejectedInstall.validation, Violation::UnquotedImagePath,
             "invalid install carries the unquoted-path violation");
    check(!store.exists("BadSvc"), "rejected install never reaches the store");
    check(store.writeCount() == 1, "rejected install performs no write");

    ServiceConfig updated = baseConfig();
    updated.imageCommandLine =
        "\"C:\\Program Files\\Vendor\\service2.exe\" --serve";
    OperationResult updateOk = manager.update("VendorSvc", updated);
    check(updateOk.ok(),
          "valid update succeeds through the same validation");
    check(store.writeCount() == 2, "valid update performs one write");
    check(store.get("VendorSvc")->imageCommandLine ==
              updated.imageCommandLine,
          "update persists the new configuration");

    ServiceConfig badUpdate = baseConfig();
    badUpdate.accountName = "bareName";
    OperationResult rejectedUpdate =
        manager.update("VendorSvc", badUpdate);
    check(rejectedUpdate.status == StoreStatus::ValidationRejected,
          "invalid update is validation-rejected");
    check(store.writeCount() == 2, "rejected update performs no write");
    check(store.get("VendorSvc")->imageCommandLine ==
              updated.imageCommandLine,
          "rejected update leaves the previous configuration intact");

    OperationResult missing = manager.update("GhostSvc", baseConfig());
    check(missing.status == StoreStatus::NotFound,
          "updating a missing service reports NotFound");

    OperationResult badName = manager.install("bad/name", baseConfig());
    check(badName.status == StoreStatus::InvalidName,
          "invalid service name rejected before validation");
    check(store.writeCount() == 2, "invalid-name install performs no write");

    const std::string hijacker = "C:\\Program.exe";
    const std::string declared =
        "C:\\Program Files\\Vendor\\service2.exe";
    FileExistsFn disk = diskContaining({hijacker, declared});
    const std::string& storedCommandLine =
        store.get("VendorSvc")->imageCommandLine;
    check(resolveLaunchedProgram(storedCommandLine, disk) == declared,
          "stored service launches only the declared program despite a "
          "same-named hijacker");
}

}  // namespace

int main() {
    test_unquotedPathRejected();
    test_sameNamedProgramPreemption();
    test_userWritableDirectoriesRejected();
    test_unresolvedVariablesAndRelativePaths();
    test_legalQuotedPathAccepted();
    test_accountDependencyRecoverySid();
    test_installAndUpdateShareValidationAndNeverWriteOnFailure();

    std::cout << (g_failures == 0 ? "ALL PASS" : "FAILURES PRESENT")
              << ": " << (g_checks - g_failures) << '/' << g_checks
              << " checks passed\n";
    return g_failures == 0 ? 0 : 1;
}
