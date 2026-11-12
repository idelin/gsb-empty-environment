cmake_minimum_required(VERSION 3.16)
project(netsec LANGUAGES CXX)
set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

# Some macOS command-line-tool installations leave /usr/bin/ar unusable
# because xcode-select has no active developer directory.  Prefer the real
# archiver from the installed Command Line Tools when that is the case.
if(APPLE AND EXISTS "/Library/Developer/CommandLineTools/usr/bin/ar")
  if(NOT CMAKE_AR OR CMAKE_AR STREQUAL "/usr/bin/ar")
    set(CMAKE_AR "/Library/Developer/CommandLineTools/usr/bin/ar")
  endif()
  if(NOT CMAKE_RANLIB OR CMAKE_RANLIB STREQUAL "/usr/bin/ranlib")
    set(CMAKE_RANLIB "/Library/Developer/CommandLineTools/usr/bin/ranlib")
  endif()
endif()

add_library(netsec netsec.cpp)
target_include_directories(netsec PUBLIC "${CMAKE_CURRENT_SOURCE_DIR}")
enable_testing()

add_executable(netsec_tests netsec_test.cpp)
target_link_libraries(netsec_tests PRIVATE netsec)
add_test(NAME netsec_page_permissions COMMAND netsec_tests)
