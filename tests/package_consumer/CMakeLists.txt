cmake_minimum_required(VERSION 3.16)
project(netsec_consumer LANGUAGES CXX)

find_package(netsec CONFIG REQUIRED)

add_executable(netsec_consumer main.cpp)
target_link_libraries(netsec_consumer PRIVATE netsec::netsec)
