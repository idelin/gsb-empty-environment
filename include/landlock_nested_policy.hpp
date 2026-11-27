#ifndef LANDLOCK_NESTED_POLICY_HPP
#define LANDLOCK_NESTED_POLICY_HPP

// User-space evaluator for nested Linux Landlock rulesets (ABI v1-v4).
//
// The policy is modeled as an outermost-to-innermost stack of layers. A
// filesystem or TCP right is granted only when every layer that handles that
// right has an applicable allow rule. Unhandled rights remain ambient, just as
// they do in the kernel. REFER is special-cased for cross-directory
// reparenting: every layer must handle and grant it, and the destination
// hierarchy must not grant any right that the source hierarchy lacks.
//
// This header performs no syscalls and has no dependency outside the C++17
// standard library.

#include <array>
#include <cstddef>
#include <cstdint>
#include <initializer_list>
#include <map>
#include <optional>
#include <stdexcept>
#include <string>
#include <string_view>
#include <utility>
#include <vector>

namespace landlock {

class PolicyError : public std::invalid_argument {
public:
    explicit PolicyError(const std::string &message)
        : std::invalid_argument(message) {}
};

enum class FsAccess : std::uint32_t {
    None = 0u,
    Execute = 1u << 0u,
    WriteFile = 1u << 1u,
    ReadFile = 1u << 2u,
    ReadDir = 1u << 3u,
    RemoveDir = 1u << 4u,
    RemoveFile = 1u << 5u,
    MakeChar = 1u << 6u,
    MakeDir = 1u << 7u,
    MakeBlock = 1u << 8u,
    MakeRegular = 1u << 9u,
    MakeSocket = 1u << 10u,
    MakeFifo = 1u << 11u,
    MakeSymlink = 1u << 12u,
    Refer = 1u << 13u,
    Truncate = 1u << 14u,
};

using AccessFs = FsAccess;

enum class NetAccess : std::uint16_t {
    None = 0u,
    BindTcp = 1u << 0u,
    ConnectTcp = 1u << 1u,
};

using AccessNet = NetAccess;

constexpr std::uint32_t fs_access_value(FsAccess access) noexcept {
    return static_cast<std::uint32_t>(access);
}

constexpr std::uint16_t net_access_value(NetAccess access) noexcept {
    return static_cast<std::uint16_t>(access);
}

constexpr FsAccess operator|(FsAccess lhs, FsAccess rhs) noexcept {
    return static_cast<FsAccess>(fs_access_value(lhs) | fs_access_value(rhs));
}

constexpr FsAccess operator&(FsAccess lhs, FsAccess rhs) noexcept {
    return static_cast<FsAccess>(fs_access_value(lhs) & fs_access_value(rhs));
}

constexpr FsAccess operator~(FsAccess access) noexcept {
    constexpr std::uint32_t all_bits = (1u << 15u) - 1u;
    return static_cast<FsAccess>(~fs_access_value(access) & all_bits);
}

constexpr FsAccess &operator|=(FsAccess &lhs, FsAccess rhs) noexcept {
    lhs = lhs | rhs;
    return lhs;
}

constexpr FsAccess &operator&=(FsAccess &lhs, FsAccess rhs) noexcept {
    lhs = lhs & rhs;
    return lhs;
}

constexpr NetAccess operator|(NetAccess lhs, NetAccess rhs) noexcept {
    return static_cast<NetAccess>(net_access_value(lhs) | net_access_value(rhs));
}

constexpr NetAccess operator&(NetAccess lhs, NetAccess rhs) noexcept {
    return static_cast<NetAccess>(net_access_value(lhs) & net_access_value(rhs));
}

constexpr NetAccess operator~(NetAccess access) noexcept {
    constexpr std::uint16_t all_bits = (1u << 2u) - 1u;
    return static_cast<NetAccess>(
        static_cast<std::uint16_t>(~net_access_value(access)) &
        static_cast<std::uint16_t>(all_bits));
}

constexpr NetAccess &operator|=(NetAccess &lhs, NetAccess rhs) noexcept {
    lhs = lhs | rhs;
    return lhs;
}

constexpr NetAccess &operator&=(NetAccess &lhs, NetAccess rhs) noexcept {
    lhs = lhs & rhs;
    return lhs;
}

constexpr bool has_fs(FsAccess mask, FsAccess bit) noexcept {
    return (fs_access_value(mask) & fs_access_value(bit)) != 0u;
}

constexpr bool contains_fs(FsAccess mask, FsAccess requested) noexcept {
    return (fs_access_value(mask) & fs_access_value(requested)) ==
           fs_access_value(requested);
}

constexpr bool has_net(NetAccess mask, NetAccess bit) noexcept {
    return (net_access_value(mask) & net_access_value(bit)) != 0u;
}

constexpr bool contains_net(NetAccess mask, NetAccess requested) noexcept {
    return (net_access_value(mask) & net_access_value(requested)) ==
           net_access_value(requested);
}

constexpr FsAccess fs_without(FsAccess mask, FsAccess bits) noexcept {
    return mask & ~bits;
}

constexpr NetAccess net_without(NetAccess mask, NetAccess bits) noexcept {
    return mask & ~bits;
}

constexpr std::array<FsAccess, 15> fs_access_bit_array() noexcept {
    return {{FsAccess::Execute,
             FsAccess::WriteFile,
             FsAccess::ReadFile,
             FsAccess::ReadDir,
             FsAccess::RemoveDir,
             FsAccess::RemoveFile,
             FsAccess::MakeChar,
             FsAccess::MakeDir,
             FsAccess::MakeBlock,
             FsAccess::MakeRegular,
             FsAccess::MakeSocket,
             FsAccess::MakeFifo,
             FsAccess::MakeSymlink,
             FsAccess::Refer,
             FsAccess::Truncate}};
}

constexpr std::array<NetAccess, 2> net_access_bit_array() noexcept {
    return {{NetAccess::BindTcp, NetAccess::ConnectTcp}};
}

constexpr FsAccess all_fs_access() noexcept {
    return static_cast<FsAccess>((1u << 15u) - 1u);
}

constexpr FsAccess fs_supported_by_abi(std::uint8_t abi_version) noexcept {
    constexpr FsAccess abi1 =
        FsAccess::Execute | FsAccess::WriteFile | FsAccess::ReadFile |
        FsAccess::ReadDir | FsAccess::RemoveDir | FsAccess::RemoveFile |
        FsAccess::MakeChar | FsAccess::MakeDir | FsAccess::MakeBlock |
        FsAccess::MakeRegular | FsAccess::MakeSocket | FsAccess::MakeFifo |
        FsAccess::MakeSymlink;
    constexpr FsAccess abi2 = abi1 | FsAccess::Refer;
    constexpr FsAccess abi3 = abi2 | FsAccess::Truncate;
    if (abi_version == 0u) {
        return FsAccess::None;
    }
    if (abi_version == 1u) {
        return abi1;
    }
    if (abi_version == 2u) {
        return abi2;
    }
    return abi3;
}

constexpr NetAccess net_supported_by_abi(std::uint8_t abi_version) noexcept {
    if (abi_version >= 4u) {
        return NetAccess::BindTcp | NetAccess::ConnectTcp;
    }
    return NetAccess::None;
}

constexpr FsAccess default_high_risk_fs() noexcept {
    return FsAccess::WriteFile | FsAccess::RemoveDir | FsAccess::RemoveFile |
           FsAccess::MakeChar | FsAccess::MakeDir | FsAccess::MakeBlock |
           FsAccess::MakeRegular | FsAccess::MakeSocket | FsAccess::MakeFifo |
           FsAccess::MakeSymlink | FsAccess::Refer | FsAccess::Truncate;
}

constexpr FsAccess all_fs_permissions() noexcept {
    return all_fs_access();
}

inline std::string fs_access_name(FsAccess access) {
    switch (access) {
    case FsAccess::None:
        return "none";
    case FsAccess::Execute:
        return "execute";
    case FsAccess::WriteFile:
        return "write_file";
    case FsAccess::ReadFile:
        return "read_file";
    case FsAccess::ReadDir:
        return "read_dir";
    case FsAccess::RemoveDir:
        return "remove_dir";
    case FsAccess::RemoveFile:
        return "remove_file";
    case FsAccess::MakeChar:
        return "make_char";
    case FsAccess::MakeDir:
        return "make_dir";
    case FsAccess::MakeBlock:
        return "make_block";
    case FsAccess::MakeRegular:
        return "make_regular";
    case FsAccess::MakeSocket:
        return "make_socket";
    case FsAccess::MakeFifo:
        return "make_fifo";
    case FsAccess::MakeSymlink:
        return "make_symlink";
    case FsAccess::Refer:
        return "refer";
    case FsAccess::Truncate:
        return "truncate";
    }
    return "unknown";
}

inline std::string fs_access_to_string(FsAccess access) {
    if (access == FsAccess::None) {
        return "none";
    }
    std::string result;
    for (FsAccess bit : fs_access_bit_array()) {
        if (!has_fs(access, bit)) {
            continue;
        }
        if (!result.empty()) {
            result += "|";
        }
        result += fs_access_name(bit);
    }
    return result;
}

inline std::string net_access_name(NetAccess access) {
    switch (access) {
    case NetAccess::None:
        return "none";
    case NetAccess::BindTcp:
        return "bind_tcp";
    case NetAccess::ConnectTcp:
        return "connect_tcp";
    }
    return "unknown";
}

inline std::string net_access_to_string(NetAccess access) {
    if (access == NetAccess::None) {
        return "none";
    }
    std::string result;
    for (NetAccess bit : net_access_bit_array()) {
        if (!has_net(access, bit)) {
            continue;
        }
        if (!result.empty()) {
            result += "|";
        }
        result += net_access_name(bit);
    }
    return result;
}

inline std::size_t fs_popcount(FsAccess access) noexcept {
    std::uint32_t value = fs_access_value(access);
    std::size_t count = 0;
    while (value != 0u) {
        value &= value - 1u;
        ++count;
    }
    return count;
}

namespace detail {

inline std::string parent_of_normalized(const std::string &path) {
    if (path == "/") {
        return "/";
    }
    const std::size_t slash = path.rfind('/');
    if (slash == 0u) {
        return "/";
    }
    return path.substr(0u, slash);
}

inline bool is_within_normalized(std::string_view path,
                                 std::string_view root) noexcept {
    if (root == "/") {
        return !path.empty() && path.front() == '/';
    }
    if (path.size() < root.size()) {
        return false;
    }
    if (path.substr(0u, root.size()) != root) {
        return false;
    }
    return path.size() == root.size() ||
           path[static_cast<std::size_t>(root.size())] == '/';
}

inline bool is_strictly_within_normalized(std::string_view path,
                                          std::string_view root) noexcept {
    return path.size() > root.size() && is_within_normalized(path, root);
}

} // namespace detail

inline std::string normalize_path(std::string_view raw_path) {
    if (raw_path.empty()) {
        throw PolicyError("path must not be empty");
    }
    if (raw_path.front() != '/') {
        throw PolicyError("path must be absolute: " + std::string(raw_path));
    }

    std::vector<std::string> components;
    std::size_t cursor = 0u;
    while (cursor < raw_path.size()) {
        while (cursor < raw_path.size() && raw_path[cursor] == '/') {
            ++cursor;
        }
        if (cursor == raw_path.size()) {
            break;
        }
        std::size_t end = cursor;
        while (end < raw_path.size() && raw_path[end] != '/') {
            ++end;
        }
        const std::string_view component =
            raw_path.substr(cursor, end - cursor);
        if (component == "..") {
            throw PolicyError("parent traversal is not allowed: " +
                              std::string(raw_path));
        }
        if (component != ".") {
            components.emplace_back(component);
        }
        cursor = end;
    }

    if (components.empty()) {
        return "/";
    }
    std::string normalized;
    normalized.reserve(raw_path.size());
    for (const std::string &component : components) {
        normalized += "/";
        normalized += component;
    }
    return normalized;
}

inline std::string path_parent(const std::string &path) {
    return detail::parent_of_normalized(normalize_path(path));
}

inline std::string path_basename(const std::string &path) {
    const std::string normalized = normalize_path(path);
    if (normalized == "/") {
        return "/";
    }
    const std::size_t slash = normalized.rfind('/');
    return normalized.substr(slash + 1u);
}

inline std::string path_join(const std::string &directory,
                            const std::string &name) {
    if (name.empty()) {
        throw PolicyError("path component must not be empty");
    }
    if (name.front() == '/') {
        throw PolicyError("path component must be relative: " + name);
    }
    return normalize_path(directory + "/" + name);
}

inline bool path_is_ancestor(const std::string &ancestor,
                             const std::string &descendant) {
    return detail::is_strictly_within_normalized(normalize_path(descendant),
                                                 normalize_path(ancestor));
}

inline bool path_is_within(const std::string &path, const std::string &root) {
    return detail::is_within_normalized(normalize_path(path),
                                        normalize_path(root));
}

enum class NodeType {
    Directory,
    RegularFile,
    Symlink,
    CharDevice,
    BlockDevice,
    Socket,
    Fifo,
    Other,
};

inline std::string node_type_name(NodeType type) {
    switch (type) {
    case NodeType::Directory:
        return "directory";
    case NodeType::RegularFile:
        return "regular_file";
    case NodeType::Symlink:
        return "symlink";
    case NodeType::CharDevice:
        return "char_device";
    case NodeType::BlockDevice:
        return "block_device";
    case NodeType::Socket:
        return "socket";
    case NodeType::Fifo:
        return "fifo";
    case NodeType::Other:
        return "other";
    }
    return "unknown";
}

class DirectoryTree {
public:
    DirectoryTree() : nodes_{{"/", NodeType::Directory}} {}

