import { describe, expect, it } from 'vitest'

import { isValidHostname } from './hostname'

describe('isValidHostname', () => {
  it('accepts valid FQDNs', () => {
    expect(isValidHostname('example.com')).toBe(true)
    expect(isValidHostname('app.example.com')).toBe(true)
    expect(isValidHostname('a-b.example.co.uk')).toBe(true)
    expect(isValidHostname('  example.com  ')).toBe(true) // trimmed
    expect(isValidHostname('xn--80ak6aa92e.com')).toBe(true) // punycode
  })

  it('rejects the cases the old .includes(".") check let through', () => {
    expect(isValidHostname('a.')).toBe(false)
    expect(isValidHostname('.com')).toBe(false)
    expect(isValidHostname('example')).toBe(false) // no TLD
    expect(isValidHostname('')).toBe(false)
    expect(isValidHostname('-bad.example.com')).toBe(false) // leading hyphen
    expect(isValidHostname('bad-.example.com')).toBe(false) // trailing hyphen
    expect(isValidHostname('exa mple.com')).toBe(false) // space
    expect(isValidHostname('*.example.com')).toBe(false) // wildcard
  })
})
