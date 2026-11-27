cmake_minimum_required(VERSION 3.16)
project(pacbti_verifier VERSION 1.0.0 LANGUAGES CXX)

# A CommandLineTools-only macOS host can have /usr/bin/{ar,ranlib,ld}
# wrappers that fail through xcode-select. Prefer the tools installed next to
# the selected compiler when they are available.
if(APPLE AND CMAKE_CXX_COMPILER)
    get_filename_component(PACBTI_CXX_REAL "${CMAKE_CXX_COMPILER}" REALPATH)
    get_filename_component(PACBTI_TOOLCHAIN_BIN "${PACBTI_CXX_REAL}" DIRECTORY)
    if(EXISTS "${PACBTI_TOOLCHAIN_BIN}/ar")
        set(CMAKE_AR "${PACBTI_TOOLCHAIN_BIN}/ar"
            CACHE FILEPATH "Archiver matching the C++ compiler" FORCE)
    endif()
    if(EXISTS "${PACBTI_TOOLCHAIN_BIN}/ranlib")
        set(CMAKE_RANLIB "${PACBTI_TOOLCHAIN_BIN}/ranlib"
            CACHE FILEPATH "Ranlib matching the C++ compiler" FORCE)
    endif()
    if(EXISTS "${PACBTI_TOOLCHAIN_BIN}/ld")
        set(CMAKE_LINKER "${PACBTI_TOOLCHAIN_BIN}/ld"
            CACHE FILEPATH "Linker matching the C++ compiler" FORCE)
    endif()
endif()

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

add_library(pacbti
    src/verifier.cpp
)
add_library(pacbti::pacbti ALIAS pacbti)

target_include_directories(pacbti
    PUBLIC
        $<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}/include>
        $<INSTALL_INTERFACE:include>
)

if(CMAKE_CXX_COMPILER_ID MATCHES "Clang|GNU")
    target_compile_options(pacbti PRIVATE
        -Wall
        -Wextra
        -Wconversion
        -Werror
    )
endif()

include(CTest)
if(BUILD_TESTING)
    add_executable(pacbti_tests
        tests/test_verifier.cpp
    )
    target_link_libraries(pacbti_tests PRIVATE pacbti::pacbti)
    set_target_properties(pacbti_tests PROPERTIES
        CXX_STANDARD 17
        CXX_STANDARD_REQUIRED ON
        CXX_EXTENSIONS OFF
    )
    if(CMAKE_CXX_COMPILER_ID MATCHES "Clang|GNU")
        target_compile_options(pacbti_tests PRIVATE
            -Wall
            -Wextra
            -Wconversion
            -Werror
        )
    endif()
    add_test(NAME pacbti_tests COMMAND pacbti_tests)
endif()
