#include "netsec.hpp"

#include <algorithm>
#include <array>
#include <cerrno>
#include <cctype>
#include <cstring>
#include <fcntl.h>
#include <initializer_list>
#include <set>
#include <string>
#include <sys/stat.h>
#include <unistd.h>
#include <utility>

namespace netsec {
namespace {

using Values = std::map<std::string, std::string, std::less<>>;

[[noreturn]] void fail(EnvironmentLoadError code, std::string message) {
    throw EnvironmentException(code, std::move(message));
}

std::string errno_message(std::string_view operation) {
    std::string message(operation);
    message += ": ";
    message += std::strerror(errno);
    return message;
}

class UniqueFd {
public:
    UniqueFd() = default;
    explicit UniqueFd(int fd) noexcept : fd_(fd) {}

    UniqueFd(const UniqueFd&) = delete;
    UniqueFd& operator=(const UniqueFd&) = delete;

    UniqueFd(UniqueFd&& other) noexcept : fd_(std::exchange(other.fd_, -1)) {}

    UniqueFd& operator=(UniqueFd&& other) noexcept {
        if (this != &other) {
            reset(other.fd_);
            other.fd_ = -1;
        }
        return *this;
    }

    ~UniqueFd() { reset(); }

    [[nodiscard]] int get() const noexcept { return fd_; }
    [[nodiscard]] explicit operator bool() const noexcept { return fd_ >= 0; }

