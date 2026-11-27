cmake_minimum_required(VERSION 3.16)

# Some minimal macOS command-line-tool installations expose a broken
# /usr/bin/ar shim when xcode-select has no selected developer directory.
# Prefer the real toolchain archive tools when they are available.
if(APPLE)
    if(NOT CMAKE_AR AND EXISTS "/Library/Developer/CommandLineTools/usr/bin/ar")
        set(CMAKE_AR "/Library/Developer/CommandLineTools/usr/bin/ar"
            CACHE FILEPATH "Archiver" FORCE)
    endif()
    if(NOT CMAKE_RANLIB AND EXISTS "/Library/Developer/CommandLineTools/usr/bin/ranlib")
        set(CMAKE_RANLIB "/Library/Developer/CommandLineTools/usr/bin/ranlib"
            CACHE FILEPATH "Ranlib" FORCE)
    endif()
endif()

project(w28_fedc326f58 LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

add_library(eh_frame_validator STATIC
    src/validator.cpp
)
add_library(ehframe::validator ALIAS eh_frame_validator)

target_include_directories(eh_frame_validator
    PUBLIC
        $<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}/include>
        $<INSTALL_INTERFACE:include>
)
target_compile_features(eh_frame_validator PUBLIC cxx_std_17)

function(ehframe_enable_strict_warnings target)
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

ehframe_enable_strict_warnings(eh_frame_validator)

include(CTest)
if(BUILD_TESTING)
    add_executable(ehframe_validator_tests
        tests/test_validator.cpp
    )
    target_link_libraries(ehframe_validator_tests PRIVATE ehframe::validator)
    ehframe_enable_strict_warnings(ehframe_validator_tests)
    add_test(NAME ehframe_validator_tests COMMAND ehframe_validator_tests)
endif()
