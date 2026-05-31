import { describe, expect, it } from 'vitest'

import {
  invalidEnvKeys,
  isSensitiveKey,
  isValidEnvKey,
  parseEnv,
  parseEnvEntries,
} from '@/lib/env-parse'

describe('parseEnv', () => {
  it('parses key/value pairs, ignoring comments and blanks', () => {
    const text = `# comment\n\nNODE_ENV=production\nexport PORT=8080\n`
    expect(parseEnv(text)).toEqual({ NODE_ENV: 'production', PORT: '8080' })
  })

  it('strips surrounding quotes', () => {
    expect(parseEnv('A="hello world"\nB=\'x\'')).toEqual({
      A: 'hello world',
      B: 'x',
    })
  })

  it('keeps = inside values', () => {
    expect(parseEnv('URL=postgres://u:p@h/db?x=1')).toEqual({
      URL: 'postgres://u:p@h/db?x=1',
    })
  })
})

describe('invalidEnvKeys', () => {
  it('flags keys that break POSIX rules', () => {
    expect(invalidEnvKeys({ VALID_KEY: '1', '1BAD': 'x', 'has-dash': 'y' })).toEqual([
      '1BAD',
      'has-dash',
    ])
  })
})

describe('parseEnvEntries', () => {
  it('preserves order and keeps first-seen position', () => {
    expect(parseEnvEntries('B=2\nA=1\nC=3')).toEqual([
      { key: 'B', value: '2' },
      { key: 'A', value: '1' },
      { key: 'C', value: '3' },
    ])
  })

  it('lets later duplicates overwrite (last wins) at the first position', () => {
    expect(parseEnvEntries('A=1\nB=2\nA=3')).toEqual([
      { key: 'A', value: '3' },
      { key: 'B', value: '2' },
    ])
  })
})

describe('isValidEnvKey', () => {
  it('accepts POSIX names and rejects others', () => {
    expect(isValidEnvKey('NODE_ENV')).toBe(true)
    expect(isValidEnvKey('_X9')).toBe(true)
    expect(isValidEnvKey('1BAD')).toBe(false)
    expect(isValidEnvKey('has-dash')).toBe(false)
    expect(isValidEnvKey('')).toBe(false)
  })
})

describe('isSensitiveKey', () => {
  it('flags credential-like keys', () => {
    for (const k of [
      'DATABASE_PASSWORD',
      'API_TOKEN',
      'JWT_SECRET',
      'STRIPE_KEY',
      'GH_PRIVATE_KEY',
      'DATABASE_DSN',
      'aws_credential',
      'AUTH_HEADER',
    ]) {
      expect(isSensitiveKey(k), k).toBe(true)
    }
  })

  it('leaves ordinary config keys plaintext', () => {
    for (const k of ['NODE_ENV', 'PORT', 'LOG_LEVEL', 'HOSTNAME', 'TZ']) {
      expect(isSensitiveKey(k), k).toBe(false)
    }
  })
})
