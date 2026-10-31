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
import { afterEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { ViolationDetail } from '../components/violation-detail'
import { ViolationRecords } from '../components/violation-records'
import type { Violation, ViolationDetail as Detail } from '../types'
import { renderRisk } from './fixtures'

const violation: Violation = {
  id: 5,
  request_id: 'req-violation-5',
  content_hash: 'hash',
  user_id: 42,
  token_id: 9,
  group_name: 'standard',
  namespace: 'ns-a',
  mode: 'enforce',
  model: 'claude-test',
  category: 'jailbreak',
  keyword: 'ignore previous',
  rule: 'ignore\\s+previous',
  provider_model: 'jev-latest',
  scores: '{"jailbreak":0.12}',
  deduction: 100,
  score_before: 100,
  score_after: 0,
  action: 'ban_suggested',
  duplicate_of: 0,
  status: 'pending',
  reviewed_by: 0,
  reviewed_at: 0,
  review_note: '',
  notification_status: 'sent',
  request_body:
    '{"model":"claude-test","messages":[{"role":"user","content":"[image]"}]}',
  request_body_bytes: 71,
  request_body_truncated: true,
  created_at: 1760000000,
}
const detail: Detail = { violation, score: 0, user_status: 1 }

afterEach(() => {
  Reflect.deleteProperty(URL, 'createObjectURL')
  Reflect.deleteProperty(URL, 'revokeObjectURL')
})

function mockDetail(next: Detail = detail) {
  return vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: next },
  })
}

it('lists violations with the score change, action and status and filters by status', async () => {
  const { request_body: _body, ...row } = violation
  const get = vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: { items: [row], total: 1 } },
  })
  const user = userEvent.setup()
  renderRisk(<ViolationRecords canWrite canUnblock />)
  const table = await screen.findByRole('table', { name: 'Violations' })
  for (const text of [
    'standard',
    'Jailbreak',
    'ignore previous',
    '100 → 0',
    'Ban suggested',
    'Pending review',
  ]) {
    expect(within(table).getByText(text)).toBeVisible()
  }
  expect(get).toHaveBeenCalledWith(
    '/api/risk/violations',
    expect.objectContaining({
      params: expect.objectContaining({ status: 'pending', p: 1 }),
    })
  )
  await user.selectOptions(
    screen.getByRole('combobox', { name: 'Review status' }),
    'upheld'
  )
  await user.type(screen.getByRole('textbox', { name: 'Request ID' }), 'req')
  await waitFor(() =>
    expect(get).toHaveBeenLastCalledWith(
      '/api/risk/violations',
      expect.objectContaining({
        params: expect.objectContaining({
          status: 'upheld',
          request_id: 'req',
        }),
      })
    )
  )
})

it('shows the evidence, scores and account state and downloads the request JSON', async () => {
  mockDetail()
  const createObjectURL = vi.fn((_blob: Blob) => 'blob:violation')
  Object.defineProperty(URL, 'createObjectURL', {
    configurable: true,
    value: createObjectURL,
  })
  Object.defineProperty(URL, 'revokeObjectURL', {
    configurable: true,
    value: vi.fn(),
  })
  const click = vi
    .spyOn(HTMLAnchorElement.prototype, 'click')
    .mockImplementation(() => {})
  const user = userEvent.setup()
  renderRisk(<ViolationDetail id={5} canWrite canUnblock onClose={() => {}} />)
  expect(await screen.findByText('Current score: 0 / 100')).toBeVisible()
  expect(screen.getByText(/Account status/)).toHaveTextContent('Enabled')
  expect(screen.getByRole('cell', { name: 'Jailbreak' })).toBeVisible()
  expect(screen.getByRole('cell', { name: '0.12' })).toBeVisible()
  expect(
    screen.getByText(
      'The saved request was truncated because it exceeded the storage limit.'
    )
  ).toBeVisible()
  const evidence = screen.getByRole('region', { name: 'Request JSON' })
  const pretty = JSON.stringify(
    JSON.parse(violation.request_body ?? ''),
    null,
    2
  )
  expect(evidence.querySelector('pre')?.textContent).toBe(pretty)
  await user.click(
    within(evidence).getByRole('button', { name: 'Download JSON' })
  )
  expect(click).toHaveBeenCalledOnce()
  expect((click.mock.contexts[0] as HTMLAnchorElement).download).toBe(
    'violation-5.json'
  )
  expect(createObjectURL.mock.calls[0][0].type).toBe(
    'application/json;charset=utf-8'
  )
})

it('hides account bans without the unblock permission', async () => {
  mockDetail()
  renderRisk(
    <ViolationDetail id={5} canWrite canUnblock={false} onClose={() => {}} />
  )
  expect(
    await screen.findByRole('button', { name: 'Uphold violation' })
  ).toBeVisible()
  expect(screen.getByRole('button', { name: 'Revoke violation' })).toBeVisible()
  expect(
    screen.queryByRole('button', { name: 'Ban account' })
  ).not.toBeInTheDocument()
})

it.each([
  ['Uphold violation', 1, '/api/risk/violations/5/resolve', 'uphold'],
  ['Revoke violation', 1, '/api/risk/violations/5/resolve', 'revoke'],
])(
  'sends %s with the review note to the matching endpoint',
  async (label, userStatus, url, action) => {
    mockDetail({ ...detail, user_status: userStatus })
    const write = vi
      .spyOn(api, 'request')
      .mockResolvedValue({ data: { success: true, data: detail } })
    const user = userEvent.setup()
    renderRisk(
      <ViolationDetail id={5} canWrite canUnblock onClose={() => {}} />
    )
    await user.click(await screen.findByRole('button', { name: label }))
    const confirm = await screen.findByRole('alertdialog')
    const submit = within(confirm).getByRole('button', { name: label })
    expect(submit).toBeDisabled()
    await user.type(
      within(confirm).getByRole('textbox', { name: 'Review note' }),
      '  Customer notified  '
    )
    await user.click(submit)
    await waitFor(() =>
      expect(write).toHaveBeenCalledWith({
        method: 'post',
        url,
        data: { action, note: 'Customer notified' },
      })
    )
  }
)

it.each([
  ['Ban account', 1, 'ban'],
  ['Unban account', 2, 'unban'],
])(
  'sends %s for the violation without a note',
  async (label, userStatus, action) => {
    mockDetail({ ...detail, user_status: userStatus })
    const write = vi
      .spyOn(api, 'request')
      .mockResolvedValue({ data: { success: true, data: detail } })
    const user = userEvent.setup()
    renderRisk(
      <ViolationDetail id={5} canWrite canUnblock onClose={() => {}} />
    )
    await user.click(await screen.findByRole('button', { name: label }))
    const confirm = await screen.findByRole('alertdialog')
    expect(within(confirm).queryByRole('textbox')).not.toBeInTheDocument()
    await user.click(within(confirm).getByRole('button', { name: label }))
    await waitFor(() =>
      expect(write).toHaveBeenCalledWith({
        method: 'post',
        url: '/api/risk/violations/5/ban',
        data: { action },
      })
    )
  }
)
