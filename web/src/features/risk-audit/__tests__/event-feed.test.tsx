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
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { EventFeed } from '../components/event-feed'
import { GateRail, RiskOverview } from '../components/overview'
import { PreviewDialog } from '../components/preview-dialog'
import type { Overview } from '../types'
import { directory, renderRisk } from './fixtures'

const overview: Overview = {
  enabled: true,
  fresh: true,
  available: true,
  config_revision: 1,
  requests: 12,
  attempts: 13,
  outcomes: {
    pass: 5,
    would_block: 2,
    error_pass: 1,
    exempt: 1,
    uncovered: 1,
    block: 2,
    error_block: 1,
  },
  stages: { hard_regex: 'on' },
  blocks: [
    { layer: 'hard_regex', outcome: 'block', reason: 'jailbreak', count: 2 },
    {
      layer: 'hard_regex',
      outcome: 'would_block',
      reason: 'jailbreak',
      count: 2,
    },
    { layer: 'model', outcome: 'error_block', reason: 'unavailable', count: 1 },
  ],
  passes: [],
  keywords: [
    { layer: 'hard_regex', keyword: 'regex-hit', count: 4 },
    { layer: 'distill', keyword: 'distill-hit', count: 1 },
  ],
  uncovered_routes: [],
}

it('shows actual blocks per stage and excludes uncovered requests from approved decisions', async () => {
  vi.spyOn(api, 'get').mockImplementation(async (url) => ({
    data: {
      success: true,
      data: url === '/api/risk/overview' ? overview : { items: [], total: 0 },
    },
  }))
  renderRisk(
    <RiskOverview group='standard' view='hard_regex' onView={() => {}} />
  )
  const stage = await screen.findByRole('tab', { name: 'Hard regex Enabled' })
  expect(within(stage).getByText('2', { exact: true })).toBeVisible()
  expect(
    within(screen.getByRole('tab', { name: 'Passed' })).getByText('9', {
      exact: true,
    })
  ).toBeVisible()
  expect(screen.getAllByRole('cell', { name: 'Jailbreak' })).toHaveLength(2)
  expect(
    screen.queryByRole('cell', { name: 'Decision model unavailable' })
  ).not.toBeInTheDocument()
  expect(screen.queryByText('distill-hit')).not.toBeInTheDocument()
})

it('shows upstream refusal observations as well as enforced upstream refusals on the pass view', async () => {
  const get = vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: {
        items: [
          {
            id: 1,
            created_at: 1760000000,
            user_id: 7,
            effective_group: 'standard',
            layer: 'upstream',
            outcome: 'would_stop',
          },
        ],
        total: 1,
      },
    },
  })
  renderRisk(
    <EventFeed group='standard' layer='upstream' compact upstreamOnly />
  )
  expect(await screen.findByText('Would stop retrying')).toBeVisible()
  expect(get).toHaveBeenCalledWith(
    '/api/risk/events',
    expect.objectContaining({
      params: expect.objectContaining({ layer: 'upstream', outcome: '' }),
    })
  )
})

it('keeps recent event details available and expands filters without leaving the stage', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: { items: [], total: 0 } },
  })
  const user = userEvent.setup()
  renderRisk(<EventFeed group='standard' layer='model' compact />)
  expect(screen.queryByLabelText('Outcome')).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Filters' }))
  expect(screen.getByLabelText('Outcome')).toBeVisible()
  expect(screen.getByRole('button', { name: 'Filters' })).toHaveAttribute(
    'aria-expanded',
    'true'
  )
})

it('distinguishes observation hits, actual blocks and model configuration status', async () => {
  const onChange = vi.fn()
  const user = userEvent.setup()
  render(
    <GateRail
      view='all'
      states={{ distill: 'on', hard_regex: 'off', model: 'unset' }}
      onChange={onChange}
    />
  )
  expect(
    screen.getByRole('tab', { name: 'Decision model Provider not configured' })
  ).toBeVisible()
  await user.click(
    screen.getByRole('tab', { name: 'Distillation detection Enabled' })
  )
  expect(onChange).toHaveBeenCalledWith('distill')
})