    void reset(int fd = -1) noexcept {
        if (fd_ >= 0) {
            ::close(fd_);
        }
        fd_ = fd;
    }

private:
    int fd_ = -1;
};

bool is_symlink_at(int directory_fd, const char* name) noexcept {
    struct stat status {};
    return ::fstatat(directory_fd, name, &status, AT_SYMLINK_NOFOLLOW) == 0 &&
           S_ISLNK(status.st_mode);
}

void reject_symlink_components(const std::string& raw) {
    std::string prefix;
    std::size_t offset = 0;
    if (!raw.empty() && raw.front() == '/') {
        prefix = "/";
        offset = 1;
    }

    while (offset <= raw.size()) {
        const std::size_t slash = raw.find('/', offset);
        const bool last = slash == std::string::npos;
        const std::size_t end = last ? raw.size() : slash;
        const std::string component = raw.substr(offset, end - offset);
        offset = last ? raw.size() + 1 : slash + 1;

        if (component.empty() || component == ".") {
            continue;
        }
        if (prefix.empty()) {
            prefix = component;
        } else {
            if (prefix.back() != '/') {
                prefix.push_back('/');
            }
            prefix += component;
        }

        struct stat status {};
        if (::lstat(prefix.c_str(), &status) != 0) {
            fail(EnvironmentLoadError::open_failed,
                 errno_message("inspect environment file path component '" +
                               component + "'"));
        }
        if (S_ISLNK(status.st_mode)) {
            fail(EnvironmentLoadError::symlink,
                 "environment file path contains a symbolic link");
        }
    }
}

// Some sandboxed deployments deny opening ancestor directories as objects even
// though path resolution to the target and lstat() are allowed. This fallback
// still rejects symlinks in every component and binds the opened fd back to
// the checked final path. The normal component-wise openat() path above is
// used whenever the platform permits it.
UniqueFd open_direct_with_checks(const std::string& raw) {
    reject_symlink_components(raw);

    const int fd = ::open(raw.c_str(),
                          O_RDONLY | O_CLOEXEC | O_NOFOLLOW | O_NONBLOCK);
    if (fd < 0) {
        fail(EnvironmentLoadError::open_failed,
             errno_message("open environment file"));
    }
    UniqueFd result(fd);

    struct stat path_status {};
    struct stat fd_status {};
    if (::lstat(raw.c_str(), &path_status) != 0 ||
        ::fstat(result.get(), &fd_status) != 0) {
        fail(EnvironmentLoadError::read_failed,
             errno_message("verify opened environment file"));
    }
    if (S_ISLNK(path_status.st_mode)) {
        fail(EnvironmentLoadError::symlink,
             "environment file path is a symbolic link");
    }
    if (path_status.st_dev != fd_status.st_dev ||
        path_status.st_ino != fd_status.st_ino) {
        fail(EnvironmentLoadError::changed_during_read,
             "environment file path changed while it was being opened");
    }
    return result;
}

// Open every path component without following symlinks. The returned
// descriptor is the authority for all subsequent checks and reads.
UniqueFd open_nofollow(const std::filesystem::path& path) {
    std::string raw = path.native();
    if (raw.empty() || raw.find('\0') != std::string::npos) {
        fail(EnvironmentLoadError::open_failed, "invalid environment file path");
    }

    while (raw.size() > 1 && raw.back() == '/') {
        raw.pop_back();
    }
    if (raw.empty() || raw == "/") {
        fail(EnvironmentLoadError::open_failed, "environment file path is a directory");
    }

    UniqueFd directory;
    std::size_t offset = 0;
    if (raw.front() == '/') {
        const int root_fd =
            ::open("/", O_RDONLY | O_DIRECTORY | O_CLOEXEC | O_NOFOLLOW);
        if (root_fd < 0) {
            fail(EnvironmentLoadError::open_failed,
                 errno_message("open root directory"));
        }
        directory.reset(root_fd);
        offset = 1;
    }

    while (offset <= raw.size()) {
        const std::size_t slash = raw.find('/', offset);
        const bool last = slash == std::string::npos;
        const std::size_t end = last ? raw.size() : slash;
        const std::string component = raw.substr(offset, end - offset);
        offset = last ? raw.size() + 1 : slash + 1;

        if (component.empty() || (!last && component == ".")) {
            continue;
        }

        const int base = directory ? directory.get() : AT_FDCWD;
        if (last) {
            const int fd = ::openat(
                base, component.c_str(),
                O_RDONLY | O_CLOEXEC | O_NOFOLLOW | O_NONBLOCK);
            if (fd < 0) {
                const int open_error = errno;
                if (is_symlink_at(base, component.c_str())) {
                    fail(EnvironmentLoadError::symlink,
                         "environment file path is a symbolic link");
                }
                if (open_error == EACCES || open_error == EPERM) {
                    return open_direct_with_checks(raw);
                }
                errno = open_error;
                fail(EnvironmentLoadError::open_failed,
                     errno_message("open environment file component '" +
                                   component + "'"));
            }
            return UniqueFd(fd);
        }

        const int fd = ::openat(
            base, component.c_str(),
            O_RDONLY | O_DIRECTORY | O_CLOEXEC | O_NOFOLLOW);
        if (fd < 0) {
            const int open_error = errno;
            if (is_symlink_at(base, component.c_str())) {
                fail(EnvironmentLoadError::symlink,
                     "environment file path contains a symbolic link");
            }
            if (open_error == EACCES || open_error == EPERM) {
                return open_direct_with_checks(raw);
            }
            errno = open_error;
            fail(EnvironmentLoadError::open_failed,
                 errno_message("open environment file directory component '" +
                               component + "'"));
        }
        directory.reset(fd);
    }

    fail(EnvironmentLoadError::open_failed, "invalid environment file path");
}

struct Timestamp {
    std::uint64_t seconds = 0;
    std::uint64_t nanoseconds = 0;

