#pragma once

#include <algorithm>
#include <cctype>
#include <cmath>
#include <cstddef>
#include <cstdint>
#include <deque>
#include <functional>
#include <iterator>
#include <limits>
#include <map>
#include <mutex>
#include <optional>
#include <set>
#include <sstream>
#include <string>
#include <string_view>
#include <unordered_map>
#include <utility>
#include <vector>

namespace netsec {

inline constexpr std::string_view kVersion = "1.0.0";

enum class MessageType : std::uint8_t {
    Unknown = 0,
    UNKNOWN = 0,
    AdsbPosition = 1,
    ADS_B_POSITION = 1,
    ADSB_POSITION = 1,
    ADSB_Position = 1,
    AdsbPositionReport = 1,
    AirbornePosition = 1,
    Position = 1,
    AdsbVelocity = 2,
    ADS_B_VELOCITY = 2,
    ADSB_VELOCITY = 2,
    ADSB_Velocity = 2,
    AdsbVelocityReport = 2,
    Velocity = 2,
    AdsbIdentification = 3,
    ADS_B_IDENTIFICATION = 3,
    ADSB_IDENTIFICATION = 3,
    ADSB_Identification = 3,
    Callsign = 3,
    Identification = 3,
    AdsbSurface = 4,
    ADS_B_SURFACE = 4,
    SurfacePosition = 4,
    ModeS = 5,
    MODE_S = 5,
    ModeSIdentification = 6,
    ModeSAltitude = 7,
    Radar = 8,
    RADAR = 8,
    RadarPlot = 8,
    Multilateration = 9,
    MLAT = 9,
};

enum class SourceType : std::uint8_t {
    Unknown = 0,
    UNKNOWN = 0,
    Adsb = 1,
    ADS_B = 1,
    ADSB = 1,
    ADS_BReceiver = 1,
    AdsbReceiver = 1,
    ModeS = 2,
    MODE_S = 2,
    Radar = 3,
    RADAR = 3,
    Mlat = 4,
    MLAT = 4,
    Multilateration = 4,
    Composite = 5,
    COMPOSITE = 5,
    Gnss = 6,
    GNSS = 6,
    Transponder = 7,
    TRANSPONDER = 7,
};

enum class MessageFlag : std::uint32_t {
    None = 0,
    NONE = 0,
    InvalidType = 1u << 0,
    INVALID_TYPE = InvalidType,
    InvalidSource = 1u << 1,
    INVALID_SOURCE = InvalidSource,
    InvalidIdentity = 1u << 2,
    INVALID_IDENTITY = InvalidIdentity,
    InvalidPosition = 1u << 3,
    INVALID_POSITION = InvalidPosition,
    InvalidAltitude = 1u << 4,
    INVALID_ALTITUDE = InvalidAltitude,
    InvalidSpeed = 1u << 5,
    INVALID_SPEED = InvalidSpeed,
    InvalidHeading = 1u << 6,
    INVALID_HEADING = InvalidHeading,
    InvalidVerticalRate = 1u << 7,
    INVALID_VERTICAL_RATE = InvalidVerticalRate,
    InvalidTimestamp = 1u << 8,
    INVALID_TIMESTAMP = InvalidTimestamp,
    MissingField = 1u << 9,
    MISSING_FIELD = MissingField,
    CrcFailure = 1u << 10,
    CRC_FAILURE = CrcFailure,
    OutOfEnvelope = 1u << 11,
    OUT_OF_ENVELOPE = OutOfEnvelope,
    PositionJump = 1u << 12,
    POSITION_JUMP = PositionJump,
    AltitudeJump = 1u << 13,
    ALTITUDE_JUMP = AltitudeJump,
    SpeedJump = 1u << 14,
    SPEED_JUMP = SpeedJump,
    Duplicate = 1u << 15,
    DUPLICATE = Duplicate,
    Replay = 1u << 16,
    REPLAY = Replay,
    Stale = 1u << 17,
    STALE = Stale,
    IdentitySpoof = 1u << 18,
    IDENTITY_SPOOF = IdentitySpoof,
    IdentityConflict = 1u << 19,
    IDENTITY_CONFLICT = IdentityConflict,
    PositionInjection = 1u << 20,
    POSITION_INJECTION = PositionInjection,
    MultiSourceMismatch = 1u << 21,
    MULTI_SOURCE_MISMATCH = MultiSourceMismatch,
    AltitudeMismatch = 1u << 22,
    ALTITUDE_MISMATCH = AltitudeMismatch,
    Uncorroborated = 1u << 23,
    UNCORROBORATED = Uncorroborated,
    LowSignalQuality = 1u << 24,
    LOW_SIGNAL_QUALITY = LowSignalQuality,
    LowReceiverQuality = 1u << 25,
    LOW_RECEIVER_QUALITY = LowReceiverQuality,
    LowCredibility = 1u << 26,
    LOW_CREDIBILITY = LowCredibility,
    SequenceRegression = 1u << 27,
    SEQUENCE_REGRESSION = SequenceRegression,
    TrackConflict = 1u << 28,
    TRACK_CONFLICT = TrackConflict,
};

using Flag = MessageFlag;
using SurveillanceType = MessageType;
using MessageSource = SourceType;
using SurveillanceSource = SourceType;

inline std::string_view flag_name(MessageFlag flag) {
    switch (flag) {
        case MessageFlag::InvalidType: return "invalid_type";
        case MessageFlag::InvalidSource: return "invalid_source";
        case MessageFlag::InvalidIdentity: return "invalid_identity";
        case MessageFlag::InvalidPosition: return "invalid_position";
        case MessageFlag::InvalidAltitude: return "invalid_altitude";
        case MessageFlag::InvalidSpeed: return "invalid_speed";
        case MessageFlag::InvalidHeading: return "invalid_heading";
        case MessageFlag::InvalidVerticalRate: return "invalid_vertical_rate";
        case MessageFlag::InvalidTimestamp: return "invalid_timestamp";
        case MessageFlag::MissingField: return "missing_field";
        case MessageFlag::CrcFailure: return "crc_failure";
        case MessageFlag::OutOfEnvelope: return "out_of_envelope";
        case MessageFlag::PositionJump: return "position_jump";
        case MessageFlag::AltitudeJump: return "altitude_jump";
        case MessageFlag::SpeedJump: return "speed_jump";
        case MessageFlag::Duplicate: return "duplicate";
        case MessageFlag::Replay: return "replay";
        case MessageFlag::Stale: return "stale";
        case MessageFlag::IdentitySpoof: return "identity_spoof";
        case MessageFlag::IdentityConflict: return "identity_conflict";
        case MessageFlag::PositionInjection: return "position_injection";
        case MessageFlag::MultiSourceMismatch: return "multi_source_mismatch";
        case MessageFlag::AltitudeMismatch: return "altitude_mismatch";
        case MessageFlag::Uncorroborated: return "uncorroborated";
        case MessageFlag::LowSignalQuality: return "low_signal_quality";
        case MessageFlag::LowReceiverQuality: return "low_receiver_quality";
        case MessageFlag::LowCredibility: return "low_credibility";
        case MessageFlag::SequenceRegression: return "sequence_regression";
        case MessageFlag::TrackConflict: return "track_conflict";
        case MessageFlag::None: return "none";
    }
    return "unknown";
}

inline std::string_view to_string(MessageFlag flag) { return flag_name(flag); }

inline constexpr std::uint32_t flag_mask(MessageFlag flag) {
    return static_cast<std::uint32_t>(flag);
}

class FlagSet {
public:
    constexpr FlagSet() = default;
    constexpr FlagSet(MessageFlag flag) : bits_(flag_mask(flag)) {}
    constexpr explicit FlagSet(std::uint32_t bits) : bits_(bits) {}

    constexpr void add(MessageFlag flag) { bits_ |= flag_mask(flag); }
    constexpr void remove(MessageFlag flag) { bits_ &= ~flag_mask(flag); }
    constexpr void clear() { bits_ = 0; }
    void push_back(MessageFlag flag) { add(flag); }
    void push_back(std::string_view name) {
        for (const auto flag : all_flags()) {
            if (flag_name(flag) == name) {
                add(flag);
                return;
            }
        }
    }
    constexpr bool contains(MessageFlag flag) const {
        return (bits_ & flag_mask(flag)) != 0;
    }

    bool contains(std::string_view name) const {
        for (const auto flag : to_vector()) {
            if (flag_name(flag) == name) {
                return true;
            }
        }
        return false;
    }

    constexpr bool has(MessageFlag flag) const { return contains(flag); }
    constexpr bool is_set(MessageFlag flag) const { return contains(flag); }
    constexpr bool empty() const { return bits_ == 0; }
    constexpr bool none() const { return bits_ == 0; }
    constexpr bool any() const { return bits_ != 0; }
    constexpr explicit operator bool() const { return bits_ != 0; }
    constexpr std::uint32_t bits() const { return bits_; }
    constexpr std::uint32_t mask() const { return bits_; }

    constexpr std::size_t count() const {
        std::uint32_t value = bits_;
        std::size_t result = 0;
        while (value != 0) {
            value &= (value - 1);
            ++result;
        }
        return result;
    }

    constexpr std::size_t count(MessageFlag flag) const { return contains(flag) ? 1u : 0u; }
    std::size_t count(std::string_view name) const { return contains(name) ? 1u : 0u; }
    constexpr std::size_t size() const { return count(); }

    class const_iterator {
    public:
        using iterator_category = std::forward_iterator_tag;
        using value_type = MessageFlag;
        using difference_type = std::ptrdiff_t;
        using pointer = const MessageFlag*;
        using reference = MessageFlag;

        constexpr const_iterator() = default;
        constexpr explicit const_iterator(std::uint32_t remaining)
            : remaining_(remaining), current_(lowest_bit(remaining)) {}

        constexpr MessageFlag operator*() const {
            return static_cast<MessageFlag>(current_);
        }
        constexpr const_iterator& operator++() {
            remaining_ &= ~current_;
            current_ = lowest_bit(remaining_);
            return *this;
        }
        constexpr const_iterator operator++(int) {
            const_iterator previous = *this;
            ++(*this);
            return previous;
        }
        constexpr bool operator==(const const_iterator& other) const {
            return remaining_ == other.remaining_;
        }
        constexpr bool operator!=(const const_iterator& other) const {
            return !(*this == other);
        }

    private:
        static constexpr std::uint32_t lowest_bit(std::uint32_t value) {
            return value == 0 ? 0 : (value & (~value + 1u));
        }

        std::uint32_t remaining_ = 0;
        std::uint32_t current_ = 0;
    };

    constexpr const_iterator begin() const { return const_iterator(bits_); }
    constexpr const_iterator end() const { return const_iterator(); }

    std::vector<MessageFlag> to_vector() const {
        std::vector<MessageFlag> result;
        std::uint32_t rest = bits_;
        while (rest != 0) {
            const std::uint32_t bit = rest & (~rest + 1u);
            result.push_back(static_cast<MessageFlag>(bit));
            rest &= ~bit;
        }
        return result;
    }

    std::vector<std::string> names() const {
        std::vector<std::string> result;
        for (const auto flag : to_vector()) {
            result.emplace_back(flag_name(flag));
        }
        return result;
    }

    operator std::vector<std::string>() const { return names(); }

    std::string to_string(std::string_view separator = ",") const {
        std::string result;
        for (const auto flag : to_vector()) {
            if (!result.empty()) {
                result.append(separator);
            }
            result.append(flag_name(flag));
        }
        return result;
    }

    static std::vector<MessageFlag> all_flags() {
        return {MessageFlag::InvalidType,
                MessageFlag::InvalidSource,
                MessageFlag::InvalidIdentity,
                MessageFlag::InvalidPosition,
                MessageFlag::InvalidAltitude,
                MessageFlag::InvalidSpeed,
                MessageFlag::InvalidHeading,
                MessageFlag::InvalidVerticalRate,
                MessageFlag::InvalidTimestamp,
                MessageFlag::MissingField,
                MessageFlag::CrcFailure,
                MessageFlag::OutOfEnvelope,
                MessageFlag::PositionJump,
                MessageFlag::AltitudeJump,
                MessageFlag::SpeedJump,
                MessageFlag::Duplicate,
                MessageFlag::Replay,
                MessageFlag::Stale,
                MessageFlag::IdentitySpoof,
                MessageFlag::IdentityConflict,
                MessageFlag::PositionInjection,
                MessageFlag::MultiSourceMismatch,
                MessageFlag::AltitudeMismatch,
                MessageFlag::Uncorroborated,
                MessageFlag::LowSignalQuality,
                MessageFlag::LowReceiverQuality,
                MessageFlag::LowCredibility,
                MessageFlag::SequenceRegression,
                MessageFlag::TrackConflict};
    }

