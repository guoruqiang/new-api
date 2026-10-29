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
    token_id: 104,
    judgment_source: '',
    screening_score: 3,
    screening_reason: 'signal',
    config_revision: 2,
    original_request: '[{"role":"user","content":"Original request text"}]',
    original_request_truncated: false,
    original_request_hash: 'request-hash',
    notification_status: 'pending',
    notification_error: '',
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
    deduction: 0,
    severity: '',
    severity_status: '',
    assessments: '[{"category":"nsfw","safety_score":0.1}]',
    judgment_version: 2,
    revision: 1,
    reviewed_at: 0,
    reviewed_by: 0,
    review_note: '',
    history: '[]',
  },
  reputation: {
    score: 80,
    initial_score: 100,
    ban_threshold: 0,
    confirmed_events: 1,
    total_deducted: 20,
    recovered_points: 0,
    last_violation_at: 1760000000,
    next_recovery_at: 1760604800,
  },
  behavior_enabled: true,
  auto_block_enabled: false,
  auto_confirm_enabled: false,
  mode: 'enforce',
}

it.each([
  {
    minutes: 30,
    status: 'pending' as const,
    category: 'nsfw',
    severity: 'fixed',
  },
  {
    minutes: 0,
    status: 'confirmed' as const,
    category: 'nsfw',
    severity: 'fixed',
  },
  {
    minutes: 30,
    status: 'confirmed' as const,
    category: 'cyber',
    severity: 'moderate',
  },
  {
    minutes: 30,
    status: 'confirmed' as const,
    category: 'cyber',
    severity: '',
  },
])(
  'bans the $status $category/$severity incident token for $minutes minutes while preserving its confirmation',
  async ({ minutes, status, category, severity }) => {
    vi.spyOn(api, 'get').mockResolvedValue({
      data: {
        success: true,
        data: {
          ...candidate,
          mode: 'observe',
          behavior_enabled: false,
          review: { ...candidate.review, status, category, severity },
        },
      },
    })
    const write = vi.spyOn(api, 'request').mockResolvedValue({
      data: {
        success: true,
        data: {
          ...candidate,
          review: { ...candidate.review, status: 'confirmed', revision: 2 },
        },
      },
    })
    const user = userEvent.setup()
    renderRisk(<ReviewDetail id={7} canWrite canUnblock onClose={() => {}} />)
    expect(
      await screen.findByText(candidate.review.original_request)
    ).toBeVisible()
    await user.click(screen.getByRole('button', { name: 'Ban this API token' }))
    const dialog = screen.getByRole('alertdialog')
    const categoryInput = within(dialog).getByRole('combobox', {
      name: 'Category',
    })
    expect(categoryInput).toHaveValue(category)
    if (status === 'confirmed') expect(categoryInput).toBeDisabled()
    else expect(categoryInput).toBeEnabled()
    expect(
      within(dialog).queryByRole('combobox', { name: 'Severity' })
    ).not.toBeInTheDocument()
    expect(
      within(dialog).getByRole('button', { name: 'Ban this API token' })
    ).toBeDisabled()
    const duration = within(dialog).getByRole('spinbutton', {
      name: 'Token ban duration (minutes, 0 = permanent)',
    })
    expect(duration).toHaveValue(30)
    await user.clear(duration)
    await user.type(duration, String(minutes))
    await user.type(
      within(dialog).getByLabelText('Review note'),
      'Verified the original role content'
    )
    await user.click(
      within(dialog).getByRole('button', { name: 'Ban this API token' })
    )
    await waitFor(() =>
      expect(write).toHaveBeenCalledWith({
        method: 'post',
        url: '/api/risk/reviews/7/resolve',
        data: {
          revision: 1,
          action: 'ban',
          category,
          note: 'Verified the original role content',
          ttl_minutes: minutes,
        },
      })
    )
  }
)

it.each([
  { mode: 'observe' as const },
  { token_id: 0 },
  { evidence: '' },
  { evidence_truncated: true },
  { original_request: '' },
  { original_request_truncated: true },
])(
  'disables token banning without an enforcement token and complete evidence: %j',
  async (patch) => {
    vi.spyOn(api, 'get').mockResolvedValue({
      data: {
        success: true,
        data: { ...candidate, review: { ...candidate.review, ...patch } },
      },
    })
    renderRisk(<ReviewDetail id={7} canWrite canUnblock onClose={() => {}} />)
    expect(
      await screen.findByRole('button', { name: 'Ban this API token' })
    ).toBeDisabled()
    expect(
      screen.getByText(
        'Token banning requires an enforced request, a token ID, and complete saved audit and original request evidence.'
      )
    ).toBeVisible()
  }
)

