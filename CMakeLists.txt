cmake_minimum_required(VERSION 3.16)
project(netsec LANGUAGES CXX)
set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

# 可导入的静态库目标：其它工程 add_subdirectory() 后链接 netsec 即可。
add_library(netsec STATIC
    freshness_validator.cpp
)
target_include_directories(netsec PUBLIC
    "$<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}>"
)
target_compile_features(netsec PUBLIC cxx_std_17)

if(CMAKE_CXX_COMPILER_ID MATCHES "GNU|Clang|AppleClang")
    target_compile_options(netsec PRIVATE -Wall -Wextra -Wpedantic)
endif()

enable_testing()

add_executable(netsec_freshness_test freshness_validator_test.cpp)
target_link_libraries(netsec_freshness_test PRIVATE netsec)
if(CMAKE_CXX_COMPILER_ID MATCHES "GNU|Clang|AppleClang")
    target_compile_options(netsec_freshness_test PRIVATE -Wall -Wextra
                                                         -Wpedantic)
endif()
add_test(NAME freshness_validator COMMAND netsec_freshness_test)
