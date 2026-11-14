cmake_minimum_required(VERSION 3.16)
project(netsec LANGUAGES CXX)
set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

# On macOS the /usr/bin/ar shim can fail when no Xcode is selected. Prefer the
# standalone Command Line Tools archive tools when they are available.
if(APPLE AND EXISTS "/Library/Developer/CommandLineTools/usr/bin/ar")
    set(CMAKE_AR "/Library/Developer/CommandLineTools/usr/bin/ar"
        CACHE FILEPATH "archiver" FORCE)
    set(CMAKE_RANLIB "/Library/Developer/CommandLineTools/usr/bin/ranlib"
        CACHE FILEPATH "ranlib" FORCE)
endif()

add_library(netsec netsec.cpp)
target_include_directories(netsec PUBLIC "${CMAKE_CURRENT_SOURCE_DIR}")
add_library(netsec::netsec ALIAS netsec)
include(CTest)

if(BUILD_TESTING)
    add_executable(netsec_tests tests/usb_descriptor_tests.cpp)
    target_link_libraries(netsec_tests PRIVATE netsec::netsec)
    add_test(NAME usb_descriptor_tests COMMAND netsec_tests)
endif()
