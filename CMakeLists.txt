cmake_minimum_required(VERSION 3.16)
project(w27_5a2173ac9e CXX)
set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

# OBJECT library keeps the build self-contained: no archiver is required, which
# matters in minimal toolchains. Hosts can also compile src/cet.cpp directly.
add_library(cetverify OBJECT
    src/cet.cpp
)
target_include_directories(cetverify PUBLIC
    ${CMAKE_CURRENT_SOURCE_DIR}/include
)
target_compile_options(cetverify PRIVATE
    -Wall
    -Wextra
    -Wconversion
    -Wpedantic
    -Werror
)

add_executable(cet_tests
    tests/cet_tests.cpp
    $<TARGET_OBJECTS:cetverify>
)
target_include_directories(cet_tests PRIVATE
    ${CMAKE_CURRENT_SOURCE_DIR}/include
)
target_compile_options(cet_tests PRIVATE
    -Wall
    -Wextra
    -Wconversion
    -Wpedantic
    -Werror
)

enable_testing()
add_test(NAME cet_tests COMMAND cet_tests)
