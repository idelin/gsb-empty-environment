cmake_minimum_required(VERSION 3.16)

# Some sandboxed macOS environments expose /usr/bin/ar as a shim that cannot
# locate the active CommandLineTools installation.  Prefer the real tool when
# the shim is unusable; normal Xcode/CLT setups are left untouched.
if(APPLE AND EXISTS "/usr/bin/ar")
  execute_process(
    COMMAND /usr/bin/ar --version
    RESULT_VARIABLE _netsec_ar_probe
    OUTPUT_QUIET
    ERROR_QUIET)
  if(NOT _netsec_ar_probe EQUAL 0 AND
     EXISTS "/Library/Developer/CommandLineTools/usr/bin/ar")
    set(CMAKE_AR
        "/Library/Developer/CommandLineTools/usr/bin/ar"
        CACHE FILEPATH "Archiver" FORCE)
    set(CMAKE_RANLIB
        "/Library/Developer/CommandLineTools/usr/bin/ranlib"
        CACHE FILEPATH "Ranlib" FORCE)
  endif()
endif()

project(netsec LANGUAGES CXX)
set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)
add_library(netsec netsec.cpp)
target_include_directories(netsec PUBLIC "${CMAKE_CURRENT_SOURCE_DIR}")
target_compile_features(netsec PUBLIC cxx_std_20)

if(MSVC)
  target_compile_options(netsec PRIVATE /W4)
else()
  target_compile_options(netsec PRIVATE -Wall -Wextra -Wpedantic)
endif()

include(CTest)
if(BUILD_TESTING)
  add_subdirectory(tests)
endif()