    void add(std::string path, NodeType type) {
        const std::string normalized = normalize_path(path);
        if (normalized == "/") {
            if (type != NodeType::Directory) {
                throw PolicyError("the root node must be a directory");
            }
            return;
        }
        add_ancestor_directories(normalized);

        auto found = nodes_.find(normalized);
        if (found != nodes_.end()) {
            if (found->second != type) {
                throw PolicyError("node already exists with a different type: " +
                                  normalized);
            }
            return;
        }
        const std::string parent = detail::parent_of_normalized(normalized);
        auto parent_it = nodes_.find(parent);
        if (parent_it == nodes_.end() ||
            parent_it->second != NodeType::Directory) {
            throw PolicyError("parent must be a directory: " + parent);
        }
        nodes_.emplace(normalized, type);
    }

    void add_directory(std::string path) {
        add(std::move(path), NodeType::Directory);
    }

    void add_file(std::string path) {
        add(std::move(path), NodeType::RegularFile);
    }

    void add_symlink(std::string path) {
        add(std::move(path), NodeType::Symlink);
    }

    bool exists(std::string_view path) const {
        return nodes_.find(normalize_path(path)) != nodes_.end();
    }

    std::optional<NodeType> type_of(std::string_view path) const {
        auto found = nodes_.find(normalize_path(path));
        if (found == nodes_.end()) {
            return std::nullopt;
        }
        return found->second;
    }

    NodeType require_type(std::string_view path) const {
        const auto type = type_of(path);
        if (!type.has_value()) {
            throw PolicyError("unknown path: " + std::string(path));
        }
        return *type;
    }

    bool is_directory(std::string_view path) const {
        const auto type = type_of(path);
        return type.has_value() && *type == NodeType::Directory;
    }

    std::string parent_of(std::string_view path) const {
        return detail::parent_of_normalized(normalize_path(path));
    }

    std::vector<std::string> descendants(std::string_view root,
                                         bool include_root = true) const {
        const std::string normalized = normalize_path(root);
        std::vector<std::string> result;
        for (const auto &entry : nodes_) {
            const bool matches =
                include_root
                    ? detail::is_within_normalized(entry.first, normalized)
                    : detail::is_strictly_within_normalized(entry.first,
                                                            normalized);
            if (matches) {
                result.push_back(entry.first);
            }
        }
        return result;
    }

