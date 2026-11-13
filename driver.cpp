// 操作序列驱动器：从 stdin（或参数指定的文件）逐行读取操作并输出每步结果。
//
//   alloc <name>            分配一块并绑定到名字
//   free  <name>            释放该名字对应的块
//   write <name> <addr>     模拟攻击者把该块的 next 改写为指向 addr（十六进制）
//   # 注释 / 空行
//
// 发生安全违规时输出 TERMINATED 及原因并以退出码 1 结束。

#include "netsec.hpp"

#include <fstream>
#include <iostream>
#include <sstream>
#include <string>
#include <unordered_map>

int main(int argc, char** argv) {
    std::ifstream file;
    std::istream* in = &std::cin;
    if (argc > 1) {
        file.open(argv[1]);
        if (!file) {
            std::cerr << "cannot open " << argv[1] << "\n";
            return 2;
        }
        in = &file;
    }

    netsec::TcacheAllocator alloc;
    std::unordered_map<std::string, void*> named;
    std::string line;
    std::size_t step = 0;

    try {
        while (std::getline(*in, line)) {
            const auto hash = line.find('#');
            if (hash != std::string::npos)
                line.erase(hash);
            std::istringstream iss(line);
            std::string op;
            if (!(iss >> op))
                continue;
            ++step;

            if (op == "alloc") {
                std::string name;
                iss >> name;
                void* p = alloc.alloc();
                named[name] = p;
                std::cout << "[step " << step << "] alloc " << name << " -> " << p
                          << " (depth=" << alloc.depth() << ")\n";
            } else if (op == "free") {
                std::string name;
                iss >> name;
                void* p = named.at(name);
                alloc.free(p);
                std::cout << "[step " << step << "] free " << name << " (" << p
                          << ") ok (depth=" << alloc.depth() << ")\n";
            } else if (op == "write") {
                std::string name, addr_s;
                iss >> name >> addr_s;
                void* p = named.at(name);
                const auto target = static_cast<std::uintptr_t>(std::stoull(addr_s, nullptr, 0));
                alloc.write_next(p, reinterpret_cast<void*>(target));
                std::cout << "[step " << step << "] write " << name << ".next -> "
                          << reinterpret_cast<void*>(target) << " (stored encoded)\n";
            } else {
                std::cerr << "[step " << step << "] unknown op: " << op << "\n";
                return 2;
            }
        }
    } catch (const netsec::SecurityViolation& e) {
        std::cout << "[step " << step << "] TERMINATED: " << e.what() << "\n";
        return 1;
    } catch (const std::exception& e) {
        std::cerr << "[step " << step << "] script error: " << e.what() << "\n";
        return 2;
    }

    std::cout << "completed: " << step << " steps, no fault\n";
    return 0;
}
