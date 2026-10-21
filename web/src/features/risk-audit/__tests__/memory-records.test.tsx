/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { MemoryRecords } from '../components/memory-records'
import { ReviewDetail } from '../components/review-detail'
import { ReviewRecords } from '../components/review-records'
import { reviewResolveSchema } from '../lib/schema'
import type { RiskReviewDetail } from '../types'
import { directory, renderRisk } from './fixtures'

const candidate: RiskReviewDetail = {
  review: {
    id: 7,
    request_id: 'review-request',
    created_at: 1760000000,
    namespace: 'ns-a',
    group_name: 'standard',
    user_id: 42,
    mode: 'enforce',
    layer: 'model',
    category: 'nsfw',
    reason: 'model',
    model: 'gpt-test',
    provider_model: 'safety-model',
    scores: '{"nsfw":0.1}',
    policy_key: 'baseline',
    policy_revision: 2,
    fingerprint: 'evidence-fingerprint',
    evidence: '<system>Actual bounded review evidence</system>',
    evidence_truncated: false,
    coverage: 'tail_window',
    status: 'pending',
    points: 0,
    revision: 1,
    reviewed_at: 0,
    reviewed_by: 0,
    review_note: '',
    history: '[]',
  },
  score: {
    score: 3,
    confirmed_events: 1,
    window_minutes: 10,
    threshold: 6,
    block_ttl_minutes: 30,
  },
  behavior_enabled: true,
  auto_block_enabled: false,
  mode: 'enforce',
}

it('shows an empty candidate list after retrying a failed load', async () => {
  vi.spyOn(api, 'get')
    .mockRejectedValueOnce(new Error('Unavailable'))
    .mockResolvedValue({
      data: { success: true, data: { items: [], total: 0 } },
    })
  const user = userEvent.setup()
  renderRisk(<ReviewRecords directory={directory} canWrite canUnblock />)
  await user.click(await screen.findByRole('button', { name: 'Retry' }))
  expect(await screen.findByText('No review candidates')).toBeVisible()
  expect(screen.getByRole('button', { name: 'Next' })).toBeDisabled()
})

it('validates review notes as nonempty Unicode text capped at 500 characters', () => {
  for (const note of ['', '   ', '😀'.repeat(501)]) {
    expect(
      reviewResolveSchema.safeParse({ category: 'nsfw', note }).success
    ).toBe(false)
  }
  expect(
    reviewResolveSchema.safeParse({ category: 'nsfw', note: '😀'.repeat(500) })
      .success
  ).toBe(true)
  expect(
    reviewResolveSchema.safeParse({
      category: 'not-a-category',
      note: 'Checked',
    }).success
  ).toBe(false)
})

it('filters pending candidates by namespace and loads evidence only when details are opened', async () => {
  const get = vi.spyOn(api, 'get').mockImplementation(async (url) => ({
    data: {
      success: true,
      data:
        url === '/api/risk/reviews/7'
          ? candidate
          : { items: [candidate.review], total: 1 },
    },
  }))
  const user = userEvent.setup()
  renderRisk(<ReviewRecords directory={directory} canWrite canUnblock />)
  expect(
    await screen.findByRole('table', { name: 'Review candidates' })
  ).toBeVisible()
  expect(get).toHaveBeenCalledWith('/api/risk/reviews', {
    params: expect.objectContaining({ status: 'pending', p: 1 }),
  })
  expect(get).not.toHaveBeenCalledWith('/api/risk/reviews/7', expect.anything())
  await user.selectOptions(screen.getByLabelText('Group namespace'), 'ns-a')
  await waitFor(() =>
    expect(get).toHaveBeenCalledWith('/api/risk/reviews', {
      params: expect.objectContaining({ namespace: 'ns-a', p: 1 }),
    })
  )
  await user.click(screen.getByRole('button', { name: 'View details' }))
  const detail = await screen.findByRole('dialog', {
    name: 'Risk review details',
  })
  expect(
    await within(detail).findByText(candidate.review.evidence)
  ).toBeVisible()
  expect(within(detail).getByText('Tail window reviewed')).toBeVisible()
  expect(within(detail).getByText('Confirmed score: 3 / 6')).toBeVisible()
  expect(within(detail).getByText('Record only')).toBeVisible()
})