    std::vector<std::string> all_paths() const {
        std::vector<std::string> result;
        result.reserve(nodes_.size());
        for (const auto &entry : nodes_) {
            result.push_back(entry.first);
        }
        return result;
    }

    std::vector<std::string> directories() const {
        std::vector<std::string> result;
        for (const auto &entry : nodes_) {
            if (entry.second == NodeType::Directory) {
                result.push_back(entry.first);
            }
        }
        return result;
    }

    std::size_t size() const noexcept {
        return nodes_.size();
    }

private:
    void add_ancestor_directories(const std::string &path) {
        std::vector<std::string> missing;
        std::string cursor = detail::parent_of_normalized(path);
        while (cursor != "/") {
            auto found = nodes_.find(cursor);
            if (found != nodes_.end()) {
                if (found->second != NodeType::Directory) {
                    throw PolicyError("ancestor is not a directory: " + cursor);
                }
                break;
            }
            missing.push_back(cursor);
            cursor = detail::parent_of_normalized(cursor);
        }
        for (auto it = missing.rbegin(); it != missing.rend(); ++it) {
            nodes_.emplace(*it, NodeType::Directory);
        }
    }

    std::map<std::string, NodeType> nodes_;
};

struct FsRule {
    std::string path;
    FsAccess allowed_access = FsAccess::None;

    FsRule() = default;

    FsRule(std::string rule_path, FsAccess access)
        : path(std::move(rule_path)), allowed_access(access) {}
};

struct TcpRule {
    std::uint16_t first_port = 0u;
    std::uint16_t last_port = 0u;
    NetAccess allowed_access = NetAccess::None;

    TcpRule() = default;

    TcpRule(std::uint16_t port, NetAccess access)
        : first_port(port), last_port(port), allowed_access(access) {}

    TcpRule(std::uint16_t first, std::uint16_t last, NetAccess access)
        : first_port(first), last_port(last), allowed_access(access) {
        if (last_port < first_port) {
            throw PolicyError("TCP port range is reversed");
        }
    }

    bool matches(std::uint16_t port) const noexcept {
        return first_port <= port && port <= last_port;
    }
};

struct PolicyLayer {
    std::string name;
    std::uint8_t abi_version = 4u;
    FsAccess handled_access_fs = FsAccess::None;
    NetAccess handled_access_net = NetAccess::None;
    std::vector<FsRule> fs_rules;
    std::vector<TcpRule> tcp_rules;

    void add_fs_rule(std::string path, FsAccess access) {
        fs_rules.emplace_back(std::move(path), access);
    }

    void allow_fs(std::string path, FsAccess access) {
        add_fs_rule(std::move(path), access);
    }

    void add_tcp_rule(std::uint16_t port, NetAccess access) {
        tcp_rules.emplace_back(port, access);
    }

    void add_tcp_rule(std::uint16_t first_port, std::uint16_t last_port,
                      NetAccess access) {
        tcp_rules.emplace_back(first_port, last_port, access);
    }

    void allow_tcp(std::uint16_t port, NetAccess access) {
        add_tcp_rule(port, access);
    }
};

class PolicyStack {
public:
    PolicyStack() = default;

    explicit PolicyStack(std::vector<PolicyLayer> layers)
        : layers_(std::move(layers)) {}

    PolicyStack(std::initializer_list<PolicyLayer> layers) : layers_(layers) {}

    PolicyStack &add(PolicyLayer layer) {
        layers_.push_back(std::move(layer));
        return *this;
    }

    void push_back(PolicyLayer layer) {
        layers_.push_back(std::move(layer));
    }

    const std::vector<PolicyLayer> &layers() const noexcept {
        return layers_;
    }

    std::vector<PolicyLayer> &layers() noexcept {
        return layers_;
    }

    std::size_t size() const noexcept {
        return layers_.size();
    }

    bool empty() const noexcept {
        return layers_.empty();
    }

    const PolicyLayer &operator[](std::size_t index) const {
        return layers_[index];
    }

    PolicyLayer &operator[](std::size_t index) {
        return layers_[index];
    }

private:
    std::vector<PolicyLayer> layers_;
};

struct LayerAccessContribution {
    std::size_t layer_index = 0u;
    std::string layer_name;
    FsAccess handled_access = FsAccess::None;
    FsAccess granted_access = FsAccess::None;
    FsAccess denied_access = FsAccess::None;
    bool handles_request = false;
    bool permits = true;
};

struct AccessDecision {
    bool allowed = false;
    std::string path;
    FsAccess requested = FsAccess::None;
    FsAccess effective_allowed = FsAccess::None;
    FsAccess denied = FsAccess::None;
    std::vector<LayerAccessContribution> layers;
    std::string reason;

    explicit operator bool() const noexcept {
        return allowed;
    }
};

struct LayerNetContribution {
    std::size_t layer_index = 0u;
    std::string layer_name;
    NetAccess handled_access = NetAccess::None;
    NetAccess granted_access = NetAccess::None;
    NetAccess denied_access = NetAccess::None;
    bool handles_request = false;
    bool permits = true;
};

struct TcpDecision {
    bool allowed = false;
    std::uint16_t port = 0u;
    NetAccess requested = NetAccess::None;
    NetAccess effective_allowed = NetAccess::None;
    NetAccess denied = NetAccess::None;
    std::vector<LayerNetContribution> layers;
    std::string reason;

    explicit operator bool() const noexcept {
        return allowed;
    }
};

enum class ReparentOperation {
    Rename,
    HardLink,
};

inline std::string reparent_operation_name(ReparentOperation operation) {
    switch (operation) {
    case ReparentOperation::Rename:
        return "rename";
    case ReparentOperation::HardLink:
        return "hard_link";
    }
    return "unknown";
}

enum class ReparentIssueKind {
    InvalidRequest,
    SourceNotFound,
    DestinationExists,
    SourceParentNotDirectory,
    DestinationParentNotDirectory,
    AbiDoesNotSupportRefer,
    ReferNotHandled,
    ReferDenied,
    RemoveDenied,
    CreateDenied,
    HardLinkSourceIsDirectory,
    MoveInsideOwnSubtree,
    DestinationHierarchyNotSubset,
    DestinationLayerNotSubset,
};

inline std::string reparent_issue_kind_name(ReparentIssueKind kind) {
    switch (kind) {
    case ReparentIssueKind::InvalidRequest:
        return "invalid_request";
    case ReparentIssueKind::SourceNotFound:
        return "source_not_found";
    case ReparentIssueKind::DestinationExists:
        return "destination_exists";
    case ReparentIssueKind::SourceParentNotDirectory:
        return "source_parent_not_directory";
    case ReparentIssueKind::DestinationParentNotDirectory:
        return "destination_parent_not_directory";
    case ReparentIssueKind::AbiDoesNotSupportRefer:
        return "abi_does_not_support_refer";
    case ReparentIssueKind::ReferNotHandled:
        return "refer_not_handled";
    case ReparentIssueKind::ReferDenied:
        return "refer_denied";
    case ReparentIssueKind::RemoveDenied:
        return "remove_denied";
    case ReparentIssueKind::CreateDenied:
        return "create_denied";
    case ReparentIssueKind::HardLinkSourceIsDirectory:
        return "hard_link_source_is_directory";
    case ReparentIssueKind::MoveInsideOwnSubtree:
        return "move_inside_own_subtree";
    case ReparentIssueKind::DestinationHierarchyNotSubset:
        return "destination_hierarchy_not_subset";
    case ReparentIssueKind::DestinationLayerNotSubset:
        return "destination_layer_not_subset";
    }
    return "unknown";
}

struct ReparentIssue {
    ReparentIssueKind kind = ReparentIssueKind::InvalidRequest;
    std::optional<std::size_t> layer_index;
    std::string message;
    FsAccess access = FsAccess::None;
    std::string source_path;
    std::string destination_path;
    FsAccess source_access = FsAccess::None;
    FsAccess destination_access = FsAccess::None;
    FsAccess privilege_gain = FsAccess::None;
};

struct ReparentDecision {
    bool allowed = false;
    ReparentOperation operation = ReparentOperation::Rename;
    std::string source;
    std::string destination;
    std::string source_parent;
    std::string destination_parent;
    std::vector<ReparentIssue> issues;

