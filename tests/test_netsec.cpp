#include <cstdio>

#include "netsec.hpp"

namespace {

int g_failures = 0;
int g_checks = 0;

#define CHECK(cond)                                                        \
  do {                                                                     \
    ++g_checks;                                                            \
    if (!(cond)) {                                                         \
      ++g_failures;                                                        \
      std::printf("FAIL %s:%d: %s\n", __FILE__, __LINE__, #cond);          \
    }                                                                      \
  } while (0)

constexpr int F_GETFL = 3;
constexpr int F_SETFL = 4;
constexpr unsigned long TIOCGWINSZ = 0x40087468;
constexpr unsigned long TIOCSETA = 0x800c7414;
constexpr unsigned long FIONREAD = 0x4004667f;

// A compromised process must never be able to widen its own rights.
void test_rights_widening() {
  netsec::FdTable table;
  const int fd = table.install(netsec::CAP_READ | netsec::CAP_FCNTL, "/r/f", false);

  // Narrowing is allowed, including dropping everything.
  CHECK(table.cap_rights_limit(fd, netsec::CAP_READ));
  CHECK(table.get(fd)->rights == netsec::CAP_READ);

  // Re-adding a dropped right must fail.
  auto widen = table.cap_rights_limit(fd, netsec::CAP_READ | netsec::CAP_WRITE);
  CHECK(!widen && widen.error() == netsec::CapErr::NotCapable);
  CHECK(table.get(fd)->rights == netsec::CAP_READ);

  // Requesting a right never held must fail too.
  CHECK(!table.cap_rights_limit(fd, netsec::CAP_IOCTL));

  // Narrowing to zero rights is legal and irreversible.
  CHECK(table.cap_rights_limit(fd, 0));
  CHECK(!table.cap_rights_limit(fd, netsec::CAP_READ));

  CHECK(!table.cap_rights_limit(999, netsec::CAP_READ));  // unknown fd
}

void test_allowlist_widening() {
  netsec::FdTable table;
  const int fd = table.install(netsec::CAP_IOCTL | netsec::CAP_FCNTL, "/r/tty", false);

  // Unrestricted -> restricted is a narrowing.
  CHECK(table.cap_ioctls_limit(fd, {TIOCGWINSZ, FIONREAD}));
  // Subset is a further narrowing.
  CHECK(table.cap_ioctls_limit(fd, {TIOCGWINSZ}));
  // Adding a command back is a widening attempt.
  auto widen = table.cap_ioctls_limit(fd, {TIOCGWINSZ, TIOCSETA});
  CHECK(!widen && widen.error() == netsec::CapErr::NotCapable);

  CHECK(table.cap_fcntls_limit(fd, {F_GETFL}));
  CHECK(!table.cap_fcntls_limit(fd, {F_GETFL, F_SETFL}));
}

// openat must not let absolute paths or ".." escape the base directory.
void test_openat_escape() {
  netsec::FdTable table;
  const int root = table.install(netsec::CAP_ALL, "/sandbox", true);

  auto abs = table.openat(root, "/etc/passwd", netsec::CAP_READ);
  CHECK(!abs && abs.error() == netsec::CapErr::IsAbsolute);

  auto dotdot = table.openat(root, "../outside", netsec::CAP_READ);
  CHECK(!dotdot && dotdot.error() == netsec::CapErr::EscapesBase);

  // Dipping into a subdir does not earn the right to climb above the base.
  auto sneaky = table.openat(root, "a/../../outside", netsec::CAP_READ);
  CHECK(!sneaky && sneaky.error() == netsec::CapErr::EscapesBase);

  // ".." that stays within the base is fine, as are "." and "//".
  auto ok = table.openat(root, "a/./b/../c.txt", netsec::CAP_READ);
  CHECK(ok);
  CHECK(table.get(*ok)->path == "/sandbox/a/c.txt");

  // A child directory descriptor is itself a base: escaping it fails even
  // though the target would still be inside the sandbox root.
  auto subdir = table.openat(root, "sub", netsec::CAP_LOOKUP, true);
  CHECK(subdir);
  auto escape_sub = table.openat(*subdir, "../sibling.txt", netsec::CAP_READ);
  CHECK(!escape_sub && escape_sub.error() == netsec::CapErr::EscapesBase);

  CHECK(!table.openat(root, "", netsec::CAP_READ));  // empty path
}

// The base dirfd needs CAP_LOOKUP, and children inherit at most its rights.
void test_openat_rights_inheritance() {
  netsec::FdTable table;
  const int nolookup = table.install(netsec::CAP_READ, "/sandbox", true);
  auto denied = table.openat(nolookup, "f.txt", netsec::CAP_READ);
  CHECK(!denied && denied.error() == netsec::CapErr::NotCapable);

  const int root = table.install(netsec::CAP_READ | netsec::CAP_LOOKUP,
                                 "/sandbox", true);
  // Asking for more than the base holds silently clamps to the base's rights.
  auto greedy = table.openat(root, "f.txt", netsec::CAP_ALL);
  CHECK(greedy);
  CHECK(table.get(*greedy)->rights == (netsec::CAP_READ | netsec::CAP_LOOKUP));

  // Inheritance is transitive: a grandchild cannot exceed the child.
  auto child = table.openat(root, "d", netsec::CAP_LOOKUP, true);
  CHECK(child);
  auto grandchild = table.openat(*child, "g.txt", netsec::CAP_ALL);
  CHECK(grandchild);
  CHECK(table.get(*grandchild)->rights == netsec::CAP_LOOKUP);
  CHECK(table.get(*grandchild)->path == "/sandbox/d/g.txt");
}

// fcntl/ioctl are gated by their sub-rights and command allow-lists.
void test_subrights() {
  netsec::FdTable table;
  const int fd = table.install(netsec::CAP_READ | netsec::CAP_FCNTL |
                                   netsec::CAP_IOCTL,
                               "/r/tty", false);

  // Unrestricted allow-lists: any command passes while the sub-right is held.
  CHECK(table.fcntl(fd, F_GETFL));
  CHECK(table.ioctl(fd, TIOCSETA));

  // Restrict ioctl to a read-only command set.
  CHECK(table.cap_ioctls_limit(fd, {TIOCGWINSZ, FIONREAD}));
  CHECK(table.ioctl(fd, TIOCGWINSZ));
  auto bad_ioctl = table.ioctl(fd, TIOCSETA);
  CHECK(!bad_ioctl && bad_ioctl.error() == netsec::CapErr::CmdNotAllowed);

  // Restrict fcntl to query-only commands.
  CHECK(table.cap_fcntls_limit(fd, {F_GETFL}));
  CHECK(table.fcntl(fd, F_GETFL));
  auto bad_fcntl = table.fcntl(fd, F_SETFL);
  CHECK(!bad_fcntl && bad_fcntl.error() == netsec::CapErr::CmdNotAllowed);

  // Dropping the sub-right itself blocks even allow-listed commands.
  CHECK(table.cap_rights_limit(fd, netsec::CAP_READ));
  auto no_fcntl = table.fcntl(fd, F_GETFL);
  CHECK(!no_fcntl && no_fcntl.error() == netsec::CapErr::NotCapable);
  auto no_ioctl = table.ioctl(fd, TIOCGWINSZ);
  CHECK(!no_ioctl && no_ioctl.error() == netsec::CapErr::NotCapable);

  // A descriptor that never had the sub-rights is rejected from the start.
  const int plain = table.install(netsec::CAP_READ, "/r/f", false);
  CHECK(!table.fcntl(plain, F_GETFL));
  CHECK(!table.ioctl(plain, FIONREAD));
}

}  // namespace

int main() {
  test_rights_widening();
  test_allowlist_widening();
  test_openat_escape();
  test_openat_rights_inheritance();
  test_subrights();

  std::printf("%d checks, %d failures\n", g_checks, g_failures);
  return g_failures == 0 ? 0 : 1;
}
