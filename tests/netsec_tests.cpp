#include "netsec.hpp"

#include <cmath>
#include <iostream>
#include <string>
#include <vector>

namespace {

int failures = 0;

void expect(bool condition, const std::string& description) {
    if (!condition) {
        ++failures;
        std::cerr << "FAIL: " << description << '\n';
    }
}

netsec::SurveillanceMessage position_message(const std::string& id, const std::string& icao,
                                             const std::string& receiver, double lat, double lon,
                                             double altitude, double timestamp,
                                             double quality = 0.95) {
    auto message = netsec::SurveillanceMessage::position(id, icao, lat, lon, altitude, timestamp);
    message.receiver_id = receiver;
    message.receiver_quality = quality;
    message.signal_quality = quality;
    return message;
}

netsec::SurveillanceMessage radar_message(const std::string& id, const std::string& receiver,
                                          double lat, double lon, double altitude,
                                          double timestamp, double quality = 0.98) {
    auto message =
        netsec::SurveillanceMessage::radar(id, lat, lon, altitude, timestamp, receiver);
    message.receiver_quality = quality;
    message.signal_quality = quality;
    return message;
}

void test_type_validation() {
    {
        netsec::SurveillanceProcessor processor;
        auto message = netsec::SurveillanceMessage::position("UAL1", "ABC123", 37.0, -122.0,
                                                             0.0, 1000.0);
        message.altitude_ft = std::numeric_limits<double>::quiet_NaN();
        const auto result = processor.process(message);
        expect(!result.valid, "airborne position without altitude is invalid");
        expect(result.flags.contains(netsec::MessageFlag::InvalidAltitude),
               "missing altitude flag");
    }
    {
        netsec::SurveillanceProcessor processor;
        auto message = netsec::SurveillanceMessage::velocity("UAL1", "ABC123", 450.0, 90.0,
                                                             -1200.0, 1000.0);
        message.ground_speed_kts = std::numeric_limits<double>::quiet_NaN();
        const auto result = processor.process(message);
        expect(!result.valid, "velocity without speed is invalid");
        expect(result.flags.contains(netsec::MessageFlag::InvalidSpeed),
               "missing speed flag");
    }
    {
        netsec::SurveillanceProcessor processor;
        auto message = position_message("UAL1", "ABC123", "R1", 37.0, -122.0, 35000.0, 1000.0);
        message.timestamp = std::numeric_limits<double>::quiet_NaN();
        const auto result = processor.process(message);
        expect(!result.valid, "position without timestamp is invalid");
        expect(result.flags.contains(netsec::MessageFlag::InvalidTimestamp),
               "missing timestamp flag");
    }
    {
        netsec::SurveillanceProcessor processor;
        auto message = position_message("UAL1", "NOTHEX", "R1", 37.0, -122.0, 35000.0, 1000.0);
        const auto result = processor.process(message);
        expect(!result.valid, "malformed ICAO address is invalid");
        expect(result.flags.contains(netsec::MessageFlag::InvalidIdentity),
               "malformed identity flag");
    }
    {
        netsec::SurveillanceProcessor processor;
        auto message = position_message("UA*123", "ABC123", "R1", 37.0, -122.0, 35000.0, 1000.0);
        const auto result = processor.process(message);
        expect(!result.valid, "malformed flight identity is invalid");
        expect(result.flags.contains(netsec::MessageFlag::InvalidIdentity),
               "malformed callsign flag");
    }
    {
        netsec::SurveillanceProcessor processor;
        auto message = position_message("UAL1", "ABC123", "R1", 95.0, -122.0, 35000.0, 1000.0);
        const auto result = processor.process(message);
        expect(!result.valid, "out-of-envelope latitude is invalid");
        expect(result.out_of_envelope, "latitude envelope flag");
        expect(result.flags.contains(netsec::MessageFlag::OutOfEnvelope),
               "out-of-envelope flag");
    }
    {
        netsec::SurveillanceProcessor processor;
        auto message = position_message("UAL1", "ABC123", "R1", 37.0, -122.0, 70000.0, 1000.0);
        const auto result = processor.process(message);
        expect(!result.valid, "out-of-envelope altitude is invalid");
        expect(result.out_of_envelope, "altitude envelope flag");
    }
    {
        netsec::SurveillanceProcessor processor;
        netsec::SurveillanceMessage message;
        message.type = netsec::MessageType::Radar;
        message.source = netsec::SourceType::Radar;
        message.flight_id = "RAD42";
        message.range_nm = 20.0;
        message.azimuth_deg = 90.0;
        message.receiver_latitude = 37.0;
        message.receiver_longitude = -122.5;
        message.timestamp = 1000.0;
        const auto result = processor.process(message);
        expect(result.valid, "radar range/azimuth converts to a valid position");
        expect(result.accepted, "valid converted radar plot is accepted");
    }
    {
        netsec::SurveillanceProcessor processor;
        netsec::SurveillanceMessage message;
        message.type = netsec::MessageType::ModeS;
        message.flight_id = "UAL1";
        message.timestamp = 1000.0;
        const auto result = processor.process(message);
        expect(!result.valid, "Mode S without ICAO address is invalid");
        expect(result.flags.contains(netsec::MessageFlag::InvalidIdentity),
               "Mode S identity flag");
    }
    {
        netsec::SurveillanceProcessor processor;
        netsec::SurveillanceMessage message;
        const auto result = processor.process(message);
        expect(!result.valid, "empty message is invalid");
        expect(result.status == netsec::ValidationStatus::Invalid, "invalid status");
    }
    {
        netsec::SurveillanceProcessor processor;
        auto message = netsec::SurveillanceMessage::velocity("UAL1", "ABC123", 450.0, 90.0,
                                                             -1200.0, 1000.0);
        message.receiver_id = "R1";
        const auto result = processor.process(message);
        expect(result.valid, "valid velocity message is structurally valid");
        expect(result.accepted, "valid velocity message is accepted");
    }
    {
        netsec::SurveillanceProcessor processor;
        auto message = netsec::SurveillanceMessage::velocity("UAL1", "ABC123", 450.0, 400.0,
                                                             -1200.0, 1000.0);
        const auto result = processor.process(message);
        expect(!result.valid, "heading above 360 degrees is invalid");
        expect(result.flags.contains(netsec::MessageFlag::InvalidHeading),
               "heading envelope flag");
    }
    {
        netsec::SurveillanceProcessor processor;
        auto message = position_message("UAL1", "ABC123", "R1", 37.0, -122.0, 35000.0, 1000.0);
        message.longitude = std::numeric_limits<double>::quiet_NaN();
        const auto result = processor.process(message);
        expect(!result.valid, "position with only one coordinate is invalid");
        expect(result.flags.contains(netsec::MessageFlag::InvalidPosition),
               "incomplete coordinate flag");
    }
    {
        netsec::SurveillanceProcessor processor;
        netsec::SurveillanceMessage message;
        message.type = netsec::MessageType::AdsbIdentification;
        message.flight_id = "UAL1";
        message.icao_address = "ABC123";
        message.timestamp = 1000.0;
        const auto result = processor.process(message);
        expect(result.valid, "identification with callsign is valid");
    }
    {
        netsec::SurveillanceProcessor processor;
        netsec::SurveillanceMessage message;
        message.type = netsec::MessageType::AdsbIdentification;
        message.icao_address = "ABC123";
        message.timestamp = 1000.0;
        const auto result = processor.process(message);
        expect(!result.valid, "identification without callsign is invalid");
    }
    {
        netsec::SurveillanceProcessor processor;
        netsec::SurveillanceMessage message;
        message.type = netsec::MessageType::Radar;
        message.flight_id = "RAD1";
        message.range_nm = -5.0;
        message.azimuth_deg = 90.0;
        message.receiver_latitude = 37.0;
        message.receiver_longitude = -122.0;
        message.timestamp = 1000.0;
        const auto result = processor.process(message);
        expect(!result.valid, "negative radar range is invalid");
        expect(result.out_of_envelope, "radar range envelope flag");
    }
}

void test_legitimate_multisource_track() {
    netsec::SurveillanceProcessor processor;
    auto first = processor.process(
        position_message("UAL123", "ABC123", "R1", 37.0000, -122.0000, 35000.0, 1000.0));
    expect(first.valid, "first legitimate ADS-B position is valid");
    expect(first.accepted, "first legitimate ADS-B position is accepted");
    expect(!first.authentic, "single-source target is not yet authenticated");

    auto radar = processor.process(
        radar_message("UAL123", "RADAR1", 37.0000, -122.0000, 35000.0, 1000.2));
    expect(radar.valid, "radar plot is valid");
    expect(radar.accepted, "corroborated radar plot is accepted");
    expect(radar.authentic, "ADS-B and radar agreement authenticates the target");
    expect(radar.corroborating_sources >= 1, "radar message has a corroborating source");
    expect(radar.radar_confirmations >= 1, "radar agreement is recorded");

    auto velocity = netsec::SurveillanceMessage::velocity("UAL123", "ABC123", 440.0, 5.0,
                                                          -800.0, 1000.8);
    velocity.receiver_id = "R1";
    const auto velocity_result = processor.process(velocity);
    expect(velocity_result.valid, "correlated velocity message is valid");
    expect(velocity_result.authentic, "velocity message inherits multi-source trust");

    const auto assessment = processor.assess_target("UAL123");
    expect(assessment.exists, "target track exists");
    expect(assessment.authentic, "track is assessed as authentic");
    expect(assessment.verified, "track is verified");
    expect(assessment.verdict == netsec::TrackVerdict::Authentic, "authentic track verdict");
    expect(assessment.source_count >= 2, "track retains multiple sources");
}

void test_multi_receiver_authentication() {
    netsec::SurveillanceProcessor processor;
    const auto first = processor.process(
        position_message("SWA22", "A1B2C3", "R1", 40.0, -100.0, 30000.0, 2000.0));
    expect(first.valid && first.accepted, "first receiver message accepted");
    expect(!first.authentic, "one receiver alone is unverified");

    const auto second = processor.process(
        position_message("SWA22", "A1B2C3", "R2", 40.0001, -100.0001, 30050.0, 2000.1));
    expect(second.authentic, "two independent receiver messages authenticate the target");
    expect(second.corroborating_sources >= 1, "second receiver corroborates the first");

    const auto assessment = processor.assess_target("SWA22");
    expect(assessment.authentic, "multi-receiver track assessment is authentic");
}

void test_duplicate_and_replay() {
    netsec::SurveillanceProcessor duplicate_processor;
    auto message =
        position_message("DUP1", "ABC123", "R1", 37.0, -122.0, 35000.0, 1000.0);
    const auto first = duplicate_processor.process(message);
    expect(first.valid && first.accepted, "first copy accepted");
    const auto duplicate = duplicate_processor.process(message);
    expect(duplicate.valid, "duplicate remains structurally valid");
    expect(duplicate.duplicate, "duplicate is flagged");
    expect(!duplicate.replay, "immediate duplicate is distinguished from replay");
    expect(!duplicate.accepted, "duplicate is not accepted into fusion");

    netsec::SurveillanceProcessor replay_processor;
    auto old_message =
        position_message("RPL1", "ABC124", "R1", 37.0, -122.0, 35000.0, 3000.0);
    expect(replay_processor.process(old_message).accepted, "replay baseline accepted");
    auto newer_message =
        position_message("RPL1", "ABC124", "R1", 37.0, -122.0, 35000.0, 3400.0);
    expect(replay_processor.process(newer_message).accepted, "newer message accepted");
    const auto replay = replay_processor.process(old_message);
    expect(replay.replay, "historical message is flagged as replay");
    expect(replay.stale, "historical message is stale");
    expect(!replay.accepted, "replayed message is rejected");
    expect(replay.status == netsec::ValidationStatus::Replay, "replay status");
}

void test_identity_spoofing() {
    netsec::SurveillanceProcessor processor;
    expect(processor
               .process(position_message("UAL77", "ABC777", "R1", 41.0, -87.0, 32000.0, 4000.0))
               .accepted,
           "legitimate identity baseline accepted");
    expect(processor
               .process(radar_message("UAL77", "RADAR1", 41.0, -87.0, 32000.0, 4000.1))
               .accepted,
           "legitimate identity radar accepted");

    auto spoof = position_message("UAL77", "DEF999", "EVIL", 25.0, -80.0, 32000.0, 4000.2);
    const auto spoof_result = processor.process(spoof);
    expect(spoof_result.valid, "spoof message can still be structurally valid");
    expect(spoof_result.identity_spoof, "identity spoof is detected");
    expect(spoof_result.flags.contains(netsec::MessageFlag::IdentitySpoof),
           "identity spoof flag");
    expect(!spoof_result.accepted, "identity spoof is rejected");
    expect(spoof_result.status == netsec::ValidationStatus::Spoof, "spoof status");

    auto spoof_altitude =
        position_message("UAL77", "DEF999", "EVIL", 41.0, -87.0, 50000.0, 4000.3);
    const auto spoof_altitude_result = processor.process(spoof_altitude);
    expect(spoof_altitude_result.identity_spoof, "same forged identity is still detected");
    expect(!spoof_altitude_result.accepted, "forged identity message remains rejected");

    const auto assessment = processor.assess_target("UAL77");
    expect(assessment.exists, "established target still exists");
    expect(!assessment.authentic || assessment.suspicious,
           "target with conflicting identity is not silently authenticated");
}

void test_registered_identity_spoof() {
    netsec::SurveillanceProcessor processor;
    processor.register_identity("DAL9", "A1B2C4");
    const auto result = processor.process(
        position_message("DAL9", "A1B2C5", "R1", 33.0, -84.0, 30000.0, 5000.0));
    expect(result.identity_spoof, "trusted identity binding detects a forged ICAO");
    expect(!result.accepted, "forged trusted identity is rejected");
}

void test_position_injection() {
    netsec::SurveillanceProcessor processor;
    expect(processor
               .process(position_message("AAL5", "ABC555", "R1", 36.0, -115.0, 34000.0, 6000.0))
               .accepted,
           "position injection baseline accepted");
    expect(processor
               .process(radar_message("AAL5", "RADAR1", 36.0, -115.0, 34000.0, 6000.1))
               .accepted,
           "position injection radar baseline accepted");

    auto injected = position_message("AAL5", "ABC555", "EVIL", 46.0, -95.0, 34000.0, 6000.5);
    const auto result = processor.process(injected);
    expect(result.valid, "injected position is structurally valid");
    expect(result.position_jump, "injected position creates a jump");
    expect(result.position_injection, "position injection is identified");
    expect(result.multi_source_mismatch, "radar cross-check disagrees with injection");
    expect(!result.accepted, "injected position is rejected");
    expect(result.wrong_altitude == false, "position injection alone does not fabricate altitude conflict");

    const auto recovered = processor.process(
        position_message("AAL5", "ABC555", "R1", 36.0001, -115.0001, 34000.0, 6001.0));
    expect(recovered.accepted, "track recovers after rejected injection");
    expect(recovered.authentic, "legitimate track remains corroborated");
}

void test_altitude_cross_check() {
    netsec::SurveillanceProcessor processor;
    expect(processor
               .process(position_message("JBU10", "ABC101", "R1", 40.0, -73.0, 35000.0, 7000.0))
               .accepted,
           "altitude baseline accepted");
    const auto wrong = processor.process(
        radar_message("JBU10", "RADAR1", 40.0, -73.0, 10000.0, 7000.1));
    expect(wrong.valid, "conflicting radar altitude is structurally valid");
    expect(wrong.altitude_mismatch, "radar altitude mismatch is flagged");
    expect(wrong.wrong_altitude, "wrong altitude is marked");
    expect(!wrong.accepted, "wrong altitude is rejected");

    const auto corrected = processor.process(
        radar_message("JBU10", "RADAR1", 40.0, -73.0, 35000.0, 7001.0));
    expect(corrected.accepted, "corrected radar altitude is accepted");
    expect(corrected.authentic, "corrected radar altitude restores authentication");
}

void test_quality_changes_credibility() {
    netsec::SurveillanceProcessor high_processor;
    const auto high = high_processor.process(
        position_message("Q1", "ABC201", "R1", 38.0, -121.0, 30000.0, 8000.0, 0.99));
    netsec::SurveillanceProcessor low_processor;
    const auto low = low_processor.process(
        position_message("Q2", "ABC202", "R2", 38.0, -121.0, 30000.0, 8000.0, 0.08));

    expect(high.valid && low.valid, "quality test messages are valid");
    expect(low.credibility < high.credibility, "lower receiver/signal quality reduces credibility");
    expect(low.credibility + 0.20 < high.credibility, "quality materially changes credibility");
    expect(low.low_quality, "low quality is marked");
    expect(low.flags.contains(netsec::MessageFlag::LowSignalQuality),
           "low signal quality flag");
    expect(!low.authentic, "low-quality single source is not authenticated");
}

void test_unverified_new_target() {
    netsec::SurveillanceProcessor processor;
    const auto result = processor.process(
        position_message("FAKE1", "ABC301", "R1", 20.0, 30.0, 41000.0, 9000.0));
    expect(result.valid && result.accepted, "single-source new target is accepted for tracking");
    expect(!result.authentic, "single-source new target is not authenticated");
    expect(result.uncorroborated, "single-source target is uncorroborated");
    expect(!processor.is_authentic_target("FAKE1"),
           "uncorroborated target is not reported as authentic");
}

void test_radar_corrects_poisoned_track() {
    netsec::SurveillanceProcessor processor;
    const auto fake = processor.process(
        position_message("POISON", "ABC401", "EVIL", 0.0, 0.0, 35000.0, 10000.0, 0.80));
    expect(fake.valid && fake.accepted, "initial single-source fake is only tentatively accepted");
    expect(!fake.authentic, "initial single-source fake is not authenticated");

    const auto radar = processor.process(
        radar_message("POISON", "RADAR1", 20.0, 40.0, 30000.0, 10000.2, 0.99));
    expect(radar.multi_source_mismatch, "radar exposes the conflicting poisoned position");
    expect(radar.trusted_correction, "radar corrects a weaker conflicting source");
    expect(!radar.position_injection, "radar correction is not itself marked as injection");
    expect(radar.accepted, "radar correction is accepted");

    const auto fake_again = processor.process(
        position_message("POISON", "ABC401", "EVIL", 0.0, 0.0, 35000.0, 10000.5, 0.80));
    expect(fake_again.position_injection, "repeat fake position is rejected against radar");
    expect(!fake_again.accepted, "repeat fake position is not accepted");

    const auto genuine = processor.process(
        position_message("POISON", "ABC401", "R1", 20.0001, 40.0001, 30000.0, 10001.0, 0.95));
    expect(genuine.accepted, "genuine ADS-B agrees with radar after correction");
    expect(genuine.authentic, "genuine track becomes authentic after corroboration");
}

void test_payload_replay_across_receivers() {
    netsec::SurveillanceProcessor processor;
    auto first = position_message("RPL2", "ABC402", "R1", 35.0, -120.0, 31000.0, 11000.0);
    first.payload_hash = "adsb-payload-1";
    expect(processor.process(first).accepted, "payload baseline accepted");

    auto second = first;
    second.receiver_id = "R2";
    second.timestamp = 11000.2;
    const auto corroborating = processor.process(second);
    expect(!corroborating.replay, "near-simultaneous second receiver is not replay");
    expect(corroborating.corroborating_sources >= 1, "second receiver corroborates payload");

    auto replay = first;
    replay.receiver_id = "R3";
    replay.timestamp = 11200.0;
    const auto replayed = processor.process(replay);
    expect(replayed.replay, "same payload far later is replay");
    expect(!replayed.accepted, "cross-receiver replay is rejected");
}

void test_registered_receiver_quality() {
    netsec::SurveillanceProcessor processor;
    processor.register_receiver("LOWQ", 0.05, false);
    auto message =
        position_message("LOW1", "ABC403", "LOWQ", 30.0, -90.0, 29000.0, 12000.0, 0.99);
    const auto result = processor.process(message);
    expect(result.valid, "message from poor receiver is structurally valid");
    expect(result.credibility < 0.40, "registered receiver quality lowers credibility");
    expect(result.low_quality, "registered low-quality receiver is marked");
    expect(!result.accepted, "low-credibility observation is not fused");
}

}  // namespace

int main() {
    test_type_validation();
    test_legitimate_multisource_track();
    test_multi_receiver_authentication();
    test_duplicate_and_replay();
    test_identity_spoofing();
    test_registered_identity_spoof();
    test_position_injection();
    test_altitude_cross_check();
    test_quality_changes_credibility();
    test_unverified_new_target();
    test_radar_corrects_poisoned_track();
    test_payload_replay_across_receivers();
    test_registered_receiver_quality();

    if (failures != 0) {
        std::cerr << failures << " assertions failed\n";
        return 1;
    }
    std::cout << "all surveillance component tests passed\n";
    return 0;
}
