cmake_minimum_required(VERSION 3.16)
project(netsec LANGUAGES CXX)
set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)
if(NOT CMAKE_BUILD_TYPE AND NOT CMAKE_CONFIGURATION_TYPES)
    set(CMAKE_BUILD_TYPE Release CACHE STRING "" FORCE)
endif()

add_library(netsec OBJECT
    netsec.cpp
    smm_buffer_guard.cpp
)
target_include_directories(netsec PUBLIC "${CMAKE_CURRENT_SOURCE_DIR}")
target_compile_options(netsec PRIVATE -Wall -Wextra -Wpedantic)

enable_testing()

add_executable(test_smm_buffer_guard
    tests/test_smm_buffer_guard.cpp
    $<TARGET_OBJECTS:netsec>
)
target_include_directories(test_smm_buffer_guard PRIVATE "${CMAKE_CURRENT_SOURCE_DIR}")
target_compile_options(test_smm_buffer_guard PRIVATE -Wall -Wextra -Wpedantic)

option(NETSEC_ENABLE_SANITIZERS "Build tests with ASan/UBSan" ON)
if(NETSEC_ENABLE_SANITIZERS)
    target_compile_options(test_smm_buffer_guard
        PRIVATE -fsanitize=address,undefined -fno-omit-frame-pointer)
    target_link_options(test_smm_buffer_guard
        PRIVATE -fsanitize=address,undefined)
endif()

add_test(NAME smm_buffer_guard COMMAND test_smm_buffer_guard)
