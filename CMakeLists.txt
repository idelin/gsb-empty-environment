cmake_minimum_required(VERSION 3.16)
project(netsec LANGUAGES CXX)
set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)
add_library(netsec netsec.cpp)
target_include_directories(netsec PUBLIC "${CMAKE_CURRENT_SOURCE_DIR}")
target_sources(netsec PRIVATE ghcb_response_validator.cpp)

enable_testing()
add_executable(ghcb_response_validator_test ghcb_response_validator_test.cpp)
target_link_libraries(ghcb_response_validator_test PRIVATE netsec)
add_test(NAME ghcb_response_validator_test
         COMMAND ghcb_response_validator_test)
