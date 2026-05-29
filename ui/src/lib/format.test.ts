import { describe, expect, it } from 'vitest'

import { durationBetween, formatBytes, formatDuration, formatPercent, shortSha } from '@/lib/format'

describe('formatBytes', () => {
  it('formats megabytes and gigabytes', () => {
    expect(formatBytes(0)).toBe('0 MB')
    expect(formatBytes(1024 * 1024)).toBe('1 MB')
    expect(formatBytes(1.5 * 1024 * 1024 * 1024)).toBe('1.5 GB')
  })
})

describe('formatPercent', () => {
  it('formats with fixed digits', () => {
    expect(formatPercent(12.345)).toBe('12.3%')
    expect(formatPercent(50, 0)).toBe('50%')
  })
})

describe('formatDuration', () => {
  it('formats seconds into compact units', () => {
    expect(formatDuration(0)).toBe('—')
    expect(formatDuration(42)).toBe('42s')
    expect(formatDuration(90)).toBe('1m 30s')
    expect(formatDuration(3 * 86400 + 4 * 3600)).toBe('3d 4h')
  })
})

describe('durationBetween', () => {
  it('returns dash for missing endpoints', () => {
    expect(durationBetween(null, null)).toBe('—')
    expect(durationBetween('2026-01-01T00:00:00Z', null)).toBe('—')
  })
  it('computes elapsed time', () => {
    expect(durationBetween('2026-01-01T00:00:00Z', '2026-01-01T00:00:42Z')).toBe('42s')
  })
})

describe('shortSha', () => {
  it('truncates and handles null', () => {
    expect(shortSha('a4f81c2abcdef')).toBe('a4f81c2')
    expect(shortSha(null)).toBe('—')
  })
})