    explicit operator bool() const noexcept {
        return allowed;
    }

    std::string reason() const {
        if (allowed) {
            return "allowed";
        }
        std::string result;
        for (const ReparentIssue &issue : issues) {
            if (!result.empty()) {
                result += "; ";
            }
            result += reparent_issue_kind_name(issue.kind);
            if (!issue.message.empty()) {
                result += ": ";
                result += issue.message;
            }
        }
        return result;
    }
};

class PolicyEvaluator {
public:
    PolicyEvaluator(const PolicyStack &policy, const DirectoryTree &tree)
        : policy_(policy), tree_(tree) {}

    const PolicyStack &policy() const noexcept {
        return policy_;
    }

    const DirectoryTree &tree() const noexcept {
        return tree_;
    }

    FsAccess handled_fs(std::size_t layer_index) const {
        if (layer_index >= policy_.size()) {
            throw PolicyError("layer index out of range");
        }
        const PolicyLayer &layer = policy_[layer_index];
        return layer.handled_access_fs & fs_supported_by_abi(layer.abi_version);
    }

    NetAccess handled_net(std::size_t layer_index) const {
        if (layer_index >= policy_.size()) {
            throw PolicyError("layer index out of range");
        }
        const PolicyLayer &layer = policy_[layer_index];
        return layer.handled_access_net & net_supported_by_abi(layer.abi_version);
    }

    FsAccess rule_grants_fs(std::size_t layer_index,
                            const std::string &raw_path) const {
        if (layer_index >= policy_.size()) {
            throw PolicyError("layer index out of range");
        }
        const PolicyLayer &layer = policy_[layer_index];
        const std::string path = normalize_path(raw_path);
        FsAccess granted = FsAccess::None;
        for (const FsRule &rule : layer.fs_rules) {
            if (rule_applies(rule, path)) {
                granted |= rule.allowed_access;
            }
        }
        return granted & handled_fs(layer_index);
    }

    NetAccess rule_grants_net(std::size_t layer_index,
                              std::uint16_t port) const {
        if (layer_index >= policy_.size()) {
            throw PolicyError("layer index out of range");
        }
        const PolicyLayer &layer = policy_[layer_index];
        NetAccess granted = NetAccess::None;
        for (const TcpRule &rule : layer.tcp_rules) {
            if (rule.matches(port)) {
                granted |= rule.allowed_access;
            }
        }
        return granted & handled_net(layer_index);
    }

    FsAccess effective_fs(const std::string &raw_path) const {
        const std::string path = normalize_path(raw_path);
        FsAccess allowed = all_fs_access();
        for (std::size_t index = 0u; index < policy_.size(); ++index) {
            const FsAccess handled = handled_fs(index);
            const FsAccess granted = rule_grants_fs(index, path);
            for (FsAccess bit : fs_access_bit_array()) {
                if (has_fs(handled, bit) && !has_fs(granted, bit)) {
                    allowed = fs_without(allowed, bit);
                }
            }
        }
        return allowed;
    }

    AccessDecision evaluate_fs(const std::string &raw_path,
                               FsAccess requested) const {
        const std::string path = normalize_path(raw_path);
        AccessDecision decision;
        decision.path = path;
        decision.requested = requested;
        decision.effective_allowed = effective_fs(path);
        decision.denied = requested & ~decision.effective_allowed;
        decision.allowed = decision.denied == FsAccess::None;

        decision.layers.reserve(policy_.size());
        for (std::size_t index = 0u; index < policy_.size(); ++index) {
            const PolicyLayer &layer = policy_[index];
            const FsAccess handled = handled_fs(index) & requested;
            const FsAccess granted = rule_grants_fs(index, path) & requested;
            LayerAccessContribution contribution;
            contribution.layer_index = index;
            contribution.layer_name = layer.name;
            contribution.handled_access = handled;
            contribution.granted_access = granted;
            contribution.denied_access = handled & ~granted;
            contribution.handles_request = handled != FsAccess::None;
            contribution.permits =
                contribution.denied_access == FsAccess::None;
            decision.layers.push_back(contribution);
        }

        if (!decision.allowed) {
            for (const LayerAccessContribution &contribution :
                 decision.layers) {
                if (contribution.permits) {
                    continue;
                }
                if (!decision.reason.empty()) {
                    decision.reason += "; ";
                }
                decision.reason += "layer " +
                                   std::to_string(contribution.layer_index);
                if (!contribution.layer_name.empty()) {
                    decision.reason += " (" + contribution.layer_name + ")";
                }
                decision.reason += " denies " +
                                   fs_access_to_string(
                                       contribution.denied_access);
            }
        }
        return decision;
    }

    AccessDecision check_access(const std::string &path,
                                FsAccess requested) const {
        return evaluate_fs(path, requested);
    }

    bool allows(const std::string &path, FsAccess requested) const {
        return evaluate_fs(path, requested).allowed;
    }

    bool can_access(const std::string &path, FsAccess requested) const {
        return allows(path, requested);
    }

    NetAccess effective_net(std::uint16_t port) const {
        NetAccess allowed = NetAccess::BindTcp | NetAccess::ConnectTcp;
        for (std::size_t index = 0u; index < policy_.size(); ++index) {
            const NetAccess handled = handled_net(index);
            const NetAccess granted = rule_grants_net(index, port);
            for (NetAccess bit : net_access_bit_array()) {
                if (has_net(handled, bit) && !has_net(granted, bit)) {
                    allowed = net_without(allowed, bit);
                }
            }
        }
        return allowed;
    }

    TcpDecision evaluate_tcp(std::uint16_t port,
                             NetAccess requested) const {
        TcpDecision decision;
        decision.port = port;
        decision.requested = requested;
        decision.effective_allowed = effective_net(port);
        decision.denied = requested & ~decision.effective_allowed;
        decision.allowed = decision.denied == NetAccess::None;

        decision.layers.reserve(policy_.size());
        for (std::size_t index = 0u; index < policy_.size(); ++index) {
            const PolicyLayer &layer = policy_[index];
            const NetAccess handled = handled_net(index) & requested;
            const NetAccess granted = rule_grants_net(index, port) & requested;
            LayerNetContribution contribution;
            contribution.layer_index = index;
            contribution.layer_name = layer.name;
            contribution.handled_access = handled;
            contribution.granted_access = granted;
            contribution.denied_access = handled & ~granted;
            contribution.handles_request = handled != NetAccess::None;
            contribution.permits =
                contribution.denied_access == NetAccess::None;
            decision.layers.push_back(contribution);
        }

        if (!decision.allowed) {
            for (const LayerNetContribution &contribution : decision.layers) {
                if (contribution.permits) {
                    continue;
                }
                if (!decision.reason.empty()) {
                    decision.reason += "; ";
                }
                decision.reason += "layer " +
                                   std::to_string(contribution.layer_index);
                if (!contribution.layer_name.empty()) {
                    decision.reason += " (" + contribution.layer_name + ")";
                }
                decision.reason += " denies " +
                                   net_access_to_string(
                                       contribution.denied_access);
            }
        }
        return decision;
    }

    TcpDecision check_tcp(std::uint16_t port, NetAccess requested) const {
        return evaluate_tcp(port, requested);
    }

    TcpDecision check_net_port(std::uint16_t port,
                               NetAccess requested) const {
        return evaluate_tcp(port, requested);
    }

    bool allows_tcp(std::uint16_t port, NetAccess requested) const {
        return evaluate_tcp(port, requested).allowed;
    }

    bool can_bind(std::uint16_t port) const {
        return allows_tcp(port, NetAccess::BindTcp);
    }

