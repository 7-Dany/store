import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  allowedDevOrigins: [
    // ngrok tunnel — update the subdomain each time you restart ngrok
    "6620-154-183-234-56.ngrok-free.app",
  ],
};

export default nextConfig;
