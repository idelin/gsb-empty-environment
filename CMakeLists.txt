cmake_minimum_required(VERSION 3.16)
project(w27_d64f4c9ae8 CXX)
set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)

# On macOS the /usr/bin/ar and /usr/bin/ranlib shims rely on an active
# xcode-select developer directory. When only the Command Line Tools are
# installed, prefer their real binaries directly so the build stays
# self-contained and does not require xcode-select configuration.
if(APPLE AND EXISTS "/Library/Developer/CommandLineTools/usr/bin/ar")
    set(CMAKE_AR "/Library/Developer/CommandLineTools/usr/bin/ar"
        CACHE FILEPATH "Archiver" FORCE)
    set(CMAKE_RANLIB "/Library/Developer/CommandLineTools/usr/bin/ranlib"
        CACHE FILEPATH "Ranlib" FORCE)
endif()

# Zero external dependencies; the whole project builds with the C++17
# standard library alone. Warnings are fatal, including narrowing and
# sign/implicit conversions.
add_library(pmp_policy STATIC
    src/pmp_policy.cpp
)
target_include_directories(pmp_policy PUBLIC
    ${CMAKE_CURRENT_SOURCE_DIR}/include
)
target_compile_options(pmp_policy PRIVATE
    -Wall
    -Wextra
    -Wconversion
    -Wsign-conversion
    -Werror
    -pedantic-errors
)

enable_testing()
add_executable(pmp_policy_tests
    tests/test_pmp_policy.cpp
)
target_include_directories(pmp_policy_tests PRIVATE
    ${CMAKE_CURRENT_SOURCE_DIR}/include
    ${CMAKE_CURRENT_SOURCE_DIR}/tests
)
target_link_libraries(pmp_policy_tests PRIVATE pmp_policy)
target_compile_options(pmp_policy_tests PRIVATE
    -Wall
    -Wextra
    -Wconversion
    -Wsign-conversion
    -Werror
    -pedantic-errors
)
add_test(NAME pmp_policy_tests COMMAND pmp_policy_tests)