    constexpr FlagSet& operator|=(MessageFlag flag) {
        add(flag);
        return *this;
    }
    constexpr FlagSet& operator|=(FlagSet other) {
        bits_ |= other.bits_;
        return *this;
    }
    constexpr FlagSet& operator&=(FlagSet other) {
        bits_ &= other.bits_;
        return *this;
    }

private:
    std::uint32_t bits_ = 0;
};

constexpr FlagSet operator|(FlagSet lhs, MessageFlag rhs) { return lhs |= rhs; }
constexpr FlagSet operator|(MessageFlag lhs, MessageFlag rhs) { return FlagSet(lhs) | rhs; }
constexpr FlagSet operator|(FlagSet lhs, FlagSet rhs) { return lhs |= rhs; }
constexpr FlagSet operator&(FlagSet lhs, MessageFlag rhs) {
    return FlagSet(lhs.bits() & flag_mask(rhs));
}
constexpr FlagSet operator&(FlagSet lhs, FlagSet rhs) {
    return FlagSet(lhs.bits() & rhs.bits());
}
constexpr bool operator==(FlagSet lhs, MessageFlag rhs) { return lhs.bits() == flag_mask(rhs); }
constexpr bool operator!=(FlagSet lhs, MessageFlag rhs) { return !(lhs == rhs); }
constexpr bool operator==(MessageFlag lhs, FlagSet rhs) { return rhs == lhs; }
constexpr bool operator!=(MessageFlag lhs, FlagSet rhs) { return !(rhs == lhs); }

inline std::string to_string(const FlagSet& flags) { return flags.to_string(); }

inline constexpr MessageFlag FLAG_INVALID_TYPE = MessageFlag::InvalidType;
inline constexpr MessageFlag FLAG_INVALID_IDENTITY = MessageFlag::InvalidIdentity;
inline constexpr MessageFlag FLAG_INVALID_POSITION = MessageFlag::InvalidPosition;
inline constexpr MessageFlag FLAG_INVALID_ALTITUDE = MessageFlag::InvalidAltitude;
inline constexpr MessageFlag FLAG_INVALID_SPEED = MessageFlag::InvalidSpeed;
inline constexpr MessageFlag FLAG_INVALID_TIMESTAMP = MessageFlag::InvalidTimestamp;
inline constexpr MessageFlag FLAG_CRC_FAILURE = MessageFlag::CrcFailure;
inline constexpr MessageFlag FLAG_OUT_OF_ENVELOPE = MessageFlag::OutOfEnvelope;
inline constexpr MessageFlag FLAG_POSITION_JUMP = MessageFlag::PositionJump;
inline constexpr MessageFlag FLAG_ALTITUDE_JUMP = MessageFlag::AltitudeJump;
inline constexpr MessageFlag FLAG_SPEED_JUMP = MessageFlag::SpeedJump;
inline constexpr MessageFlag FLAG_DUPLICATE = MessageFlag::Duplicate;
inline constexpr MessageFlag FLAG_REPLAY = MessageFlag::Replay;
inline constexpr MessageFlag FLAG_STALE = MessageFlag::Stale;
inline constexpr MessageFlag FLAG_IDENTITY_SPOOF = MessageFlag::IdentitySpoof;
inline constexpr MessageFlag FLAG_IDENTITY_CONFLICT = MessageFlag::IdentityConflict;
inline constexpr MessageFlag FLAG_POSITION_INJECTION = MessageFlag::PositionInjection;
inline constexpr MessageFlag FLAG_MULTI_SOURCE_MISMATCH = MessageFlag::MultiSourceMismatch;
inline constexpr MessageFlag FLAG_ALTITUDE_MISMATCH = MessageFlag::AltitudeMismatch;
inline constexpr MessageFlag FLAG_UNCORROBORATED = MessageFlag::Uncorroborated;
inline constexpr MessageFlag FLAG_LOW_SIGNAL_QUALITY = MessageFlag::LowSignalQuality;
inline constexpr MessageFlag FLAG_LOW_RECEIVER_QUALITY = MessageFlag::LowReceiverQuality;
inline constexpr MessageFlag FLAG_LOW_CREDIBILITY = MessageFlag::LowCredibility;
inline constexpr MessageFlag FLAG_SEQUENCE_REGRESSION = MessageFlag::SequenceRegression;
inline constexpr MessageFlag FLAG_TRACK_CONFLICT = MessageFlag::TrackConflict;

enum class ValidationStatus : std::uint8_t {
    Invalid = 0,
    INVALID = 0,
    Rejected = 1,
    REJECTED = 1,
    Duplicate = 2,
    DUPLICATE = 2,
    Replay = 3,
    REPLAY = 3,
    Spoof = 4,
    SPOOF = 4,
    Suspicious = 5,
    SUSPICIOUS = 5,
    Unverified = 6,
    UNVERIFIED = 6,
    Accepted = 7,
    ACCEPTED = 7,
    Verified = 8,
    VERIFIED = 8,
    Authentic = 9,
    AUTHENTIC = 9,
    Ok = 7,
    OK = 7,
    Valid = 7,
    VALID = 7,
};

using Status = ValidationStatus;
using ValidationFlag = MessageFlag;
using ResultFlag = MessageFlag;
using FlagType = MessageFlag;
using Flags = FlagSet;
using ValidationFlags = FlagSet;

inline std::string_view to_string(MessageType type) {
    switch (type) {
        case MessageType::AdsbPosition: return "adsb_position";
        case MessageType::AdsbVelocity: return "adsb_velocity";
        case MessageType::AdsbIdentification: return "adsb_identification";
        case MessageType::AdsbSurface: return "adsb_surface";
        case MessageType::ModeS: return "mode_s";
        case MessageType::ModeSIdentification: return "mode_s_identification";
        case MessageType::ModeSAltitude: return "mode_s_altitude";
        case MessageType::Radar: return "radar";
        case MessageType::Multilateration: return "multilateration";
        case MessageType::Unknown: return "unknown";
    }
    return "unknown";
}

inline std::string_view to_string(SourceType source) {
    switch (source) {
        case SourceType::Adsb: return "adsb";
        case SourceType::ModeS: return "mode_s";
        case SourceType::Radar: return "radar";
        case SourceType::Mlat: return "mlat";
        case SourceType::Composite: return "composite";
        case SourceType::Gnss: return "gnss";
        case SourceType::Transponder: return "transponder";
        case SourceType::Unknown: return "unknown";
    }
    return "unknown";
}

inline std::string_view to_string(ValidationStatus status) {
    switch (status) {
        case ValidationStatus::Invalid: return "invalid";
        case ValidationStatus::Rejected: return "rejected";
        case ValidationStatus::Duplicate: return "duplicate";
        case ValidationStatus::Replay: return "replay";
        case ValidationStatus::Spoof: return "spoof";
        case ValidationStatus::Suspicious: return "suspicious";
        case ValidationStatus::Unverified: return "unverified";
        case ValidationStatus::Accepted: return "accepted";
        case ValidationStatus::Verified: return "verified";
        case ValidationStatus::Authentic: return "authentic";
    }
    return "unknown";
}

inline MessageType parse_message_type(std::string_view text) {
    std::string value(text);
    std::transform(value.begin(), value.end(), value.begin(), [](unsigned char c) {
        return static_cast<char>(std::toupper(c));
    });
    std::replace(value.begin(), value.end(), '-', '_');
    if (value == "ADSB_POSITION" || value == "POSITION" || value == "AIRBORNE_POSITION") {
        return MessageType::AdsbPosition;
    }
    if (value == "ADSB_VELOCITY" || value == "VELOCITY") {
        return MessageType::AdsbVelocity;
    }
    if (value == "ADSB_IDENTIFICATION" || value == "IDENTIFICATION" || value == "CALLSIGN") {
        return MessageType::AdsbIdentification;
    }
    if (value == "ADSB_SURFACE" || value == "SURFACE") {
        return MessageType::AdsbSurface;
    }
    if (value == "MODE_S" || value == "MODES") {
        return MessageType::ModeS;
    }
    if (value == "RADAR" || value == "RADAR_PLOT") {
        return MessageType::Radar;
    }
    if (value == "MLAT" || value == "MULTILATERATION") {
        return MessageType::Multilateration;
    }
    return MessageType::Unknown;
}

inline SourceType parse_source_type(std::string_view text) {
    std::string value(text);
    std::transform(value.begin(), value.end(), value.begin(), [](unsigned char c) {
        return static_cast<char>(std::toupper(c));
    });
    std::replace(value.begin(), value.end(), '-', '_');
    if (value == "ADSB" || value == "ADS_B") return SourceType::Adsb;
    if (value == "MODE_S" || value == "MODES") return SourceType::ModeS;
    if (value == "RADAR") return SourceType::Radar;
    if (value == "MLAT" || value == "MULTILATERATION") return SourceType::Mlat;
    if (value == "COMPOSITE") return SourceType::Composite;
    if (value == "GNSS") return SourceType::Gnss;
    if (value == "TRANSPONDER") return SourceType::Transponder;
    return SourceType::Unknown;
}

struct SurveillanceMessage {
    MessageType type = MessageType::Unknown;
    MessageType message_type = MessageType::Unknown;
    SourceType source = SourceType::Unknown;
    SourceType source_type = SourceType::Unknown;

    std::string flight_id;
    std::string callsign;
    std::string target_id;
    std::string aircraft_id;
    std::string icao_address;
    std::string icao24;
    std::string icao;
    std::string aircraft_address;
    std::string receiver_id;
    std::string source_id;
    std::string receiver;

    double latitude = std::numeric_limits<double>::quiet_NaN();
    double lat = std::numeric_limits<double>::quiet_NaN();
    double latitude_deg = std::numeric_limits<double>::quiet_NaN();
    double longitude = std::numeric_limits<double>::quiet_NaN();
    double lon = std::numeric_limits<double>::quiet_NaN();
    double longitude_deg = std::numeric_limits<double>::quiet_NaN();
    double altitude_ft = std::numeric_limits<double>::quiet_NaN();
    double altitude = std::numeric_limits<double>::quiet_NaN();
    double alt = std::numeric_limits<double>::quiet_NaN();
    double altitude_m = std::numeric_limits<double>::quiet_NaN();
    double barometric_altitude_ft = std::numeric_limits<double>::quiet_NaN();
    double geometric_altitude_ft = std::numeric_limits<double>::quiet_NaN();
    double ground_speed_kts = std::numeric_limits<double>::quiet_NaN();
    double speed_kts = std::numeric_limits<double>::quiet_NaN();
    double speed = std::numeric_limits<double>::quiet_NaN();
    double ground_speed = std::numeric_limits<double>::quiet_NaN();
    double velocity_kts = std::numeric_limits<double>::quiet_NaN();
    double heading_deg = std::numeric_limits<double>::quiet_NaN();
    double raw_heading_deg = std::numeric_limits<double>::quiet_NaN();
    double heading = std::numeric_limits<double>::quiet_NaN();
    double track_deg = std::numeric_limits<double>::quiet_NaN();
    double vertical_rate_fpm = std::numeric_limits<double>::quiet_NaN();
    double vertical_rate = std::numeric_limits<double>::quiet_NaN();
    double vertical_speed_fpm = std::numeric_limits<double>::quiet_NaN();
    double timestamp = std::numeric_limits<double>::quiet_NaN();
    double timestamp_s = std::numeric_limits<double>::quiet_NaN();
    double timestamp_ms = std::numeric_limits<double>::quiet_NaN();
    double timestamp_us = std::numeric_limits<double>::quiet_NaN();
    double timestamp_ns = std::numeric_limits<double>::quiet_NaN();
    double time = std::numeric_limits<double>::quiet_NaN();
    double time_s = std::numeric_limits<double>::quiet_NaN();
    double time_ms = std::numeric_limits<double>::quiet_NaN();
    double ts = std::numeric_limits<double>::quiet_NaN();

    std::int64_t sequence = -1;
    std::int64_t sequence_number = -1;
    std::int64_t sequence_no = -1;
    std::string payload_hash;
    std::string raw_payload;
    bool crc_valid = true;
    bool crc_ok = true;
    bool valid_crc = true;
    bool on_surface = false;

    double receiver_quality = std::numeric_limits<double>::quiet_NaN();
    double signal_quality = std::numeric_limits<double>::quiet_NaN();
    double signal_strength = std::numeric_limits<double>::quiet_NaN();
    double snr_db = std::numeric_limits<double>::quiet_NaN();
    double snr = std::numeric_limits<double>::quiet_NaN();
    double rssi_dbm = std::numeric_limits<double>::quiet_NaN();
    double rssi = std::numeric_limits<double>::quiet_NaN();
    double range_nm = std::numeric_limits<double>::quiet_NaN();
    double azimuth_deg = std::numeric_limits<double>::quiet_NaN();
    double receiver_latitude = std::numeric_limits<double>::quiet_NaN();
    double receiver_longitude = std::numeric_limits<double>::quiet_NaN();

    void set_position(double new_lat, double new_lon) {
        latitude = new_lat;
        longitude = new_lon;
    }

    void set_altitude(double new_altitude_ft) { altitude_ft = new_altitude_ft; }

    static SurveillanceMessage position(std::string id, std::string address, double lat_value,
                                        double lon_value, double alt_ft, double ts) {
        SurveillanceMessage message;
        message.type = MessageType::AdsbPosition;
        message.source = SourceType::Adsb;
        message.flight_id = std::move(id);
        message.icao_address = std::move(address);
        message.latitude = lat_value;
        message.longitude = lon_value;
        message.altitude_ft = alt_ft;
        message.timestamp = ts;
        return message;
    }