it('filters server events and loads evidence details without rendering HTML', async () => {
  const event = {
    id: 1,
    created_at: 1760000000,
    request_id: 'request-risk-1',
    user_id: 7,
    effective_group: 'standard',
    layer: 'hard_regex',
    outcome: 'would_block',
    rule: 'restricted',
    preview: '<script>danger()</script>',
    mode: 'observe',
    scores: '{"safety":0.1}',
  }
  const get = vi.spyOn(api, 'get').mockImplementation(async (url) => ({
    data: {
      success: true,
      data: url === '/api/risk/events/1' ? event : { items: [event], total: 1 },
    },
  }))
  const user = userEvent.setup()
  renderRisk(<EventFeed group='standard' layer='hard_regex' />)
  expect(
    await screen.findByRole('cell', { name: 'Observation hit' })
  ).toBeVisible()
  await user.selectOptions(screen.getByLabelText('Outcome'), 'block')
  await waitFor(() =>
    expect(get).toHaveBeenCalledWith(
      '/api/risk/events',
      expect.objectContaining({
        params: expect.objectContaining({
          group: 'standard',
          layer: 'hard_regex',
          outcome: 'block',
          p: 1,
        }),
      })
    )
  )
  await user.click(screen.getByRole('button', { name: 'View details' }))
  expect(await screen.findByText('<script>danger()</script>')).toBeVisible()
  expect(screen.getByRole('dialog').querySelector('script')).toBeNull()
  expect(
    screen.getByRole('button', { name: 'Copy to clipboard' })
  ).toBeVisible()
})

it('renders an empty event range and supports recovery from a failed request', async () => {
  const get = vi
    .spyOn(api, 'get')
    .mockRejectedValueOnce(new Error('offline'))
    .mockResolvedValue({
      data: { success: true, data: { items: [], total: 0 } },
    })
  const user = userEvent.setup()
  renderRisk(<EventFeed group='' layer='' />)
  await user.click(await screen.findByRole('button', { name: 'Retry' }))
  expect(await screen.findByText('No risk events in this range.')).toBeVisible()
  expect(get).toHaveBeenCalledTimes(2)
  expect(screen.getByRole('button', { name: 'Next' })).toBeDisabled()
})

it.each([
  ['tail_window', 'Tail window reviewed'],
  ['incomplete', 'Incomplete coverage'],
])('shows %s coverage distinctly in event details', async (coverage, label) => {
  const event = {
    id: 1,
    created_at: 1760000000,
    request_id: 'request-risk-window',
    user_id: 7,
    effective_group: 'standard',
    layer: 'model',
    outcome: 'pass',
    reason: 'model_safe',
    coverage,
  }
  vi.spyOn(api, 'get').mockImplementation(async (url) => ({
    data: {
      success: true,
      data: url === '/api/risk/events/1' ? event : { items: [event], total: 1 },
    },
  }))
  const user = userEvent.setup()
  renderRisk(<EventFeed group='standard' layer='model' />)
  await user.click(await screen.findByRole('button', { name: 'View details' }))
  expect(await screen.findByText(label)).toBeVisible()
  expect(screen.queryByText(coverage)).not.toBeInTheDocument()
})

it('shows the reviewed tail window alongside a safe preview verdict', async () => {
  const write = vi.spyOn(api, 'request').mockResolvedValue({
    data: {
      success: true,
      data: {
        audited: true,
        mode: 'observe',
        model_run: true,
        decision: {
          outcome: 'pass',
          reason: 'model_safe',
          coverage: 'tail_window',
          screening_score: 3,
          screening_reason: 'user_rule',
          model_triggered: true,
          scores: { safety: 0.9 },
        },
      },
    },
  })
  const user = userEvent.setup()
  renderRisk(<PreviewDialog directory={directory} />)
  await user.click(
    screen.getByRole('button', { name: 'Preview effective policy' })
  )
  await user.click(screen.getByRole('switch', { name: 'Run decision model' }))
  await user.click(screen.getByRole('button', { name: 'Evaluate preview' }))
  expect(await screen.findByRole('status')).toHaveTextContent(
    'Coverage: Tail window reviewed'
  )
  expect(screen.getByRole('status')).toHaveTextContent('Model judged safe')
  expect(screen.getByRole('status')).toHaveTextContent(
    'Screening score: 3 · User rule match'
  )
  expect(write).toHaveBeenCalledWith(
    expect.objectContaining({
      url: '/api/risk/preview',
      data: expect.objectContaining({
        group_name: 'standard',
        run_model: true,
      }),
    })
  )
})
