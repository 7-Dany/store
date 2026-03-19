import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  // ─── React Compiler ────────────────────────────────────────────────────────
  // Stable in Next.js 16. Automatically memoizes components — no more manual
  // memo(), useMemo(), useCallback(). Increases compile times slightly.
  // Uncomment to enable:
  reactCompiler: true,

  // ─── Package import optimisation ───────────────────────────────────────────
  // Turbopack handles this automatically. Still useful for webpack fallback.
  experimental: {
    optimizePackageImports: ["@tabler/icons-react"],
    turbopackFileSystemCacheForDev: true,
  },

  allowedDevOrigins: [
    // ngrok tunnel — update the subdomain each time you restart ngrok
    "6620-154-183-234-56.ngrok-free.app",
  ],
};

export default nextConfig;
