#include "netsec.hpp"

#include <vector>

namespace netsec {

const char* to_string(CapErr err) {
  switch (err) {
    case CapErr::NoSuchFd:      return "NoSuchFd";
    case CapErr::NotCapable:    return "NotCapable";
    case CapErr::IsAbsolute:    return "IsAbsolute";
    case CapErr::EscapesBase:   return "EscapesBase";
    case CapErr::BadPath:       return "BadPath";
    case CapErr::CmdNotAllowed: return "CmdNotAllowed";
  }
  return "?";
}

Result<std::string> resolve_relative(const std::string& base_path,
                                     const std::string& path) {
  if (path.empty()) {
    return Result<std::string>::fail(CapErr::BadPath);
  }
  if (path.front() == '/') {
    return Result<std::string>::fail(CapErr::IsAbsolute);
  }

  std::vector<std::string> stack;
  std::size_t pos = 0;
  while (pos <= path.size()) {
    const std::size_t slash = path.find('/', pos);
    const std::string comp =
        path.substr(pos, slash == std::string::npos ? std::string::npos
                                                    : slash - pos);
    if (comp.empty() || comp == ".") {
      // skip
    } else if (comp == "..") {
      if (stack.empty()) {
        return Result<std::string>::fail(CapErr::EscapesBase);
      }
      stack.pop_back();
    } else {
      stack.push_back(comp);
    }
    if (slash == std::string::npos) break;
    pos = slash + 1;
  }
  if (stack.empty()) {
    return Result<std::string>::fail(CapErr::BadPath);
  }

  std::string out = base_path;
  for (const auto& comp : stack) {
    if (!out.empty() && out.back() != '/') out += '/';
    out += comp;
  }
  return Result<std::string>::ok(std::move(out));
}

int FdTable::install(Rights rights, std::string path, bool is_dir) {
  const int fd = next_fd_++;
  FdEntry entry;
  entry.rights = rights;
  entry.path = std::move(path);
  entry.is_dir = is_dir;
  table_.emplace(fd, std::move(entry));
  return fd;
}

Result<bool> FdTable::cap_rights_limit(int fd, Rights next) {
  auto it = table_.find(fd);
  if (it == table_.end()) return Result<bool>::fail(CapErr::NoSuchFd);
  // Monotonic narrowing: any bit we do not already hold is a widening attempt.
  if ((next & ~it->second.rights) != 0) {
    return Result<bool>::fail(CapErr::NotCapable);
  }
  it->second.rights = next;
  return Result<bool>::ok(true);
}

template <typename Set>
static Result<Set> narrow_allowlist(const std::optional<Set>& current,
                                    const Set& next) {
  if (!current.has_value()) {
    return Result<Set>::ok(next);  // unrestricted -> restricted is a narrowing
  }
  for (const auto& cmd : next) {
    if (current->count(cmd) == 0) {
      return Result<Set>::fail(CapErr::NotCapable);  // widening attempt
    }
  }
  return Result<Set>::ok(next);
}

Result<bool> FdTable::cap_ioctls_limit(int fd, std::set<unsigned long> cmds) {
  auto it = table_.find(fd);
  if (it == table_.end()) return Result<bool>::fail(CapErr::NoSuchFd);
  auto narrowed = narrow_allowlist(it->second.ioctl_allow, cmds);
  if (!narrowed) return Result<bool>::fail(narrowed.error());
  it->second.ioctl_allow = std::move(narrowed.value());
  return Result<bool>::ok(true);
}

Result<bool> FdTable::cap_fcntls_limit(int fd, std::set<int> cmds) {
  auto it = table_.find(fd);
  if (it == table_.end()) return Result<bool>::fail(CapErr::NoSuchFd);
  auto narrowed = narrow_allowlist(it->second.fcntl_allow, cmds);
  if (!narrowed) return Result<bool>::fail(narrowed.error());
  it->second.fcntl_allow = std::move(narrowed.value());
  return Result<bool>::ok(true);
}

Result<int> FdTable::openat(int dirfd, const std::string& path,
                            Rights requested, bool is_dir) {
  auto it = table_.find(dirfd);
  if (it == table_.end()) return Result<int>::fail(CapErr::NoSuchFd);
  const FdEntry& base = it->second;
  if ((base.rights & CAP_LOOKUP) == 0) {
    return Result<int>::fail(CapErr::NotCapable);
  }

  auto resolved = resolve_relative(base.path, path);
  if (!resolved) return Result<int>::fail(resolved.error());

  // Rights inheritance: the child can never exceed its base directory.
  FdEntry entry;
  entry.rights = requested & base.rights;
  entry.path = std::move(resolved.value());
  entry.is_dir = is_dir;

  const int fd = next_fd_++;
  table_.emplace(fd, std::move(entry));
  return Result<int>::ok(fd);
}

Result<bool> FdTable::fcntl(int fd, int cmd) {
  auto it = table_.find(fd);
  if (it == table_.end()) return Result<bool>::fail(CapErr::NoSuchFd);
  const FdEntry& entry = it->second;
  if ((entry.rights & CAP_FCNTL) == 0) {
    return Result<bool>::fail(CapErr::NotCapable);
  }
  if (entry.fcntl_allow.has_value() && entry.fcntl_allow->count(cmd) == 0) {
    return Result<bool>::fail(CapErr::CmdNotAllowed);
  }
  return Result<bool>::ok(true);
}

Result<bool> FdTable::ioctl(int fd, unsigned long cmd) {
  auto it = table_.find(fd);
  if (it == table_.end()) return Result<bool>::fail(CapErr::NoSuchFd);
  const FdEntry& entry = it->second;
  if ((entry.rights & CAP_IOCTL) == 0) {
    return Result<bool>::fail(CapErr::NotCapable);
  }
  if (entry.ioctl_allow.has_value() && entry.ioctl_allow->count(cmd) == 0) {
    return Result<bool>::fail(CapErr::CmdNotAllowed);
  }
  return Result<bool>::ok(true);
}

const FdEntry* FdTable::get(int fd) const {
  auto it = table_.find(fd);
  return it == table_.end() ? nullptr : &it->second;
}

}  // namespace netsec
