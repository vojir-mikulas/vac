import { afterEach, describe, expect, it } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'

import { DiffRows } from '@/features/activity/activity-diff-dialog'
import type { ActivityDiff } from '@/lib/api/audit'

afterEach(cleanup)

const diff: ActivityDiff = {
  kind: 'env',
  current_as_of: '2026-06-03T00:00:00Z',
  changed_since: false,
  rows: [
    { label: 'PLAIN_KEY', status: 'changed', before: 'old', after: 'new', masked: false },
    { label: 'API_SECRET', status: 'changed', before: 'topsecret', after: 'rotated', masked: true },
    { label: 'ADDED_KEY', status: 'added', after: 'hello', masked: false },
  ],
}

describe('<DiffRows>', () => {
  it('renders per-row status pills', () => {
    render(<DiffRows diff={diff} />)
    expect(screen.getAllByText('changed')).toHaveLength(2)
    expect(screen.getByText('added')).toBeTruthy()
  })

  it('masks masked rows and never renders the underlying value', () => {
    render(<DiffRows diff={diff} />)
    // The masked secret's plaintext must not appear anywhere in the DOM.
    expect(screen.queryByText('topsecret')).toBeNull()
    expect(screen.queryByText('rotated')).toBeNull()
    // ••••  appears for both sides of the masked row.
    expect(screen.getAllByText('••••').length).toBeGreaterThanOrEqual(2)
    // Non-masked values still render.
    expect(screen.getByText('old')).toBeTruthy()
    expect(screen.getByText('new')).toBeTruthy()
  })

  it('hides unchanged env rows behind a toggle', () => {
    const withUnchanged: ActivityDiff = {
      ...diff,
      rows: [
        ...diff.rows,
        { label: 'STABLE', status: 'unchanged', before: 'x', after: 'x', masked: false },
      ],
    }
    render(<DiffRows diff={withUnchanged} />)
    expect(screen.queryByText('STABLE')).toBeNull()
    expect(screen.getByText('Show 1 unchanged')).toBeTruthy()
  })
})