    static SurveillanceMessage velocity(std::string id, std::string address, double speed_kts_value,
                                        double heading_value, double vertical_rate_value, double ts) {
        SurveillanceMessage message;
        message.type = MessageType::AdsbVelocity;
        message.source = SourceType::Adsb;
        message.flight_id = std::move(id);
        message.icao_address = std::move(address);
        message.ground_speed_kts = speed_kts_value;
        message.heading_deg = heading_value;
        message.vertical_rate_fpm = vertical_rate_value;
        message.timestamp = ts;
        return message;
    }

    static SurveillanceMessage radar(std::string id, double lat_value, double lon_value,
                                     double alt_ft, double ts, std::string receiver) {
        SurveillanceMessage message;
        message.type = MessageType::Radar;
        message.source = SourceType::Radar;
        message.flight_id = std::move(id);
        message.latitude = lat_value;
        message.longitude = lon_value;
        message.altitude_ft = alt_ft;
        message.timestamp = ts;
        message.receiver_id = std::move(receiver);
        return message;
    }
};

using Message = SurveillanceMessage;
using AdsbMessage = SurveillanceMessage;

inline SurveillanceMessage make_position_message(std::string flight_id, std::string icao,
                                                 double latitude, double longitude,
                                                 double altitude_ft, double timestamp) {
    return SurveillanceMessage::position(std::move(flight_id), std::move(icao), latitude,
                                         longitude, altitude_ft, timestamp);
}

inline SurveillanceMessage make_velocity_message(std::string flight_id, std::string icao,
                                                 double ground_speed_kts, double heading_deg,
                                                 double vertical_rate_fpm, double timestamp) {
    return SurveillanceMessage::velocity(std::move(flight_id), std::move(icao),
                                         ground_speed_kts, heading_deg, vertical_rate_fpm,
                                         timestamp);
}

inline SurveillanceMessage make_radar_message(std::string target_id, double latitude,
                                              double longitude, double altitude_ft,
                                              double timestamp, std::string receiver_id) {
    return SurveillanceMessage::radar(std::move(target_id), latitude, longitude, altitude_ft,
                                      timestamp, std::move(receiver_id));
}

struct SurveillanceConfig {
    double min_latitude = -90.0;
    double max_latitude = 90.0;
    double min_longitude = -180.0;
    double max_longitude = 180.0;
    double min_altitude_ft = -1500.0;
    double max_altitude_ft = 60000.0;
    double min_ground_speed_kts = 0.0;
    double max_ground_speed_kts = 1500.0;
    double max_surface_speed_kts = 300.0;
    double max_vertical_rate_fpm = 20000.0;
    double max_heading_deg = 360.0;
    double max_range_nm = 2000.0;
    std::size_t max_callsign_length = 8;
    std::size_t max_track_id_length = 32;
    double min_timestamp_s = 0.0;

    double max_position_jump_nm = 2.0;
    double position_jump_speed_margin_kts = 200.0;
    double max_altitude_jump_ft = 3000.0;
    double altitude_jump_rate_margin_fpm = 2000.0;
    double max_speed_change_kts = 300.0;
    double severe_jump_factor = 3.0;

    double duplicate_window_s = 30.0;
    double replay_window_s = 300.0;
    double correlation_window_s = 12.0;
    double identity_window_s = 120.0;
    double max_timestamp_latency_s = 10.0;

    double radar_position_gate_nm = 2.5;
    double altitude_gate_ft = 1200.0;
    double speed_gate_kts = 200.0;
    double position_gate_nm = 3.0;

    double low_quality_threshold = 0.35;
    double low_credibility_threshold = 0.45;
    double authentic_threshold = 0.70;
    double trusted_threshold = 0.85;
    double min_track_credibility = 0.25;
    bool require_corroboration = true;
    bool reject_uncorroborated = false;
    bool reject_on_jump = true;
    bool reject_on_mismatch = true;
    bool reject_on_spoof = true;
    bool reject_on_replay = true;
    bool reject_on_duplicate = true;
    bool require_icao_for_adsb = false;
    bool require_crc = false;

    static SurveillanceConfig permissive() {
        SurveillanceConfig config;
        config.require_corroboration = false;
        config.reject_uncorroborated = false;
        config.authentic_threshold = 0.40;
        return config;
    }

    static SurveillanceConfig strict() {
        SurveillanceConfig config;
        config.max_position_jump_nm = 2.0;
        config.radar_position_gate_nm = 1.5;
        config.altitude_gate_ft = 500.0;
        config.authentic_threshold = 0.80;
        config.require_icao_for_adsb = true;
        config.require_crc = true;
        return config;
    }
};

using Config = SurveillanceConfig;
using TrackingConfig = SurveillanceConfig;
using ValidationConfig = SurveillanceConfig;
using FusionConfig = SurveillanceConfig;
using SurveillanceOptions = SurveillanceConfig;

struct ValidationResult {
    bool valid = false;
    bool accepted = false;
    bool authentic = false;
    bool verified = false;
    bool suspicious = false;
    bool flagged = false;

    bool duplicate = false;
    bool replay = false;
    bool stale = false;
    bool trusted_correction = false;
    bool corrected = false;
    bool identity_spoof = false;
    bool identity_conflict = false;
    bool position_injection = false;
    bool jump = false;
    bool position_jump = false;
    bool altitude_jump = false;
    bool speed_jump = false;
    bool out_of_envelope = false;
    bool multi_source_mismatch = false;
    bool source_mismatch = false;
    bool altitude_mismatch = false;
    bool wrong_altitude = false;
    bool uncorroborated = false;
    bool crc_failure = false;
    bool low_quality = false;
    bool low_credit = false;

    double credibility = 0.0;
    double confidence = 0.0;
    double score = 0.0;
    double trust_score = 0.0;
    double authenticity_score = 0.0;
    double authenticity = 0.0;
    int corroborating_sources = 0;
    int radar_confirmations = 0;
    std::size_t observation_count = 0;

    ValidationStatus status = ValidationStatus::Invalid;
    FlagSet flags;
    std::uint32_t flag_mask = 0;
    std::vector<std::string> reasons;
    std::string reason;

    std::string flight_id;
    std::string callsign;
    std::string icao_address;
    std::string track_id;
    std::string track_key;
    std::string receiver_id;
    MessageType type = MessageType::Unknown;
    SourceType source = SourceType::Unknown;
    double timestamp = std::numeric_limits<double>::quiet_NaN();

    bool has_flag(MessageFlag flag) const { return flags.contains(flag); }
    bool has(MessageFlag flag) const { return flags.contains(flag); }
    bool is_flagged(MessageFlag flag) const { return flags.contains(flag); }
    bool contains(std::string_view name) const { return flags.contains(name); }
    bool has_flag(std::string_view name) const { return flags.contains(name); }
    bool ok() const { return accepted; }
    bool is_valid() const { return valid; }
    bool is_accepted() const { return accepted; }
    bool is_authentic() const { return authentic; }
    bool is_duplicate() const { return duplicate; }
    bool is_replay() const { return replay; }
    bool is_spoofed() const { return identity_spoof; }
    bool is_injected() const { return position_injection; }
    double trust() const { return credibility; }
    std::vector<std::string> flag_names() const { return flags.names(); }
    std::string flag_text() const { return flags.to_string(); }
    explicit operator bool() const { return accepted; }
};

using Result = ValidationResult;
using Report = ValidationResult;
using Decision = ValidationResult;

enum class TrackVerdict : std::uint8_t {
    Unknown = 0,
    UNKNOWN = 0,
    Authentic = 1,
    AUTHENTIC = 1,
    Unverified = 2,
    UNVERIFIED = 2,
    Suspicious = 3,
    SUSPICIOUS = 3,
    Spoofed = 4,
    SPOOFED = 4,
};

using Verdict = TrackVerdict;

inline std::string_view to_string(TrackVerdict verdict) {
    switch (verdict) {
        case TrackVerdict::Unknown: return "unknown";
        case TrackVerdict::Authentic: return "authentic";
        case TrackVerdict::Unverified: return "unverified";
        case TrackVerdict::Suspicious: return "suspicious";
        case TrackVerdict::Spoofed: return "spoofed";
    }
    return "unknown";
}

struct TrackAssessment {
    bool exists = false;
    bool authentic = false;
    bool verified = false;
    bool suspicious = false;
    bool spoofed = false;
    bool radar_confirmed = false;
    std::size_t message_count = 0;
    std::size_t rejected_count = 0;
    std::size_t source_count = 0;
    std::size_t receiver_count = 0;
    int corroborating_sources = 0;
    double credibility = 0.0;
    double last_timestamp = std::numeric_limits<double>::quiet_NaN();
    std::string flight_id;
    std::string callsign;
    std::string icao_address;
    TrackVerdict verdict = TrackVerdict::Unknown;
    FlagSet flags;

