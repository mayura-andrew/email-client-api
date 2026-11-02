// The desktop app embeds the UI as static files (see main.go). In development,
// `bun run dev` proxies /api to `go run ./cmd/devserver`.
const dev = process.env.NODE_ENV !== "production";

/** @type {import('next').NextConfig} */
const nextConfig = {
  reactStrictMode: true,
  ...(dev
    ? { async rewrites() { return [{ source: "/api/:path*", destination: "http://127.0.0.1:4000/api/:path*" }]; } }
    : { output: "export" }),
};

export default nextConfig;
