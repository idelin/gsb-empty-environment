cmake_minimum_required(VERSION 3.16)
project(cbpf_seccomp_verifier LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

# Some command-line-tools-only macOS hosts expose a wrapper at /usr/bin/ar
# that requires xcode-select even though the real archiver is present.
if(APPLE)
    get_filename_component(_cbpf_cxx_real "${CMAKE_CXX_COMPILER}" REALPATH)
    get_filename_component(_cbpf_cxx_dir "${_cbpf_cxx_real}" DIRECTORY)
    if(EXISTS "${_cbpf_cxx_dir}/ar")
        set(CMAKE_AR "${_cbpf_cxx_dir}/ar" CACHE FILEPATH "C++ archiver" FORCE)
    endif()
    if(EXISTS "${_cbpf_cxx_dir}/ranlib")
        set(CMAKE_RANLIB "${_cbpf_cxx_dir}/ranlib" CACHE FILEPATH "C++ archive indexer" FORCE)
    endif()
endif()

add_library(cbpf_seccomp
    src/verifier.cpp
)
target_include_directories(cbpf_seccomp
    PUBLIC
        ${CMAKE_CURRENT_SOURCE_DIR}/include
)
target_compile_options(cbpf_seccomp PRIVATE
    -Wall
    -Wextra
    -Wconversion
    -Werror
)

enable_testing()

add_executable(cbpf_seccomp_tests
    tests/verifier_tests.cpp
)
target_link_libraries(cbpf_seccomp_tests PRIVATE cbpf_seccomp)
target_compile_options(cbpf_seccomp_tests PRIVATE
    -Wall
    -Wextra
    -Wconversion
    -Werror
)

add_test(NAME cbpf_seccomp_tests COMMAND cbpf_seccomp_tests)
