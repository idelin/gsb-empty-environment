#include "netsec.hpp"

#include <algorithm>
#include <chrono>
#include <iostream>
#include <string>
#include <string_view>

namespace {

int failures = 0;

#define CHECK(condition)                                                     \
  do {                                                                       \
    if (!(condition)) {                                                      \
      std::cerr << __FILE__ << ':' << __LINE__ << ": CHECK failed: "         \
                << #condition << '\n';                                       \
      ++failures;                                                            \
    }                                                                        \
  } while (false)

#define CHECK_EQ(lhs, rhs)                                                   \
  do {                                                                       \
    const auto actual_value = (lhs);                                         \
    const auto expected_value = (rhs);                                       \
    if (!(actual_value == expected_value)) {                                 \
      std::cerr << __FILE__ << ':' << __LINE__ << ": CHECK_EQ failed: "      \
                << #lhs << " != " << #rhs << '\n';                           \
      ++failures;                                                            \
    }                                                                        \
  } while (false)

using namespace netsec;

Request request(std::uint64_t id, std::string method = "GET") {
  Request value;
  value.id = id;
  value.method = std::move(method);
  value.target = "/" + std::to_string(id);
  return value;
}

Response response(std::uint16_t status, std::string body,
                  std::string content_type = "text/plain") {
  Response value;
  value.status = status;
  value.version = "HTTP/1.1";
  value.body = std::move(body);
  value.content_length = value.body.size();
  value.headers.push_back({"Content-Type", std::move(content_type)});
  return value;
}

std::string wire_response(std::string_view body,
                          std::string_view extra_headers = {}) {
  std::string wire = "HTTP/1.1 200 OK\r\nContent-Length: ";
  wire += std::to_string(body.size());
  wire += "\r\n";
  wire += extra_headers;
  wire += "\r\n";
  wire += body;
  return wire;
}

void test_legal_pipelining_in_order() {
  ConnectionPairer pairer;
  CHECK(pairer.enqueue(request(1)).ok());
  CHECK(pairer.enqueue(request(2)).ok());
  CHECK(pairer.enqueue(request(3)).ok());
  CHECK_EQ(pairer.pending_requests(), std::size_t{3});

  const std::string first = wire_response("alpha");
  const std::string second = wire_response("beta");
  const std::string third = wire_response("gamma");
  const std::string all = first + second + third;

  Result result;
  for (const char byte : all) {
    Result step = pairer.feed(std::string_view(&byte, 1));
    CHECK(step.ok());
    for (Delivery& delivery : step.deliveries) {
      result.deliveries.push_back(std::move(delivery));
    }
  }

  CHECK_EQ(result.deliveries.size(), std::size_t{3});
  CHECK_EQ(pairer.pending_requests(), std::size_t{0});
  if (result.deliveries.size() == 3) {
    CHECK_EQ(result.deliveries[0].request.id, std::uint64_t{1});
    CHECK_EQ(result.deliveries[0].response.body, std::string("alpha"));
    CHECK_EQ(result.deliveries[1].request.id, std::uint64_t{2});
    CHECK_EQ(result.deliveries[1].response.body, std::string("beta"));
    CHECK_EQ(result.deliveries[2].request.id, std::uint64_t{3});
    CHECK_EQ(result.deliveries[2].response.body, std::string("gamma"));
  }
  CHECK(pairer.reusable());
}

void test_interim_and_chunked_response() {
  ConnectionPairer pairer;
  CHECK(pairer.enqueue(request(10)).ok());

  Result result = pairer.feed(
      "HTTP/1.1 100 Continue\r\n\r\n"
      "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n"
      "5\r\nhello\r\n6\r\n world\r\n0\r\nX-Trailer: ok\r\n\r\n");
  CHECK(result.ok());
  CHECK_EQ(result.deliveries.size(), std::size_t{1});
  if (!result.deliveries.empty()) {
    CHECK_EQ(result.deliveries[0].request.id, std::uint64_t{10});
    CHECK_EQ(result.deliveries[0].response.status, std::uint16_t{200});
    CHECK_EQ(result.deliveries[0].response.body, std::string("hello world"));
    CHECK(result.deliveries[0].response.chunked);
  }
}

void test_out_of_order_structured_response() {
  ConnectionPairer pairer;
  CHECK(pairer.enqueue(request(1)).ok());
  CHECK(pairer.enqueue(request(2)).ok());

  Result result = pairer.on_response(2, response(200, "second"));
  CHECK(!result.ok());
  CHECK_EQ(static_cast<int>(result.failure.code),
           static_cast<int>(ErrorCode::out_of_order_response));
  CHECK(result.failure.fatal);
  CHECK(result.deliveries.empty());
  CHECK(pairer.closed());
  CHECK_EQ(pairer.pending_requests(), std::size_t{0});
}

void test_out_of_order_raw_head_body_response_is_not_delivered() {
  ConnectionPairer pairer;
  CHECK(pairer.enqueue(request(1, "HEAD")).ok());
  CHECK(pairer.enqueue(request(2, "GET")).ok());

  // This is the response that belonged to request 2. Positional parsing would
  // otherwise hand its headers to the HEAD request. Because two requests are
  // outstanding, a representation length on a HEAD response is ambiguous.
  Result result = pairer.feed(wire_response("leaked"));
  CHECK(!result.ok());
  CHECK_EQ(static_cast<int>(result.failure.code),
           static_cast<int>(ErrorCode::framing_ambiguous));
  CHECK(result.deliveries.empty());
  CHECK(pairer.closed());
}

void test_early_close_after_one_response() {
  ConnectionPairer pairer;
  CHECK(pairer.enqueue(request(1)).ok());
  CHECK(pairer.enqueue(request(2)).ok());

  Result first = pairer.feed(wire_response("first"));
  CHECK(first.ok());
  CHECK_EQ(first.deliveries.size(), std::size_t{1});
  if (!first.deliveries.empty()) {
    CHECK_EQ(first.deliveries.front().request.id, std::uint64_t{1});
    CHECK_EQ(first.deliveries.front().response.body, std::string("first"));
  }

  Result eof = pairer.end_of_stream();
  CHECK(!eof.ok());
  CHECK_EQ(static_cast<int>(eof.failure.code),
           static_cast<int>(ErrorCode::early_close));
  CHECK(eof.deliveries.empty());
  CHECK(pairer.closed());
  CHECK_EQ(pairer.pending_requests(), std::size_t{0});
}

void test_early_close_in_partial_body() {
  ConnectionPairer pairer;
  CHECK(pairer.enqueue(request(1)).ok());
  Result first =
      pairer.feed("HTTP/1.1 200 OK\r\nContent-Length: 5\r\n\r\nhe");
  CHECK(first.ok());
  CHECK(first.deliveries.empty());

  Result eof = pairer.end_of_stream();
  CHECK(!eof.ok());
  CHECK_EQ(static_cast<int>(eof.failure.code),
           static_cast<int>(ErrorCode::early_close));
  CHECK(eof.deliveries.empty());
  CHECK(pairer.closed());
}

void test_close_delimited_single_request_completes_at_eof() {
  ConnectionPairer pairer;
  CHECK(pairer.enqueue(request(1)).ok());
  Result result = pairer.feed(
      "HTTP/1.1 200 OK\r\nConnection: close\r\n\r\nclose-delimited");
  CHECK(result.ok());
  CHECK(result.deliveries.empty());

  Result eof = pairer.end_of_stream();
  CHECK(eof.ok());
  CHECK_EQ(eof.deliveries.size(), std::size_t{1});
  if (!eof.deliveries.empty()) {
    CHECK_EQ(eof.deliveries.front().response.body,
             std::string("close-delimited"));
  }
  CHECK(pairer.closed());
}

void test_length_mismatch_structured() {
  ConnectionPairer pairer;
  CHECK(pairer.enqueue(request(1)).ok());

  Response value = response(200, "four");
  value.content_length = 5;
  Result result = pairer.on_response(std::move(value));
  CHECK(!result.ok());
  CHECK_EQ(static_cast<int>(result.failure.code),
           static_cast<int>(ErrorCode::length_mismatch));
  CHECK(result.deliveries.empty());
  CHECK(pairer.closed());
}

void test_conflicting_framing_is_rejected() {
  ConnectionPairer pairer;
  CHECK(pairer.enqueue(request(1)).ok());
  Result result = pairer.feed(
      "HTTP/1.1 200 OK\r\nContent-Length: 5\r\n"
      "Transfer-Encoding: chunked\r\n\r\nhello");
  CHECK(!result.ok());
  CHECK_EQ(static_cast<int>(result.failure.code),
           static_cast<int>(ErrorCode::framing_ambiguous));
  CHECK(result.deliveries.empty());
  CHECK(pairer.closed());
}

void test_conflicting_duplicate_content_length_is_rejected() {
  ConnectionPairer pairer;
  CHECK(pairer.enqueue(request(1)).ok());
  Result result = pairer.feed(
      "HTTP/1.1 200 OK\r\nContent-Length: 5\r\n"
      "Content-Length: 6\r\n\r\nhello!");
  CHECK(!result.ok());
  CHECK_EQ(static_cast<int>(result.failure.code),
           static_cast<int>(ErrorCode::malformed_response));
  CHECK(result.deliveries.empty());
  CHECK(pairer.closed());
}

void test_head_and_bodyless_status_rules() {
  {
    ConnectionPairer pairer;
    CHECK(pairer.enqueue(request(1, "HEAD")).ok());
    Response value;
    value.status = 200;
    value.body = "must not exist";
    value.content_length = value.body.size();
    Result result = pairer.on_response(std::move(value));
    CHECK(!result.ok());
    CHECK_EQ(static_cast<int>(result.failure.code),
             static_cast<int>(ErrorCode::method_mismatch));
    CHECK(pairer.closed());
  }
  {
    ConnectionPairer pairer;
    CHECK(pairer.enqueue(request(1)).ok());
    Result result = pairer.feed(
        "HTTP/1.1 204 No Content\r\nContent-Length: 3\r\n\r\nabc");
    CHECK(!result.ok());
    CHECK_EQ(static_cast<int>(result.failure.code),
             static_cast<int>(ErrorCode::length_mismatch));
    CHECK(result.deliveries.empty());
    CHECK(pairer.closed());
  }
}

void test_pipeline_depth_is_bounded() {
  ConnectionPairer::Config config;
  config.max_pipeline_depth = 2;
  ConnectionPairer pairer(config);
  CHECK(pairer.enqueue(request(1)).ok());
  CHECK(pairer.enqueue(request(2)).ok());

  Result overflow = pairer.enqueue(request(3));
  CHECK(!overflow.ok());
  CHECK_EQ(static_cast<int>(overflow.failure.code),
           static_cast<int>(ErrorCode::pipeline_depth_exceeded));
  CHECK_EQ(pairer.pending_requests(), std::size_t{2});
  CHECK(!pairer.closed());

  Result result = pairer.feed(wire_response("one") + wire_response("two"));
  CHECK(result.ok());
  CHECK_EQ(result.deliveries.size(), std::size_t{2});
}

void test_timeouts_are_bounded() {
  {
    ConnectionPairer::Config config;
    config.request_timeout = std::chrono::milliseconds{1000};
    ConnectionPairer pairer(config);
    const TimePoint start = Clock::now();
    CHECK(pairer.enqueue(request(1), start).ok());
    Result result = pairer.poll(start + std::chrono::milliseconds{1001});
    CHECK(!result.ok());
    CHECK_EQ(static_cast<int>(result.failure.code),
             static_cast<int>(ErrorCode::request_timeout));
    CHECK(pairer.closed());
  }
  {
    ConnectionPairer::Config config;
    config.response_timeout = std::chrono::milliseconds{1000};
    ConnectionPairer pairer(config);
    const TimePoint start = Clock::now();
    CHECK(pairer.enqueue(request(1), start).ok());
    Result fed = pairer.feed(
        "HTTP/1.1 200 OK\r\nContent-Length: 5\r\n\r\nhe", start);
    CHECK(fed.ok());
    Result result = pairer.poll(start + std::chrono::milliseconds{1001});
    CHECK(!result.ok());
    CHECK_EQ(static_cast<int>(result.failure.code),
             static_cast<int>(ErrorCode::response_timeout));
    CHECK(pairer.closed());
  }
}

void test_backend_error_is_bounded() {
  ConnectionPairer pairer;
  CHECK(pairer.enqueue(request(1)).ok());
  Result result = pairer.on_backend_error("backend reset");
  CHECK(!result.ok());
  CHECK_EQ(static_cast<int>(result.failure.code),
           static_cast<int>(ErrorCode::backend_error_limit));
  CHECK(pairer.closed());
  CHECK_EQ(pairer.pending_requests(), std::size_t{0});
}

void test_status_contract_mismatch() {
  ConnectionPairer pairer;
  Request value = request(1);
  value.expected_status = 201;
  CHECK(pairer.enqueue(value).ok());
  Result result = pairer.on_response(response(200, "ok"));
  CHECK(!result.ok());
  CHECK_EQ(static_cast<int>(result.failure.code),
           static_cast<int>(ErrorCode::status_mismatch));
  CHECK(result.deliveries.empty());
  CHECK(pairer.closed());
}

void test_connection_reuse_clears_all_state() {
  ConnectionPairer::Config config;
  config.require_explicit_reuse = true;
  ConnectionPairer pairer(config);

  CHECK(pairer.enqueue(request(1)).ok());
  Result first = pairer.on_response(response(200, "one"));
  CHECK(first.ok());
  CHECK_EQ(first.deliveries.size(), std::size_t{1});
  if (!first.deliveries.empty()) {
    CHECK_EQ(first.deliveries.front().request.id, std::uint64_t{1});
    CHECK_EQ(first.deliveries.front().response.body, std::string("one"));
  }

  Result missing_reset = pairer.enqueue(request(2));
  CHECK(!missing_reset.ok());
  CHECK_EQ(static_cast<int>(missing_reset.failure.code),
           static_cast<int>(ErrorCode::connection_not_reusable));

  const std::uint64_t before = pairer.generation();
  CHECK(pairer.prepare_reuse().ok());
  CHECK(pairer.generation() > before);
  CHECK(pairer.enqueue(request(2)).ok());

  Result second = pairer.on_response(response(200, "two"));
  CHECK(second.ok());
  CHECK_EQ(second.deliveries.size(), std::size_t{1});
  if (!second.deliveries.empty()) {
    CHECK_EQ(second.deliveries.front().request.id, std::uint64_t{2});
    CHECK_EQ(second.deliveries.front().response.body, std::string("two"));
  }
}

void test_reuse_with_pending_state_fails_closed() {
  ConnectionPairer pairer;
  CHECK(pairer.enqueue(request(1)).ok());
  Result result = pairer.prepare_reuse();
  CHECK(!result.ok());
  CHECK_EQ(static_cast<int>(result.failure.code),
           static_cast<int>(ErrorCode::connection_not_reusable));
  CHECK(pairer.closed());
}

void test_unsolicited_bytes_fail_closed() {
  ConnectionPairer pairer;
  CHECK(pairer.enqueue(request(1)).ok());
  Result first = pairer.feed(wire_response("one"));
  CHECK(first.ok());
  CHECK_EQ(first.deliveries.size(), std::size_t{1});

  Result unsolicited = pairer.feed("HTTP/1.1 200 OK\r\nContent-Length: 1\r\n\r\nx");
  CHECK(!unsolicited.ok());
  CHECK_EQ(static_cast<int>(unsolicited.failure.code),
           static_cast<int>(ErrorCode::unexpected_response));
  CHECK(unsolicited.deliveries.empty());
  CHECK(pairer.closed());
  CHECK(!pairer.prepare_reuse().ok());
}

void test_request_binding_header_detects_exchange() {
  ConnectionPairer::Config config;
  config.response_id_header = "X-Request-Id";
  ConnectionPairer pairer(config);

  Request first = request(41);
  first.response_token = "token-first";
  Request second = request(42);
  second.response_token = "token-second";
  CHECK(pairer.enqueue(first).ok());
  CHECK(pairer.enqueue(second).ok());

  Response wrong = response(200, "wrong");
  wrong.headers.push_back({"X-Request-Id", "token-second"});
  Result result = pairer.on_response(std::move(wrong));
  CHECK(!result.ok());
  CHECK_EQ(static_cast<int>(result.failure.code),
           static_cast<int>(ErrorCode::out_of_order_response));
  CHECK(result.deliveries.empty());
  CHECK(pairer.closed());
}

void test_valid_head_with_representation_length() {
  ConnectionPairer pairer;
  CHECK(pairer.enqueue(request(1, "HEAD")).ok());
  Result result = pairer.feed(
      "HTTP/1.1 200 OK\r\nContent-Length: 1234\r\n\r\n");
  CHECK(result.ok());
  CHECK_EQ(result.deliveries.size(), std::size_t{1});
  if (!result.deliveries.empty()) {
    CHECK(result.deliveries.front().response.body.empty());
    CHECK_EQ(*result.deliveries.front().response.content_length,
             std::uint64_t{1234});
  }
}

void test_every_byte_split_preserves_pairing() {
  const std::string wire =
      wire_response("a") + wire_response("bb") +
      "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n"
      "3\r\nccc\r\n0\r\n\r\n";

  for (std::size_t split = 1; split <= wire.size(); ++split) {
    ConnectionPairer pairer;
    CHECK(pairer.enqueue(request(1)).ok());
    CHECK(pairer.enqueue(request(2)).ok());
    CHECK(pairer.enqueue(request(3)).ok());

    Result all;
    for (std::size_t offset = 0; offset < wire.size(); offset += split) {
      const std::size_t count = std::min(split, wire.size() - offset);
      Result step = pairer.feed(std::string_view(wire).substr(offset, count));
      CHECK(step.ok());
      for (Delivery& delivery : step.deliveries) {
        all.deliveries.push_back(std::move(delivery));
      }
    }
    CHECK_EQ(all.deliveries.size(), std::size_t{3});
    if (all.deliveries.size() == 3) {
      CHECK_EQ(all.deliveries[0].response.body, std::string("a"));
      CHECK_EQ(all.deliveries[1].response.body, std::string("bb"));
      CHECK_EQ(all.deliveries[2].response.body, std::string("ccc"));
    }
  }
}

void test_connection_close_with_pending_requests_is_fatal() {
  ConnectionPairer pairer;
  CHECK(pairer.enqueue(request(1)).ok());
  CHECK(pairer.enqueue(request(2)).ok());
  Result result = pairer.feed(
      "HTTP/1.1 200 OK\r\nConnection: close\r\nContent-Length: 0\r\n\r\n");
  CHECK(!result.ok());
  CHECK_EQ(static_cast<int>(result.failure.code),
           static_cast<int>(ErrorCode::early_close));
  CHECK_EQ(result.deliveries.size(), std::size_t{1});
  if (!result.deliveries.empty()) {
    CHECK_EQ(result.deliveries.front().request.id, std::uint64_t{1});
  }
  CHECK(pairer.closed());
}

void test_unframed_keepalive_response_is_rejected() {
  ConnectionPairer pairer;
  CHECK(pairer.enqueue(request(1)).ok());
  Result result =
      pairer.feed("HTTP/1.1 200 OK\r\n\r\nunframed-close-delimited");
  CHECK(!result.ok());
  CHECK_EQ(static_cast<int>(result.failure.code),
           static_cast<int>(ErrorCode::framing_ambiguous));
  CHECK(result.deliveries.empty());
  CHECK(pairer.closed());
}

void test_http10_close_delimited_response() {
  ConnectionPairer pairer;
  CHECK(pairer.enqueue(request(1)).ok());
  Result fed =
      pairer.feed("HTTP/1.0 200 OK\r\n\r\nold-style");
  CHECK(fed.ok());
  CHECK(fed.deliveries.empty());
  Result eof = pairer.end_of_stream();
  CHECK(eof.ok());
  CHECK_EQ(eof.deliveries.size(), std::size_t{1});
  if (!eof.deliveries.empty()) {
    CHECK_EQ(eof.deliveries.front().response.body, std::string("old-style"));
  }
}

void test_limits_fail_closed() {
  {
    ConnectionPairer::Config config;
    config.max_body_bytes = 4;
    ConnectionPairer pairer(config);
    CHECK(pairer.enqueue(request(1)).ok());
    Result result = pairer.feed(
        "HTTP/1.1 200 OK\r\nContent-Length: 5\r\n\r\nhello");
    CHECK(!result.ok());
    CHECK_EQ(static_cast<int>(result.failure.code),
             static_cast<int>(ErrorCode::body_too_large));
    CHECK(result.deliveries.empty());
    CHECK(pairer.closed());
  }
  {
    ConnectionPairer::Config config;
    config.max_header_bytes = 32;
    ConnectionPairer pairer(config);
    CHECK(pairer.enqueue(request(1)).ok());
    Result result = pairer.feed(
        "HTTP/1.1 200 OK\r\nX-Fill: 012345678901234567890123456789\r\n\r\n");
    CHECK(!result.ok());
    CHECK_EQ(static_cast<int>(result.failure.code),
             static_cast<int>(ErrorCode::header_too_large));
    CHECK(result.deliveries.empty());
    CHECK(pairer.closed());
  }
  {
    ConnectionPairer::Config config;
    config.max_interim_responses = 1;
    ConnectionPairer pairer(config);
    CHECK(pairer.enqueue(request(1)).ok());
    Result result = pairer.feed(
        "HTTP/1.1 100 Continue\r\n\r\n"
        "HTTP/1.1 103 Early Hints\r\n\r\n");
    CHECK(!result.ok());
    CHECK_EQ(static_cast<int>(result.failure.code),
             static_cast<int>(ErrorCode::unexpected_response));
    CHECK(result.deliveries.empty());
    CHECK(pairer.closed());
  }
}

void test_malformed_chunk_is_rejected() {
  ConnectionPairer pairer;
  CHECK(pairer.enqueue(request(1)).ok());
  Result result = pairer.feed(
      "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n"
      "zz\r\n");
  CHECK(!result.ok());
  CHECK_EQ(static_cast<int>(result.failure.code),
           static_cast<int>(ErrorCode::malformed_response));
  CHECK(result.deliveries.empty());
  CHECK(pairer.closed());
}

void test_binding_header_allows_head_pipeline_with_length() {
  ConnectionPairer::Config config;
  config.response_id_header = "X-Request-Id";
  ConnectionPairer pairer(config);

  Request head_request = request(1, "HEAD");
  head_request.response_token = "head";
  Request get_request = request(2);
  get_request.response_token = "get";
  CHECK(pairer.enqueue(head_request).ok());
  CHECK(pairer.enqueue(get_request).ok());

  Result result = pairer.feed(
      "HTTP/1.1 200 OK\r\nContent-Length: 5\r\nX-Request-Id: head\r\n\r\n"
      "HTTP/1.1 200 OK\r\nContent-Length: 5\r\nX-Request-Id: get\r\n\r\n"
      "hello");
  CHECK(result.ok());
  CHECK_EQ(result.deliveries.size(), std::size_t{2});
  if (result.deliveries.size() == 2) {
    CHECK_EQ(result.deliveries[0].request.id, std::uint64_t{1});
    CHECK(result.deliveries[0].response.body.empty());
    CHECK_EQ(result.deliveries[1].request.id, std::uint64_t{2});
    CHECK_EQ(result.deliveries[1].response.body, std::string("hello"));
  }
}

void test_structured_complete_object_without_declared_length() {
  ConnectionPairer pairer;
  CHECK(pairer.enqueue(request(1)).ok());
  Response value;
  value.status = 200;
  value.body = "complete-object";
  Result result = pairer.on_response(std::move(value));
  CHECK(result.ok());
  CHECK_EQ(result.deliveries.size(), std::size_t{1});
  if (!result.deliveries.empty()) {
    CHECK_EQ(result.deliveries.front().response.body,
             std::string("complete-object"));
    CHECK_EQ(*result.deliveries.front().response.content_length,
             std::uint64_t{15});
  }
  CHECK(pairer.prepare_reuse().ok());
}

void test_raw_out_of_order_detected_by_status_contract() {
  ConnectionPairer pairer;
  Request first = request(1);
  first.expected_status = 201;
  Request second = request(2);
  second.expected_status = 200;
  CHECK(pairer.enqueue(first).ok());
  CHECK(pairer.enqueue(second).ok());

  // The 200 belongs to request 2 but arrived while request 1 is at the head.
  Result result = pairer.feed(wire_response(""));
  CHECK(!result.ok());
  CHECK_EQ(static_cast<int>(result.failure.code),
           static_cast<int>(ErrorCode::status_mismatch));
  CHECK(result.deliveries.empty());
  CHECK(pairer.closed());
}

}  // namespace