    bool can_connect(std::uint16_t port) const {
        return allows_tcp(port, NetAccess::ConnectTcp);
    }

    ReparentDecision check_rename(const std::string &source,
                                  const std::string &destination) const {
        return evaluate_reparent(ReparentOperation::Rename, source, destination);
    }

    ReparentDecision check_hard_link(const std::string &source,
                                     const std::string &destination) const {
        return evaluate_reparent(ReparentOperation::HardLink, source,
                                 destination);
    }

    ReparentDecision check_hardlink(const std::string &source,
                                    const std::string &destination) const {
        return check_hard_link(source, destination);
    }

    static FsAccess removal_permission(NodeType type) noexcept {
        return type == NodeType::Directory ? FsAccess::RemoveDir
                                           : FsAccess::RemoveFile;
    }

    static FsAccess creation_permission(NodeType type) noexcept {
        switch (type) {
        case NodeType::Directory:
            return FsAccess::MakeDir;
        case NodeType::RegularFile:
            return FsAccess::MakeRegular;
        case NodeType::Symlink:
            return FsAccess::MakeSymlink;
        case NodeType::CharDevice:
            return FsAccess::MakeChar;
        case NodeType::BlockDevice:
            return FsAccess::MakeBlock;
        case NodeType::Socket:
            return FsAccess::MakeSocket;
        case NodeType::Fifo:
            return FsAccess::MakeFifo;
        case NodeType::Other:
            return FsAccess::MakeRegular;
        }
        return FsAccess::None;
    }

private:
    bool rule_applies(const FsRule &rule, const std::string &query) const {
        const std::string rule_path = normalize_path(rule.path);
        if (rule_path == query) {
            return true;
        }
        if (!detail::is_strictly_within_normalized(query, rule_path)) {
            return false;
        }
        const std::optional<NodeType> type = tree_.type_of(rule_path);
        if (type.has_value() && *type != NodeType::Directory) {
            return false;
        }
        return true;
    }

    std::optional<std::size_t>
    first_blocking_layer(const AccessDecision &decision) const {
        for (const LayerAccessContribution &contribution : decision.layers) {
            if (!contribution.permits) {
                return contribution.layer_index;
            }
        }
        return std::nullopt;
    }

    ReparentDecision evaluate_reparent(ReparentOperation operation,
                                       const std::string &raw_source,
                                       const std::string &raw_destination) const {
        const std::string source = normalize_path(raw_source);
        std::string destination;
        try {
            destination = normalize_path(raw_destination);
        } catch (const PolicyError &) {
            ReparentDecision invalid;
            invalid.operation = operation;
            invalid.source = source;
            ReparentIssue issue;
            issue.kind = ReparentIssueKind::InvalidRequest;
            issue.message = "invalid destination path";
            invalid.issues.push_back(std::move(issue));
            return invalid;
        }

        ReparentDecision decision;
        decision.operation = operation;
        decision.source = source;
        decision.destination = destination;
        decision.source_parent = detail::parent_of_normalized(source);
        decision.destination_parent =
            detail::parent_of_normalized(destination);

        auto add_issue = [&decision](ReparentIssueKind kind, std::string message,
                                     std::optional<std::size_t> layer_index) {
            ReparentIssue issue;
            issue.kind = kind;
            issue.message = std::move(message);
            issue.layer_index = layer_index;
            decision.issues.push_back(std::move(issue));
        };

        if (source == "/") {
            add_issue(ReparentIssueKind::InvalidRequest,
                      "the root directory cannot be reparented", std::nullopt);
            return decision;
        }
        if (destination == "/") {
            add_issue(ReparentIssueKind::InvalidRequest,
                      "the destination cannot be the root directory",
                      std::nullopt);
            return decision;
        }

        const std::optional<NodeType> source_type = tree_.type_of(source);
        if (!source_type.has_value()) {
            add_issue(ReparentIssueKind::SourceNotFound,
                      "source does not exist", std::nullopt);
            return decision;
        }
        if (operation == ReparentOperation::HardLink &&
            *source_type == NodeType::Directory) {
            add_issue(ReparentIssueKind::HardLinkSourceIsDirectory,
                      "hard links to directories are not supported",
                      std::nullopt);
            return decision;
        }
        if (operation == ReparentOperation::Rename && source == destination) {
            decision.allowed = true;
            return decision;
        }
        if (tree_.exists(destination)) {
            add_issue(ReparentIssueKind::DestinationExists,
                      "destination already exists", std::nullopt);
            return decision;
        }
        if (!tree_.is_directory(decision.source_parent)) {
            add_issue(ReparentIssueKind::SourceParentNotDirectory,
                      "source parent is not a directory", std::nullopt);
            return decision;
        }
        if (!tree_.is_directory(decision.destination_parent)) {
            add_issue(ReparentIssueKind::DestinationParentNotDirectory,
                      "destination parent is not a directory", std::nullopt);
            return decision;
        }

        const bool cross_directory =
            decision.source_parent != decision.destination_parent;
        if (operation == ReparentOperation::Rename &&
            *source_type == NodeType::Directory &&
            detail::is_within_normalized(decision.destination_parent, source)) {
            add_issue(ReparentIssueKind::MoveInsideOwnSubtree,
                      "a directory cannot be moved below itself", std::nullopt);
            return decision;
        }

        if (cross_directory) {
            for (std::size_t index = 0u; index < policy_.size(); ++index) {
                const PolicyLayer &layer = policy_[index];
                if (layer.abi_version < 2u) {
                    add_issue(ReparentIssueKind::AbiDoesNotSupportRefer,
                              "Landlock ABI " +
                                  std::to_string(layer.abi_version) +
                                  " predates LANDLOCK_ACCESS_FS_REFER",
                              index);
                    continue;
                }
                if (!has_fs(layer.handled_access_fs, FsAccess::Refer)) {
                    add_issue(ReparentIssueKind::ReferNotHandled,
                              "REFER is not declared in handled_access_fs; "
                              "cross-directory reparenting is fail-closed",
                              index);
                    continue;
                }
                const FsAccess source_grants =
                    rule_grants_fs(index, decision.source_parent);
                const FsAccess destination_grants =
                    rule_grants_fs(index, decision.destination_parent);
                if (!has_fs(source_grants, FsAccess::Refer)) {
                    add_issue(ReparentIssueKind::ReferDenied,
                              "source directory does not grant REFER", index);
                }
                if (!has_fs(destination_grants, FsAccess::Refer)) {
                    add_issue(ReparentIssueKind::ReferDenied,
                              "destination directory does not grant REFER",
                              index);
                }
            }
        }

        if (operation == ReparentOperation::Rename) {
            const FsAccess required =
                removal_permission(*source_type);
            const AccessDecision removal =
                evaluate_fs(decision.source_parent, required);
            if (!removal.allowed) {
                ReparentIssue issue;
                issue.kind = ReparentIssueKind::RemoveDenied;
                issue.message = removal.reason;
                issue.layer_index = first_blocking_layer(removal);
                issue.access = required;
                issue.source_path = decision.source_parent;
                decision.issues.push_back(std::move(issue));
            }
        }

        const FsAccess required_creation =
            creation_permission(*source_type);
        const AccessDecision creation =
            evaluate_fs(decision.destination_parent, required_creation);
        if (!creation.allowed) {
            ReparentIssue issue;
            issue.kind = ReparentIssueKind::CreateDenied;
            issue.message = creation.reason;
            issue.layer_index = first_blocking_layer(creation);
            issue.access = required_creation;
            issue.destination_path = decision.destination_parent;
            decision.issues.push_back(std::move(issue));
        }

        const std::vector<std::string> source_nodes =
            operation == ReparentOperation::HardLink
                ? std::vector<std::string>{source}
                : tree_.descendants(source, true);
        for (const std::string &source_node : source_nodes) {
            std::string destination_node = destination;
            if (source_node != source) {
                destination_node =
                    destination + source_node.substr(source.size());
            }
            if (tree_.exists(destination_node)) {
                ReparentIssue issue;
                issue.kind = ReparentIssueKind::DestinationExists;
                issue.message = "mapped destination path already exists";
                issue.source_path = source_node;
                issue.destination_path = destination_node;
                decision.issues.push_back(std::move(issue));
            }
            const FsAccess source_access = effective_fs(source_node);
            const FsAccess destination_access = effective_fs(destination_node);
            const FsAccess privilege_gain =
                destination_access & ~source_access;
            if (privilege_gain != FsAccess::None) {
                ReparentIssue issue;
                issue.kind = ReparentIssueKind::DestinationHierarchyNotSubset;
                issue.message =
                    "destination grant is not a subset of the source grant";
                issue.source_path = source_node;
                issue.destination_path = destination_node;
                issue.source_access = source_access;
                issue.destination_access = destination_access;
                issue.privilege_gain = privilege_gain;
                decision.issues.push_back(std::move(issue));
            }

            for (std::size_t index = 0u; index < policy_.size(); ++index) {
                const FsAccess handled = handled_fs(index);
                const FsAccess source_layer_access =
                    rule_grants_fs(index, source_node) & handled;
                const FsAccess destination_layer_access =
                    rule_grants_fs(index, destination_node) & handled;
                const FsAccess layer_gain =
                    destination_layer_access & ~source_layer_access & handled;
                const FsAccess extra_gain =
                    layer_gain & ~privilege_gain;
                if (extra_gain == FsAccess::None) {
                    continue;
                }
                ReparentIssue issue;
                issue.kind = ReparentIssueKind::DestinationLayerNotSubset;
                issue.layer_index = index;
                issue.message =
                    "this layer grants additional rights at the destination "
                    "even though the effective intersection happens to deny "
                    "them";
                issue.source_path = source_node;
                issue.destination_path = destination_node;
                issue.source_access = source_layer_access;
                issue.destination_access = destination_layer_access;
                issue.privilege_gain = extra_gain;
                decision.issues.push_back(std::move(issue));
            }
        }

        decision.allowed = decision.issues.empty();
        return decision;
    }