it('hides token banning without the unblock permission', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: candidate },
  })
  renderRisk(
    <ReviewDetail id={7} canWrite canUnblock={false} onClose={() => {}} />
  )
  expect(
    await screen.findByText(candidate.review.original_request)
  ).toBeVisible()
  expect(
    screen.queryByRole('button', { name: 'Ban this API token' })
  ).not.toBeInTheDocument()
})

it.each(['pending', 'confirmed'] as const)(
  'keeps a legacy %s review inspectable without allowing new token penalties',
  async (status) => {
    vi.spyOn(api, 'get').mockResolvedValue({
      data: {
        success: true,
        data: {
          ...candidate,
          review: { ...candidate.review, status, token_id: 0 },
        },
      },
    })
    renderRisk(<ReviewDetail id={7} canWrite canUnblock onClose={() => {}} />)
    expect(
      await screen.findByText(
        'Reviews without an API token cannot be confirmed or used to restrict tokens. Existing confirmations can still be revoked.'
      )
    ).toBeVisible()
    expect(screen.getByText(candidate.review.evidence)).toBeVisible()
    expect(
      screen.getByRole('button', { name: 'Ban this API token' })
    ).toBeDisabled()
    if (status === 'pending') {
      expect(
        screen.getByRole('button', { name: 'Confirm violation' })
      ).toBeDisabled()
    } else {
      expect(
        screen.getByRole('button', { name: 'Revoke confirmation' })
      ).toBeEnabled()
    }
  }
)

it.each([{ original_request: '' }, { original_request_truncated: true }])(
  'prevents indirect token penalties when original evidence is incomplete: %j',
  async (patch) => {
    vi.spyOn(api, 'get').mockResolvedValue({
      data: {
        success: true,
        data: { ...candidate, review: { ...candidate.review, ...patch } },
      },
    })
    renderRisk(<ReviewDetail id={7} canWrite canUnblock onClose={() => {}} />)
    expect(
      await screen.findByRole('button', { name: 'Confirm violation' })
    ).toBeDisabled()
    expect(
      screen.getByRole('button', { name: 'Ban this API token' })
    ).toBeDisabled()
    expect(
      screen.getByRole('button', { name: 'Dismiss candidate' })
    ).toBeEnabled()
    expect(
      screen.getByText(
        'Complete request evidence is required to confirm this violation'
      )
    ).toBeVisible()
  }
)

it('does not let an invalid cancelled ban duration block a later review action', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: candidate },
  })
  const write = vi.spyOn(api, 'request').mockResolvedValue({
    data: {
      success: true,
      data: {
        ...candidate,
        review: { ...candidate.review, status: 'dismissed', revision: 2 },
      },
    },
  })
  const user = userEvent.setup()
  renderRisk(<ReviewDetail id={7} canWrite canUnblock onClose={() => {}} />)
  await user.click(
    await screen.findByRole('button', { name: 'Ban this API token' })
  )
  const banDialog = screen.getByRole('alertdialog')
  const duration = within(banDialog).getByRole('spinbutton', {
    name: 'Token ban duration (minutes, 0 = permanent)',
  })
  await user.clear(duration)
  await user.type(duration, '1441')
  await user.click(within(banDialog).getByRole('button', { name: 'Cancel' }))
  await user.click(screen.getByRole('button', { name: 'Dismiss candidate' }))
  const reviewDialog = screen.getByRole('alertdialog')
  await user.type(
    within(reviewDialog).getByLabelText('Review note'),
    'False positive'
  )
  await user.click(
    within(reviewDialog).getByRole('button', { name: 'Dismiss candidate' })
  )
  await waitFor(() =>
    expect(write).toHaveBeenCalledWith(
      expect.objectContaining({
        data: {
          revision: 1,
          action: 'dismiss',
          category: 'nsfw',
          note: 'False positive',
        },
      })
    )
  )
})