    bool is_authentic() const { return authentic; }
    bool is_verified() const { return verified; }
    bool is_spoofed() const { return spoofed; }
    bool is_suspicious() const { return suspicious; }
};

namespace detail {

inline bool is_finite(double value) { return std::isfinite(value); }

inline bool first_finite(std::initializer_list<double> values, double& output) {
    for (const double value : values) {
        if (is_finite(value)) {
            output = value;
            return true;
        }
    }
    return false;
}

inline std::int64_t first_sequence(std::initializer_list<std::int64_t> values) {
    for (const std::int64_t value : values) {
        if (value >= 0) {
            return value;
        }
    }
    return -1;
}

inline std::string trim(std::string_view input) {
    std::size_t begin = 0;
    while (begin < input.size() && std::isspace(static_cast<unsigned char>(input[begin])) != 0) {
        ++begin;
    }
    std::size_t end = input.size();
    while (end > begin && std::isspace(static_cast<unsigned char>(input[end - 1])) != 0) {
        --end;
    }
    return std::string(input.substr(begin, end - begin));
}

inline std::string upper(std::string value) {
    std::transform(value.begin(), value.end(), value.begin(), [](unsigned char c) {
        return static_cast<char>(std::toupper(c));
    });
    return value;
}

inline bool is_hex(std::string_view value) {
    if (value.empty()) {
        return false;
    }
    for (const char c : value) {
        if (std::isxdigit(static_cast<unsigned char>(c)) == 0) {
            return false;
        }
    }
    return true;
}

inline std::string normalize_icao(std::string_view input) {
    std::string value = upper(trim(input));
    if (value.size() > 2 && value[0] == '0' && value[1] == 'X') {
        value = value.substr(2);
    }
    return value;
}

inline bool valid_callsign(std::string_view value) {
    if (value.empty()) {
        return false;
    }
    for (const char c : value) {
        const auto uc = static_cast<unsigned char>(c);
        if (std::isalnum(uc) == 0 && c != ' ' && c != '-' && c != '_') {
            return false;
        }
    }
    return true;
}

inline std::string first_nonempty(std::initializer_list<const std::string*> values) {
    for (const std::string* value : values) {
        if (value != nullptr && !value->empty()) {
            return trim(*value);
        }
    }
    return {};
}

inline double normalize_quality(double value, double fallback) {
    if (!is_finite(value)) {
        return fallback;
    }
    if (value < 0.0) {
        return std::clamp((value + 120.0) / 60.0, 0.0, 1.0);
    }
    if (value <= 1.0) {
        return std::clamp(value, 0.0, 1.0);
    }
    if (value <= 100.0) {
        return std::clamp(value / 100.0, 0.0, 1.0);
    }
    return 1.0;
}

inline double normalize_signal(const SurveillanceMessage& message) {
    double quality = 1.0;
    if (first_finite({message.signal_quality, message.signal_strength}, quality)) {
        quality = normalize_quality(quality, 1.0);
    }
    double snr_value = 0.0;
    if (first_finite({message.snr_db, message.snr}, snr_value)) {
        const double snr_quality = std::clamp((snr_value + 5.0) / 35.0, 0.0, 1.0);
        quality = std::min(quality, snr_quality);
    }
    double rssi_value = 0.0;
    if (first_finite({message.rssi_dbm, message.rssi}, rssi_value)) {
        const double rssi_quality = std::clamp((rssi_value + 110.0) / 50.0, 0.0, 1.0);
        quality = std::min(quality, rssi_quality);
    }
    return std::clamp(quality, 0.0, 1.0);
}

inline double to_radians(double degrees) { return degrees * 3.14159265358979323846 / 180.0; }

inline double to_degrees(double radians) { return radians * 180.0 / 3.14159265358979323846; }

inline double distance_nm(double lat1, double lon1, double lat2, double lon2) {
    constexpr double radius_nm = 3440.065;
    const double phi1 = to_radians(lat1);
    const double phi2 = to_radians(lat2);
    const double delta_phi = to_radians(lat2 - lat1);
    const double delta_lambda = to_radians(lon2 - lon1);
    const double a = std::sin(delta_phi / 2.0) * std::sin(delta_phi / 2.0) +
                     std::cos(phi1) * std::cos(phi2) * std::sin(delta_lambda / 2.0) *
                         std::sin(delta_lambda / 2.0);
    return 2.0 * radius_nm * std::atan2(std::sqrt(a), std::sqrt(std::max(0.0, 1.0 - a)));
}

inline std::pair<double, double> destination(double lat, double lon, double bearing_deg,
                                             double distance) {
    constexpr double radius_nm = 3440.065;
    const double angular = distance / radius_nm;
    const double bearing = to_radians(bearing_deg);
    const double phi1 = to_radians(lat);
    const double lambda1 = to_radians(lon);
    const double phi2 = std::asin(std::sin(phi1) * std::cos(angular) +
                                  std::cos(phi1) * std::sin(angular) * std::cos(bearing));
    const double lambda2 =
        lambda1 + std::atan2(std::sin(bearing) * std::sin(angular) * std::cos(phi1),
                             std::cos(angular) - std::sin(phi1) * std::sin(phi2));
    const double lon2 = std::fmod(to_degrees(lambda2) + 540.0, 360.0) - 180.0;
    return {to_degrees(phi2), lon2};
}

inline double normalize_heading(double value) {
    double result = std::fmod(value, 360.0);
    if (result < 0.0) {
        result += 360.0;
    }
    return result;
}

inline double normalize_timestamp_seconds(double value) {
    if (value > 1.0e17) {
        return value / 1.0e9;
    }
    if (value > 1.0e14) {
        return value / 1.0e6;
    }
    if (value > 1.0e11) {
        return value / 1.0e3;
    }
    return value;
}

inline std::uint64_t stable_hash(std::string_view text) {
    std::uint64_t hash = 1469598103934665603ull;
    for (const char c : text) {
        hash ^= static_cast<unsigned char>(c);
        hash *= 1099511628211ull;
    }
    return hash;
}

struct NormalizedMessage {
    MessageType type = MessageType::Unknown;
    SourceType source = SourceType::Unknown;
    std::string flight_id;
    std::string callsign;
    std::string icao;
    std::string receiver_id;
    std::string source_id;
    std::string source_key;
    std::string fingerprint;
    double latitude = std::numeric_limits<double>::quiet_NaN();
    double longitude = std::numeric_limits<double>::quiet_NaN();
    double altitude_ft = std::numeric_limits<double>::quiet_NaN();
    double ground_speed_kts = std::numeric_limits<double>::quiet_NaN();
    double heading_deg = std::numeric_limits<double>::quiet_NaN();
    double raw_heading_deg = std::numeric_limits<double>::quiet_NaN();
    double vertical_rate_fpm = std::numeric_limits<double>::quiet_NaN();
    double timestamp = std::numeric_limits<double>::quiet_NaN();
    double receiver_quality = 1.0;
    double signal_quality = 1.0;
    std::int64_t sequence = -1;
    bool crc_valid = true;
    bool on_surface = false;
    bool has_position = false;
    bool has_altitude = false;
    bool has_speed = false;
    bool has_heading = false;
    bool has_vertical_rate = false;
    bool has_timestamp = false;
    bool has_payload_evidence = false;
    bool radar = false;
};

inline std::string make_fingerprint(const SurveillanceMessage& message,
                                    const NormalizedMessage& normalized) {
    if (!message.payload_hash.empty()) {
        return "h:" + message.payload_hash;
    }
    if (!message.raw_payload.empty()) {
        return "r:" + std::to_string(stable_hash(message.raw_payload));
    }
    std::ostringstream out;
    out.precision(12);
    out << static_cast<int>(normalized.type) << '|' << static_cast<int>(normalized.source) << '|'
        << normalized.source_key << '|' << normalized.flight_id << '|' << normalized.icao << '|'
        << normalized.timestamp << '|' << normalized.latitude << '|' << normalized.longitude << '|'
        << normalized.altitude_ft << '|' << normalized.ground_speed_kts << '|'
        << normalized.heading_deg << '|' << normalized.vertical_rate_fpm << '|'
        << normalized.sequence;
    return "c:" + std::to_string(stable_hash(out.str()));
}

inline NormalizedMessage normalize(const SurveillanceMessage& message) {
    NormalizedMessage normalized;

    normalized.type = message.type;
    if (normalized.type == MessageType::Unknown) {
        normalized.type = message.message_type;
    }
    normalized.source = message.source;
    if (normalized.source == SourceType::Unknown) {
        normalized.source = message.source_type;
    }

    normalized.flight_id = upper(first_nonempty(
        {&message.flight_id, &message.callsign, &message.target_id, &message.aircraft_id}));
    normalized.callsign = normalized.flight_id;
    normalized.icao = normalize_icao(first_nonempty(
        {&message.icao_address, &message.icao24, &message.icao, &message.aircraft_address}));
    normalized.receiver_id =
        first_nonempty({&message.receiver_id, &message.source_id, &message.receiver});
    normalized.source_id = first_nonempty({&message.source_id, &message.receiver_id});

    double value = 0.0;
    const bool has_latitude =
        first_finite({message.latitude, message.latitude_deg, message.lat}, value);
    if (has_latitude) {
        normalized.latitude = value;
    }
    const bool has_longitude =
        first_finite({message.longitude, message.longitude_deg, message.lon}, value);
    if (has_longitude) {
        normalized.longitude = value;
    }
    normalized.has_position = has_latitude && has_longitude;
    if (!normalized.has_position && is_finite(message.range_nm) && is_finite(message.azimuth_deg) &&
        is_finite(message.receiver_latitude) && is_finite(message.receiver_longitude)) {
        const auto converted = destination(message.receiver_latitude, message.receiver_longitude,
                                           message.azimuth_deg, message.range_nm);
        normalized.latitude = converted.first;
        normalized.longitude = converted.second;
        normalized.has_position = true;
    }
    if (first_finite({message.altitude_ft, message.altitude, message.alt,
                      message.barometric_altitude_ft, message.geometric_altitude_ft},
                     value)) {
        normalized.altitude_ft = value;
        normalized.has_altitude = true;
    } else if (is_finite(message.altitude_m)) {
        normalized.altitude_ft = message.altitude_m * 3.280839895;
        normalized.has_altitude = true;
    }
    if (first_finite({message.ground_speed_kts, message.speed_kts, message.speed,
                      message.ground_speed, message.velocity_kts},
                     value)) {
        normalized.ground_speed_kts = value;
        normalized.has_speed = true;
    }
    if (first_finite({message.heading_deg, message.heading, message.track_deg}, value)) {
        normalized.raw_heading_deg = value;
        normalized.heading_deg = normalize_heading(value);
        normalized.has_heading = true;
    }
    if (first_finite({message.vertical_rate_fpm, message.vertical_rate,
                      message.vertical_speed_fpm},
                     value)) {
        normalized.vertical_rate_fpm = value;
        normalized.has_vertical_rate = true;
    }
    if (first_finite({message.timestamp, message.timestamp_s, message.time, message.time_s,
                      message.ts},
                     value)) {
        normalized.timestamp = normalize_timestamp_seconds(value);
        normalized.has_timestamp = true;
    } else if (first_finite({message.timestamp_ms, message.time_ms}, value)) {
        normalized.timestamp = value / 1.0e3;
        normalized.has_timestamp = true;
    } else if (first_finite({message.timestamp_us}, value)) {
        normalized.timestamp = value / 1.0e6;
        normalized.has_timestamp = true;
    } else if (first_finite({message.timestamp_ns}, value)) {
        normalized.timestamp = value / 1.0e9;
        normalized.has_timestamp = true;
    }
    normalized.sequence =
        first_sequence({message.sequence, message.sequence_number, message.sequence_no});
    normalized.crc_valid = message.crc_valid && message.crc_ok && message.valid_crc;
    normalized.has_payload_evidence =
        !message.payload_hash.empty() || !message.raw_payload.empty();
    normalized.on_surface = message.on_surface;
    normalized.receiver_quality = normalize_quality(message.receiver_quality, 1.0);
    normalized.signal_quality = normalize_signal(message);

    if (normalized.type == MessageType::Unknown) {
        if (normalized.source == SourceType::Radar) {
            normalized.type = MessageType::Radar;
        } else if (normalized.has_position && normalized.has_altitude) {
            normalized.type = MessageType::AdsbPosition;
        } else if (normalized.has_speed) {
            normalized.type = MessageType::AdsbVelocity;
        } else if (!normalized.callsign.empty() && !normalized.icao.empty()) {
            normalized.type = normalized.source == SourceType::ModeS
                                  ? MessageType::ModeS
                                  : MessageType::AdsbIdentification;
        } else if (normalized.source == SourceType::ModeS) {
            normalized.type = MessageType::ModeS;
        }
    }

    if (normalized.source == SourceType::Unknown) {
        switch (normalized.type) {
            case MessageType::Radar: normalized.source = SourceType::Radar; break;
            case MessageType::ModeS:
            case MessageType::ModeSIdentification:
            case MessageType::ModeSAltitude: normalized.source = SourceType::ModeS; break;
            case MessageType::Multilateration: normalized.source = SourceType::Mlat; break;
            case MessageType::AdsbPosition:
            case MessageType::AdsbVelocity:
            case MessageType::AdsbIdentification:
            case MessageType::AdsbSurface: normalized.source = SourceType::Adsb; break;
            case MessageType::Unknown: break;
        }
    }

    normalized.radar =
        normalized.type == MessageType::Radar || normalized.source == SourceType::Radar;
    normalized.source_key = std::string(to_string(normalized.source)) + "|" +
                            normalized.receiver_id + "|" + normalized.source_id;
    normalized.fingerprint = make_fingerprint(message, normalized);
    return normalized;
}

inline void add_reason(ValidationResult& result, std::string reason) {
    result.reasons.push_back(std::move(reason));
    if (result.reason.empty()) {
        result.reason = result.reasons.back();
    }
}

inline void add_flag(ValidationResult& result, MessageFlag flag, std::string reason = {}) {
    result.flags.add(flag);
    result.flag_mask = result.flags.bits();
    result.flagged = true;
    if (!reason.empty()) {
        add_reason(result, std::move(reason));
    }
}

inline void validate_structure(const SurveillanceMessage& message,
                               const NormalizedMessage& normalized,
                               const SurveillanceConfig& config, ValidationResult& result) {
    if (message.type != MessageType::Unknown && message.message_type != MessageType::Unknown &&
        message.type != message.message_type) {
        add_flag(result, MessageFlag::InvalidType, "conflicting message type fields");
    }
    if (message.source != SourceType::Unknown && message.source_type != SourceType::Unknown &&
        message.source != message.source_type) {
        add_flag(result, MessageFlag::InvalidSource, "conflicting source type fields");
    }
    if (normalized.type == MessageType::Unknown) {
        add_flag(result, MessageFlag::InvalidType, "unknown surveillance message type");
    }

    if (!normalized.crc_valid) {
        add_flag(result, MessageFlag::CrcFailure, "message CRC is invalid");
    }

    if (!normalized.has_timestamp) {
        add_flag(result, MessageFlag::InvalidTimestamp, "timestamp is missing");
    } else if (normalized.timestamp < config.min_timestamp_s) {
        add_flag(result, MessageFlag::InvalidTimestamp, "timestamp is before the accepted epoch");
    } else if (normalized.timestamp > 1.0e12) {
        add_flag(result, MessageFlag::InvalidTimestamp, "timestamp is outside the accepted range");
    }

    if (normalized.flight_id.empty() && normalized.icao.empty()) {
        add_flag(result, MessageFlag::InvalidIdentity, "message has no flight identity");
    }
    if (!normalized.icao.empty()) {
        if (normalized.icao.size() != 6 || !is_hex(normalized.icao) ||
            normalized.icao == "000000" || normalized.icao == "FFFFFF") {
            add_flag(result, MessageFlag::InvalidIdentity,
                     "ICAO address is not a valid 24-bit address");
        }
    } else if (config.require_icao_for_adsb &&
               (normalized.type == MessageType::AdsbPosition ||
                normalized.type == MessageType::AdsbVelocity ||
                normalized.type == MessageType::AdsbIdentification ||
                normalized.type == MessageType::AdsbSurface)) {
        add_flag(result, MessageFlag::InvalidIdentity, "ADS-B message has no ICAO address");
    }
    if (!normalized.flight_id.empty()) {
        if (!valid_callsign(normalized.flight_id)) {
            add_flag(result, MessageFlag::InvalidIdentity,
                     "flight identity contains invalid characters");
        }
        const std::size_t identity_limit =
            normalized.radar ? config.max_track_id_length : config.max_callsign_length;
        if (normalized.flight_id.size() > identity_limit) {
            add_flag(result, MessageFlag::InvalidIdentity,
                     "flight identity is longer than the type limit");
        }
    }

    const bool needs_position = normalized.type == MessageType::AdsbPosition ||
                                normalized.type == MessageType::AdsbSurface ||
                                normalized.type == MessageType::Radar;
    if (needs_position && !normalized.has_position) {
        add_flag(result, MessageFlag::InvalidPosition,
                 "position is missing for this message type");
    }
    if (normalized.has_position) {
        if (normalized.latitude < config.min_latitude ||
            normalized.latitude > config.max_latitude) {
            add_flag(result, MessageFlag::OutOfEnvelope, "latitude is outside the envelope");
            add_flag(result, MessageFlag::InvalidPosition);
        }
        if (normalized.longitude < config.min_longitude ||
            normalized.longitude > config.max_longitude) {
            add_flag(result, MessageFlag::OutOfEnvelope, "longitude is outside the envelope");
            add_flag(result, MessageFlag::InvalidPosition);
        }
    } else if (normalized.type == MessageType::Radar && is_finite(message.range_nm)) {
        add_flag(result, MessageFlag::InvalidPosition,
                 "radar range/azimuth cannot be converted without a receiver position");
    }

    const bool needs_altitude =
        (normalized.type == MessageType::AdsbPosition && !normalized.on_surface) ||
        normalized.type == MessageType::ModeSAltitude;
    if (needs_altitude && !normalized.has_altitude) {
        add_flag(result, MessageFlag::InvalidAltitude,
                 "altitude is missing for this message type");
    }
    if (normalized.has_altitude) {
        if (normalized.altitude_ft < config.min_altitude_ft ||
            normalized.altitude_ft > config.max_altitude_ft) {
            add_flag(result, MessageFlag::OutOfEnvelope, "altitude is outside the envelope");
            add_flag(result, MessageFlag::InvalidAltitude);
        }
    }

    if (normalized.type == MessageType::AdsbVelocity && !normalized.has_speed) {
        add_flag(result, MessageFlag::InvalidSpeed,
                 "velocity message is missing ground speed");
    }
    if (normalized.has_speed) {
        if (normalized.ground_speed_kts < config.min_ground_speed_kts ||
            normalized.ground_speed_kts > config.max_ground_speed_kts) {
            add_flag(result, MessageFlag::OutOfEnvelope, "ground speed is outside the envelope");
            add_flag(result, MessageFlag::InvalidSpeed);
        }
        if (normalized.type == MessageType::AdsbSurface &&
            normalized.ground_speed_kts > config.max_surface_speed_kts) {
            add_flag(result, MessageFlag::OutOfEnvelope,
                     "surface ground speed is outside the envelope");
            add_flag(result, MessageFlag::InvalidSpeed);
        }
    }
    if (normalized.has_heading) {
        if (normalized.raw_heading_deg < 0.0 ||
            normalized.raw_heading_deg > config.max_heading_deg) {
            add_flag(result, MessageFlag::OutOfEnvelope, "heading is outside the envelope");
            add_flag(result, MessageFlag::InvalidHeading);
        }
    }
    if (normalized.has_vertical_rate &&
        std::abs(normalized.vertical_rate_fpm) > config.max_vertical_rate_fpm) {
        add_flag(result, MessageFlag::OutOfEnvelope, "vertical rate is outside the envelope");
        add_flag(result, MessageFlag::InvalidVerticalRate);
    }
    if ((normalized.type == MessageType::ModeS ||
         normalized.type == MessageType::ModeSIdentification ||
         normalized.type == MessageType::ModeSAltitude) &&
        normalized.icao.empty()) {
        add_flag(result, MessageFlag::InvalidIdentity, "Mode S message has no ICAO address");
    }
    if ((normalized.type == MessageType::AdsbIdentification ||
         normalized.type == MessageType::ModeSIdentification) &&
        normalized.flight_id.empty()) {
        add_flag(result, MessageFlag::InvalidIdentity,
                 "identification message has no callsign");
    }
    if (is_finite(message.range_nm) &&
        (message.range_nm < 0.0 || message.range_nm > config.max_range_nm)) {
        add_flag(result, MessageFlag::OutOfEnvelope, "radar range is outside the envelope");
        add_flag(result, MessageFlag::InvalidPosition);
    }
    if (is_finite(message.azimuth_deg) &&
        (message.azimuth_deg < 0.0 || message.azimuth_deg > 360.0)) {
        add_flag(result, MessageFlag::OutOfEnvelope, "radar azimuth is outside the envelope");
        add_flag(result, MessageFlag::InvalidPosition);
    }
}

inline double quality_factor(double receiver_quality, double signal_quality) {
    return std::clamp(0.45 + 0.30 * receiver_quality + 0.25 * signal_quality, 0.0, 1.0);
}

}  // namespace detail

class SurveillanceProcessor {
    static void add_flag(ValidationResult& result, MessageFlag flag, std::string reason = {}) {
        detail::add_flag(result, flag, std::move(reason));
    }
    static bool is_finite(double value) { return detail::is_finite(value); }
    static double distance_nm(double lat1, double lon1, double lat2, double lon2) {
        return detail::distance_nm(lat1, lon1, lat2, lon2);
    }
    static std::string normalize_icao(std::string_view value) {
        return detail::normalize_icao(value);
    }
    static double normalize_quality(double value, double fallback) {
        return detail::normalize_quality(value, fallback);
    }
    static double quality_factor(double receiver_quality, double signal_quality) {
        return detail::quality_factor(receiver_quality, signal_quality);
    }
    static std::string trim(std::string_view value) { return detail::trim(value); }
    static std::string upper(std::string value) { return detail::upper(std::move(value)); }

public:
    explicit SurveillanceProcessor(SurveillanceConfig config = {}) : config_(std::move(config)) {}