    const PolicyStack &policy_;
    const DirectoryTree &tree_;
};

enum class AuditSeverity {
    Info,
    Warning,
    Error,
    Critical,
};

inline std::string audit_severity_name(AuditSeverity severity) {
    switch (severity) {
    case AuditSeverity::Info:
        return "info";
    case AuditSeverity::Warning:
        return "warning";
    case AuditSeverity::Error:
        return "error";
    case AuditSeverity::Critical:
        return "critical";
    }
    return "unknown";
}

enum class AuditFindingKind {
    MissingHandledAccess,
    UncontrolledHighRiskAccess,
    UnsupportedHandledAccess,
    UnsupportedRuleAccess,
    RuleExceedsHandledAccess,
    MissingRulePath,
    InvalidRule,
    ReparentEscape,
    HardLinkEscape,
};

inline std::string audit_finding_kind_name(AuditFindingKind kind) {
    switch (kind) {
    case AuditFindingKind::MissingHandledAccess:
        return "missing_handled_access";
    case AuditFindingKind::UncontrolledHighRiskAccess:
        return "uncontrolled_high_risk_access";
    case AuditFindingKind::UnsupportedHandledAccess:
        return "unsupported_handled_access";
    case AuditFindingKind::UnsupportedRuleAccess:
        return "unsupported_rule_access";
    case AuditFindingKind::RuleExceedsHandledAccess:
        return "rule_exceeds_handled_access";
    case AuditFindingKind::MissingRulePath:
        return "missing_rule_path";
    case AuditFindingKind::InvalidRule:
        return "invalid_rule";
    case AuditFindingKind::ReparentEscape:
        return "reparent_escape";
    case AuditFindingKind::HardLinkEscape:
        return "hard_link_escape";
    }
    return "unknown";
}

struct ReparentWitness {
    ReparentOperation operation = ReparentOperation::Rename;
    std::string source;
    std::string destination;
    std::string moved_root_source;
    std::string moved_root_destination;
    std::string source_parent;
    std::string destination_parent;
    FsAccess source_access = FsAccess::None;
    FsAccess destination_access = FsAccess::None;
    FsAccess privilege_gain = FsAccess::None;
    bool blocked_by_refer_hierarchy = true;

    std::string describe() const {
        std::string result = reparent_operation_name(operation) + " " +
                             moved_root_source + " -> " +
                             moved_root_destination;
        if (source != moved_root_source) {
            result += " (moved node " + source + " -> " + destination + ")";
        }
        result += " gains " + fs_access_to_string(privilege_gain);
        result += "; source=" + fs_access_to_string(source_access);
        result += "; destination=" +
                  fs_access_to_string(destination_access);
        if (blocked_by_refer_hierarchy) {
            result += "; blocked by REFER hierarchy subset validation";
        }
        return result;
    }
};

struct AuditOptions {
    FsAccess high_risk_fs = default_high_risk_fs();
    NetAccess high_risk_net =
        NetAccess::BindTcp | NetAccess::ConnectTcp;
    bool detect_reparent_escapes = true;
    std::size_t max_escape_findings = 64u;
};

struct AuditFinding {
    AuditFindingKind kind = AuditFindingKind::InvalidRule;
    AuditSeverity severity = AuditSeverity::Warning;
    std::string code;
    std::string message;
    std::optional<std::size_t> layer_index;
    FsAccess access = FsAccess::None;
    NetAccess net_access = NetAccess::None;
    std::string path;
    std::string destination_path;
    std::optional<ReparentWitness> reparent;
};

struct AuditReport {
    std::vector<AuditFinding> findings;

    bool clean() const noexcept {
        return findings.empty();
    }

    bool has_critical() const noexcept {
        for (const AuditFinding &finding : findings) {
            if (finding.severity == AuditSeverity::Critical) {
                return true;
            }
        }
        return false;
    }

    bool has_errors() const noexcept {
        for (const AuditFinding &finding : findings) {
            if (finding.severity == AuditSeverity::Error ||
                finding.severity == AuditSeverity::Critical) {
                return true;
            }
        }
        return false;
    }

    bool has_missing_handled_access() const noexcept {
        for (const AuditFinding &finding : findings) {
            if (finding.kind == AuditFindingKind::MissingHandledAccess) {
                return true;
            }
        }
        return false;
    }

    bool has_uncontrolled_access() const noexcept {
        for (const AuditFinding &finding : findings) {
            if (finding.kind ==
                AuditFindingKind::UncontrolledHighRiskAccess) {
                return true;
            }
        }
        return false;
    }

    bool has_reparent_escape() const noexcept {
        return first_escape() != nullptr;
    }

    const AuditFinding *first_of_kind(AuditFindingKind kind) const noexcept {
        for (const AuditFinding &finding : findings) {
            if (finding.kind == kind) {
                return &finding;
            }
        }
        return nullptr;
    }

    const AuditFinding *first_escape() const noexcept {
        for (const AuditFinding &finding : findings) {
            if (finding.kind == AuditFindingKind::ReparentEscape ||
                finding.kind == AuditFindingKind::HardLinkEscape) {
                return &finding;
            }
        }
        return nullptr;
    }

    std::string summary() const {
        std::size_t critical = 0u;
        std::size_t errors = 0u;
        for (const AuditFinding &finding : findings) {
            if (finding.severity == AuditSeverity::Critical) {
                ++critical;
            } else if (finding.severity == AuditSeverity::Error) {
                ++errors;
            }
        }
        std::string result = std::to_string(findings.size()) + " finding(s)";
        result += ", " + std::to_string(critical) + " critical";
        result += ", " + std::to_string(errors) + " error";
        if (!findings.empty()) {
            result += "; first: " + findings.front().code;
        }
        return result;
    }
};

class PolicyAuditor {
public:
    PolicyAuditor(const PolicyStack &policy, const DirectoryTree &tree,
                  AuditOptions options = AuditOptions())
        : evaluator_(policy, tree), tree_(tree), options_(options) {}