    friend bool operator==(const Timestamp&, const Timestamp&) = default;
};

Timestamp mtime_of(const struct stat& status) noexcept {
#if defined(__APPLE__)
    return {static_cast<std::uint64_t>(status.st_mtimespec.tv_sec),
            static_cast<std::uint64_t>(status.st_mtimespec.tv_nsec)};
#else
    return {static_cast<std::uint64_t>(status.st_mtim.tv_sec),
            static_cast<std::uint64_t>(status.st_mtim.tv_nsec)};
#endif
}

Timestamp ctime_of(const struct stat& status) noexcept {
#if defined(__APPLE__)
    return {static_cast<std::uint64_t>(status.st_ctimespec.tv_sec),
            static_cast<std::uint64_t>(status.st_ctimespec.tv_nsec)};
#else
    return {static_cast<std::uint64_t>(status.st_ctim.tv_sec),
            static_cast<std::uint64_t>(status.st_ctim.tv_nsec)};
#endif
}

bool same_file_state(const struct stat& before, const struct stat& after) noexcept {
    return before.st_dev == after.st_dev && before.st_ino == after.st_ino &&
           before.st_size == after.st_size && before.st_uid == after.st_uid &&
           before.st_gid == after.st_gid && before.st_mode == after.st_mode &&
           mtime_of(before) == mtime_of(after) &&
           ctime_of(before) == ctime_of(after);
}

std::string trim_horizontal(std::string_view text) {
    const auto is_space = [](unsigned char character) {
        return character == ' ' || character == '\t';
    };

    while (!text.empty() && is_space(static_cast<unsigned char>(text.front()))) {
        text.remove_prefix(1);
    }
    while (!text.empty() && is_space(static_cast<unsigned char>(text.back()))) {
        text.remove_suffix(1);
    }
    return std::string(text);
}

bool valid_variable_name(std::string_view name) noexcept {
    if (name.empty() || name.size() > 255) {
        return false;
    }
    const auto first = static_cast<unsigned char>(name.front());
    if (!(std::isalpha(first) || first == '_')) {
        return false;
    }
    return std::all_of(name.begin() + 1, name.end(), [](unsigned char character) {
        return std::isalnum(character) || character == '_';
    });
}

bool equals_any(std::string_view name,
                std::initializer_list<std::string_view> candidates) noexcept {
    return std::any_of(candidates.begin(), candidates.end(),
                       [name](std::string_view candidate) {
                           return name == candidate;
                       });
}

bool starts_with(std::string_view value, std::string_view prefix) noexcept {
    return value.size() >= prefix.size() &&
           value.substr(0, prefix.size()) == prefix;
}

class Sha256 {
public:
    void update(const std::uint8_t* data, std::size_t size) {
        total_bytes_ += size;

        if (buffer_size_ != 0) {
            const std::size_t copied = std::min(size, buffer_.size() - buffer_size_);
            std::copy_n(data, copied, buffer_.begin() +
                                          static_cast<std::ptrdiff_t>(buffer_size_));
            buffer_size_ += copied;
            data += copied;
            size -= copied;
            if (buffer_size_ == buffer_.size()) {
                transform(buffer_.data());
                buffer_size_ = 0;
            }
        }

        while (size >= buffer_.size()) {
            transform(data);
            data += buffer_.size();
            size -= buffer_.size();
        }

        std::copy_n(data, size,
                    buffer_.begin() + static_cast<std::ptrdiff_t>(buffer_size_));
        buffer_size_ += size;
    }

    std::array<std::uint8_t, 32> finish() {
        const std::uint64_t bit_length = total_bytes_ * 8;

        buffer_[buffer_size_++] = 0x80;
        if (buffer_size_ > 56) {
            std::fill(buffer_.begin() + static_cast<std::ptrdiff_t>(buffer_size_),
                      buffer_.end(), 0);
            transform(buffer_.data());
            buffer_size_ = 0;
        }
        std::fill(buffer_.begin() + static_cast<std::ptrdiff_t>(buffer_size_),
                  buffer_.begin() + 56, 0);

        for (std::size_t index = 0; index < 8; ++index) {
            buffer_[63 - index] =
                static_cast<std::uint8_t>(bit_length >> (index * 8));
        }
        transform(buffer_.data());

        std::array<std::uint8_t, 32> digest {};
        for (std::size_t index = 0; index < state_.size(); ++index) {
            digest[index * 4] =
                static_cast<std::uint8_t>(state_[index] >> 24);
            digest[index * 4 + 1] =
                static_cast<std::uint8_t>(state_[index] >> 16);
            digest[index * 4 + 2] =
                static_cast<std::uint8_t>(state_[index] >> 8);
            digest[index * 4 + 3] =
                static_cast<std::uint8_t>(state_[index]);
        }
        return digest;
    }

private:
    static constexpr std::uint32_t rotate_right(std::uint32_t value,
                                                unsigned shift) noexcept {
        return (value >> shift) | (value << (32 - shift));
    }

