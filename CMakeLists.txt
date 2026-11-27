cmake_minimum_required(VERSION 3.16)
project(w28_ec6bb39acf CXX)
set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)

add_library(machoaudit STATIC
    src/audit.cpp
    src/sha256.cpp
)
target_include_directories(machoaudit PUBLIC
    ${CMAKE_CURRENT_SOURCE_DIR}/include
)
target_compile_options(machoaudit PRIVATE
    -Wall -Wextra -Wconversion -Werror
)

enable_testing()
add_executable(machoaudit_tests
    tests/test_main.cpp
)
target_link_libraries(machoaudit_tests PRIVATE machoaudit)
target_include_directories(machoaudit_tests PRIVATE
    ${CMAKE_CURRENT_SOURCE_DIR}/tests
)
target_compile_options(machoaudit_tests PRIVATE
    -Wall -Wextra -Wconversion -Werror
)
add_test(NAME machoaudit_tests COMMAND machoaudit_tests)