    AuditReport audit() const {
        AuditReport report;
        audit_handled_access(report);
        audit_rules(report);
        if (options_.detect_reparent_escapes) {
            audit_reparent_escapes(report);
        }
        return report;
    }

private:
    void audit_handled_access(AuditReport &report) const {
        const PolicyStack &policy = evaluator_.policy();

        for (std::size_t index = 0u; index < policy.size(); ++index) {
            const PolicyLayer &layer = policy[index];
            const FsAccess supported = fs_supported_by_abi(layer.abi_version);
            const FsAccess declared = layer.handled_access_fs;
            const FsAccess unsupported_declared = declared & ~supported;
            for (FsAccess bit : fs_access_bit_array()) {
                if (!has_fs(unsupported_declared, bit)) {
                    continue;
                }
                AuditFinding finding;
                finding.kind = AuditFindingKind::UnsupportedHandledAccess;
                finding.severity = AuditSeverity::Error;
                finding.code = "fs.unsupported_handled_access";
                finding.message =
                    "handled_access_fs declares a right unsupported by this "
                    "layer's Landlock ABI";
                finding.layer_index = index;
                finding.access = bit;
                report.findings.push_back(std::move(finding));
            }

            const FsAccess missing =
                options_.high_risk_fs & supported & ~declared;
            for (FsAccess bit : fs_access_bit_array()) {
                if (!has_fs(missing, bit)) {
                    continue;
                }
                AuditFinding finding;
                finding.kind = AuditFindingKind::MissingHandledAccess;
                finding.severity = AuditSeverity::Warning;
                finding.code = bit == FsAccess::Refer
                                   ? "fs.refer_not_handled"
                                   : "fs.missing_handled_access";
                finding.message =
                    bit == FsAccess::Refer
                        ? "REFER is not handled by this layer; "
                          "cross-directory reparenting is fail-closed, but "
                          "this layer cannot grant REFER"
                        : "this layer does not restrict the high-risk right; "
                          "the layer relies on inherited restrictions";
                finding.layer_index = index;
                finding.access = bit;
                report.findings.push_back(std::move(finding));
            }

            const NetAccess supported_net =
                net_supported_by_abi(layer.abi_version);
            const NetAccess declared_net = layer.handled_access_net;
            const NetAccess unsupported_net_declared =
                declared_net & ~supported_net;
            for (NetAccess bit : net_access_bit_array()) {
                if (!has_net(unsupported_net_declared, bit)) {
                    continue;
                }
                AuditFinding finding;
                finding.kind = AuditFindingKind::UnsupportedHandledAccess;
                finding.severity = AuditSeverity::Error;
                finding.code = "net.unsupported_handled_access";
                finding.message =
                    "handled_access_net declares a right unsupported by this "
                    "layer's Landlock ABI";
                finding.layer_index = index;
                finding.net_access = bit;
                report.findings.push_back(std::move(finding));
            }

            const NetAccess missing_net =
                options_.high_risk_net & supported_net & ~declared_net;
            for (NetAccess bit : net_access_bit_array()) {
                if (!has_net(missing_net, bit)) {
                    continue;
                }
                AuditFinding finding;
                finding.kind = AuditFindingKind::MissingHandledAccess;
                finding.severity = AuditSeverity::Warning;
                finding.code = "net.missing_handled_access";
                finding.message =
                    "this layer does not restrict the high-risk TCP right; "
                    "the layer relies on inherited restrictions";
                finding.layer_index = index;
                finding.net_access = bit;
                report.findings.push_back(std::move(finding));
            }
        }

        for (FsAccess bit : fs_access_bit_array()) {
            if (!has_fs(options_.high_risk_fs, bit)) {
                continue;
            }
            bool supported_anywhere = false;
            bool handled_anywhere = false;
            for (std::size_t index = 0u; index < policy.size(); ++index) {
                const PolicyLayer &layer = policy[index];
                const FsAccess supported =
                    fs_supported_by_abi(layer.abi_version);
                if (has_fs(supported, bit)) {
                    supported_anywhere = true;
                }
                if (has_fs(supported, bit) &&
                    has_fs(layer.handled_access_fs, bit)) {
                    handled_anywhere = true;
                }
            }
            if (!supported_anywhere || handled_anywhere ||
                bit == FsAccess::Refer) {
                continue;
            }
            AuditFinding finding;
            finding.kind = AuditFindingKind::UncontrolledHighRiskAccess;
            finding.severity = AuditSeverity::Critical;
            finding.code = "fs.uncontrolled_high_risk_access";
            finding.message =
                "no Landlock layer handles this high-risk right; rules cannot "
                "restrict it and the ambient permission remains in force";
            finding.access = bit;
            report.findings.push_back(std::move(finding));
        }

        for (NetAccess bit : net_access_bit_array()) {
            if (!has_net(options_.high_risk_net, bit)) {
                continue;
            }
            bool supported_anywhere = false;
            bool handled_anywhere = false;
            for (std::size_t index = 0u; index < policy.size(); ++index) {
                const PolicyLayer &layer = policy[index];
                const NetAccess supported =
                    net_supported_by_abi(layer.abi_version);
                if (has_net(supported, bit)) {
                    supported_anywhere = true;
                }
                if (has_net(supported, bit) &&
                    has_net(layer.handled_access_net, bit)) {
                    handled_anywhere = true;
                }
            }
            if (!supported_anywhere || handled_anywhere) {
                continue;
            }
            AuditFinding finding;
            finding.kind = AuditFindingKind::UncontrolledHighRiskAccess;
            finding.severity = AuditSeverity::Critical;
            finding.code = "net.uncontrolled_high_risk_access";
            finding.message =
                "no Landlock layer handles this high-risk TCP right; the "
                "ambient network permission remains in force";
            finding.net_access = bit;
            report.findings.push_back(std::move(finding));
        }
    }