it('requires a note before confirming and submits the selected category with the candidate revision', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: candidate },
  })
  const write = vi.spyOn(api, 'request').mockResolvedValue({
    data: {
      success: true,
      data: {
        ...candidate,
        review: {
          ...candidate.review,
          status: 'confirmed',
          points: 1,
          category: 'cyber',
          revision: 2,
          review_note: 'Confirmed from submitted evidence',
          reviewed_at: 1760000001,
          reviewed_by: 1,
        },
        score: { ...candidate.score, score: 4, confirmed_events: 2 },
      },
    },
  })
  const user = userEvent.setup()
  renderRisk(<ReviewDetail id={7} canWrite canUnblock onClose={() => {}} />)
  await user.click(
    await screen.findByRole('button', { name: 'Confirm violation' })
  )
  const confirmation = screen.getByRole('alertdialog')
  expect(
    within(confirmation).getByText('Points on confirmation: 3')
  ).toBeVisible()
  expect(
    within(confirmation).getByRole('button', { name: 'Confirm violation' })
  ).toBeDisabled()
  await user.selectOptions(
    within(confirmation).getByLabelText('Category'),
    'cyber'
  )
  expect(
    within(confirmation).getByText('Points on confirmation: 1')
  ).toBeVisible()
  await user.type(
    within(confirmation).getByLabelText('Review note'),
    'Confirmed from submitted evidence'
  )
  await user.click(
    within(confirmation).getByRole('button', { name: 'Confirm violation' })
  )
  await waitFor(() =>
    expect(write).toHaveBeenCalledWith({
      method: 'post',
      url: '/api/risk/reviews/7/resolve',
      data: {
        revision: 1,
        action: 'confirm',
        category: 'cyber',
        note: 'Confirmed from submitted evidence',
      },
    })
  )
  expect(await screen.findByText('Confirmed score: 4 / 6')).toBeVisible()
  expect(
    screen.getByRole('button', { name: 'Revoke confirmation' })
  ).toBeEnabled()
})

it('keeps an observation confirmation at zero points and shows that it cannot block', async () => {
  const observed = {
    ...candidate,
    review: { ...candidate.review, mode: 'observe' as const },
  }
  vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: observed },
  })
  const write = vi.spyOn(api, 'request').mockResolvedValue({
    data: {
      success: true,
      data: {
        ...observed,
        review: { ...observed.review, status: 'confirmed', revision: 2 },
      },
    },
  })
  const user = userEvent.setup()
  renderRisk(<ReviewDetail id={7} canWrite canUnblock onClose={() => {}} />)
  expect(
    await screen.findByText(/This candidate came from observation mode/)
  ).toBeVisible()
  await user.click(screen.getByRole('button', { name: 'Confirm violation' }))
  const confirmation = screen.getByRole('alertdialog')
  expect(
    within(confirmation).getByText('Points on confirmation: 0')
  ).toBeVisible()
  await user.type(
    within(confirmation).getByLabelText('Review note'),
    'Observation only'
  )
  await user.click(
    within(confirmation).getByRole('button', { name: 'Confirm violation' })
  )
  await waitFor(() => expect(write).toHaveBeenCalledOnce())
  expect(
    await screen.findByRole('button', { name: 'Revoke confirmation' })
  ).toBeEnabled()
})

