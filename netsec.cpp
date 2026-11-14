#ifndef _GNU_SOURCE
#define _GNU_SOURCE
#endif

#include "netsec.hpp"

#include <algorithm>
#include <array>
#include <cerrno>
#include <charconv>
#include <chrono>
#include <fcntl.h>
#include <map>
#include <poll.h>
#include <pthread.h>
#include <signal.h>
#include <sys/stat.h>
#include <sys/syscall.h>
#include <sys/types.h>
#include <sys/wait.h>
#include <unistd.h>

namespace netsec {

namespace {

constexpr std::size_t kMaxPathBytes = 4096;
constexpr std::size_t kMaxPatternTokens = 64;
constexpr std::size_t kMaxEnvironmentRead = 1024 * 1024;

enum class PathKind { Directory, Executable, ReadOnlyFile };

std::string_view trim(std::string_view value) {
  while (!value.empty()) {
    const unsigned char c = static_cast<unsigned char>(value.front());
    if (c != ' ' && c != '\t' && c != '\r' && c != '\n') {
      break;
    }
    value.remove_prefix(1);
  }
  while (!value.empty()) {
    const unsigned char c = static_cast<unsigned char>(value.back());
    if (c != ' ' && c != '\t' && c != '\r' && c != '\n') {
      break;
    }
    value.remove_suffix(1);
  }
  return value;
}

bool has_control(std::string_view value) {
  for (const unsigned char c : value) {
    if (c < 0x20 || c == 0x7f) {
      return true;
    }
  }
  return false;
}

bool is_clean_absolute(std::string_view path) {
  if (path.empty() || path.size() > kMaxPathBytes || path.front() != '/') {
    return false;
  }
  if (has_control(path)) {
    return false;
  }
  if (path == "/") {
    return true;
  }
  if (path.back() == '/') {
    return false;
  }
  std::size_t start = 1;
  while (start < path.size()) {
    const std::size_t slash = path.find('/', start);
    const std::size_t end =
        slash == std::string_view::npos ? path.size() : slash;
    const std::string_view component = path.substr(start, end - start);
    if (component.empty() || component == "." || component == "..") {
      return false;
    }
    if (component.size() > 255) {
      return false;
    }
    if (slash == std::string_view::npos) {
      break;
    }
    start = slash + 1;
  }
  return true;
}

bool owner_is_trusted(std::uint32_t owner, std::uint32_t trusted_uid) {
  return owner == trusted_uid || owner == 0;
}

bool parse_u64(std::string_view text, std::uint64_t& output) {
  text = trim(text);
  if (text.empty()) {
    return false;
  }
  std::uint64_t value = 0;
  const auto result =
      std::from_chars(text.data(), text.data() + text.size(), value, 10);
  if (result.ec != std::errc{} ||
      result.ptr != text.data() + text.size()) {
    return false;
  }
  output = value;
  return true;
}

bool parse_u32(std::string_view text, std::uint32_t& output) {
  std::uint64_t value = 0;
  if (!parse_u64(text, value) || value > UINT32_MAX) {
    return false;
  }
  output = static_cast<std::uint32_t>(value);
  return true;
}

bool parse_i32(std::string_view text, int& output) {
  text = trim(text);
  if (text.empty()) {
    return false;
  }
  int value = 0;
  const auto result =
      std::from_chars(text.data(), text.data() + text.size(), value, 10);
  if (result.ec != std::errc{} ||
      result.ptr != text.data() + text.size()) {
    return false;
  }
  output = value;
  return true;
}

bool parse_bool(std::string_view text, bool& output) {
  text = trim(text);
  if (text == "true" || text == "1") {
    output = true;
    return true;
  }
  if (text == "false" || text == "0") {
    output = false;
    return true;
  }
  return false;
}

bool parse_u64_list(std::string_view text,
                    std::vector<std::uint64_t>& output) {
  output.clear();
  text = trim(text);
  if (text.empty()) {
    return true;
  }
  std::size_t start = 0;
  while (start <= text.size()) {
    const std::size_t comma = text.find(',', start);
    const std::size_t end =
        comma == std::string_view::npos ? text.size() : comma;
    const std::string_view item = trim(text.substr(start, end - start));
    std::uint64_t value = 0;
    if (!parse_u64(item, value)) {
      return false;
    }
    output.push_back(value);
    if (comma == std::string_view::npos) {
      break;
    }
    start = comma + 1;
  }
  return true;
}

bool parse_u32_list(std::string_view text,
                    std::vector<std::uint32_t>& output) {
  std::vector<std::uint64_t> parsed;
  if (!parse_u64_list(text, parsed)) {
    return false;
  }
  output.clear();
  output.reserve(parsed.size());
  for (const std::uint64_t value : parsed) {
    if (value > UINT32_MAX) {
      return false;
    }
    output.push_back(static_cast<std::uint32_t>(value));
  }
  return true;
}

std::string decimal_u64(std::uint64_t value) {
  std::array<char, 32> buffer{};
  const auto result =
      std::to_chars(buffer.data(), buffer.data() + buffer.size(), value);
  return std::string(buffer.data(), result.ptr);
}

template <typename T>
bool contains_value(const std::vector<T>& values, T value) {
  return std::find(values.begin(), values.end(), value) != values.end();
}

FileValidation validate_chain(const Environment& environment,
                              std::string_view path, PathKind kind,
                              std::uint32_t trusted_uid,
                              PolicyReason failure_reason) {
  FileValidation validation;
  validation.reason = failure_reason;
  if (!is_clean_absolute(path) || path == "/") {
    validation.detail = "path must be a clean absolute path below /";
    return validation;
  }

  std::size_t start = 1;
  std::string prefix;
  while (start < path.size()) {
    const std::size_t slash = path.find('/', start);
    const std::size_t end =
        slash == std::string::npos ? path.size() : slash;
    prefix.push_back('/');
    prefix.append(path.substr(start, end - start));

    const FileFacts facts = environment.lstat(prefix);
    if (!facts.exists) {
      validation.detail = "path component does not exist";
      return validation;
    }
    if (facts.is_symlink) {
      validation.detail = "path contains a symbolic link";
      return validation;
    }
    if (!owner_is_trusted(facts.owner, trusted_uid)) {
      validation.detail = "path component has an untrusted owner";
      return validation;
    }
    if ((facts.mode & 0022U) != 0U) {
      validation.detail =
          "path component is writable by group or other";
      return validation;
    }

    const bool final_component = slash == std::string::npos;
    if (!final_component) {
      if (!facts.is_directory) {
        validation.detail = "path component is not a directory";
        return validation;
      }
    } else {
      if (kind == PathKind::Directory && !facts.is_directory) {
        validation.detail = "dump directory is not a directory";
        return validation;
      }
      if (kind != PathKind::Directory && !facts.is_regular) {
        validation.detail = "target is not a regular file";
        return validation;
      }
      if (kind == PathKind::Executable) {
        if ((facts.mode & 0111U) == 0U) {
          validation.detail = "handler is not executable";
          return validation;
        }
        if ((facts.mode & 06000U) != 0U) {
          validation.detail =
              "handler must not be setuid or setgid";
          return validation;
        }
      }
      validation.reason = PolicyReason::Allowed;
      validation.detail.clear();
      validation.facts = facts;
      return validation;
    }

    if (slash == std::string::npos) {
      break;
    }
    start = slash + 1;
  }

  validation.detail = "path has no final component";
  return validation;
}

bool valid_placeholder(char value) {
  return value == 'P' || value == 'u' || value == 'g' || value == 's' ||
         value == 't' || value == 'd' || value == '%';
}

bool valid_literal_character(char value) {
  const unsigned char c = static_cast<unsigned char>(value);
  if ((c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z') ||
      (c >= 'a' && c <= 'z')) {
    return true;
  }
  switch (value) {
    case '_':
    case '.':
    case '/':
    case ':':
    case '=':
    case '+':
    case ',':
    case '@':
    case '-':
      return true;
    default:
      return false;
  }
}

bool validate_argument_token(std::string_view token, std::string& error) {
  if (token.empty()) {
    error = "core pattern contains an empty argument";
    return false;
  }
  if (has_control(token)) {
    error = "core pattern argument contains a control character";
    return false;
  }
  for (std::size_t index = 0; index < token.size(); ++index) {
    const char c = token[index];
    if (c == '%') {
      if (index + 1 >= token.size() ||
          !valid_placeholder(token[index + 1])) {
        error =
            "core pattern uses a user-controlled or unknown expansion";
        return false;
      }
      ++index;
      continue;
    }
    if (!valid_literal_character(c)) {
      error = "core pattern argument contains a forbidden character";
      return false;
    }
  }
  return true;
}

bool path_matches_facts(const struct stat& status,
                        std::uint64_t device, std::uint64_t inode,
                        std::uint32_t owner, std::uint32_t mode,
                        bool directory) {
  if (static_cast<std::uint64_t>(status.st_dev) != device ||
      static_cast<std::uint64_t>(status.st_ino) != inode ||
      static_cast<std::uint32_t>(status.st_uid) != owner ||
      static_cast<std::uint32_t>(status.st_mode & 07777) != mode) {
    return false;
  }
  if (directory) {
    return S_ISDIR(status.st_mode);
  }
  return S_ISREG(status.st_mode);
}

std::optional<std::string> read_optional_file(
    const Environment& environment, std::string_view path) {
  return environment.read_file(path);
}

struct SigpipeGuard {
  SigpipeGuard() {
    sigemptyset(&set_);
    sigaddset(&set_, SIGPIPE);
    active_ = pthread_sigmask(SIG_BLOCK, &set_, &old_) == 0;
  }

  ~SigpipeGuard() {
    if (!active_) {
      return;
    }
    sigset_t pending{};
    if (sigpending(&pending) == 0 && sigismember(&pending, SIGPIPE) == 1) {
      int received = 0;
      while (sigwait(&set_, &received) != 0 && errno == EINTR) {
      }
    }
    pthread_sigmask(SIG_SETMASK, &old_, nullptr);
  }

  sigset_t set_{};
  sigset_t old_{};
  bool active_ = false;
};

long remaining_milliseconds(
    std::chrono::steady_clock::time_point deadline) {
  const auto now = std::chrono::steady_clock::now();
  if (now >= deadline) {
    return 0;
  }
  const auto remaining =
      std::chrono::duration_cast<std::chrono::milliseconds>(deadline - now);
  return static_cast<long>(remaining.count());
}

int poll_timeout(std::chrono::steady_clock::time_point deadline) {
  const long remaining = remaining_milliseconds(deadline);
  if (remaining <= 0) {
    return 0;
  }
  return static_cast<int>(std::min<long>(remaining, 100));
}

bool wait_for_child(pid_t child, std::chrono::steady_clock::time_point deadline,
                    int& status) {
  while (true) {
    const pid_t result = waitpid(child, &status, WNOHANG);
    if (result == child) {
      return true;
    }
    if (result < 0) {
      if (errno == EINTR) {
        continue;
      }
      return false;
    }
    if (remaining_milliseconds(deadline) <= 0) {
      return false;
    }
    poll(nullptr, 0, poll_timeout(deadline));
  }
}

void terminate_child(pid_t child, int& status) {
  kill(child, SIGKILL);
  while (waitpid(child, &status, 0) < 0 && errno == EINTR) {
  }
}

int open_verified_directory(const ExecutionPlan& plan) {
  const int fd =
      open(plan.dump_directory.c_str(),
           O_RDONLY | O_DIRECTORY | O_NOFOLLOW | O_CLOEXEC);
  if (fd < 0) {
    return -1;
  }
  struct stat status{};
  if (fstat(fd, &status) != 0 ||
      !path_matches_facts(status, plan.dump_directory_device,
                          plan.dump_directory_inode,
                          plan.dump_directory_owner,
                          plan.dump_directory_mode, true) ||
      (status.st_mode & 0022) != 0) {
    close(fd);
    return -1;
  }
  return fd;
}

int open_verified_handler(const ExecutionPlan& plan) {
  const int fd =
      open(plan.executable.c_str(), O_RDONLY | O_NOFOLLOW | O_CLOEXEC);
  if (fd < 0) {
    return -1;
  }
  struct stat status{};
  if (fstat(fd, &status) != 0 ||
      !path_matches_facts(status, plan.handler_device, plan.handler_inode,
                          plan.handler_owner, plan.handler_mode, false)) {
    close(fd);
    return -1;
  }
  return fd;
}

[[noreturn]] void child_exec(const ExecutionPlan& plan, int directory_fd,
                             int handler_fd, int input_fd, int max_fd,
                             char* const* argv, char* const* envp) {
  sigset_t empty{};
  sigemptyset(&empty);
  sigprocmask(SIG_SETMASK, &empty, nullptr);
  struct sigaction default_action {};
  default_action.sa_handler = SIG_DFL;
  sigemptyset(&default_action.sa_mask);
  sigaction(SIGPIPE, &default_action, nullptr);
  umask(0077);

  if (dup2(input_fd, STDIN_FILENO) < 0) {
    _exit(126);
  }

  const int null_fd = open("/dev/null", O_WRONLY);
  if (null_fd >= 0) {
    dup2(null_fd, STDOUT_FILENO);
    dup2(null_fd, STDERR_FILENO);
    if (null_fd > STDERR_FILENO) {
      close(null_fd);
    }
  }

  if (dup2(directory_fd, 3) < 0 || dup2(handler_fd, 4) < 0) {
    _exit(126);
  }
  fcntl(3, F_SETFD, 0);
  fcntl(4, F_SETFD, 0);

  if (directory_fd > 4) {
    close(directory_fd);
  }
  if (handler_fd > 4) {
    close(handler_fd);
  }
  if (input_fd > 4) {
    close(input_fd);
  }

  for (int fd = 5; fd < max_fd; ++fd) {
    close(fd);
  }

  if (chdir(plan.working_directory.c_str()) != 0) {
    _exit(126);
  }

#if defined(__linux__) && defined(SYS_execveat)
  syscall(SYS_execveat, 4, "", argv, envp, AT_EMPTY_PATH);
#elif defined(__linux__)
  fexecve(4, argv, envp);
#else
  execve(plan.executable.c_str(), argv, envp);
#endif
  _exit(127);
}

}

std::string_view to_string(PolicyReason reason) noexcept {
  switch (reason) {
    case PolicyReason::Allowed:
      return "allowed";
    case PolicyReason::InvalidRequest:
      return "invalid_request";
    case PolicyReason::InvalidConfiguration:
      return "invalid_configuration";
    case PolicyReason::RuntimeUnavailable:
      return "runtime_unavailable";
    case PolicyReason::CorePatternMismatch:
      return "core_pattern_mismatch";
    case PolicyReason::UnsafeConfig:
      return "unsafe_config";
    case PolicyReason::UnsafeHandler:
      return "unsafe_handler";
    case PolicyReason::UnsafeDumpDirectory:
      return "unsafe_dump_directory";
    case PolicyReason::SuidDumpableUnsafe:
      return "suid_dumpable_unsafe";
    case PolicyReason::SuidProcessDenied:
      return "suid_process_denied";
    case PolicyReason::PrivilegedProcessDenied:
      return "privileged_process_denied";
    case PolicyReason::IdentityDenied:
      return "identity_denied";
    case PolicyReason::NamespaceDenied:
      return "namespace_denied";
    case PolicyReason::DumpableDenied:
      return "dumpable_denied";
    case PolicyReason::CoreTooLarge:
      return "core_too_large";
    case PolicyReason::ArgumentLimitExceeded:
      return "argument_limit_exceeded";
    case PolicyReason::SpawnFailed:
      return "spawn_failed";
    case PolicyReason::LauncherFailed:
      return "launcher_failed";
    case PolicyReason::SizeLimitExceeded:
      return "size_limit_exceeded";
    case PolicyReason::Timeout:
      return "timeout";
  }
  return "unknown";
}

std::optional<std::string> SystemEnvironment::read_file(
    std::string_view path) const {
  if (path.empty() || has_control(path)) {
    return std::nullopt;
  }
  const std::string name(path);
  const int fd = open(name.c_str(), O_RDONLY | O_NOFOLLOW | O_CLOEXEC);
  if (fd < 0) {
    return std::nullopt;
  }
  std::string contents;
  std::array<char, 4096> buffer{};
  while (true) {
    const ssize_t count = read(fd, buffer.data(), buffer.size());
    if (count == 0) {
      break;
    }
    if (count < 0) {
      if (errno == EINTR) {
        continue;
      }
      close(fd);
      return std::nullopt;
    }
    contents.append(buffer.data(), static_cast<std::size_t>(count));
    if (contents.size() > kMaxEnvironmentRead) {
      close(fd);
      return std::nullopt;
    }
  }
  close(fd);
  return contents;
}

FileFacts SystemEnvironment::lstat(std::string_view path) const {
  FileFacts facts;
  if (path.empty() || has_control(path)) {
    return facts;
  }
  const std::string name(path);
  struct stat status{};
  if (::lstat(name.c_str(), &status) != 0) {
    return facts;
  }
  facts.exists = true;
  facts.is_symlink = S_ISLNK(status.st_mode);
  facts.is_regular = S_ISREG(status.st_mode);
  facts.is_directory = S_ISDIR(status.st_mode);
  facts.owner = static_cast<std::uint32_t>(status.st_uid);
  facts.group = static_cast<std::uint32_t>(status.st_gid);
  facts.mode = static_cast<std::uint32_t>(status.st_mode & 07777);
  facts.device = static_cast<std::uint64_t>(status.st_dev);
  facts.inode = static_cast<std::uint64_t>(status.st_ino);
  return facts;
}

std::uint32_t SystemEnvironment::effective_uid() const {
  return static_cast<std::uint32_t>(geteuid());
}

FileValidation validate_trusted_directory(
    const Environment& environment, std::string_view path,
    std::uint32_t trusted_uid) {
  return validate_chain(environment, path, PathKind::Directory, trusted_uid,
                        PolicyReason::UnsafeDumpDirectory);
}

FileValidation validate_trusted_executable(
    const Environment& environment, std::string_view path,
    std::uint32_t trusted_uid) {
  return validate_chain(environment, path, PathKind::Executable, trusted_uid,
                        PolicyReason::UnsafeHandler);
}

FileValidation validate_read_only_file(
    const Environment& environment, std::string_view path,
    std::uint32_t trusted_uid) {
  return validate_chain(environment, path, PathKind::ReadOnlyFile,
                        trusted_uid, PolicyReason::UnsafeConfig);
}

std::optional<CorePattern> CorePattern::parse(std::string_view pattern,
                                              std::string* error) {
  if (error != nullptr) {
    error->clear();
  }
  auto fail = [&](std::string message) -> std::optional<CorePattern> {
    if (error != nullptr) {
      *error = std::move(message);
    }
    return std::nullopt;
  };

  if (pattern.empty() || pattern.size() > kMaxPathBytes) {
    return fail("core pattern is empty or too long");
  }
  if (pattern.front() == ' ' || pattern.back() == ' ') {
    return fail("core pattern must not have surrounding spaces");
  }
  if (pattern.front() != '|') {
    return fail("core pattern must use the pipe form");
  }
  if (has_control(pattern)) {
    return fail("core pattern contains a control character");
  }

  std::vector<std::string> tokens;
  std::size_t start = 1;
  while (start < pattern.size()) {
    const std::size_t space = pattern.find(' ', start);
    const std::size_t end =
        space == std::string_view::npos ? pattern.size() : space;
    if (end == start) {
      return fail("core pattern contains an empty field");
    }
    tokens.emplace_back(pattern.substr(start, end - start));
    if (tokens.size() > kMaxPatternTokens) {
      return fail("core pattern has too many fields");
    }
    if (space == std::string_view::npos) {
      break;
    }
    start = space + 1;
    if (start == pattern.size()) {
      return fail("core pattern has a trailing space");
    }
  }

  if (tokens.empty()) {
    return fail("core pattern does not name a handler");
  }
  if (!is_clean_absolute(tokens.front()) || tokens.front() == "/") {
    return fail("core pattern handler must be an absolute file path");
  }
  if (tokens.front().find('%') != std::string::npos) {
    return fail("core pattern handler must not use expansions");
  }
  for (std::size_t index = 1; index < tokens.size(); ++index) {
    std::string argument_error;
    if (!validate_argument_token(tokens[index], argument_error)) {
      return fail(std::move(argument_error));
    }
  }

  CorePattern parsed;
  parsed.handler = tokens.front();
  parsed.tokens = std::move(tokens);
  parsed.text = "|" + parsed.handler;
  for (std::size_t index = 1; index < parsed.tokens.size(); ++index) {
    parsed.text.push_back(' ');
    parsed.text.append(parsed.tokens[index]);
  }
  if (parsed.text.size() > kMaxPathBytes) {
    return fail("core pattern is too long");
  }
  return parsed;
}

std::vector<std::string> CorePattern::render(
    const CorePatternValues& values, std::string* error) const {
  if (error != nullptr) {
    error->clear();
  }
  auto fail = [&](std::string message) {
    if (error != nullptr) {
      *error = std::move(message);
    }
    return std::vector<std::string>{};
  };

  if (tokens.empty()) {
    return fail("core pattern has no handler");
  }

  std::vector<std::string> rendered;
  rendered.reserve(tokens.size());
  rendered.push_back(tokens.front());

  for (std::size_t token_index = 1; token_index < tokens.size();
       ++token_index) {
    const std::string& token = tokens[token_index];
    std::string value;
    for (std::size_t index = 0; index < token.size(); ++index) {
      const char c = token[index];
      if (c != '%') {
        value.push_back(c);
        continue;
      }
      if (index + 1 >= token.size()) {
        return fail("core pattern ends with an incomplete expansion");
      }
      const char expansion = token[++index];
      switch (expansion) {
        case '%':
          value.push_back('%');
          break;
        case 'P':
          value.append(decimal_u64(values.pid));
          break;
        case 'u':
          value.append(decimal_u64(values.uid));
          break;
        case 'g':
          value.append(decimal_u64(values.gid));
          break;
        case 's':
          value.append(decimal_u64(values.signal));
          break;
        case 't':
          value.append(decimal_u64(values.timestamp));
          break;
        case 'd':
          value.append(decimal_u64(values.dump_mode));
          break;
        default:
          return fail("core pattern uses an unsupported expansion");
      }
    }
    if (has_control(value)) {
      return fail("rendered core pattern argument is not text-safe");
    }
    rendered.push_back(std::move(value));
  }
  return rendered;
}

std::optional<CorePattern> parse_core_pattern(std::string_view pattern,
                                              std::string* error) {
  return CorePattern::parse(pattern, error);
}

bool is_safe_core_pattern(std::string_view pattern, std::string* error) {
  return CorePattern::parse(pattern, error).has_value();
}

std::optional<PolicyConfig> load_policy_config(
    std::string_view path, const Environment& environment,
    const ConfigLoadOptions& options, std::string* error) {
  if (error != nullptr) {
    error->clear();
  }
  auto fail = [&](std::string message) -> std::optional<PolicyConfig> {
    if (error != nullptr) {
      *error = std::move(message);
    }
    return std::nullopt;
  };

  const FileValidation file =
      validate_read_only_file(environment, path, options.trusted_uid);
  if (!file.ok()) {
    return fail("configuration file is not trusted: " + file.detail);
  }

  std::optional<std::string> contents = environment.read_file(path);
  if (!contents.has_value()) {
    return fail("configuration file cannot be read");
  }
  if (contents->size() > options.max_config_bytes) {
    return fail("configuration file is too large");
  }
  if (contents->find('\0') != std::string::npos) {
    return fail("configuration file contains a NUL byte");
  }

  std::map<std::string, std::string> values;
  std::size_t start = 0;
  while (start <= contents->size()) {
    const std::size_t newline = contents->find('\n', start);
    const std::size_t end =
        newline == std::string::npos ? contents->size() : newline;
    std::string_view line(contents->data() + start, end - start);
    line = trim(line);
    if (!line.empty() && line.front() != '#') {
      const std::size_t equals = line.find('=');
      if (equals == std::string_view::npos) {
        return fail("configuration line has no '='");
      }
      std::string key(trim(line.substr(0, equals)));
      std::string value(trim(line.substr(equals + 1)));
      if (key.empty()) {
        return fail("configuration has an empty key");
      }
      constexpr std::array<std::string_view, 17> allowed_keys = {
          "core_pattern",
          "handler",
          "dump_directory",
          "max_core_bytes",
          "max_pid",
          "max_signal",
          "max_dump_mode",
          "max_arguments",
          "max_argument_bytes",
          "allow_root_processes",
          "allow_setuid_processes",
          "allow_non_initial_namespaces",
          "allow_suid_dumpable_one",
          "allowed_uids",
          "allowed_gids",
          "allowed_pid_namespaces",
          "allowed_user_namespaces",
      };
      if (std::find(allowed_keys.begin(), allowed_keys.end(),
                    std::string_view(key)) == allowed_keys.end()) {
        return fail("configuration contains an unknown key");
      }
      if (values.find(key) != values.end()) {
        return fail("configuration has a duplicate key");
      }
      values.emplace(std::move(key), std::move(value));
    }
    if (newline == std::string::npos) {
      break;
    }
    start = newline + 1;
  }

  const auto pattern_it = values.find("core_pattern");
  if (pattern_it == values.end()) {
    return fail("configuration is missing core_pattern");
  }
  const auto directory_it = values.find("dump_directory");
  if (directory_it == values.end()) {
    return fail("configuration is missing dump_directory");
  }

  std::string pattern_error;
  std::optional<CorePattern> pattern =
      CorePattern::parse(pattern_it->second, &pattern_error);
  if (!pattern.has_value()) {
    return fail("invalid core_pattern: " + pattern_error);
  }
  if (!is_clean_absolute(directory_it->second) ||
      directory_it->second == "/") {
    return fail("dump_directory must be a clean absolute path");
  }

  const auto handler_it = values.find("handler");
  if (handler_it != values.end() && handler_it->second != pattern->handler) {
    return fail("handler does not match the core_pattern program");
  }

  PolicyConfig config;
  config.pattern = std::move(*pattern);
  config.dump_directory = directory_it->second;
  config.trusted_uid = options.trusted_uid;

  const auto set_u64 = [&](std::string_view key,
                           std::uint64_t& target) -> bool {
    const auto it = values.find(std::string(key));
    if (it == values.end()) {
      return true;
    }
    return parse_u64(it->second, target);
  };
  const auto set_u32 = [&](std::string_view key,
                           std::uint32_t& target) -> bool {
    const auto it = values.find(std::string(key));
    if (it == values.end()) {
      return true;
    }
    return parse_u32(it->second, target);
  };
  const auto set_size = [&](std::string_view key,
                            std::size_t& target) -> bool {
    std::uint64_t value = 0;
    const auto it = values.find(std::string(key));
    if (it == values.end()) {
      return true;
    }
    if (!parse_u64(it->second, value) || value > SIZE_MAX) {
      return false;
    }
    target = static_cast<std::size_t>(value);
    return true;
  };
  const auto set_bool = [&](std::string_view key, bool& target) -> bool {
    const auto it = values.find(std::string(key));
    if (it == values.end()) {
      return true;
    }
    return parse_bool(it->second, target);
  };
  const auto set_u64_list = [&](std::string_view key,
                                std::vector<std::uint64_t>& target) -> bool {
    const auto it = values.find(std::string(key));
    if (it == values.end()) {
      return true;
    }
    return parse_u64_list(it->second, target);
  };
  const auto set_u32_list = [&](std::string_view key,
                                std::vector<std::uint32_t>& target) -> bool {
    const auto it = values.find(std::string(key));
    if (it == values.end()) {
      return true;
    }
    return parse_u32_list(it->second, target);
  };

  CoreDumpLimits& limits = config.limits;
  if (!set_u64("max_core_bytes", limits.max_core_bytes) ||
      !set_u32("max_pid", limits.max_pid) ||
      !set_u32("max_signal", limits.max_signal) ||
      !set_u32("max_dump_mode", limits.max_dump_mode) ||
      !set_size("max_arguments", limits.max_arguments) ||
      !set_size("max_argument_bytes", limits.max_argument_bytes) ||
      !set_bool("allow_root_processes", limits.allow_root_processes) ||
      !set_bool("allow_setuid_processes", limits.allow_setuid_processes) ||
      !set_bool("allow_non_initial_namespaces",
                limits.allow_non_initial_namespaces) ||
      !set_bool("allow_suid_dumpable_one", limits.allow_suid_dumpable_one) ||
      !set_u32_list("allowed_uids", limits.allowed_uids) ||
      !set_u32_list("allowed_gids", limits.allowed_gids) ||
      !set_u64_list("allowed_pid_namespaces",
                    limits.allowed_pid_namespaces) ||
      !set_u64_list("allowed_user_namespaces",
                    limits.allowed_user_namespaces)) {
    return fail("configuration contains an invalid limit value");
  }

  if (limits.max_arguments == 0 || limits.max_arguments > 1024 ||
      limits.max_argument_bytes == 0 || limits.max_argument_bytes > 1024 * 1024 ||
      limits.max_dump_mode > 2 || limits.max_signal == 0 ||
      limits.max_pid == 0) {
    return fail("configuration limits are outside the supported range");
  }

  return config;
}

KernelRuntimeState read_kernel_runtime(const Environment& environment) {
  KernelRuntimeState runtime;

  if (std::optional<std::string> pattern =
          read_optional_file(environment, "/proc/sys/kernel/core_pattern")) {
    *pattern = std::string(trim(*pattern));
    runtime.core_pattern = std::move(pattern);
  }

  std::optional<int> fs_suid;
  if (std::optional<std::string> text =
          read_optional_file(environment, "/proc/sys/fs/suid_dumpable")) {
    int value = 0;
    if (parse_i32(*text, value)) {
      fs_suid = value;
    }
  }

  std::optional<int> kernel_suid;
  if (std::optional<std::string> text =
          read_optional_file(environment,
                             "/proc/sys/kernel/suid_dumpable")) {
    int value = 0;
    if (parse_i32(*text, value)) {
      kernel_suid = value;
    }
  }

  runtime.fs_suid_dumpable = fs_suid.has_value() ? fs_suid : kernel_suid;
  runtime.suid_dumpable = kernel_suid.has_value() ? kernel_suid : fs_suid;
  return runtime;
}

PolicyDecision PolicyDecision::deny(PolicyReason reason,
                                    std::string detail) {
  PolicyDecision decision;
  decision.reason = reason;
  decision.detail = std::move(detail);
  return decision;
}

PolicyDecision PolicyDecision::permit(ExecutionPlan plan) {
  PolicyDecision decision;
  decision.reason = PolicyReason::Allowed;
  decision.plan = std::move(plan);
  return decision;
}

PolicyDecision evaluate_core_dump(
    const PolicyConfig& config, const CoreDumpIdentity& identity,
    const KernelRuntimeState& runtime, const Environment& environment,
    std::string* error) {
  std::optional<CoreDumpPolicy> policy =
      CoreDumpPolicy::create(config, environment, error);
  if (!policy.has_value()) {
    return PolicyDecision::deny(
        PolicyReason::InvalidConfiguration,
        error != nullptr ? *error : "policy configuration is invalid");
  }
  return policy->evaluate(identity, runtime);
}

CoreDumpPolicy::CoreDumpPolicy(PolicyConfig config,
                               const Environment& environment)
    : config_(std::move(config)), environment_(&environment) {}

std::optional<CoreDumpPolicy> CoreDumpPolicy::create(
    PolicyConfig config, const Environment& environment, std::string* error) {
  if (error != nullptr) {
    error->clear();
  }
  auto fail = [&](std::string message) -> std::optional<CoreDumpPolicy> {
    if (error != nullptr) {
      *error = std::move(message);
    }
    return std::nullopt;
  };

  std::string pattern_error;
  std::optional<CorePattern> pattern =
      CorePattern::parse(config.pattern.text, &pattern_error);
  if (!pattern.has_value()) {
    return fail("invalid core pattern: " + pattern_error);
  }
  config.pattern = std::move(*pattern);

  if (!is_clean_absolute(config.dump_directory) ||
      config.dump_directory == "/") {
    return fail("dump directory must be a clean absolute path");
  }
  if (config.limits.max_arguments == 0 || config.limits.max_arguments > 1024 ||
      config.limits.max_argument_bytes == 0 ||
      config.limits.max_argument_bytes > 1024 * 1024 ||
      config.limits.max_dump_mode > 2 || config.limits.max_signal == 0 ||
      config.limits.max_pid == 0) {
    return fail("policy limits are outside the supported range");
  }

  const FileValidation handler = validate_trusted_executable(
      environment, config.pattern.handler, config.trusted_uid);
  if (!handler.ok()) {
    return fail("handler is not trusted: " + handler.detail);
  }
  const FileValidation directory = validate_trusted_directory(
      environment, config.dump_directory, config.trusted_uid);
  if (!directory.ok()) {
    return fail("dump directory is not trusted: " + directory.detail);
  }

  return CoreDumpPolicy(std::move(config), environment);
}

std::optional<CoreDumpPolicy> CoreDumpPolicy::load(
    std::string_view config_path, const Environment& environment,
    const ConfigLoadOptions& options, std::string* error) {
  std::optional<PolicyConfig> config =
      load_policy_config(config_path, environment, options, error);
  if (!config.has_value()) {
    return std::nullopt;
  }
  return CoreDumpPolicy::create(std::move(*config), environment, error);
}

PolicyDecision CoreDumpPolicy::evaluate(
    const CoreDumpIdentity& identity,
    const KernelRuntimeState& runtime) const {
  const CoreDumpLimits& limits = config_.limits;

  if (identity.pid == 0 || identity.pid > limits.max_pid) {
    return PolicyDecision::deny(PolicyReason::InvalidRequest,
                                "crashing pid is outside policy limits");
  }
  if (identity.signal == 0 || identity.signal > limits.max_signal) {
    return PolicyDecision::deny(PolicyReason::InvalidRequest,
                                "crash signal is outside policy limits");
  }
  if (identity.dump_mode > limits.max_dump_mode) {
    return PolicyDecision::deny(PolicyReason::InvalidRequest,
                                "core dump mode is outside policy limits");
  }

  const bool initial_pid = identity.in_initial_pid_namespace;
  const bool initial_user = identity.in_initial_user_namespace;
  const bool initial_mount = identity.in_initial_mount_namespace;
  if (!initial_pid || !initial_user || !initial_mount) {
    if (!limits.allow_non_initial_namespaces) {
      return PolicyDecision::deny(
          PolicyReason::NamespaceDenied,
          "crashing process is outside the initial namespaces");
    }
    if (!initial_pid && !limits.allowed_pid_namespaces.empty() &&
        !contains_value(limits.allowed_pid_namespaces,
                        identity.pid_namespace_inode)) {
      return PolicyDecision::deny(
          PolicyReason::NamespaceDenied,
          "pid namespace is not in the policy allowlist");
    }
    if (!initial_user && !limits.allowed_user_namespaces.empty() &&
        !contains_value(limits.allowed_user_namespaces,
                        identity.user_namespace_inode)) {
      return PolicyDecision::deny(
          PolicyReason::NamespaceDenied,
          "user namespace is not in the policy allowlist");
    }
  }

  const std::uint32_t effective_uid =
      identity.euid == kUnsetId ? identity.uid : identity.euid;
  const bool privileged_identity =
      identity.setuid || identity.setgid || identity.file_capabilities;

  if (privileged_identity) {
    if (!limits.allow_setuid_processes) {
      return PolicyDecision::deny(
          PolicyReason::SuidProcessDenied,
          "privileged process dumps are disabled");
    }
  }
  if ((identity.uid == 0 || effective_uid == 0) &&
      !limits.allow_root_processes) {
    return PolicyDecision::deny(
        PolicyReason::PrivilegedProcessDenied,
        "root process dumps are disabled");
  }
  if (!identity.dumpable) {
    return PolicyDecision::deny(PolicyReason::DumpableDenied,
                                "crashing process is not dumpable");
  }
  if (!limits.allowed_uids.empty() &&
      (!contains_value(limits.allowed_uids, identity.uid) ||
       (identity.euid != kUnsetId &&
        !contains_value(limits.allowed_uids, identity.euid)))) {
    return PolicyDecision::deny(PolicyReason::IdentityDenied,
                                "crashing uid is not allowlisted");
  }
  if (!limits.allowed_gids.empty() &&
      (!contains_value(limits.allowed_gids, identity.gid) ||
       (identity.egid != kUnsetId &&
        !contains_value(limits.allowed_gids, identity.egid)))) {
    return PolicyDecision::deny(PolicyReason::IdentityDenied,
                                "crashing gid is not allowlisted");
  }

  if (!runtime.core_pattern.has_value()) {
    return PolicyDecision::deny(PolicyReason::RuntimeUnavailable,
                                "kernel core_pattern is unavailable");
  }
  if (*runtime.core_pattern != config_.pattern.text) {
    return PolicyDecision::deny(
        PolicyReason::CorePatternMismatch,
        "live core_pattern does not match the trusted configuration");
  }

  if (!runtime.suid_dumpable.has_value() ||
      !runtime.fs_suid_dumpable.has_value()) {
    return PolicyDecision::deny(
        PolicyReason::RuntimeUnavailable,
        "suid dumpable sysctls are unavailable");
  }
  const int suid_dumpable = *runtime.suid_dumpable;
  const int fs_suid_dumpable = *runtime.fs_suid_dumpable;
  if (suid_dumpable < 0 || suid_dumpable > 2 ||
      fs_suid_dumpable < 0 || fs_suid_dumpable > 2) {
    return PolicyDecision::deny(PolicyReason::SuidDumpableUnsafe,
                                "suid dumpable value is invalid");
  }
  if (suid_dumpable == 2 || fs_suid_dumpable == 2) {
    return PolicyDecision::deny(
        PolicyReason::SuidDumpableUnsafe,
        "unrestricted suid core dumps are forbidden");
  }
  if ((suid_dumpable == 1 || fs_suid_dumpable == 1) &&
      !limits.allow_suid_dumpable_one) {
    return PolicyDecision::deny(
        PolicyReason::SuidDumpableUnsafe,
        "suid dumpable mode 1 is disabled by policy");
  }
  if (privileged_identity &&
      (suid_dumpable != 0 || fs_suid_dumpable != 0)) {
    return PolicyDecision::deny(
        PolicyReason::SuidDumpableUnsafe,
        "setuid dumps require both suid sysctls to be zero");
  }

  if (identity.core_bytes > limits.max_core_bytes) {
    return PolicyDecision::deny(PolicyReason::CoreTooLarge,
                                "declared core size exceeds policy limit");
  }

  const FileValidation handler = validate_trusted_executable(
      *environment_, config_.pattern.handler, config_.trusted_uid);
  if (!handler.ok()) {
    return PolicyDecision::deny(PolicyReason::UnsafeHandler,
                                handler.detail);
  }
  const FileValidation directory = validate_trusted_directory(
      *environment_, config_.dump_directory, config_.trusted_uid);
  if (!directory.ok()) {
    return PolicyDecision::deny(PolicyReason::UnsafeDumpDirectory,
                                directory.detail);
  }

  CorePatternValues values;
  values.pid = identity.pid;
  values.uid = identity.uid;
  values.gid = identity.gid;
  values.signal = identity.signal;
  values.timestamp = identity.timestamp;
  values.dump_mode = identity.dump_mode;

  std::string render_error;
  std::vector<std::string> argv =
      config_.pattern.render(values, &render_error);
  if (argv.empty()) {
    return PolicyDecision::deny(PolicyReason::InvalidConfiguration,
                                render_error);
  }
  if (argv.size() > limits.max_arguments) {
    return PolicyDecision::deny(
        PolicyReason::ArgumentLimitExceeded,
        "rendered handler argument count exceeds policy limit");
  }
  std::size_t argument_bytes = 0;
  for (const std::string& argument : argv) {
    if (argument.empty() || has_control(argument)) {
      return PolicyDecision::deny(
          PolicyReason::ArgumentLimitExceeded,
          "rendered handler argument is not text-safe");
    }
    argument_bytes += argument.size() + 1;
    if (argument_bytes > limits.max_argument_bytes) {
      return PolicyDecision::deny(
          PolicyReason::ArgumentLimitExceeded,
          "rendered handler arguments exceed policy limit");
    }
  }

  ExecutionPlan plan;
  plan.executable = config_.pattern.handler;
  plan.argv = std::move(argv);
  plan.env = {
      "LC_ALL=C",
      "CORE_DUMP_POLICY=strict",
      "CORE_DUMP_DIR_FD=3",
      "CORE_DUMP_MAX_BYTES=" + decimal_u64(limits.max_core_bytes),
  };
  plan.working_directory = "/";
  plan.dump_directory = config_.dump_directory;
  plan.handler_device = handler.facts.device;
  plan.handler_inode = handler.facts.inode;
  plan.handler_owner = handler.facts.owner;
  plan.handler_mode = handler.facts.mode;
  plan.dump_directory_device = directory.facts.device;
  plan.dump_directory_inode = directory.facts.inode;
  plan.dump_directory_owner = directory.facts.owner;
  plan.dump_directory_mode = directory.facts.mode;
  plan.max_core_bytes = limits.max_core_bytes;
  return PolicyDecision::permit(std::move(plan));
}

PolicyDecision CoreDumpPolicy::evaluate(
    const CoreDumpIdentity& identity) const {
  return evaluate(identity, read_kernel_runtime(*environment_));
}

PosixProcessLauncher::PosixProcessLauncher(PosixLauncherOptions options)
    : options_(options) {}

LaunchResult PosixProcessLauncher::launch(const ExecutionPlan& plan,
                                          int core_fd) {
  LaunchResult result;
  if (plan.executable.empty() || plan.argv.empty() ||
      plan.argv.front() != plan.executable ||
      !is_clean_absolute(plan.executable) ||
      !is_clean_absolute(plan.dump_directory) ||
      !is_clean_absolute(plan.working_directory)) {
    result.reason = PolicyReason::InvalidRequest;
    result.detail = "execution plan is incomplete";
    return result;
  }

  std::vector<char*> argv;
  argv.reserve(plan.argv.size() + 1);
  for (const std::string& argument : plan.argv) {
    argv.push_back(const_cast<char*>(argument.c_str()));
  }
  argv.push_back(nullptr);

  std::vector<char*> envp;
  envp.reserve(plan.env.size() + 1);
  for (const std::string& variable : plan.env) {
    envp.push_back(const_cast<char*>(variable.c_str()));
  }
  envp.push_back(nullptr);

  const int directory_fd = open_verified_directory(plan);
  if (directory_fd < 0) {
    result.reason = PolicyReason::UnsafeDumpDirectory;
    result.detail = "dump directory changed after policy evaluation";
    return result;
  }
  const int handler_fd = open_verified_handler(plan);
  if (handler_fd < 0) {
    close(directory_fd);
    result.reason = PolicyReason::UnsafeHandler;
    result.detail = "handler changed after policy evaluation";
    return result;
  }

  int pipe_fds[2] = {-1, -1};
#if defined(__linux__)
  const int pipe_result = pipe2(pipe_fds, O_CLOEXEC);
#else
  const int pipe_result = pipe(pipe_fds);
  if (pipe_result == 0) {
    fcntl(pipe_fds[0], F_SETFD, FD_CLOEXEC);
    fcntl(pipe_fds[1], F_SETFD, FD_CLOEXEC);
  }
#endif
  if (pipe_result != 0) {
    close(directory_fd);
    close(handler_fd);
    result.reason = PolicyReason::SpawnFailed;
    result.detail = "cannot create core input pipe";
    return result;
  }

  const pid_t child = fork();
  if (child < 0) {
    close(pipe_fds[0]);
    close(pipe_fds[1]);
    close(directory_fd);
    close(handler_fd);
    result.reason = PolicyReason::SpawnFailed;
    result.detail = "cannot fork handler";
    return result;
  }
  if (child == 0) {
    close(pipe_fds[1]);
    child_exec(plan, directory_fd, handler_fd, pipe_fds[0], options_.max_fd,
               argv.data(), envp.data());
  }

  close(pipe_fds[0]);
  close(directory_fd);
  close(handler_fd);
  result.launched = true;

  SigpipeGuard sigpipe;
  fcntl(pipe_fds[1], F_SETFL, fcntl(pipe_fds[1], F_GETFL, 0) | O_NONBLOCK);

  const auto deadline =
      std::chrono::steady_clock::now() +
      std::chrono::milliseconds(options_.timeout_ms);
  int status = 0;
  bool input_complete = core_fd < 0;
  bool pipe_broken = false;
  std::uint64_t total = 0;
  std::array<char, 16 * 1024> buffer{};

  while (!input_complete && !pipe_broken) {
    pollfd input_poll{};
    input_poll.fd = core_fd;
    input_poll.events = POLLIN;
    const int ready = poll(&input_poll, 1, poll_timeout(deadline));
    if (ready == 0) {
      if (remaining_milliseconds(deadline) <= 0) {
        terminate_child(child, status);
        close(pipe_fds[1]);
        result.reason = PolicyReason::Timeout;
        result.detail = "core reader timed out";
        result.bytes_written = total;
        return result;
      }
      continue;
    }
    if (ready < 0) {
      if (errno == EINTR) {
        continue;
      }
      terminate_child(child, status);
      close(pipe_fds[1]);
      result.reason = PolicyReason::LauncherFailed;
      result.detail = "core input poll failed";
      result.bytes_written = total;
      return result;
    }

    const ssize_t count = read(core_fd, buffer.data(), buffer.size());
    if (count < 0) {
      if (errno == EINTR || errno == EAGAIN || errno == EWOULDBLOCK) {
        continue;
      }
      terminate_child(child, status);
      close(pipe_fds[1]);
      result.reason = PolicyReason::LauncherFailed;
      result.detail = "core input read failed";
      result.bytes_written = total;
      return result;
    }
    if (count == 0) {
      input_complete = true;
      break;
    }

    const std::uint64_t amount = static_cast<std::uint64_t>(count);
    if (amount > plan.max_core_bytes - std::min(total, plan.max_core_bytes)) {
      terminate_child(child, status);
      close(pipe_fds[1]);
      result.reason = PolicyReason::SizeLimitExceeded;
      result.detail = "core stream exceeded policy size limit";
      result.bytes_written = total;
      return result;
    }

    std::size_t offset = 0;
    while (offset < static_cast<std::size_t>(count)) {
      pollfd output_poll{};
      output_poll.fd = pipe_fds[1];
      output_poll.events = POLLOUT;
      const int writable = poll(&output_poll, 1, poll_timeout(deadline));
      if (writable == 0) {
        if (remaining_milliseconds(deadline) <= 0) {
          terminate_child(child, status);
          close(pipe_fds[1]);
          result.reason = PolicyReason::Timeout;
          result.detail = "handler did not consume core data";
          result.bytes_written = total;
          return result;
        }
        continue;
      }
      if (writable < 0) {
        if (errno == EINTR) {
          continue;
        }
        terminate_child(child, status);
        close(pipe_fds[1]);
        result.reason = PolicyReason::LauncherFailed;
        result.detail = "handler pipe poll failed";
        result.bytes_written = total;
        return result;
      }
      const ssize_t written =
          write(pipe_fds[1], buffer.data() + offset,
                static_cast<std::size_t>(count) - offset);
      if (written > 0) {
        offset += static_cast<std::size_t>(written);
        total += static_cast<std::uint64_t>(written);
        continue;
      }
      if (written < 0 &&
          (errno == EINTR || errno == EAGAIN || errno == EWOULDBLOCK)) {
        continue;
      }
      pipe_broken = true;
      break;
    }

    if (total > plan.max_core_bytes) {
      terminate_child(child, status);
      close(pipe_fds[1]);
      result.reason = PolicyReason::SizeLimitExceeded;
      result.detail = "core stream exceeded policy size limit";
      result.bytes_written = total;
      return result;
    }
  }

  close(pipe_fds[1]);

  if (!wait_for_child(child, deadline, status)) {
    terminate_child(child, status);
    result.reason = PolicyReason::Timeout;
    result.detail = "handler did not exit before the deadline";
    result.bytes_written = total;
    return result;
  }

  result.bytes_written = total;
  result.completed = true;
  if (!input_complete && pipe_broken) {
    result.reason = PolicyReason::LauncherFailed;
    result.detail = "handler closed the core stream before completion";
    return result;
  }
  if (WIFEXITED(status) && WEXITSTATUS(status) == 0) {
    result.reason = PolicyReason::Allowed;
    result.exit_code = 0;
    return result;
  }

  result.reason = PolicyReason::LauncherFailed;
  if (WIFEXITED(status)) {
    result.exit_code = WEXITSTATUS(status);
    result.detail = "handler exited with a non-zero status";
  } else if (WIFSIGNALED(status)) {
    result.exit_code = 128 + WTERMSIG(status);
    result.detail = "handler terminated by a signal";
  } else {
    result.detail = "handler did not exit normally";
  }
  return result;
}

CoreDumpExecutor::CoreDumpExecutor(const CoreDumpPolicy& policy,
                                   ProcessLauncher& launcher)
    : policy_(policy), launcher_(launcher) {}

LaunchResult CoreDumpExecutor::execute(
    const CoreDumpIdentity& identity, const KernelRuntimeState& runtime,
    int core_fd) {
  const PolicyDecision decision = policy_.evaluate(identity, runtime);
  if (!decision.allowed()) {
    LaunchResult result;
    result.reason = decision.reason;
    result.detail = decision.detail;
    return result;
  }
  return launcher_.launch(*decision.plan, core_fd);
}

LaunchResult CoreDumpExecutor::execute(const CoreDumpIdentity& identity,
                                       int core_fd) {
  return execute(identity, read_kernel_runtime(policy_.environment()),
                 core_fd);
}

}