    void audit_rules(AuditReport &report) const {
        const PolicyStack &policy = evaluator_.policy();
        for (std::size_t index = 0u; index < policy.size(); ++index) {
            const PolicyLayer &layer = policy[index];
            const FsAccess supported = fs_supported_by_abi(layer.abi_version);
            const NetAccess supported_net =
                net_supported_by_abi(layer.abi_version);

            for (const FsRule &rule : layer.fs_rules) {
                std::string rule_path;
                try {
                    rule_path = normalize_path(rule.path);
                } catch (const PolicyError &error) {
                    AuditFinding finding;
                    finding.kind = AuditFindingKind::InvalidRule;
                    finding.severity = AuditSeverity::Error;
                    finding.code = "fs.invalid_rule_path";
                    finding.message = error.what();
                    finding.layer_index = index;
                    finding.path = rule.path;
                    report.findings.push_back(std::move(finding));
                    continue;
                }

                const FsAccess unsupported = rule.allowed_access & ~supported;
                for (FsAccess bit : fs_access_bit_array()) {
                    if (!has_fs(unsupported, bit)) {
                        continue;
                    }
                    AuditFinding finding;
                    finding.kind = AuditFindingKind::UnsupportedRuleAccess;
                    finding.severity = AuditSeverity::Error;
                    finding.code = "fs.unsupported_rule_access";
                    finding.message =
                        "rule grants a right unsupported by this layer's ABI";
                    finding.layer_index = index;
                    finding.access = bit;
                    finding.path = rule_path;
                    report.findings.push_back(std::move(finding));
                }

                const FsAccess outside_handled =
                    rule.allowed_access & ~layer.handled_access_fs;
                for (FsAccess bit : fs_access_bit_array()) {
                    if (!has_fs(outside_handled, bit)) {
                        continue;
                    }
                    AuditFinding finding;
                    finding.kind = AuditFindingKind::RuleExceedsHandledAccess;
                    finding.severity = AuditSeverity::Error;
                    finding.code = "fs.rule_exceeds_handled_access";
                    finding.message =
                        "allowed_access is not a subset of "
                        "handled_access_fs; the kernel rejects this rule";
                    finding.layer_index = index;
                    finding.access = bit;
                    finding.path = rule_path;
                    report.findings.push_back(std::move(finding));
                }

                if (!tree_.exists(rule_path)) {
                    AuditFinding finding;
                    finding.kind = AuditFindingKind::MissingRulePath;
                    finding.severity = AuditSeverity::Warning;
                    finding.code = "fs.rule_path_missing";
                    finding.message =
                        "rule path does not exist in the modeled directory tree";
                    finding.layer_index = index;
                    finding.path = rule_path;
                    report.findings.push_back(std::move(finding));
                }
            }

            for (const TcpRule &rule : layer.tcp_rules) {
                if (rule.last_port < rule.first_port) {
                    AuditFinding finding;
                    finding.kind = AuditFindingKind::InvalidRule;
                    finding.severity = AuditSeverity::Error;
                    finding.code = "net.invalid_port_range";
                    finding.message = "TCP rule range is reversed";
                    finding.layer_index = index;
                    finding.path = std::to_string(rule.first_port) + "-" +
                                   std::to_string(rule.last_port);
                    report.findings.push_back(std::move(finding));
                }

                const NetAccess unsupported =
                    rule.allowed_access & ~supported_net;
                for (NetAccess bit : net_access_bit_array()) {
                    if (!has_net(unsupported, bit)) {
                        continue;
                    }
                    AuditFinding finding;
                    finding.kind = AuditFindingKind::UnsupportedRuleAccess;
                    finding.severity = AuditSeverity::Error;
                    finding.code = "net.unsupported_rule_access";
                    finding.message =
                        "TCP rule grants a right unsupported by this layer's "
                        "ABI";
                    finding.layer_index = index;
                    finding.net_access = bit;
                    report.findings.push_back(std::move(finding));
                }

                const NetAccess outside_handled =
                    rule.allowed_access & ~layer.handled_access_net;
                for (NetAccess bit : net_access_bit_array()) {
                    if (!has_net(outside_handled, bit)) {
                        continue;
                    }
                    AuditFinding finding;
                    finding.kind = AuditFindingKind::RuleExceedsHandledAccess;
                    finding.severity = AuditSeverity::Error;
                    finding.code = "net.rule_exceeds_handled_access";
                    finding.message =
                        "allowed_access is not a subset of "
                        "handled_access_net; the kernel rejects this rule";
                    finding.layer_index = index;
                    finding.net_access = bit;
                    report.findings.push_back(std::move(finding));
                }
            }
        }
    }

    bool destination_tree_is_free(const std::string &source,
                                  const std::string &candidate,
                                  ReparentOperation operation) const {
        if (tree_.exists(candidate)) {
            return false;
        }
        if (operation == ReparentOperation::HardLink ||
            tree_.require_type(source) != NodeType::Directory) {
            return true;
        }
        const std::vector<std::string> nodes =
            tree_.descendants(source, true);
        for (const std::string &node : nodes) {
            if (node == source) {
                continue;
            }
            const std::string mapped =
                candidate + node.substr(source.size());
            if (tree_.exists(mapped)) {
                return false;
            }
        }
        return true;
    }

    std::optional<std::string>
    choose_destination(const std::string &source,
                       const std::string &destination_parent,
                       ReparentOperation operation) const {
        const std::string base_name = path_basename(source);
        for (std::size_t attempt = 0u; attempt < 16u; ++attempt) {
            std::string name = base_name;
            if (attempt != 0u) {
                name += "~ll-audit" + std::to_string(attempt);
            }
            const std::string candidate =
                path_join(destination_parent, name);
            if (destination_tree_is_free(source, candidate, operation)) {
                return candidate;
            }
        }
        return std::nullopt;
    }

    void audit_reparent_escapes(AuditReport &report) const {
        if (options_.max_escape_findings == 0u) {
            return;
        }

        std::size_t escape_findings = 0u;
        const std::vector<std::string> all_nodes = tree_.all_paths();
        const std::vector<std::string> directories = tree_.directories();

        for (const std::string &source : all_nodes) {
            if (source == "/") {
                continue;
            }
            const NodeType source_type = tree_.require_type(source);
            const std::string source_parent = tree_.parent_of(source);
            std::array<ReparentOperation, 2> operations{
                {ReparentOperation::Rename, ReparentOperation::HardLink}};
            const std::size_t operation_count =
                source_type == NodeType::Directory ? 1u : 2u;

            for (std::size_t operation_index = 0u;
                 operation_index < operation_count; ++operation_index) {
                const ReparentOperation operation =
                    operations[operation_index];
                for (const std::string &destination_parent : directories) {
                    if (destination_parent == source_parent) {
                        continue;
                    }
                    if (operation == ReparentOperation::Rename &&
                        source_type == NodeType::Directory &&
                        detail::is_within_normalized(destination_parent,
                                                     source)) {
                        continue;
                    }
                    const std::optional<std::string> destination =
                        choose_destination(source, destination_parent,
                                           operation);
                    if (!destination.has_value()) {
                        continue;
                    }

                    const ReparentDecision decision =
                        operation == ReparentOperation::Rename
                            ? evaluator_.check_rename(source, *destination)
                            : evaluator_.check_hard_link(source, *destination);

                    const ReparentIssue *best_subset_issue = nullptr;
                    std::size_t best_gain_count = 0u;
                    bool has_effective_subset_issue = false;
                    bool has_other_issue = false;
                    for (const ReparentIssue &issue : decision.issues) {
                        if (issue.kind ==
                            ReparentIssueKind::DestinationHierarchyNotSubset) {
                            has_effective_subset_issue = true;
                            const std::size_t gain_count =
                                fs_popcount(issue.privilege_gain);
                            if (best_subset_issue == nullptr ||
                                gain_count > best_gain_count) {
                                best_subset_issue = &issue;
                                best_gain_count = gain_count;
                            }
                        } else if (issue.kind ==
                                   ReparentIssueKind::
                                       DestinationLayerNotSubset) {
                            continue;
                        } else {
                            has_other_issue = true;
                        }
                    }
                    if (!has_effective_subset_issue || has_other_issue ||
                        best_subset_issue == nullptr) {
                        continue;
                    }

                    ReparentWitness witness;
                    witness.operation = operation;
                    witness.source = best_subset_issue->source_path;
                    witness.destination =
                        best_subset_issue->destination_path;
                    witness.moved_root_source = source;
                    witness.moved_root_destination = *destination;
                    witness.source_parent = source_parent;
                    witness.destination_parent = destination_parent;
                    witness.source_access =
                        best_subset_issue->source_access;
                    witness.destination_access =
                        best_subset_issue->destination_access;
                    witness.privilege_gain =
                        best_subset_issue->privilege_gain;
                    witness.blocked_by_refer_hierarchy = true;

                    AuditFinding finding;
                    finding.kind =
                        operation == ReparentOperation::Rename
                            ? AuditFindingKind::ReparentEscape
                            : AuditFindingKind::HardLinkEscape;
                    finding.severity = AuditSeverity::Critical;
                    finding.code =
                        operation == ReparentOperation::Rename
                            ? "reparent.rename_escape"
                            : "reparent.hard_link_escape";
                    finding.message =
                        "cross-directory " +
                        reparent_operation_name(operation) +
                        " would grant a superset of the source rights at the "
                        "destination; the REFER hierarchy subset guard blocks "
                        "this escape";
                    finding.path = witness.source;
                    finding.destination_path = witness.destination;
                    finding.reparent = witness;
                    report.findings.push_back(std::move(finding));
                    ++escape_findings;
                    if (escape_findings >= options_.max_escape_findings) {
                        return;
                    }
                }
            }
        }
    }

    PolicyEvaluator evaluator_;
    const DirectoryTree &tree_;
    AuditOptions options_;
};

inline AuditReport audit_policy(const PolicyStack &policy,
                                const DirectoryTree &tree,
                                AuditOptions options = AuditOptions()) {
    return PolicyAuditor(policy, tree, options).audit();
}

} // namespace landlock

#endif
