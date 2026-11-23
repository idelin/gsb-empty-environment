module;

export module gsb.reassembly;

// Re-export the header unit so `import gsb.reassembly;` exposes the complete
// public API without forcing consumers to include the header themselves.
export import "gsb/reassembly.hpp";
