cmake_minimum_required(VERSION 3.16)
project(w28_6ee02432eb CXX)
set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)

add_library(mediasafe STATIC
    src/crc32.cpp
    src/png_audit.cpp
    src/tiff_audit.cpp
    src/dispatch.cpp
)
target_include_directories(mediasafe PUBLIC
    ${CMAKE_CURRENT_SOURCE_DIR}/include
)

if(MSVC)
    target_compile_options(mediasafe PRIVATE /W4 /WX)
else()
    target_compile_options(mediasafe PRIVATE
        -Wall -Wextra -Wconversion -Werror -pedantic
    )
endif()

enable_testing()
add_executable(mediasafe_tests tests/test_audit.cpp tests/test_tiff.cpp)
target_link_libraries(mediasafe_tests PRIVATE mediasafe)
if(NOT MSVC)
    target_compile_options(mediasafe_tests PRIVATE
        -Wall -Wextra -Wconversion -Werror -pedantic
    )
else()
    target_compile_options(mediasafe_tests PRIVATE /W4 /WX)
endif()
add_test(NAME mediasafe_tests COMMAND mediasafe_tests)