int main() {
  test_legal_pipelining_in_order();
  test_interim_and_chunked_response();
  test_out_of_order_structured_response();
  test_out_of_order_raw_head_body_response_is_not_delivered();
  test_early_close_after_one_response();
  test_early_close_in_partial_body();
  test_close_delimited_single_request_completes_at_eof();
  test_length_mismatch_structured();
  test_conflicting_framing_is_rejected();
  test_conflicting_duplicate_content_length_is_rejected();
  test_head_and_bodyless_status_rules();
  test_pipeline_depth_is_bounded();
  test_timeouts_are_bounded();
  test_backend_error_is_bounded();
  test_status_contract_mismatch();
  test_connection_reuse_clears_all_state();
  test_reuse_with_pending_state_fails_closed();
  test_unsolicited_bytes_fail_closed();
  test_request_binding_header_detects_exchange();
  test_valid_head_with_representation_length();
  test_every_byte_split_preserves_pairing();
  test_connection_close_with_pending_requests_is_fatal();
  test_unframed_keepalive_response_is_rejected();
  test_http10_close_delimited_response();
  test_limits_fail_closed();
  test_malformed_chunk_is_rejected();
  test_binding_header_allows_head_pipeline_with_length();
  test_structured_complete_object_without_declared_length();
  test_raw_out_of_order_detected_by_status_contract();

  if (failures != 0) {
    std::cerr << failures << " test check(s) failed\n";
    return 1;
  }
  std::cout << "all tests passed\n";
  return 0;
}
