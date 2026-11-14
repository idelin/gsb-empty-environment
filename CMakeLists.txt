cmake_minimum_required(VERSION 3.16)
project(netsec LANGUAGES CXX)
set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

if(APPLE)
    get_filename_component(_netsec_compiler_real "${CMAKE_CXX_COMPILER}" REALPATH)
    get_filename_component(_netsec_compiler_dir "${_netsec_compiler_real}" DIRECTORY)
    if(EXISTS "${_netsec_compiler_dir}/ar" AND EXISTS "${_netsec_compiler_dir}/ranlib")
        set(CMAKE_AR "${_netsec_compiler_dir}/ar" CACHE FILEPATH
            "Archiver from the selected Apple compiler toolchain" FORCE)
        set(CMAKE_RANLIB "${_netsec_compiler_dir}/ranlib" CACHE FILEPATH
            "Ranlib from the selected Apple compiler toolchain" FORCE)
    endif()
endif()

add_library(netsec netsec.cpp)
add_library(netsec::netsec ALIAS netsec)
target_include_directories(netsec PUBLIC "${CMAKE_CURRENT_SOURCE_DIR}")
target_compile_features(netsec PUBLIC cxx_std_20)

include(CTest)
if(BUILD_TESTING)
    add_executable(netsec_tests tests/netsec_tests.cpp)
    target_link_libraries(netsec_tests PRIVATE netsec::netsec)
    target_compile_features(netsec_tests PRIVATE cxx_std_20)
    add_test(NAME netsec_identity_tests COMMAND netsec_tests)
endif()

enable_testing()
