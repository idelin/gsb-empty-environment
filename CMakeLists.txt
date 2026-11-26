cmake_minimum_required(VERSION 3.16)

if(APPLE AND EXISTS "/Library/Developer/CommandLineTools/usr/bin/ar")
    set(CMAKE_AR "/Library/Developer/CommandLineTools/usr/bin/ar" CACHE FILEPATH "" FORCE)
    set(CMAKE_RANLIB "/Library/Developer/CommandLineTools/usr/bin/ranlib" CACHE FILEPATH "" FORCE)
endif()

project(w27_5f5c6982d0 CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

add_library(pki_verifier
    src/der.cpp
    src/x509.cpp
    src/names.cpp
    src/validator.cpp
)
target_include_directories(pki_verifier PUBLIC include)
target_compile_options(pki_verifier PRIVATE
    -Wall -Wextra -Wconversion -Wsign-conversion -Wpedantic -Werror
)

option(PKI_BUILD_TESTS "Build PKI verifier tests" ON)
if(PKI_BUILD_TESTS)
    enable_testing()
    add_executable(pki_tests tests/test_pki.cpp)
    target_link_libraries(pki_tests PRIVATE pki_verifier)
    target_compile_options(pki_tests PRIVATE
        -Wall -Wextra -Wconversion -Wsign-conversion -Wpedantic -Werror
    )
    add_test(NAME pki_tests COMMAND pki_tests)
endif()