it.each([
  {
    evidence: '',
    behavior_enabled: true,
    message:
      'Review evidence is unavailable or was not stored. A short preview alone cannot confirm a violation.',
  },
  {
    evidence: candidate.review.evidence,
    behavior_enabled: false,
    message: 'Behavior scoring is disabled for this group',
  },
])(
  'prevents confirmation when evidence or scoring is unavailable',
  async (input) => {
    vi.spyOn(api, 'get').mockResolvedValue({
      data: {
        success: true,
        data: {
          ...candidate,
          behavior_enabled: input.behavior_enabled,
          review: { ...candidate.review, evidence: input.evidence },
        },
      },
    })
    renderRisk(<ReviewDetail id={7} canWrite canUnblock onClose={() => {}} />)
    expect(await screen.findByText(input.message)).toBeVisible()
    expect(
      screen.getByRole('button', { name: 'Confirm violation' })
    ).toBeDisabled()
    expect(
      screen.getByRole('button', { name: 'Dismiss candidate' })
    ).toBeEnabled()
  }
)

it('keeps the note after a revision conflict and prevents resubmitting a stale confirmation', async () => {
  const get = vi
    .spyOn(api, 'get')
    .mockResolvedValue({ data: { success: true, data: candidate } })
  const write = vi.spyOn(api, 'request').mockRejectedValue({
    response: { status: 409, data: { message: 'Conflict' } },
  })
  const user = userEvent.setup()
  renderRisk(<ReviewDetail id={7} canWrite canUnblock onClose={() => {}} />)
  await user.click(
    await screen.findByRole('button', { name: 'Confirm violation' })
  )
  const confirmation = screen.getByRole('alertdialog')
  await user.type(
    within(confirmation).getByLabelText('Review note'),
    'Keep this review note'
  )
  await user.click(
    within(confirmation).getByRole('button', { name: 'Confirm violation' })
  )
  expect(
    await within(confirmation).findByText(/This review changed/)
  ).toBeVisible()
  expect(within(confirmation).getByLabelText('Review note')).toHaveValue(
    'Keep this review note'
  )
  expect(
    within(confirmation).getByRole('button', { name: 'Confirm violation' })
  ).toBeDisabled()
  expect(write).toHaveBeenCalledOnce()
  await waitFor(() => expect(get).toHaveBeenCalledTimes(2))
})

it.each([
  {
    status: 'pending' as const,
    action: 'dismiss',
    label: 'Dismiss candidate',
    result: 'dismissed',
  },
  {
    status: 'confirmed' as const,
    action: 'revoke',
    label: 'Revoke confirmation',
    result: 'revoked',
  },
])(
  'records $action with confirmation and refreshes the returned score',
  async (input) => {
    const current = {
      ...candidate,
      review: { ...candidate.review, status: input.status, revision: 4 },
    }
    vi.spyOn(api, 'get').mockResolvedValue({
      data: { success: true, data: current },
    })
    const write = vi.spyOn(api, 'request').mockResolvedValue({
      data: {
        success: true,
        data: {
          ...candidate,
          review: { ...current.review, status: input.result, revision: 5 },
          score: { ...candidate.score, score: 0 },
        },
      },
    })
    const user = userEvent.setup()
    renderRisk(<ReviewDetail id={7} canWrite canUnblock onClose={() => {}} />)
    await user.click(await screen.findByRole('button', { name: input.label }))
    const confirmation = screen.getByRole('alertdialog')
    expect(write).not.toHaveBeenCalled()
    expect(
      within(confirmation).getByRole('button', { name: input.label })
    ).toBeDisabled()
    await user.type(
      within(confirmation).getByLabelText('Review note'),
      'False positive'
    )
    await user.click(
      within(confirmation).getByRole('button', { name: input.label })
    )
    await waitFor(() =>
      expect(write).toHaveBeenCalledWith(
        expect.objectContaining({
          data: {
            action: input.action,
            revision: 4,
            category: 'nsfw',
            note: 'False positive',
          },
        })
      )
    )
    expect(await screen.findByText('Confirmed score: 0 / 6')).toBeVisible()
    expect(
      screen.queryByRole('button', { name: input.label })
    ).not.toBeInTheDocument()
  }
)

