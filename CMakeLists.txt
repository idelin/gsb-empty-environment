cmake_minimum_required(VERSION 3.16)
if(APPLE AND EXISTS "/Library/Developer/CommandLineTools/usr/bin/ar")
    if(NOT CMAKE_AR OR CMAKE_AR STREQUAL "/usr/bin/ar")
        set(CMAKE_AR "/Library/Developer/CommandLineTools/usr/bin/ar"
            CACHE FILEPATH "Path to ar" FORCE)
        set(CMAKE_RANLIB "/Library/Developer/CommandLineTools/usr/bin/ranlib"
            CACHE FILEPATH "Path to ranlib" FORCE)
    endif()
    set(CMAKE_CXX_ARCHIVE_CREATE
        "/Library/Developer/CommandLineTools/usr/bin/ar qc <TARGET> <OBJECTS>")
    set(CMAKE_CXX_ARCHIVE_FINISH
        "/Library/Developer/CommandLineTools/usr/bin/ranlib <TARGET>")
endif()

project(w27_df8b667ecd CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

add_library(machosec
    src/sha256.cpp
    src/entitlements.cpp
    src/verifier.cpp
)
target_include_directories(machosec PUBLIC
    ${CMAKE_CURRENT_SOURCE_DIR}/include
    PRIVATE
    ${CMAKE_CURRENT_SOURCE_DIR}/src
)

add_executable(machosec_verify tools/machosec_verify.cpp)
target_link_libraries(machosec_verify PRIVATE machosec)

add_executable(machosec_samples
    tools/generate_samples.cpp
    tests/sample_builder.cpp
)
target_include_directories(machosec_samples PRIVATE
    ${CMAKE_CURRENT_SOURCE_DIR}/tests
)
target_link_libraries(machosec_samples PRIVATE machosec)

include(CTest)
if(BUILD_TESTING)
    add_executable(machosec_tests
        tests/test_verifier.cpp
        tests/sample_builder.cpp
    )
    target_include_directories(machosec_tests PRIVATE
        ${CMAKE_CURRENT_SOURCE_DIR}/tests
    )
    target_link_libraries(machosec_tests PRIVATE machosec)
    add_test(NAME machosec_tests COMMAND machosec_tests)
endif()

if(CMAKE_CXX_COMPILER_ID MATCHES "GNU|Clang|AppleClang")
    target_compile_options(machosec PRIVATE
        -Wall -Wextra -Wconversion -Werror -Wpedantic
    )
    target_compile_options(machosec_verify PRIVATE
        -Wall -Wextra -Wconversion -Werror -Wpedantic
    )
    target_compile_options(machosec_samples PRIVATE
        -Wall -Wextra -Wconversion -Werror -Wpedantic
    )
    if(TARGET machosec_tests)
        target_compile_options(machosec_tests PRIVATE
            -Wall -Wextra -Wconversion -Werror -Wpedantic
        )
    endif()
endif()
