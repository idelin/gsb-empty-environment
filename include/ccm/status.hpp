// Uniform error reporting.  The library never throws across the enclave
// boundary: every fallible entry point returns a Status / Result pair.
#ifndef CCM_STATUS_HPP
#define CCM_STATUS_HPP

#include <string>
#include <utility>

namespace ccm {

enum class StatusCode {
    Ok = 0,
    InvalidArgument,
    Overflow,
    OutsideExternal,
    OverlapsEnclave,
    OutOfBoundsOffset,
    CycleDetected,
    OverlappingRegions,
    ByteReadTwice,
    SchemaInvalid,
    SemanticViolation,
    AdversarialViolation,
};

struct Status {
    StatusCode code = StatusCode::Ok;
    const char* message = "";

    Status() = default;
    Status(StatusCode c, const char* m) : code(c), message(m) {}

    bool ok() const { return code == StatusCode::Ok; }
    explicit operator bool() const { return ok(); }

    static Status ok_status() { return Status{}; }
};

inline const char* status_name(StatusCode code) {
    switch (code) {
        case StatusCode::Ok:
            return "Ok";
        case StatusCode::InvalidArgument:
            return "InvalidArgument";
        case StatusCode::Overflow:
            return "Overflow";
        case StatusCode::OutsideExternal:
            return "OutsideExternal";
        case StatusCode::OverlapsEnclave:
            return "OverlapsEnclave";
        case StatusCode::OutOfBoundsOffset:
            return "OutOfBoundsOffset";
        case StatusCode::CycleDetected:
            return "CycleDetected";
        case StatusCode::OverlappingRegions:
            return "OverlappingRegions";
        case StatusCode::ByteReadTwice:
            return "ByteReadTwice";
        case StatusCode::SchemaInvalid:
            return "SchemaInvalid";
        case StatusCode::SemanticViolation:
            return "SemanticViolation";
        case StatusCode::AdversarialViolation:
            return "AdversarialViolation";
    }
    return "Unknown";
}

template <typename T>
class Result {
public:
    Result(T value) : status_(), value_(std::move(value)) {}  // NOLINT
    Result(Status err) : status_(err), value_() {}            // NOLINT

    bool ok() const { return status_.ok(); }
    explicit operator bool() const { return ok(); }
    const Status& status() const { return status_; }

    const T& value() const { return value_; }
    T& value() { return value_; }

private:
    Status status_;
    T value_;
};

}  // namespace ccm

#endif  // CCM_STATUS_HPP
