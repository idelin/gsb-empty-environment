cmake_minimum_required(VERSION 3.16)

project(elf64_audit VERSION 1.0.0 LANGUAGES CXX)

# Homebrew/CLT-only setups can select the xcrun shim /usr/bin/ar, which fails
# when no active developer directory is configured. Prefer the CLT tools.
if(APPLE)
    if(CMAKE_AR STREQUAL "/usr/bin/ar" AND
       EXISTS "/Library/Developer/CommandLineTools/usr/bin/ar")
        set(CMAKE_AR "/Library/Developer/CommandLineTools/usr/bin/ar"
            CACHE FILEPATH "Archiver for ELF64 audit builds" FORCE)
    endif()
    if(CMAKE_RANLIB STREQUAL "/usr/bin/ranlib" AND
       EXISTS "/Library/Developer/CommandLineTools/usr/bin/ranlib")
        set(CMAKE_RANLIB "/Library/Developer/CommandLineTools/usr/bin/ranlib"
            CACHE FILEPATH "Ranlib for ELF64 audit builds" FORCE)
    endif()
endif()

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

function(elf64_audit_enable_warnings target)
    if(MSVC)
        target_compile_options(${target} PRIVATE /W4 /WX)
    else()
        target_compile_options(${target} PRIVATE
            -Wall
            -Wextra
            -Wconversion
            -Werror
        )
    endif()
endfunction()

add_library(elf64_audit
    src/elf64_audit.cpp
)
add_library(elf64_audit::elf64_audit ALIAS elf64_audit)

target_include_directories(elf64_audit
    PUBLIC
        $<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}/include>
        $<INSTALL_INTERFACE:include>
)

target_compile_features(elf64_audit PUBLIC cxx_std_17)
elf64_audit_enable_warnings(elf64_audit)

add_executable(elf64-audit
    tools/elf64_audit.cpp
)
target_link_libraries(elf64-audit PRIVATE elf64_audit::elf64_audit)
elf64_audit_enable_warnings(elf64-audit)

include(CTest)
if(BUILD_TESTING)
    add_executable(elf64_audit_tests
        tests/test_elf64_audit.cpp
    )
    target_link_libraries(elf64_audit_tests PRIVATE elf64_audit::elf64_audit)
    elf64_audit_enable_warnings(elf64_audit_tests)
    add_test(NAME elf64_audit_tests COMMAND elf64_audit_tests)
endif()
