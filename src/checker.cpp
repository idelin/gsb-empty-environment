#include "checker.h"

#include <sstream>
#include <vector>
#include <string>

namespace atlascheck {

struct Tex { int w, h; };
struct Place { int x, y; bool rot; };

std::string verify(const std::string& spec) {
    std::istringstream in(spec);
    long long S;
    int n;
    long long pad;
    std::string modeTok;
    if (!(in >> S >> n >> pad >> modeTok)) return "parse error: header";

    bool shared;
    if (modeTok == "shared" || modeTok == "0") shared = true;
    else if (modeTok == "exclusive" || modeTok == "1") shared = false;
    else return "parse error: mode must be shared/0 or exclusive/1";

    if (S < 0 || pad < 0 || n < 0) return "invalid: negative S/pad/n";

    // Required empty gap between any two distinct textures.
    const long long gap = shared ? pad : 2 * pad;

    std::vector<Tex> tex(n);
    for (int i = 0; i < n; ++i)
        if (!(in >> tex[i].w >> tex[i].h)) return "parse error: texture sizes";

    std::vector<Place> pl(n);
    for (int i = 0; i < n; ++i) {
        int r;
        if (!(in >> pl[i].x >> pl[i].y >> r)) return "parse error: placement";
        pl[i].rot = (r != 0);
    }

    std::vector<long long> x0(n), y0(n), x1(n), y1(n);
    std::ostringstream errs;
    int nerr = 0;

    for (int i = 0; i < n; ++i) {
        long long w = pl[i].rot ? tex[i].h : tex[i].w;
        long long h = pl[i].rot ? tex[i].w : tex[i].h;
        x0[i] = pl[i].x; y0[i] = pl[i].y;
        x1[i] = pl[i].x + w; y1[i] = pl[i].y + h;
        // Padding must also fit inside the atlas, including at the border.
        if (x0[i] < pad || y0[i] < pad || x1[i] > S - pad || y1[i] > S - pad) {
            errs << "texture " << i << " violates border padding: [" << x0[i]
                 << "," << y0[i] << "]-[" << x1[i] << "," << y1[i]
                 << "] with pad=" << pad << " in S=" << S << "\n";
            ++nerr;
        }
    }

    for (int i = 0; i < n; ++i)
        for (int j = i + 1; j < n; ++j) {
            bool separated =
                x1[i] + gap <= x0[j] || x1[j] + gap <= x0[i] ||
                y1[i] + gap <= y0[j] || y1[j] + gap <= y0[i];
            if (!separated) {
                errs << "textures " << i << " and " << j
                     << " violate separation gap=" << gap << "\n";
                ++nerr;
            }
        }

    if (nerr == 0) return "";
    std::ostringstream head;
    head << nerr << " violation(s):\n" << errs.str();
    return head.str();
}

} // namespace atlascheck
