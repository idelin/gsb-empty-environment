cmake_minimum_required(VERSION 3.16)
project(netsec LANGUAGES CXX)
set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

# Some macOS runners have Command Line Tools installed without a working
# xcode-select shim, so /usr/bin/ar, /usr/bin/ranlib and /usr/bin/ld fail even
# though the tools themselves are present. Prefer the direct paths only when
# CMake selected its standard macOS defaults.
if(APPLE)
    set(_netsec_clt_bin "/Library/Developer/CommandLineTools/usr/bin")
    if(EXISTS "${_netsec_clt_bin}/ar" AND EXISTS "${_netsec_clt_bin}/ranlib")
        if(CMAKE_AR STREQUAL "/usr/bin/ar")
            set(CMAKE_AR "${_netsec_clt_bin}/ar" CACHE FILEPATH
                "Command-line tools archiver" FORCE)
        endif()
        if(CMAKE_RANLIB STREQUAL "/usr/bin/ranlib")
            set(CMAKE_RANLIB "${_netsec_clt_bin}/ranlib" CACHE FILEPATH
                "Command-line tools ranlib" FORCE)
        endif()
        if(CMAKE_LINKER STREQUAL "/usr/bin/ld" AND
           EXISTS "${_netsec_clt_bin}/ld")
            set(CMAKE_LINKER "${_netsec_clt_bin}/ld" CACHE FILEPATH
                "Command-line tools linker" FORCE)
        endif()
    endif()
endif()

add_library(netsec netsec.cpp)
target_compile_features(netsec PUBLIC cxx_std_20)
target_include_directories(netsec PUBLIC "${CMAKE_CURRENT_SOURCE_DIR}")

if(CMAKE_CXX_COMPILER_ID MATCHES "Clang|GNU")
    target_compile_options(netsec PRIVATE -Wall -Wextra -Wpedantic)
endif()

enable_testing()

add_executable(environment_loader_test
    tests/environment_loader_test.cpp
)
target_link_libraries(environment_loader_test PRIVATE netsec)
target_compile_features(environment_loader_test PRIVATE cxx_std_20)

if(CMAKE_CXX_COMPILER_ID MATCHES "Clang|GNU")
    target_compile_options(environment_loader_test PRIVATE
        -Wall -Wextra -Wpedantic
    )
endif()

add_test(NAME environment_loader COMMAND environment_loader_test)
