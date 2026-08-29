/** @type {import('next').NextConfig} */
const nextConfig = {
  ...(process.env.NEXT_BUILD_STANDALONE === 'false' ? {} : { output: 'standalone' }),
}

export default nextConfig