    SurveillanceProcessor(const SurveillanceProcessor& other) {
        std::lock_guard<std::mutex> lock(other.mutex_);
        config_ = other.config_;
        tracks_ = other.tracks_;
        callsign_tracks_ = other.callsign_tracks_;
        icao_tracks_ = other.icao_tracks_;
        seen_ = other.seen_;
        receivers_ = other.receivers_;
        trusted_identities_ = other.trusted_identities_;
        arrival_counter_ = other.arrival_counter_;
        watermark_ = other.watermark_;
    }

    SurveillanceProcessor& operator=(const SurveillanceProcessor& other) {
        if (this == &other) {
            return *this;
        }
        std::scoped_lock lock(mutex_, other.mutex_);
        config_ = other.config_;
        tracks_ = other.tracks_;
        callsign_tracks_ = other.callsign_tracks_;
        icao_tracks_ = other.icao_tracks_;
        seen_ = other.seen_;
        receivers_ = other.receivers_;
        trusted_identities_ = other.trusted_identities_;
        arrival_counter_ = other.arrival_counter_;
        watermark_ = other.watermark_;
        return *this;
    }

    ValidationResult process(const SurveillanceMessage& message) {
        std::lock_guard<std::mutex> lock(mutex_);
        return process_locked(message);
    }

    ValidationResult operator()(const SurveillanceMessage& message) { return process(message); }
    ValidationResult ingest(const SurveillanceMessage& message) { return process(message); }
    ValidationResult submit(const SurveillanceMessage& message) { return process(message); }
    ValidationResult receive(const SurveillanceMessage& message) { return process(message); }
    ValidationResult validate(const SurveillanceMessage& message) { return process(message); }
    ValidationResult validate(const SurveillanceMessage& message) const {
        SurveillanceProcessor temporary(config_);
        return temporary.process(message);
    }
    ValidationResult add_message(const SurveillanceMessage& message) { return process(message); }
    ValidationResult handle(const SurveillanceMessage& message) { return process(message); }
    ValidationResult handle_message(const SurveillanceMessage& message) { return process(message); }
    ValidationResult push(const SurveillanceMessage& message) { return process(message); }
    ValidationResult ingest_message(const SurveillanceMessage& message) { return process(message); }
    ValidationResult validate_and_ingest(const SurveillanceMessage& message) {
        return process(message);
    }
    ValidationResult process_message(const SurveillanceMessage& message) {
        return process(message);
    }
    ValidationResult processMessage(const SurveillanceMessage& message) {
        return process(message);
    }
    ValidationResult ingestMessage(const SurveillanceMessage& message) {
        return process(message);
    }
    ValidationResult validateMessage(const SurveillanceMessage& message) {
        return process(message);
    }

    std::vector<ValidationResult> process_batch(
        const std::vector<SurveillanceMessage>& messages) {
        std::lock_guard<std::mutex> lock(mutex_);
        std::vector<ValidationResult> results;
        results.reserve(messages.size());
        for (const auto& message : messages) {
            results.push_back(process_locked(message));
        }
        return results;
    }

    std::vector<ValidationResult> ingest(const std::vector<SurveillanceMessage>& messages) {
        return process_batch(messages);
    }

    void register_receiver(std::string receiver_id, double quality = 1.0, bool trusted = true) {
        std::lock_guard<std::mutex> lock(mutex_);
        ReceiverInfo info;
        info.quality = detail::normalize_quality(quality, 1.0);
        info.trusted = trusted;
        receivers_[detail::upper(detail::trim(receiver_id))] = info;
    }

    void add_receiver(std::string receiver_id, double quality = 1.0, bool trusted = true) {
        register_receiver(std::move(receiver_id), quality, trusted);
    }

    void set_receiver_quality(std::string receiver_id, double quality, bool trusted = true) {
        register_receiver(std::move(receiver_id), quality, trusted);
    }

    void register_identity(std::string flight_id, std::string icao_address, bool trusted = true) {
        std::lock_guard<std::mutex> lock(mutex_);
        std::string id = detail::upper(detail::trim(flight_id));
        std::string address = detail::normalize_icao(icao_address);
        if (!id.empty() && !address.empty()) {
            trusted_identities_[id] = {address, trusted};
        }
    }

    void trust_identity(std::string flight_id, std::string icao_address) {
        register_identity(std::move(flight_id), std::move(icao_address), true);
    }

    void add_identity(std::string flight_id, std::string icao_address, bool trusted = true) {
        register_identity(std::move(flight_id), std::move(icao_address), trusted);
    }

    TrackAssessment assess_target(const std::string& flight_id_or_icao) const {
        std::lock_guard<std::mutex> lock(mutex_);
        const std::string query = detail::upper(detail::trim(flight_id_or_icao));
        const std::string icao_query = detail::normalize_icao(flight_id_or_icao);
        const TrackState* best = nullptr;
        for (const auto& [key, state] : tracks_) {
            (void)key;
            if (state.flight_id == query || state.icao == icao_query) {
                if (best == nullptr || state.accepted_messages > best->accepted_messages) {
                    best = &state;
                }
            }
        }
        TrackAssessment assessment;
        if (best == nullptr) {
            return assessment;
        }
        assessment.exists = true;
        assessment.flight_id = best->flight_id;
        assessment.callsign = best->flight_id;
        assessment.icao_address = best->icao;
        assessment.message_count = best->accepted_messages;
        assessment.rejected_count = best->rejected_messages;
        assessment.source_count = best->observations.size();
        assessment.receiver_count = best->receivers.size();
        assessment.radar_confirmed = best->radar_confirmed;
        assessment.last_timestamp = best->latest_timestamp;
        assessment.corroborating_sources = corroboration_count(*best);
        assessment.credibility = best->credibility;
        assessment.flags = best->flags;
        const bool conflicting = has_callsign_conflict(best->flight_id, best->key);
        if (conflicting) {
            assessment.flags.add(MessageFlag::IdentityConflict);
            const auto candidates = callsign_tracks_.find(best->flight_id);
            if (candidates != callsign_tracks_.end()) {
                for (const auto& key : candidates->second) {
                    if (key == best->key) {
                        continue;
                    }
                    const auto other = tracks_.find(key);
                    if (other != tracks_.end() && other->second.identity_spoofed) {
                        assessment.spoofed = true;
                        assessment.suspicious = true;
                    }
                }
            }
        }
        if (best->identity_spoofed) {
            assessment.spoofed = true;
            assessment.suspicious = true;
            assessment.verdict = TrackVerdict::Spoofed;
        } else if (assessment.credibility >= config_.authentic_threshold &&
                   (!config_.require_corroboration || assessment.corroborating_sources > 0)) {
            assessment.authentic = true;
            assessment.verified = true;
            assessment.verdict = TrackVerdict::Authentic;
        } else if (assessment.credibility >= config_.low_credibility_threshold) {
            assessment.verdict = TrackVerdict::Unverified;
        } else {
            assessment.suspicious = true;
            assessment.verdict = TrackVerdict::Suspicious;
        }
        return assessment;
    }

    TrackAssessment assess(const std::string& flight_id_or_icao) const {
        return assess_target(flight_id_or_icao);
    }

    bool is_authentic_target(const std::string& flight_id_or_icao) const {
        return assess_target(flight_id_or_icao).authentic;
    }

    bool target_is_authentic(const std::string& flight_id_or_icao) const {
        return is_authentic_target(flight_id_or_icao);
    }

    bool is_spoofed(const std::string& flight_id_or_icao) const {
        return assess_target(flight_id_or_icao).spoofed;
    }

    std::size_t track_count() const {
        std::lock_guard<std::mutex> lock(mutex_);
        return tracks_.size();
    }

    SurveillanceConfig& config() { return config_; }
    const SurveillanceConfig& config() const { return config_; }

    void set_config(SurveillanceConfig config) {
        std::lock_guard<std::mutex> lock(mutex_);
        config_ = std::move(config);
    }

    void reset() {
        std::lock_guard<std::mutex> lock(mutex_);
        tracks_.clear();
        callsign_tracks_.clear();
        icao_tracks_.clear();
        seen_.clear();
        arrival_counter_ = 0;
        watermark_ = std::numeric_limits<double>::quiet_NaN();
    }