    void transform(const std::uint8_t* block) {
        static constexpr std::array<std::uint32_t, 64> constants = {
            0x428a2f98U, 0x71374491U, 0xb5c0fbcfU, 0xe9b5dba5U,
            0x3956c25bU, 0x59f111f1U, 0x923f82a4U, 0xab1c5ed5U,
            0xd807aa98U, 0x12835b01U, 0x243185beU, 0x550c7dc3U,
            0x72be5d74U, 0x80deb1feU, 0x9bdc06a7U, 0xc19bf174U,
            0xe49b69c1U, 0xefbe4786U, 0x0fc19dc6U, 0x240ca1ccU,
            0x2de92c6fU, 0x4a7484aaU, 0x5cb0a9dcU, 0x76f988daU,
            0x983e5152U, 0xa831c66dU, 0xb00327c8U, 0xbf597fc7U,
            0xc6e00bf3U, 0xd5a79147U, 0x06ca6351U, 0x14292967U,
            0x27b70a85U, 0x2e1b2138U, 0x4d2c6dfcU, 0x53380d13U,
            0x650a7354U, 0x766a0abbU, 0x81c2c92eU, 0x92722c85U,
            0xa2bfe8a1U, 0xa81a664bU, 0xc24b8b70U, 0xc76c51a3U,
            0xd192e819U, 0xd6990624U, 0xf40e3585U, 0x106aa070U,
            0x19a4c116U, 0x1e376c08U, 0x2748774cU, 0x34b0bcb5U,
            0x391c0cb3U, 0x4ed8aa4aU, 0x5b9cca4fU, 0x682e6ff3U,
            0x748f82eeU, 0x78a5636fU, 0x84c87814U, 0x8cc70208U,
            0x90befffaU, 0xa4506cebU, 0xbef9a3f7U, 0xc67178f2U,
        };

        std::array<std::uint32_t, 64> schedule {};
        for (std::size_t index = 0; index < 16; ++index) {
            schedule[index] =
                (static_cast<std::uint32_t>(block[index * 4]) << 24) |
                (static_cast<std::uint32_t>(block[index * 4 + 1]) << 16) |
                (static_cast<std::uint32_t>(block[index * 4 + 2]) << 8) |
                static_cast<std::uint32_t>(block[index * 4 + 3]);
        }
        for (std::size_t index = 16; index < schedule.size(); ++index) {
            const std::uint32_t s0 =
                rotate_right(schedule[index - 15], 7) ^
                rotate_right(schedule[index - 15], 18) ^
                (schedule[index - 15] >> 3);
            const std::uint32_t s1 =
                rotate_right(schedule[index - 2], 17) ^
                rotate_right(schedule[index - 2], 19) ^
                (schedule[index - 2] >> 10);
            schedule[index] = schedule[index - 16] + s0 +
                              schedule[index - 7] + s1;
        }

        std::uint32_t a = state_[0];
        std::uint32_t b = state_[1];
        std::uint32_t c = state_[2];
        std::uint32_t d = state_[3];
        std::uint32_t e = state_[4];
        std::uint32_t f = state_[5];
        std::uint32_t g = state_[6];
        std::uint32_t h = state_[7];

        for (std::size_t index = 0; index < schedule.size(); ++index) {
            const std::uint32_t sigma1 =
                rotate_right(e, 6) ^ rotate_right(e, 11) ^ rotate_right(e, 25);
            const std::uint32_t choice = (e & f) ^ (~e & g);
            const std::uint32_t temp1 =
                h + sigma1 + choice + constants[index] + schedule[index];
            const std::uint32_t sigma0 =
                rotate_right(a, 2) ^ rotate_right(a, 13) ^ rotate_right(a, 22);
            const std::uint32_t majority =
                (a & b) ^ (a & c) ^ (b & c);
            const std::uint32_t temp2 = sigma0 + majority;

            h = g;
            g = f;
            f = e;
            e = d + temp1;
            d = c;
            c = b;
            b = a;
            a = temp1 + temp2;
        }

        state_[0] += a;
        state_[1] += b;
        state_[2] += c;
        state_[3] += d;
        state_[4] += e;
        state_[5] += f;
        state_[6] += g;
        state_[7] += h;
    }

