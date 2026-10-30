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

import { EventFeed } from '../components/event-feed'
import { RiskOverview } from '../components/overview'
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
  stages: { keyword: 'on', model: 'unset' },
  blocks: [
    { layer: 'model', outcome: 'block', reason: 'model_violation', count: 2 },
  ],
  passes: [{ layer: 'keyword', outcome: 'pass', reason: 'no_hit', count: 5 }],
  keywords: [{ layer: 'keyword', keyword: 'regex-hit', count: 4 }],
  pending_reviews: 3,
  uncovered_routes: [],
}

it('summarizes outcomes, stage states and pending reviews and opens the violation list', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: overview },
  })
  const open = vi.fn()
  const user = userEvent.setup()
  renderRisk(<RiskOverview group='standard' onOpenViolations={open} />)
  const blocked = await screen.findByRole('region', { name: 'Blocked' })
  expect(within(blocked).getByText('3', { exact: true })).toBeVisible()
  expect(
    within(screen.getByRole('region', { name: 'Passed' })).getByText('7', {
      exact: true,
    })
  ).toBeVisible()
  expect(screen.getByText('Provider not configured')).toBeVisible()
  expect(screen.getByRole('cell', { name: 'Model confirmed violation' })).toBeVisible()
  expect(screen.getByRole('cell', { name: 'No keyword match' })).toBeVisible()
  expect(screen.getByRole('cell', { name: 'regex-hit' })).toBeVisible()
  const pending = screen.getByRole('region', { name: 'Pending review' })
  expect(within(pending).getByText('3', { exact: true })).toBeVisible()
  await user.click(within(pending).getByRole('button', { name: 'Review violations' }))
  expect(open).toHaveBeenCalledOnce()
})

it('filters server events and shows event details without request text', async () => {
  const event = {
    id: 1,
    created_at: 1760000000,
    request_id: 'request-risk-1',
    user_id: 7,
    effective_group: 'standard',
    layer: 'keyword',
    outcome: 'would_block',
    reason: 'model_violation',
    keyword: 'restricted',
    rule: 'restricted.*',
    mode: 'observe',
  }
  const get = vi.spyOn(api, 'get').mockImplementation(async (url) => ({
    data: {
      success: true,
      data: url === '/api/risk/events/1' ? event : { items: [event], total: 1 },
    },
  }))
  const user = userEvent.setup()
  renderRisk(<EventFeed group='standard' />)
  expect(await screen.findByRole('cell', { name: 'restricted' })).toBeVisible()
  await user.selectOptions(screen.getByLabelText('Outcome'), 'block')
  await user.selectOptions(screen.getByLabelText('Layer'), 'keyword')
  await user.type(screen.getByRole('spinbutton', { name: 'User ID' }), '7')
  await waitFor(() =>
    expect(get).toHaveBeenCalledWith(
      '/api/risk/events',
      expect.objectContaining({
        params: expect.objectContaining({
          group: 'standard',
          layer: 'keyword',
          outcome: 'block',
          user_id: '7',
          p: 1,
        }),
      })
    )
  )
  await user.click(screen.getByRole('button', { name: 'View details' }))
  const dialog = await screen.findByRole('dialog')
  expect(await within(dialog).findByText('restricted.*')).toBeVisible()
  expect(within(dialog).queryByText('Evidence preview')).not.toBeInTheDocument()
  expect(
    within(dialog).getByRole('button', { name: 'Copy to clipboard' })
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
  renderRisk(<EventFeed group='' />)
  await user.click(await screen.findByRole('button', { name: 'Retry' }))
  expect(await screen.findByText('No risk events in this range.')).toBeVisible()
  expect(get).toHaveBeenCalledTimes(2)
  expect(screen.getByRole('button', { name: 'Next' })).toBeDisabled()
})

it('previews a violation with its deduction and sends only the supported fields', async () => {
  const write = vi.spyOn(api, 'request').mockResolvedValue({
    data: {
      success: true,
      data: {
        enabled: true,
        audited: true,
        exempt: false,
        mode: 'enforce',
        binding_source: 'default',
        model_run: true,
        deduction: 100,
        decision: {
          outcome: 'block',
          layer: 'model',
          reason: 'model_violation',
          category: 'jailbreak',
          keyword: 'ignore previous',
          scores: { jailbreak: 0.1 },
        },
      },
    },
  })
  const user = userEvent.setup()
  renderRisk(<PreviewDialog directory={directory} />)
  await user.click(
    screen.getByRole('button', { name: 'Preview effective policy' })
  )
  expect(
    screen.queryByRole('spinbutton', { name: 'User ID' })
  ).not.toBeInTheDocument()
  await user.click(screen.getByRole('switch', { name: 'Run decision model' }))
  await user.click(screen.getByRole('button', { name: 'Evaluate preview' }))
  const status = await screen.findByRole('status')
  expect(status).toHaveTextContent('Model confirmed violation')
  expect(status).toHaveTextContent('Category: Jailbreak')
  expect(status).toHaveTextContent(
    'This violation would deduct 100 points.'
  )
  expect(write.mock.calls[0][0]).toMatchObject({
    url: '/api/risk/preview',
    data: {
      group_name: 'standard',
      via_auto: false,
      body: expect.any(String),
      run_model: true,
    },
  })
  expect(write.mock.calls[0][0].data).not.toHaveProperty('user_id')
})
