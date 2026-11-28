cmake_minimum_required(VERSION 3.16)
project(w28_2bfa9f476e CXX)
set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)

add_library(userns_security STATIC
    src/userns_security.cpp
)
target_include_directories(userns_security PUBLIC
    ${CMAKE_CURRENT_SOURCE_DIR}/include
)
target_compile_options(userns_security PRIVATE
    -Wall
    -Wextra
    -Wconversion
    -Wpedantic
    -Wshadow
    -Wsign-conversion
    -Werror
)

enable_testing()
add_executable(userns_security_tests
    tests/test_userns_security.cpp
)
target_link_libraries(userns_security_tests PRIVATE userns_security)
target_compile_options(userns_security_tests PRIVATE
    -Wall
    -Wextra
    -Wconversion
    -Wpedantic
    -Wshadow
    -Wsign-conversion
    -Werror
)
add_test(NAME userns_security_tests COMMAND userns_security_tests)
