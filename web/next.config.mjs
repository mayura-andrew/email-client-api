// The web and API services run on different hostnames in production, which
// browsers treat as different sites, so the admin session cookie would not be
// sent. With API_PROXY_URL set, Next forwards /api/* to the API and the browser
// only ever talks to one origin.
const apiProxy = process.env.API_PROXY_URL?.replace(/\/+$/, "");

/** @type {import('next').NextConfig} */
const nextConfig = {
  reactStrictMode: true,
  ...(apiProxy && {
    env: { NEXT_PUBLIC_API_URL: "" },
    async rewrites() {
      return [{ source: "/api/:path*", destination: `${apiProxy}/api/:path*` }];
    },
  }),
};

export default nextConfig;
