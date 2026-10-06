#pragma once

#include <type_traits>
#include <utility>
#include <variant>

namespace lfr {

template <typename E>
struct Unexpected {
  E error;
};

template <typename E>
Unexpected<E> unexpected(E error) {
  return Unexpected<E>{std::move(error)};
}

// Minimal expected-like type (C++17) so the library does not depend on C++23.
template <typename T, typename E>
class Expected {
public:
  Expected(const T& value) : data_(value) {}
  Expected(T&& value) : data_(std::move(value)) {}

  // Allows implicit conversion from types convertible to T, e.g.
  // shared_ptr<Window> -> shared_ptr<const Window>.
  template <typename U,
            typename = std::enable_if_t<
                std::is_constructible<T, U&&>::value &&
                !std::is_same<typename std::decay<U>::type, Unexpected<E>>::value>>
  Expected(U&& value) : data_(T(std::forward<U>(value))) {}

  Expected(Unexpected<E> error) : data_(std::move(error.error)) {}

  bool has_value() const { return std::holds_alternative<T>(data_); }
  explicit operator bool() const { return has_value(); }

  T& value() & { return std::get<T>(data_); }
  const T& value() const& { return std::get<T>(data_); }
  T&& value() && { return std::get<T>(std::move(data_)); }

  const E& error() const { return std::get<E>(data_); }

  T& operator*() & { return value(); }
  const T& operator*() const& { return value(); }
  T* operator->() { return &value(); }
  const T* operator->() const { return &value(); }

private:
  std::variant<T, E> data_;
};

}  // namespace lfr