    void clear() { reset(); }

private:
    struct ReceiverInfo {
        double quality = 1.0;
        bool trusted = true;
    };

    struct IdentityInfo {
        std::string icao;
        bool trusted = true;
    };

    struct SourceObservation {
        SourceType source = SourceType::Unknown;
        std::string source_key;
        std::string receiver_id;
        double timestamp = std::numeric_limits<double>::quiet_NaN();
        double latitude = std::numeric_limits<double>::quiet_NaN();
        double longitude = std::numeric_limits<double>::quiet_NaN();
        double altitude_ft = std::numeric_limits<double>::quiet_NaN();
        double ground_speed_kts = std::numeric_limits<double>::quiet_NaN();
        std::int64_t sequence = -1;
        std::string fingerprint;
        double credibility = 0.0;
        bool has_position = false;
        bool has_altitude = false;
        bool radar = false;
    };

    struct SeenRecord {
        std::uint64_t arrival = 0;
        double timestamp = std::numeric_limits<double>::quiet_NaN();
        std::string source_key;
        std::string track_key;
        std::string flight_id;
        std::string icao;
        double latitude = std::numeric_limits<double>::quiet_NaN();
        double longitude = std::numeric_limits<double>::quiet_NaN();
        double altitude_ft = std::numeric_limits<double>::quiet_NaN();
        bool has_position = false;
        bool has_altitude = false;
    };

    struct TrackState {
        std::string key;
        std::string flight_id;
        std::string icao;
        bool has_position = false;
        double latitude = std::numeric_limits<double>::quiet_NaN();
        double longitude = std::numeric_limits<double>::quiet_NaN();
        double altitude_ft = std::numeric_limits<double>::quiet_NaN();
        double ground_speed_kts = std::numeric_limits<double>::quiet_NaN();
        double heading_deg = std::numeric_limits<double>::quiet_NaN();
        double vertical_rate_fpm = std::numeric_limits<double>::quiet_NaN();
        double latest_timestamp = std::numeric_limits<double>::quiet_NaN();
        double last_arrival_timestamp = std::numeric_limits<double>::quiet_NaN();
        double credibility = 0.0;
        std::int64_t last_sequence = -1;
        std::size_t accepted_messages = 0;
        std::size_t rejected_messages = 0;
        bool radar_confirmed = false;
        bool identity_spoofed = false;
        bool trusted_binding = false;
        std::map<std::string, SourceObservation> observations;
        std::set<std::string> receivers;
        FlagSet flags;
    };

    SurveillanceConfig config_;
    mutable std::mutex mutex_;
    std::map<std::string, TrackState> tracks_;
    std::map<std::string, std::set<std::string>> callsign_tracks_;
    std::map<std::string, std::set<std::string>> icao_tracks_;
    std::map<std::string, SeenRecord> seen_;
    std::map<std::string, ReceiverInfo> receivers_;
    std::map<std::string, IdentityInfo> trusted_identities_;
    std::uint64_t arrival_counter_ = 0;
    double watermark_ = std::numeric_limits<double>::quiet_NaN();

    static std::string make_track_key(const detail::NormalizedMessage& normalized) {
        if (!normalized.flight_id.empty() && !normalized.icao.empty()) {
            return normalized.flight_id + "|" + normalized.icao;
        }
        if (!normalized.icao.empty()) {
            return "|" + normalized.icao;
        }
        return normalized.flight_id + "|";
    }

    TrackState& ensure_track(const detail::NormalizedMessage& normalized,
                             const std::string& key) {
        auto found = tracks_.find(key);
        if (found != tracks_.end()) {
            return found->second;
        }
        TrackState state;
        state.key = key;
        state.flight_id = normalized.flight_id;
        state.icao = normalized.icao;
        if (!state.flight_id.empty()) {
            callsign_tracks_[state.flight_id].insert(key);
        }
        if (!state.icao.empty()) {
            icao_tracks_[state.icao].insert(key);
        }
        return tracks_.emplace(key, std::move(state)).first->second;
    }

    TrackState& resolve_track(const detail::NormalizedMessage& normalized,
                              const std::string& requested_key) {
        const auto exact = tracks_.find(requested_key);
        if (exact != tracks_.end()) {
            return exact->second;
        }
        if (!normalized.flight_id.empty()) {
            const auto candidates = callsign_tracks_.find(normalized.flight_id);
            if (candidates != callsign_tracks_.end() && candidates->second.size() == 1) {
                const auto candidate = tracks_.find(*candidates->second.begin());
                if (candidate != tracks_.end()) {
                    TrackState& state = candidate->second;
                    if (normalized.icao.empty() || normalized.icao == state.icao) {
                        return state;
                    }
                    if (state.icao.empty()) {
                        bool compatible = true;
                        if (state.has_position && normalized.has_position &&
                            detail::distance_nm(state.latitude, state.longitude,
                                                normalized.latitude, normalized.longitude) >
                                config_.position_gate_nm) {
                            compatible = false;
                        }
                        if (detail::is_finite(state.latest_timestamp) &&
                            detail::is_finite(normalized.timestamp) &&
                            std::abs(state.latest_timestamp - normalized.timestamp) >
                                config_.identity_window_s) {
                            compatible = false;
                        }
                        if (compatible) {
                            state.icao = normalized.icao;
                            icao_tracks_[state.icao].insert(state.key);
                            return state;
                        }
                    }
                }
            }
        } else if (!normalized.icao.empty()) {
            const auto candidates = icao_tracks_.find(normalized.icao);
            if (candidates != icao_tracks_.end() && candidates->second.size() == 1) {
                const auto candidate = tracks_.find(*candidates->second.begin());
                if (candidate != tracks_.end()) {
                    return candidate->second;
                }
            }
        }
        return ensure_track(normalized, requested_key);
    }

    std::vector<const SourceObservation*> recent_observations(
        const TrackState& track, double timestamp, const std::string& exclude_key) const {
        std::vector<const SourceObservation*> result;
        for (const auto& [key, observation] : track.observations) {
            if (!exclude_key.empty() && key == exclude_key) {
                continue;
            }
            if (!detail::is_finite(observation.timestamp) || !detail::is_finite(timestamp)) {
                continue;
            }
            if (std::abs(observation.timestamp - timestamp) <= config_.correlation_window_s) {
                result.push_back(&observation);
            }
        }
        return result;
    }

    int corroboration_count(const TrackState& track) const {
        int count = 0;
        for (const auto& [key, observation] : track.observations) {
            (void)key;
            if (observation.credibility >= config_.low_credibility_threshold) {
                ++count;
            }
        }
        return std::max(0, count - 1);
    }

    bool has_callsign_conflict(const std::string& flight_id,
                               const std::string& track_key) const {
        if (flight_id.empty()) {
            return false;
        }
        const auto found = callsign_tracks_.find(flight_id);
        if (found == callsign_tracks_.end()) {
            return false;
        }
        return found->second.size() > 1 && found->second.contains(track_key);
    }

    int track_evidence(const TrackState& track) const {
        int score = 0;
        if (track.trusted_binding) {
            score += 3;
        }
        score += static_cast<int>(track.receivers.size());
        if (track.radar_confirmed) {
            score += 2;
        }
        if (track.accepted_messages > 2) {
            score += 1;
        }
        return score;
    }

    int incoming_evidence(const TrackState& track,
                          const detail::NormalizedMessage& normalized) const {
        int score = 0;
        if (track.trusted_binding) {
            score += 3;
        }
        if (normalized.radar) {
            score += 1;
        }
        if (normalized.receiver_quality >= 0.7 && normalized.signal_quality >= 0.7) {
            score += 1;
        }
        return score;
    }

    void check_identity_conflicts(const detail::NormalizedMessage& normalized, TrackState& track,
                                  ValidationResult& result) {
        const auto trusted = trusted_identities_.find(normalized.flight_id);
        if (trusted != trusted_identities_.end() && !normalized.icao.empty() &&
            trusted->second.icao != normalized.icao) {
            add_flag(result, MessageFlag::IdentitySpoof,
                     "flight identity is bound to a different ICAO address");
            if (trusted->second.trusted) {
                track.identity_spoofed = true;
            }
        }

        if (!normalized.flight_id.empty()) {
            const auto found = callsign_tracks_.find(normalized.flight_id);
            if (found != callsign_tracks_.end()) {
                for (const auto& key : found->second) {
                    if (key == track.key) {
                        continue;
                    }
                    const auto other = tracks_.find(key);
                    if (other == tracks_.end()) {
                        continue;
                    }
                    if (!normalized.icao.empty() && other->second.icao == normalized.icao) {
                        continue;
                    }
                    if (detail::is_finite(other->second.latest_timestamp) &&
                        detail::is_finite(normalized.timestamp) &&
                        std::abs(other->second.latest_timestamp - normalized.timestamp) >
                            config_.identity_window_s &&
                        other->second.latest_timestamp < normalized.timestamp) {
                        continue;
                    }
                    const int existing_score = track_evidence(other->second);
                    const int incoming_score = incoming_evidence(track, normalized);
                    if (existing_score >= incoming_score + 2) {
                        add_flag(result, MessageFlag::IdentitySpoof,
                                 "flight identity is already bound to a corroborated ICAO address");
                        track.identity_spoofed = true;
                        other->second.flags.add(MessageFlag::IdentityConflict);
                    } else if (incoming_score >= existing_score + 2) {
                        add_flag(result, MessageFlag::IdentityConflict,
                                 "new identity evidence supersedes another candidate track");
                        other->second.identity_spoofed = true;
                    } else {
                        add_flag(result, MessageFlag::IdentityConflict,
                                 "flight identity is claimed by more than one ICAO address");
                        add_flag(result, MessageFlag::IdentitySpoof,
                                 "ambiguous flight identity claim is rejected as spoofing");
                    }
                }
            }
        }

        if (!normalized.icao.empty()) {
            const auto found = icao_tracks_.find(normalized.icao);
            if (found != icao_tracks_.end()) {
                for (const auto& key : found->second) {
                    if (key == track.key) {
                        continue;
                    }
                    const auto other = tracks_.find(key);
                    if (other == tracks_.end()) {
                        continue;
                    }
                    if (!normalized.flight_id.empty() &&
                        other->second.flight_id == normalized.flight_id) {
                        continue;
                    }
                    if (detail::is_finite(other->second.latest_timestamp) &&
                        detail::is_finite(normalized.timestamp) &&
                        std::abs(other->second.latest_timestamp - normalized.timestamp) >
                            config_.identity_window_s &&
                        other->second.latest_timestamp < normalized.timestamp) {
                        continue;
                    }
                    const int existing_score = track_evidence(other->second);
                    const int incoming_score = incoming_evidence(track, normalized);
                    if (existing_score >= incoming_score + 2) {
                        add_flag(result, MessageFlag::IdentitySpoof,
                                 "ICAO address is claimed by a different established flight identity");
                        track.identity_spoofed = true;
                    } else {
                        add_flag(result, MessageFlag::IdentityConflict,
                                 "ICAO address is claimed by more than one flight identity");
                        add_flag(result, MessageFlag::IdentitySpoof,
                                 "ambiguous ICAO identity claim is rejected as spoofing");
                    }
                }
            }
        }
    }

    void check_duplicate(const detail::NormalizedMessage& normalized, TrackState& track,
                         ValidationResult& result) {
        const auto found = seen_.find(normalized.fingerprint);
        if (found == seen_.end()) {
            return;
        }
        const SeenRecord& previous = found->second;
        const bool same_source = previous.source_key == normalized.source_key;
        const bool same_track = previous.track_key == track.key;
        const bool same_message_time =
            detail::is_finite(previous.timestamp) &&
            detail::is_finite(normalized.timestamp) &&
            std::abs(previous.timestamp - normalized.timestamp) <= 1.0e-3;
        const bool newer_track_message =
            detail::is_finite(track.latest_timestamp) &&
            detail::is_finite(normalized.timestamp) &&
            normalized.timestamp + 1.0e-6 < track.latest_timestamp;
        const bool aged = detail::is_finite(previous.timestamp) &&
                          detail::is_finite(normalized.timestamp) &&
                          normalized.timestamp - previous.timestamp > config_.replay_window_s;
        if (same_source && same_track) {
            add_flag(result, MessageFlag::Duplicate,
                     "identical message was already received from this source");
            result.duplicate = true;
        }
        const bool content_changed =
            previous.flight_id != normalized.flight_id || previous.icao != normalized.icao ||
            (previous.has_position && normalized.has_position &&
             detail::distance_nm(previous.latitude, previous.longitude, normalized.latitude,
                                 normalized.longitude) > 0.01) ||
            (previous.has_altitude && normalized.has_altitude &&
             std::abs(previous.altitude_ft - normalized.altitude_ft) > 10.0);
        const bool altered_timestamp_reuse =
            normalized.has_payload_evidence && same_source && !same_message_time;
        const bool cross_receiver_reuse =
            normalized.has_payload_evidence && !same_source &&
            (content_changed ||
             (detail::is_finite(previous.timestamp) &&
              detail::is_finite(normalized.timestamp) &&
              std::abs(previous.timestamp - normalized.timestamp) >
                  config_.correlation_window_s));
        if (newer_track_message || aged || altered_timestamp_reuse || cross_receiver_reuse) {
            add_flag(result, MessageFlag::Replay,
                     "message repeats historical content after newer track data");
            result.replay = true;
        }
    }

