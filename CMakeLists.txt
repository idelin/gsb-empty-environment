cmake_minimum_required(VERSION 3.16)
project(netsec LANGUAGES CXX)

# Some macOS command-line-only environments expose /usr/bin/ar as an xcrun
# shim that fails without a developer-tools selection. Prefer the real
# CommandLineTools archiver when it is available.
if(APPLE)
  set(NETSEC_CLT_AR "/Library/Developer/CommandLineTools/usr/bin/ar")
  set(NETSEC_CLT_RANLIB "/Library/Developer/CommandLineTools/usr/bin/ranlib")
  if(EXISTS "${NETSEC_CLT_AR}" AND EXISTS "${NETSEC_CLT_RANLIB}")
    if(CMAKE_AR STREQUAL "/usr/bin/ar")
      set(CMAKE_AR "${NETSEC_CLT_AR}" CACHE FILEPATH "Archiver" FORCE)
    endif()
    if(CMAKE_RANLIB STREQUAL "/usr/bin/ranlib")
      set(CMAKE_RANLIB "${NETSEC_CLT_RANLIB}" CACHE FILEPATH "Ranlib" FORCE)
    endif()
  endif()
endif()

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)
add_library(netsec netsec.cpp)
target_include_directories(netsec PUBLIC "${CMAKE_CURRENT_SOURCE_DIR}")
enable_testing()

add_executable(netsec_tests tests/netsec_tests.cpp)
target_link_libraries(netsec_tests PRIVATE netsec)
target_compile_options(netsec_tests PRIVATE -Wall -Wextra -Wpedantic)
add_test(NAME netsec_tests COMMAND netsec_tests)
