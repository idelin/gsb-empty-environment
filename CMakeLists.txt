cmake_minimum_required(VERSION 3.16)
project(netsec LANGUAGES CXX)
set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)
add_library(netsec netsec.cpp)
target_include_directories(netsec PUBLIC "${CMAKE_CURRENT_SOURCE_DIR}")
enable_testing()

option(NETSEC_SANITIZE "Build tests with AddressSanitizer + UBSan" ON)

add_executable(test_shared_buffer tests/test_shared_buffer.cpp)
target_link_libraries(test_shared_buffer PRIVATE netsec)
target_compile_features(test_shared_buffer PRIVATE cxx_std_17)
if(NETSEC_SANITIZE AND
   (CMAKE_CXX_COMPILER_ID MATCHES "Clang" OR CMAKE_CXX_COMPILER_ID STREQUAL "GNU"))
    target_compile_options(test_shared_buffer PRIVATE
        -fsanitize=address,undefined -fno-omit-frame-pointer)
    target_link_options(test_shared_buffer PRIVATE
        -fsanitize=address,undefined)
endif()

add_test(NAME shared_buffer
         COMMAND test_shared_buffer)