    void check_timestamp(const detail::NormalizedMessage& normalized, const TrackState& track,
                         ValidationResult& result) {
        if (!detail::is_finite(track.latest_timestamp) ||
            !detail::is_finite(normalized.timestamp)) {
            return;
        }
        if (normalized.timestamp < track.latest_timestamp - config_.max_timestamp_latency_s) {
            add_flag(result, MessageFlag::Stale,
                     "message timestamp is older than the track watermark");
            result.stale = true;
            if (normalized.timestamp < track.latest_timestamp - config_.replay_window_s) {
                add_flag(result, MessageFlag::Replay,
                         "message timestamp is far outside the live track window");
                result.replay = true;
            }
        }
        const auto source = track.observations.find(normalized.source_key);
        if (source != track.observations.end() && source->second.sequence >= 0 &&
            normalized.sequence >= 0 && normalized.sequence <= source->second.sequence &&
            normalized.timestamp <= source->second.timestamp + 1.0e-6) {
            add_flag(result, MessageFlag::SequenceRegression,
                     "message sequence did not advance for this source");
            result.replay = true;
            add_flag(result, MessageFlag::Replay, "message sequence regressed");
        }
    }

    void check_jump(const detail::NormalizedMessage& normalized, const TrackState& track,
                    ValidationResult& result, bool& severe) {
        severe = false;
        const auto source = track.observations.find(normalized.source_key);
        const SourceObservation* reference =
            source != track.observations.end() &&
                    (source->second.has_position || source->second.has_altitude)
                ? &source->second
                : nullptr;
        const bool same_source_reference =
            reference != nullptr && reference->has_position;
        const bool reference_has_position =
            (reference != nullptr && reference->has_position) ||
            (reference == nullptr && track.has_position);
        const bool reference_has_altitude =
            (reference != nullptr && reference->has_altitude) ||
            (reference == nullptr && detail::is_finite(track.altitude_ft));
        const double reference_timestamp =
            reference != nullptr && detail::is_finite(reference->timestamp)
                ? reference->timestamp
                : track.latest_timestamp;
        const double reference_latitude =
            reference != nullptr && reference->has_position ? reference->latitude
                                                            : track.latitude;
        const double reference_longitude =
            reference != nullptr && reference->has_position ? reference->longitude
                                                            : track.longitude;
        const double reference_altitude =
            reference != nullptr && reference->has_altitude ? reference->altitude_ft
                                                            : track.altitude_ft;
        const double reference_speed =
            reference != nullptr && detail::is_finite(reference->ground_speed_kts)
                ? reference->ground_speed_kts
                : track.ground_speed_kts;

        if (!detail::is_finite(normalized.timestamp) ||
            !detail::is_finite(reference_timestamp)) {
            return;
        }
        const double dt = normalized.timestamp - reference_timestamp;
        if (dt < 0.0) {
            return;
        }

        const double absolute_gate =
            same_source_reference ? config_.max_position_jump_nm
                                  : std::max(config_.max_position_jump_nm,
                                             config_.position_gate_nm);
        if (normalized.has_position && reference_has_position) {
            const double actual_nm =
                detail::distance_nm(reference_latitude, reference_longitude,
                                    normalized.latitude, normalized.longitude);
            const double reported_speed =
                std::max(detail::is_finite(normalized.ground_speed_kts)
                             ? normalized.ground_speed_kts
                             : 0.0,
                         detail::is_finite(reference_speed) ? reference_speed : 0.0);
            const double possible_nm =
                (reported_speed + config_.position_jump_speed_margin_kts) * dt / 3600.0;
            const double allowed_nm = std::max(absolute_gate, possible_nm + 1.0);
            if (actual_nm > allowed_nm) {
                add_flag(result, MessageFlag::PositionJump,
                         "position moved farther than speed and elapsed time permit");
                result.position_jump = true;
                result.jump = true;
                severe = actual_nm > allowed_nm * config_.severe_jump_factor;
            }
        }

        if (normalized.has_altitude && reference_has_altitude &&
            detail::is_finite(reference_altitude)) {
            const double allowed_alt =
                config_.max_altitude_jump_ft + config_.altitude_jump_rate_margin_fpm * dt / 60.0;
            if (std::abs(normalized.altitude_ft - reference_altitude) > allowed_alt) {
                add_flag(result, MessageFlag::AltitudeJump,
                         "altitude changed faster than the altitude envelope allows");
                result.altitude_jump = true;
                result.jump = true;
                result.wrong_altitude = true;
            }
        }

        if (normalized.has_speed && detail::is_finite(reference_speed) && dt <= 2.0 &&
            std::abs(normalized.ground_speed_kts - reference_speed) >
                config_.max_speed_change_kts) {
            add_flag(result, MessageFlag::SpeedJump, "ground speed changed abruptly");
            result.speed_jump = true;
        }
    }

    void cross_validate(const detail::NormalizedMessage& normalized, const TrackState& track,
                        ValidationResult& result) {
        int corroborating = 0;
        int radar_confirmations = 0;
        bool position_mismatch = false;
        bool altitude_mismatch = false;
        bool speed_mismatch = false;
        bool radar_position_mismatch = false;
        bool radar_altitude_mismatch = false;
        double max_conflict_credibility = 0.0;
        std::set<std::string> counted_sources;
        const auto recent =
            recent_observations(track, normalized.timestamp, normalized.source_key);
        int existing_radar_count = 0;
        for (const auto* observation : recent) {
            if (observation->radar) {
                ++existing_radar_count;
            }
        }

        for (const auto* observation : recent) {
            if (observation->source_key == normalized.source_key) {
                continue;
            }
            bool matches = true;
            if (observation->has_position && normalized.has_position) {
                const double gate = observation->radar || normalized.radar
                                        ? config_.radar_position_gate_nm
                                        : config_.position_gate_nm;
                if (detail::distance_nm(observation->latitude, observation->longitude,
                                        normalized.latitude, normalized.longitude) > gate) {
                    matches = false;
                    position_mismatch = true;
                    max_conflict_credibility =
                        std::max(max_conflict_credibility, observation->credibility);
                    if (observation->radar || normalized.radar) {
                        radar_position_mismatch = true;
                    }
                }
            }
            if (observation->has_altitude && normalized.has_altitude) {
                const double gate = observation->radar || normalized.radar
                                        ? config_.altitude_gate_ft
                                        : config_.altitude_gate_ft * 1.5;
                if (std::abs(observation->altitude_ft - normalized.altitude_ft) > gate) {
                    matches = false;
                    altitude_mismatch = true;
                    max_conflict_credibility =
                        std::max(max_conflict_credibility, observation->credibility);
                    if (observation->radar || normalized.radar) {
                        radar_altitude_mismatch = true;
                    }
                }
            }
            if (detail::is_finite(observation->ground_speed_kts) &&
                detail::is_finite(normalized.ground_speed_kts) &&
                std::abs(observation->ground_speed_kts - normalized.ground_speed_kts) >
                    config_.speed_gate_kts) {
                matches = false;
                speed_mismatch = true;
            }
            if (!matches) {
                continue;
            }
            if (counted_sources.insert(observation->source_key).second) {
                ++corroborating;
            }
            if (observation->radar || normalized.radar) {
                ++radar_confirmations;
            }
        }

        result.corroborating_sources = corroborating;
        result.radar_confirmations = radar_confirmations;
        result.observation_count = track.observations.size();

        if (position_mismatch) {
            add_flag(result, MessageFlag::MultiSourceMismatch,
                     "independent sources disagree on target position");
            result.multi_source_mismatch = true;
            result.source_mismatch = true;
            if (!(normalized.radar && existing_radar_count == 0)) {
                result.position_injection = true;
            }
        }
        if (altitude_mismatch) {
            add_flag(result, MessageFlag::AltitudeMismatch,
                     "independent sources disagree on target altitude");
            result.altitude_mismatch = true;
            result.wrong_altitude = true;
        }
        if (radar_position_mismatch && !(normalized.radar && existing_radar_count == 0)) {
            add_flag(result, MessageFlag::PositionInjection,
                     "ADS-B position contradicts the radar plot");
            result.position_injection = true;
        }
        if (radar_altitude_mismatch) {
            result.wrong_altitude = true;
        }
        if (speed_mismatch) {
            add_flag(result, MessageFlag::SpeedJump,
                     "independent sources disagree on ground speed");
            result.speed_jump = true;
        }
        const double incoming_quality = detail::quality_factor(
            normalized.receiver_quality, normalized.signal_quality);
        const bool radar_authority =
            normalized.radar && existing_radar_count == 0 &&
            incoming_quality > config_.low_quality_threshold &&
            (position_mismatch ||
             (altitude_mismatch && incoming_quality > max_conflict_credibility + 0.15));
        if (radar_authority) {
            result.trusted_correction = true;
            result.corrected = true;
            add_flag(result, MessageFlag::TrackConflict,
                     "radar observation overrides a weaker conflicting source");
        }
        if (corroborating == 0) {
            add_flag(result, MessageFlag::Uncorroborated,
                     "no independent source currently corroborates this report");
            result.uncorroborated = true;
        }
        if (normalized.radar && corroborating > 0) {
            result.radar_confirmations = std::max(result.radar_confirmations, 1);
        }
    }

    double compute_credibility(const detail::NormalizedMessage& normalized,
                               double effective_receiver_quality, bool receiver_trusted,
                               const ValidationResult& result) const {
        double credibility =
            detail::quality_factor(effective_receiver_quality, normalized.signal_quality);
        if (!receiver_trusted) {
            credibility *= 0.55;
        }
        if (!normalized.crc_valid) {
            credibility *= 0.2;
        } else {
            credibility += 0.08;
        }
        credibility += std::min(0.18, 0.06 * result.corroborating_sources);
        if (result.radar_confirmations > 0) {
            credibility += 0.06;
        }
        if (result.identity_spoof) credibility -= 0.65;
        if (result.identity_conflict) credibility -= 0.30;
        if (result.replay) credibility -= 0.55;
        if (result.duplicate) credibility -= 0.25;
        if (result.position_jump) credibility -= 0.35;
        if (result.altitude_jump) credibility -= 0.30;
        if (result.multi_source_mismatch && !result.trusted_correction) credibility -= 0.45;
        if (result.altitude_mismatch && !result.trusted_correction) credibility -= 0.35;
        if (result.trusted_correction) credibility -= 0.08;
        if (result.out_of_envelope) credibility -= 0.50;
        if (result.stale) credibility -= 0.20;
        if (result.uncorroborated) credibility -= 0.12;
        if (result.crc_failure) credibility -= 0.50;
        return std::clamp(credibility, 0.0, 1.0);
    }

    void apply_quality(const detail::NormalizedMessage& normalized, ValidationResult& result,
                       bool& receiver_trusted, double& effective_receiver_quality) {
        const auto receiver = receivers_.find(detail::upper(normalized.receiver_id));
        double receiver_quality = normalized.receiver_quality;
        receiver_trusted = true;
        if (receiver != receivers_.end()) {
            receiver_quality = std::min(receiver_quality, receiver->second.quality);
            receiver_trusted = receiver->second.trusted;
        }
        effective_receiver_quality = receiver_quality;
        if (receiver_quality < config_.low_quality_threshold) {
            add_flag(result, MessageFlag::LowReceiverQuality, "receiver quality is low");
            result.low_quality = true;
        }
        if (normalized.signal_quality < config_.low_quality_threshold) {
            add_flag(result, MessageFlag::LowSignalQuality, "signal quality is low");
            result.low_quality = true;
        }
        result.credibility = detail::quality_factor(receiver_quality, normalized.signal_quality);
        result.confidence = result.credibility;
    }

