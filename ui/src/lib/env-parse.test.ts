import { describe, expect, it } from 'vitest'

import { invalidEnvKeys, parseEnv } from '@/lib/env-parse'

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