it('shows model confirmation and notification status without inventing an administrator ID', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: {
        ...candidate,
        review: {
          ...candidate.review,
          judgment_source: 'model',
          status: 'confirmed',
          review_note: 'High confidence',
          reviewed_by: 0,
          reviewed_at: 1760000001,
          notification_status: 'skipped',
          notification_error: 'no_bound_email',
        },
      },
    },
  })
  renderRisk(<ReviewDetail id={7} canWrite canUnblock onClose={() => {}} />)
  expect((await screen.findAllByText('Model confirmation'))[0]).toBeVisible()
  expect(screen.getByText('Notification skipped')).toBeVisible()
  expect(screen.getByText('No bound email address')).toBeVisible()
  expect(screen.queryByText(/Reviewer ID: 0/)).not.toBeInTheDocument()
  expect(
    screen.getByRole('region', { name: 'Submitted review evidence' })
  ).toBeVisible()
  expect(
    screen.getByRole('region', { name: 'Original role content' })
  ).toBeVisible()
})

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

it('distinguishes a duplicate incident with zero actual deduction from its original model assessment', async () => {
  const assessments = '[{"category":"nsfw","applied_deduction":100}]'
  vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: {
        ...candidate,
        review: {
          ...candidate.review,
          status: 'confirmed',
          deduction: 0,
          assessments,
          notification_status: 'skipped',
          notification_error: 'duplicate_incident',
        },
      },
    },
  })
  renderRisk(<ReviewDetail id={7} canWrite canUnblock onClose={() => {}} />)
  expect(
    await screen.findByRole('definition', {
      name: 'Actual reputation deduction',
    })
  ).toHaveTextContent(/^0$/)
  const assessment = screen.getByRole('region', {
    name: 'Model category assessments',
  })
  expect(assessment).toHaveTextContent(assessments)
  expect(assessment).toHaveTextContent(
    'Saved model assessments are shown as recorded. Historical severity or deduction values may differ from the actual reputation deduction above.'
  )
  expect(
    screen.getByText(
      'This violation was already processed. This duplicate does not deduct reputation or send another email.'
    )
  ).toBeVisible()
})

it('validates review notes and optional integer token restriction duration', () => {
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
  for (const ttl_minutes of [0, 30, 1440]) {
    expect(
      reviewResolveSchema.safeParse({
        category: 'nsfw',
        note: 'Checked',
        ttl_minutes,
      }).success
    ).toBe(true)
  }
  for (const ttl_minutes of [-1, 1.5, 1441]) {
    expect(
      reviewResolveSchema.safeParse({
        category: 'nsfw',
        note: 'Checked',
        ttl_minutes,
      }).success
    ).toBe(false)
  }
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
  expect(within(detail).getByText('User reputation: 80 / 100')).toBeVisible()
  expect(within(detail).getByText(/Next reputation recovery/)).toBeVisible()
  expect(
    within(detail).getByRole('region', { name: 'Model category assessments' })
  ).toHaveTextContent(candidate.review.assessments)
  expect(within(detail).getByText('Record only')).toBeVisible()
  expect(
    within(detail).queryByText('Severity assessment')
  ).not.toBeInTheDocument()
  expect(
    within(detail).queryByText('Needs manual severity')
  ).not.toBeInTheDocument()
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
          deduction: 20,
          severity: '',
          severity_status: '',
          category: 'cyber',
          revision: 2,
          review_note: 'Confirmed from submitted evidence',
          reviewed_at: 1760000001,
          reviewed_by: 1,
        },
        reputation: {
          ...candidate.reputation,
          score: 60,
          confirmed_events: 2,
          total_deducted: 40,
        },
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
    within(confirmation).queryByRole('combobox', { name: 'Severity' })
  ).not.toBeInTheDocument()
  expect(
    within(confirmation).getByRole('button', { name: 'Confirm violation' })
  ).toBeDisabled()
  await user.selectOptions(
    within(confirmation).getByLabelText('Category'),
    'cyber'
  )
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
  expect(await screen.findByText('User reputation: 60 / 100')).toBeVisible()
  expect(
    screen.getByRole('button', { name: 'Revoke confirmation' })
  ).toBeEnabled()
})

it('keeps observation confirmation from deducting reputation or suspending tokens', async () => {
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
    within(confirmation).getByText(
      'Each distinct confirmed violation deducts 20 reputation points. Observation confirmations do not change reputation.'
    )
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
          reputation: {
            ...candidate.reputation,
            score: 100,
            total_deducted: 0,
          },
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
    expect(await screen.findByText('User reputation: 100 / 100')).toBeVisible()
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