    std::array<std::uint32_t, 8> state_ = {
        0x6a09e667U, 0xbb67ae85U, 0x3c6ef372U, 0xa54ff53aU,
        0x510e527fU, 0x9b05688cU, 0x1f83d9abU, 0x5be0cd19U,
    };
    std::array<std::uint8_t, 64> buffer_ {};
    std::size_t buffer_size_ = 0;
    std::uint64_t total_bytes_ = 0;
};

std::string sha256_hex(std::string_view data) {
    Sha256 digest;
    digest.update(reinterpret_cast<const std::uint8_t*>(data.data()), data.size());
    const auto bytes = digest.finish();

    static constexpr char hex[] = "0123456789abcdef";
    std::string result;
    result.reserve(bytes.size() * 2);
    for (const std::uint8_t byte : bytes) {
        result.push_back(hex[byte >> 4]);
        result.push_back(hex[byte & 0x0f]);
    }
    return result;
}

struct CheckedFile {
    std::string contents;
    struct stat status {};
};

CheckedFile read_checked_file(const std::filesystem::path& path,
                              const EnvironmentPolicy& policy) {
    UniqueFd fd = open_nofollow(path);

    struct stat before {};
    if (::fstat(fd.get(), &before) != 0) {
        fail(EnvironmentLoadError::read_failed, errno_message("stat environment file"));
    }
    if (!S_ISREG(before.st_mode)) {
        fail(EnvironmentLoadError::not_regular_file,
             "environment file is not a regular file");
    }

    const uid_t trusted_owner = policy.trusted_owner.value_or(::geteuid());
    if (before.st_uid != trusted_owner) {
        fail(EnvironmentLoadError::wrong_owner,
             "environment file is not owned by the trusted service account");
    }
    if ((before.st_mode & (S_IWGRP | S_IWOTH)) != 0) {
        fail(EnvironmentLoadError::unsafe_permissions,
             "environment file is writable by group or other users");
    }
    if (before.st_size < 0 ||
        static_cast<std::uint64_t>(before.st_size) > policy.max_file_bytes) {
        fail(EnvironmentLoadError::file_too_large,
             "environment file exceeds the configured size limit");
    }

    const auto expected_size = static_cast<std::size_t>(before.st_size);
    std::string contents(expected_size, '\0');
    std::size_t total = 0;
    while (total < contents.size()) {
        const ssize_t count =
            ::read(fd.get(), contents.data() + total, contents.size() - total);
        if (count < 0) {
            if (errno == EINTR) {
                continue;
            }
            fail(EnvironmentLoadError::read_failed,
                 errno_message("read environment file"));
        }
        if (count == 0) {
            break;
        }
        total += static_cast<std::size_t>(count);
    }

    struct stat after {};
    if (::fstat(fd.get(), &after) != 0) {
        fail(EnvironmentLoadError::read_failed,
             errno_message("restat environment file"));
    }
    if (!same_file_state(before, after) || total != expected_size ||
        static_cast<std::uint64_t>(total) !=
            static_cast<std::uint64_t>(after.st_size)) {
        fail(EnvironmentLoadError::changed_during_read,
             "environment file changed while it was being read");
    }

    contents.resize(total);
    return {std::move(contents), after};
}

Values parse_environment(std::string_view contents,
                         const EnvironmentPolicy& policy) {
    const std::set<std::string> allowed(policy.allowed_variables.begin(),
                                        policy.allowed_variables.end());

    Values values;
    std::size_t offset = 0;
    std::size_t line_number = 0;

    while (offset < contents.size()) {
        ++line_number;
        const std::size_t newline = contents.find('\n', offset);
        const std::size_t end =
            newline == std::string_view::npos ? contents.size() : newline;
        std::string_view line = contents.substr(offset, end - offset);
        offset = newline == std::string_view::npos ? contents.size() : newline + 1;

        if (!line.empty() && line.back() == '\r') {
            line.remove_suffix(1);
        }
        if (line.size() > policy.max_line_bytes) {
            fail(EnvironmentLoadError::line_too_long,
                 "environment file line " + std::to_string(line_number) +
                     " exceeds the configured length limit");
        }
        if (line.find('\0') != std::string_view::npos ||
            line.find('\r') != std::string_view::npos) {
            fail(EnvironmentLoadError::invalid_syntax,
                 "environment file contains an invalid control character");
        }
        for (const unsigned char character : line) {
            if (character < 0x20 && character != '\t') {
                fail(EnvironmentLoadError::invalid_syntax,
                     "environment file contains an invalid control character");
            }
        }

        const auto first_non_space =
            line.find_first_not_of(" \t");
        if (first_non_space == std::string_view::npos) {
            continue;
        }
        if (line[first_non_space] == '#') {
            if (policy.allow_comments) {
                continue;
            }
            fail(EnvironmentLoadError::invalid_syntax,
                 "comments are disabled in environment files");
        }

        const std::size_t equals = line.find('=');
        if (equals == std::string_view::npos) {
            fail(EnvironmentLoadError::invalid_syntax,
                 "environment file line " + std::to_string(line_number) +
                     " is missing '='");
        }

        const std::string name = trim_horizontal(line.substr(0, equals));
        const std::string value = trim_horizontal(line.substr(equals + 1));
        if (!valid_variable_name(name)) {
            fail(EnvironmentLoadError::invalid_variable_name,
                 "environment file line " + std::to_string(line_number) +
                     " has an invalid variable name");
        }
        if (is_forbidden_environment_variable(name)) {
            fail(EnvironmentLoadError::forbidden_variable,
                 "environment variable '" + name +
                     "' belongs to a forbidden security-sensitive class");
        }
        if (allowed.find(name) == allowed.end()) {
            fail(EnvironmentLoadError::variable_not_allowed,
                 "environment variable '" + name +
                     "' is not in the service allowlist");
        }
        if (values.find(name) != values.end()) {
            fail(EnvironmentLoadError::duplicate_variable,
                 "environment variable '" + name + "' is defined more than once");
        }

        values.emplace(name, value);
    }

    return values;
}

bool same_bound_identity(const EnvironmentSnapshot& left,
                         const EnvironmentSnapshot& right) noexcept {
    return left.device == right.device && left.inode == right.inode &&
           left.size == right.size && left.owner == right.owner &&
           left.mode == right.mode && left.sha256 == right.sha256;
}

} // namespace

EnvironmentException::EnvironmentException(EnvironmentLoadError code,
                                           std::string message)
    : std::runtime_error(std::move(message)), code_(code) {}

EnvironmentLoadError EnvironmentException::code() const noexcept {
    return code_;
}

EnvironmentFileLoader::EnvironmentFileLoader(std::filesystem::path path,
                                             EnvironmentPolicy policy)
    : path_(std::move(path)), policy_(std::move(policy)) {
    if (path_.empty()) {
        throw std::invalid_argument("environment file path must not be empty");
    }
    if (policy_.max_line_bytes == 0 || policy_.max_file_bytes == 0) {
        throw std::invalid_argument("environment file limits must be non-zero");
    }
    for (const std::string& name : policy_.allowed_variables) {
        if (!valid_variable_name(name)) {
            throw std::invalid_argument(
                "environment allowlist contains an invalid variable name");
        }
    }
}

EnvironmentSnapshot EnvironmentFileLoader::load() {
    std::lock_guard<std::mutex> lock(mutex_);

    CheckedFile file = read_checked_file(path_, policy_);
    Values values = parse_environment(file.contents, policy_);
    const std::string hash = sha256_hex(file.contents);

    EnvironmentSnapshot candidate;
    candidate.variables = std::move(values);
    candidate.device = static_cast<std::uint64_t>(file.status.st_dev);
    candidate.inode = static_cast<std::uint64_t>(file.status.st_ino);
    candidate.size = static_cast<std::uint64_t>(file.status.st_size);
    candidate.owner = static_cast<std::uint64_t>(file.status.st_uid);
    candidate.mode = static_cast<std::uint32_t>(file.status.st_mode);
    candidate.sha256 = hash;

    if (!current_) {
        candidate.generation = 1;
    } else if (same_bound_identity(*current_, candidate)) {
        candidate.generation = current_->generation;
    } else {
        candidate.generation = current_->generation + 1;
    }

    current_ = std::move(candidate);
    return *current_;
}

std::optional<EnvironmentSnapshot> EnvironmentFileLoader::current() const {
    std::lock_guard<std::mutex> lock(mutex_);
    return current_;
}

const std::filesystem::path& EnvironmentFileLoader::path() const noexcept {
    return path_;
}

bool is_forbidden_environment_variable(std::string_view name) noexcept {
    // Dynamic linker injection and allocator/loader tunables.
    if (starts_with(name, "LD_") || starts_with(name, "DYLD_") ||
        starts_with(name, "_RLD") || starts_with(name, "BASH_FUNC_") ||
        equals_any(name, {"LIBPATH", "SHLIB_PATH", "GLIBC_TUNABLES",
                          "GCONV_PATH",
                          "MALLOC_CONF", "MALLOC_CHECK_", "MALLOC_PERTURB_",
                          "MALLOC_TRACE"})) {
        return true;
    }

    // Command, library, module and locale search paths.
    if (equals_any(name,
                   {"PATH", "CDPATH", "PYTHONPATH", "PERL5LIB", "PERLLIB",
                    "RUBYLIB", "NODE_PATH", "CLASSPATH", "GOPATH", "GOBIN",
                    "MANPATH", "INFOPATH", "PKG_CONFIG_PATH",
                    "CMAKE_PREFIX_PATH", "LOCPATH", "NLSPATH", "TERMINFO",
                    "TERMINFO_DIRS", "XDG_DATA_DIRS", "XDG_CONFIG_DIRS",
                    "QT_PLUGIN_PATH", "QT_QPA_PLATFORM_PLUGIN_PATH",
                    "GTK_PATH", "GIO_EXTRA_MODULES", "GI_TYPELIB_PATH",
                    "GST_PLUGIN_PATH", "GST_PLUGIN_SYSTEM_PATH",
                    "JAVA_HOME", "JRE_HOME", "GOROOT"})) {
        return true;
    }

    // Interpreter startup hooks and launchers that execute attacker-selected
    // programs or modules.
    return equals_any(
        name,
        {"BASH_ENV", "ENV", "SHELL", "SHELLOPTS", "BASHOPTS", "IFS", "PS4",
         "PROMPT_COMMAND", "PYTHONSTARTUP", "PYTHONHOME", "PYTHONINSPECT",
         "PYTHONBREAKPOINT", "PYTHONUSERBASE", "PERL5OPT", "PERL5DB",
         "PERL_UNICODE", "RUBYOPT", "RUBYGEMS", "GEM_HOME", "GEM_PATH",
         "NODE_OPTIONS", "NODE_EXTRA_CA_CERTS", "JAVA_TOOL_OPTIONS",
         "_JAVA_OPTIONS", "JDK_JAVA_OPTIONS", "GOFLAGS", "GOENV",
         "GOTRACEBACK", "GODEBUG", "ZDOTDIR", "BASH_LOADABLES_PATH",
         "PYTHONEXECUTABLE", "RUBYSHELL", "PERL5SHELL", "EDITOR", "VISUAL",
         "PAGER",
         "SSH_ASKPASS", "SUDO_ASKPASS", "GIT_ASKPASS", "BROWSER"});
}

} // namespace netsec
