cmake_minimum_required(VERSION 3.16)
project(netsec LANGUAGES CXX)
set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)
add_library(netsec netsec.cpp)
add_library(netsec::netsec ALIAS netsec)
target_include_directories(netsec PUBLIC "${CMAKE_CURRENT_SOURCE_DIR}")
target_compile_features(netsec PUBLIC cxx_std_20)
set_target_properties(netsec PROPERTIES POSITION_INDEPENDENT_CODE ON)

enable_testing()

add_executable(netsec_tests tests/netsec_tests.cpp)
target_link_libraries(netsec_tests PRIVATE netsec)
target_compile_features(netsec_tests PRIVATE cxx_std_20)

add_test(NAME netsec_tests COMMAND netsec_tests)
