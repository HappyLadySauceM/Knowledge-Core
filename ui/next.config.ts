import type { NextConfig } from "next";

const apiOrigin = process.env.KNOWLEDGE_CORE_API_ORIGIN ?? "http://127.0.0.1:8080";

const nextConfig: NextConfig = {
  output: "standalone",
  reactStrictMode: true,
  poweredByHeader: false,
  async rewrites() {
    return [
      {
        source: "/api/v1/assets/:path*",
        destination: `${apiOrigin}/api/v1/assets/:path*`,
      },
    ];
  },
};

export default nextConfig;
