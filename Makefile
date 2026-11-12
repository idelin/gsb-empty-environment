CXX ?= clang++
CXXFLAGS ?= -std=c++17 -Wall -Wextra -Wpedantic -Werror

BUILD_DIR := build
TEST_BIN_O0 := $(BUILD_DIR)/test_secure_buffer_o0
TEST_BIN_O3 := $(BUILD_DIR)/test_secure_buffer_o3
ASM_O3 := $(BUILD_DIR)/zero_check_o3.s
ASM_NEG_O3 := $(BUILD_DIR)/zero_check_negative_o3.s

.PHONY: all test asm-check clean

all: $(TEST_BIN_O0) $(TEST_BIN_O3)

$(BUILD_DIR):
	mkdir -p $(BUILD_DIR)

$(TEST_BIN_O0): test_secure_buffer.cpp netsec.hpp observable_allocator.hpp | $(BUILD_DIR)
	$(CXX) $(CXXFLAGS) -O0 -g -o $@ test_secure_buffer.cpp

$(TEST_BIN_O3): test_secure_buffer.cpp netsec.hpp observable_allocator.hpp | $(BUILD_DIR)
	$(CXX) $(CXXFLAGS) -O3 -DNDEBUG -o $@ test_secure_buffer.cpp

# Assembly-level evidence: under -O3 the clear must still emit the platform
# secure-zero call (memset_s here), proving it cannot be optimized away.
$(ASM_O3): zero_check.cpp netsec.hpp | $(BUILD_DIR)
	$(CXX) $(CXXFLAGS) -O3 -DNDEBUG -S -o $@ zero_check.cpp

$(ASM_NEG_O3): zero_check_negative.cpp | $(BUILD_DIR)
	$(CXX) $(CXXFLAGS) -O3 -DNDEBUG -S -o $@ zero_check_negative.cpp

asm-check: $(ASM_O3) $(ASM_NEG_O3)
	@grep -E 'memset_s|SecureZeroMemory|explicit_bzero' $(ASM_O3) > /dev/null \
	  && echo "[ PASS ] secure-zero primitive retained under -O3:" \
	  && grep -E 'memset_s|SecureZeroMemory|explicit_bzero' $(ASM_O3) | head -n 2 \
	  || (echo "[ FAIL ] no guaranteed-clear primitive found in $(ASM_O3)"; exit 1)
	@if grep -E 'bl[[:space:]]+_memset|call[[:space:]]+memset' $(ASM_NEG_O3) > /dev/null; then \
	  echo "[ FAIL ] plain memset unexpectedly retained in $(ASM_NEG_O3)"; exit 1; \
	else \
	  echo "[ PASS ] plain memset dead store eliminated under -O3 (negative control)"; \
	fi

test: all asm-check
	$(TEST_BIN_O0)
	$(TEST_BIN_O3)

clean:
	rm -rf $(BUILD_DIR)
