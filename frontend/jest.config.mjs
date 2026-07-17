import nextJest from 'next/jest.js'

// next/jest wires up the Next.js SWC transform, CSS module mocking, the "@/*"
// path alias and .env loading for the test environment.
const createJestConfig = nextJest({ dir: './' })

/** @type {import('jest').Config} */
const config = {
  testEnvironment: 'jest-environment-jsdom',
  setupFilesAfterEnv: ['<rootDir>/jest.setup.ts'],
}

// Exported as a function call so next/jest can load the (async) Next.js config.
export default createJestConfig(config)
