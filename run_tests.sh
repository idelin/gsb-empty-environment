#!/bin/sh
# 构建并运行简化链接器的测试用例
set -u
cd "$(dirname "$0")"

CXX=${CXX:-c++}
$CXX -std=c++17 -Wall -Wextra -O2 -o linker linker.cpp || exit 1

pass=0; fail=0
check() {  # check <desc> <expected_exit> <grep-pattern> <output>
    desc=$1; want=$2; pat=$3; out=$4
    got=$5
    if [ "$got" -eq "$want" ] && echo "$out" | grep -q "$pat"; then
        echo "PASS: $desc"; pass=$((pass+1))
    else
        echo "FAIL: $desc (exit=$got want=$want, pattern '$pat')"; fail=$((fail+1))
    fi
}

# 1. 按需加载 + 迭代到不动点：util1 引入 log2，util2 第二轮被加载
out=$(./linker examples/main.o examples/extra.o examples/libutil.a 2>&1); rc=$?
check "fixpoint: util1 loaded for helper" 0 "libutil.a(util1)" "$out" $rc
check "fixpoint: util2 loaded in pass 2" 0 "pass 2" "$out" $rc
check "fixpoint: success" 0 "link: success" "$out" $rc

# 2. 归档弱定义不触发加载；已有弱定义满足时强定义成员也不加载
check "archive weak def not extracted" 0 "libutil.a(weaklog)  (no strong definition needed)" "$out" $rc
check "unneeded strong member not extracted" 0 "libutil.a(logstrong)  (no strong definition needed)" "$out" $rc
check "weak def from object wins by default" 0 "log: weak definition from extra" "$out" $rc

# 3. 强定义冲突：两种输入顺序都必须报错
out=$(./linker examples/conflict_a.o examples/conflict_b.o 2>&1); rc=$?
check "strong conflict a-before-b" 1 "multiple strong definitions of 'dup'" "$out" $rc
out=$(./linker examples/conflict_b.o examples/conflict_a.o 2>&1); rc=$?
check "strong conflict b-before-a" 1 "multiple strong definitions of 'dup'" "$out" $rc
echo "$out" | grep -q "conflict_a" && echo "$out" | grep -q "conflict_b" \
    && { echo "PASS: conflict lists both modules"; pass=$((pass+1)); } \
    || { echo "FAIL: conflict lists both modules"; fail=$((fail+1)); }

# 4. 强定义覆盖弱定义（同一符号：弱在目标文件，强在目标文件）
cat > /tmp/strong.o << 'EOT'
module strongmod
def strong log
EOT
out=$(./linker examples/extra.o /tmp/strong.o 2>&1); rc=$?
check "strong overrides weak" 0 "log: strong definition from strongmod" "$out" $rc

# 5. 未定义引用无法解析 => 错误，并列出引用者
out=$(./linker examples/dangling.o 2>&1); rc=$?
check "undefined reference error" 1 "undefined reference to 'missing'" "$out" $rc
check "undefined lists referrer" 1 "referenced by dangling" "$out" $rc

echo "----"
echo "passed=$pass failed=$fail"
[ "$fail" -eq 0 ]