it('allows read-only operators to inspect truncated evidence without review actions', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: {
        ...candidate,
        review: { ...candidate.review, evidence_truncated: true },
      },
    },
  })
  renderRisk(
    <ReviewDetail
      id={7}
      canWrite={false}
      canUnblock={false}
      onClose={() => {}}
    />
  )
  expect(
    await screen.findByText(/Stored review evidence is truncated/)
  ).toBeVisible()
  expect(screen.getByText(candidate.review.evidence)).toHaveClass(
    'max-h-80',
    'overflow-auto',
    'whitespace-pre-wrap'
  )
  expect(
    screen.queryByRole('button', { name: 'Confirm violation' })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('button', { name: 'Dismiss candidate' })
  ).not.toBeInTheDocument()
})

it('prevents confirmation when saved evidence was truncated even with write permission', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: {
        ...candidate,
        review: { ...candidate.review, evidence_truncated: true },
      },
    },
  })
  renderRisk(<ReviewDetail id={7} canWrite canUnblock onClose={() => {}} />)
  expect(
    await screen.findByText(
      'Stored review evidence is truncated; this incident cannot be confirmed'
    )
  ).toBeVisible()
  expect(
    screen.getByRole('button', { name: 'Confirm violation' })
  ).toBeDisabled()
  expect(
    screen.getByRole('button', { name: 'Dismiss candidate' })
  ).toBeEnabled()
})

it('hides revocation without unblock permission even when risk write is granted', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: {
        ...candidate,
        review: { ...candidate.review, status: 'confirmed', points: 3 },
      },
    },
  })
  renderRisk(
    <ReviewDetail id={7} canWrite canUnblock={false} onClose={() => {}} />
  )
  expect(await screen.findByText(candidate.review.evidence)).toBeVisible()
  expect(
    screen.queryByRole('button', { name: 'Revoke confirmation' })
  ).not.toBeInTheDocument()
})

it('requires namespace selection and confirmation before clearing group records', async () => {
  const get = vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: { items: [], total: 0 } },
  })
  const write = vi
    .spyOn(api, 'request')
    .mockResolvedValue({ data: { success: true, data: { removed: 1 } } })
  const user = userEvent.setup()
  renderRisk(
    <MemoryRecords directory={directory} canWrite canUnblock={false} />
  )
  expect(get).not.toHaveBeenCalled()
  await user.selectOptions(screen.getByLabelText('Group namespace'), 'ns-a')
  await user.click(
    await screen.findByRole('button', { name: 'Clear selected group' })
  )
  expect(write).not.toHaveBeenCalled()
  await user.click(screen.getByRole('button', { name: 'Continue' }))
  await waitFor(() =>
    expect(write).toHaveBeenCalledWith(
      expect.objectContaining({
        url: '/api/risk/refusals/clear',
        data: { namespace: 'ns-a', confirm: true },
      })
    )
  )
  await user.selectOptions(screen.getByLabelText('Records'), 'blocks')
  expect(
    screen.queryByRole('button', { name: 'Clear selected group' })
  ).not.toBeInTheDocument()
})

it('hides release controls without unblock permission even when write is granted', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: {
        items: [
          {
            id: 1,
            subject_type: 'user',
            subject_value: '7',
            reason: 'exact',
            expires_at: 0,
            released_at: 0,
            hit_count: 1,
          },
        ],
        total: 1,
      },
    },
  })
  const user = userEvent.setup()
  renderRisk(
    <MemoryRecords directory={directory} canWrite canUnblock={false} />
  )
  await user.selectOptions(screen.getByLabelText('Group namespace'), 'ns-a')
  await user.selectOptions(screen.getByLabelText('Records'), 'blocks')
  expect(await screen.findByText('User: 7')).toBeVisible()
  expect(
    screen.queryByRole('button', { name: 'Release block' })
  ).not.toBeInTheDocument()
})