    ValidationResult process_locked(const SurveillanceMessage& message) {
        ++arrival_counter_;
        detail::NormalizedMessage normalized = detail::normalize(message);
        ValidationResult result;
        result.flight_id = normalized.flight_id;
        result.callsign = normalized.callsign;
        result.icao_address = normalized.icao;
        result.receiver_id = normalized.receiver_id;
        result.type = normalized.type;
        result.source = normalized.source;
        result.timestamp = normalized.timestamp;

        bool receiver_trusted = true;
        double effective_receiver_quality = 1.0;
        detail::validate_structure(message, normalized, config_, result);
        apply_quality(normalized, result, receiver_trusted, effective_receiver_quality);
        result.out_of_envelope =
            result.flags.contains(MessageFlag::OutOfEnvelope);
        result.crc_failure = result.flags.contains(MessageFlag::CrcFailure);
        result.wrong_altitude =
            result.flags.contains(MessageFlag::InvalidAltitude);
        result.flagged = !result.flags.empty();

        const bool structural_valid =
            !result.flags.contains(MessageFlag::InvalidType) &&
            !result.flags.contains(MessageFlag::InvalidSource) &&
            !result.flags.contains(MessageFlag::InvalidIdentity) &&
            !result.flags.contains(MessageFlag::InvalidPosition) &&
            !result.flags.contains(MessageFlag::InvalidAltitude) &&
            !result.flags.contains(MessageFlag::InvalidSpeed) &&
            !result.flags.contains(MessageFlag::InvalidHeading) &&
            !result.flags.contains(MessageFlag::InvalidVerticalRate) &&
            !result.flags.contains(MessageFlag::InvalidTimestamp) &&
            !result.flags.contains(MessageFlag::OutOfEnvelope) &&
            !result.flags.contains(MessageFlag::CrcFailure);
        result.valid = structural_valid;

        if (!structural_valid) {
            result.accepted = false;
            result.authentic = false;
            result.status = ValidationStatus::Invalid;
            result.credibility = std::min(result.credibility, 0.15);
            result.confidence = result.credibility;
            result.score = result.credibility;
            result.trust_score = result.credibility;
            result.authenticity_score = 0.0;
            result.flag_mask = result.flags.bits();
            result.reason = result.reason.empty() ? "message failed type validation"
                                                  : result.reason;
            return result;
        }

        const std::string requested_key = make_track_key(normalized);
        TrackState& track = resolve_track(normalized, requested_key);
        result.track_id = track.key;
        result.track_key = track.key;

        if (const auto binding = trusted_identities_.find(normalized.flight_id);
            binding != trusted_identities_.end() && binding->second.trusted) {
            track.trusted_binding = binding->second.icao == normalized.icao;
        }

        check_duplicate(normalized, track, result);
        check_timestamp(normalized, track, result);
        check_identity_conflicts(normalized, track, result);
        result.identity_spoof =
            result.flags.contains(MessageFlag::IdentitySpoof);
        result.identity_conflict =
            result.flags.contains(MessageFlag::IdentityConflict);
        cross_validate(normalized, track, result);

        bool severe_jump = false;
        if (!result.duplicate && !result.replay && !result.trusted_correction) {
            check_jump(normalized, track, result, severe_jump);
        }
        if (result.position_jump && (severe_jump || result.multi_source_mismatch)) {
            add_flag(result, MessageFlag::PositionInjection,
                     "large position discontinuity is inconsistent with the track");
            result.position_injection = true;
        }

        result.crc_failure = result.flags.contains(MessageFlag::CrcFailure);
        result.flag_mask = result.flags.bits();
        result.credibility = compute_credibility(normalized, effective_receiver_quality,
                                                 receiver_trusted, result);
        result.confidence = result.credibility;
        result.score = result.credibility;
        result.trust_score = result.credibility;

        const bool hard_reject =
            (config_.reject_on_spoof && (result.identity_spoof || result.identity_conflict)) ||
            (config_.reject_on_replay && (result.replay || result.stale)) ||
            (config_.reject_on_duplicate && result.duplicate) ||
            (config_.reject_on_jump &&
             (result.position_jump || result.altitude_jump || result.position_injection)) ||
            (config_.reject_on_mismatch &&
             ((result.multi_source_mismatch || result.altitude_mismatch) &&
              !result.trusted_correction)) ||
            (config_.reject_uncorroborated && result.uncorroborated &&
             !result.trusted_correction);

        result.accepted = structural_valid && !hard_reject;
        if (result.accepted && result.credibility < config_.low_credibility_threshold) {
            result.accepted = false;
            add_flag(result, MessageFlag::LowCredibility,
                     "credibility is below the acceptance floor");
        }
        if (result.accepted && !result.identity_spoof && !result.identity_conflict &&
            !result.replay && !result.duplicate && !result.position_jump &&
            (!result.multi_source_mismatch || result.trusted_correction) &&
            (!result.altitude_mismatch || result.trusted_correction) &&
            result.credibility >= config_.authentic_threshold) {
            result.authentic = true;
            result.verified = true;
        }
        if (config_.require_corroboration && result.corroborating_sources <= 0 &&
            !result.trusted_correction) {
            result.authentic = false;
            result.verified = false;
        }
        result.authenticity_score =
            result.authentic
                ? result.credibility
                : (result.uncorroborated ? result.credibility * 0.55
                                         : result.credibility * 0.75);
        result.authenticity = result.authenticity_score;
        result.suspicious =
            result.identity_spoof || result.identity_conflict || result.replay ||
            result.position_injection ||
            (result.multi_source_mismatch && !result.trusted_correction) ||
            (result.altitude_mismatch && !result.trusted_correction) ||
            result.position_jump || result.altitude_jump ||
            result.out_of_envelope || result.crc_failure || result.low_quality;

        if (result.identity_spoof) {
            result.status = ValidationStatus::Spoof;
            track.identity_spoofed = true;
        } else if (result.replay) {
            result.status = ValidationStatus::Replay;
        } else if (result.duplicate) {
            result.status = ValidationStatus::Duplicate;
        } else if (result.position_jump || result.position_injection ||
                   (result.multi_source_mismatch && !result.trusted_correction) ||
                   (result.altitude_mismatch && !result.trusted_correction) ||
                   result.altitude_jump || result.stale) {
            result.status = ValidationStatus::Suspicious;
        } else if (!result.accepted) {
            result.status = ValidationStatus::Rejected;
        } else if (result.authentic) {
            result.status = ValidationStatus::Authentic;
        } else {
            result.status = ValidationStatus::Unverified;
        }

        const bool update_allowed =
            structural_valid && !result.duplicate && !result.replay &&
            !result.stale && !result.identity_spoof && !result.position_jump &&
            !result.altitude_jump && !result.position_injection &&
            (!result.multi_source_mismatch || result.trusted_correction) &&
            (!result.altitude_mismatch || result.trusted_correction) &&
            result.credibility >= config_.min_track_credibility;

        if (update_allowed) {
            if (result.trusted_correction) {
                remove_conflicting_observations(track, normalized);
            }
            update_track(track, normalized, result);
        } else if (!result.duplicate && !result.replay) {
            ++track.rejected_messages;
            track.flags |= result.flags;
        }

        seen_[normalized.fingerprint] =
            SeenRecord{arrival_counter_, normalized.timestamp, normalized.source_key, track.key,
                       normalized.flight_id, normalized.icao, normalized.latitude,
                       normalized.longitude, normalized.altitude_ft, normalized.has_position,
                       normalized.has_altitude};
        if (detail::is_finite(normalized.timestamp)) {
            if (!detail::is_finite(watermark_) || normalized.timestamp > watermark_) {
                watermark_ = normalized.timestamp;
            }
            if (!detail::is_finite(track.last_arrival_timestamp) ||
                normalized.timestamp > track.last_arrival_timestamp) {
                track.last_arrival_timestamp = normalized.timestamp;
            }
        }

        result.reason =
            result.reason.empty() ? std::string(to_string(result.status)) : result.reason;
        return result;
    }

    void remove_conflicting_observations(TrackState& track,
                                         const detail::NormalizedMessage& normalized) {
        std::vector<std::string> remove_keys;
        for (const auto& [key, observation] : track.observations) {
            if (observation.radar) {
                continue;
            }
            bool conflicts = false;
            if (observation.has_position && normalized.has_position &&
                detail::distance_nm(observation.latitude, observation.longitude,
                                    normalized.latitude, normalized.longitude) >
                    config_.radar_position_gate_nm) {
                conflicts = true;
            }
            if (observation.has_altitude && normalized.has_altitude &&
                std::abs(observation.altitude_ft - normalized.altitude_ft) >
                    config_.altitude_gate_ft) {
                conflicts = true;
            }
            if (conflicts) {
                remove_keys.push_back(key);
            }
        }
        for (const auto& key : remove_keys) {
            track.observations.erase(key);
        }
        track.receivers.clear();
        for (const auto& [key, observation] : track.observations) {
            (void)key;
            if (!observation.receiver_id.empty()) {
                track.receivers.insert(observation.receiver_id);
            } else {
                track.receivers.insert(observation.source_key);
            }
        }
    }

    void update_track(TrackState& track, const detail::NormalizedMessage& normalized,
                      ValidationResult& result) {
        ++track.accepted_messages;
        const bool advances_track =
            !detail::is_finite(track.latest_timestamp) ||
            !detail::is_finite(normalized.timestamp) ||
            normalized.timestamp >= track.latest_timestamp;
        if (advances_track && normalized.has_position) {
            track.has_position = true;
            track.latitude = normalized.latitude;
            track.longitude = normalized.longitude;
        }
        if (advances_track && normalized.has_altitude) {
            track.altitude_ft = normalized.altitude_ft;
        }
        if (advances_track && normalized.has_speed) {
            track.ground_speed_kts = normalized.ground_speed_kts;
        }
        if (advances_track && normalized.has_heading) {
            track.heading_deg = normalized.heading_deg;
        }
        if (advances_track && normalized.has_vertical_rate) {
            track.vertical_rate_fpm = normalized.vertical_rate_fpm;
        }
        if (advances_track && detail::is_finite(normalized.timestamp)) {
            track.latest_timestamp = normalized.timestamp;
        }
        track.credibility = std::max(track.credibility, result.credibility);
        if (normalized.sequence >= 0) {
            track.last_sequence = std::max(track.last_sequence, normalized.sequence);
        }
        if (!normalized.receiver_id.empty()) {
            track.receivers.insert(normalized.receiver_id);
        } else if (!normalized.source_key.empty()) {
            track.receivers.insert(normalized.source_key);
        }

        SourceObservation observation;
        observation.source = normalized.source;
        observation.source_key = normalized.source_key;
        observation.receiver_id = normalized.receiver_id;
        observation.timestamp = normalized.timestamp;
        observation.latitude = normalized.latitude;
        observation.longitude = normalized.longitude;
        observation.altitude_ft = normalized.altitude_ft;
        observation.ground_speed_kts = normalized.ground_speed_kts;
        observation.sequence = normalized.sequence;
        observation.fingerprint = normalized.fingerprint;
        observation.credibility = result.credibility;
        observation.has_position = normalized.has_position;
        observation.has_altitude = normalized.has_altitude;
        observation.radar = normalized.radar;

        const auto existing = track.observations.find(normalized.source_key);
        if (existing == track.observations.end() ||
            !detail::is_finite(existing->second.timestamp) ||
            !detail::is_finite(normalized.timestamp) ||
            normalized.timestamp >= existing->second.timestamp) {
            track.observations[normalized.source_key] = std::move(observation);
        }
        if (normalized.radar) {
            track.radar_confirmed = track.radar_confirmed ||
                                    result.radar_confirmations > 0 ||
                                    result.trusted_correction;
        } else if (result.radar_confirmations > 0) {
            track.radar_confirmed = true;
        }
    }
};

using SurveillanceValidator = SurveillanceProcessor;
using SurveillanceMessageProcessor = SurveillanceProcessor;
using SurveillanceMessageValidator = SurveillanceProcessor;
using SurveillanceFusion = SurveillanceProcessor;
using SurveillanceEngine = SurveillanceProcessor;
using SurveillanceIntegrity = SurveillanceProcessor;
using TrackFusion = SurveillanceProcessor;
using TrackFusionComponent = SurveillanceProcessor;
using TrackFusionEngine = SurveillanceProcessor;
using FusionEngine = SurveillanceProcessor;
using MessageProcessor = SurveillanceProcessor;
using MessageValidator = SurveillanceProcessor;
using AdsbValidator = SurveillanceProcessor;
using MessageAuthenticator = SurveillanceProcessor;

inline ValidationResult validate_message(const SurveillanceMessage& message,
                                         SurveillanceConfig config = {}) {
    SurveillanceProcessor processor(std::move(config));
    return processor.process(message);
}

inline ValidationResult validate(const SurveillanceMessage& message) {
    return validate_message(message);
}

inline bool validate(const SurveillanceMessage& message, ValidationResult& report) {
    report = validate_message(message);
    return report.valid && report.accepted;
}

inline ValidationResult check_message(const SurveillanceMessage& message,
                                      SurveillanceConfig config = {}) {
    return validate_message(message, std::move(config));
}

inline bool check_message(const SurveillanceMessage& message, ValidationResult& report,
                          SurveillanceConfig config = {}) {
    report = validate_message(message, std::move(config));
    return report.valid && report.accepted;
}

inline ValidationResult inspect_message(const SurveillanceMessage& message,
                                        SurveillanceConfig config = {}) {
    return validate_message(message, std::move(config));
}

inline ValidationResult process_message(const SurveillanceMessage& message,
                                        SurveillanceConfig config = {}) {
    return validate_message(message, std::move(config));
}

inline ValidationResult ingest_message(const SurveillanceMessage& message,
                                       SurveillanceConfig config = {}) {
    return validate_message(message, std::move(config));
}

inline ValidationResult processMessage(const SurveillanceMessage& message,
                                       SurveillanceConfig config = {}) {
    return validate_message(message, std::move(config));
}

inline ValidationResult validateMessage(const SurveillanceMessage& message,
                                        SurveillanceConfig config = {}) {
    return validate_message(message, std::move(config));
}

}  // namespace netsec

namespace surveillance = netsec;
namespace adsb = netsec;
namespace aviation_surveillance = netsec;
namespace track_fusion = netsec;
