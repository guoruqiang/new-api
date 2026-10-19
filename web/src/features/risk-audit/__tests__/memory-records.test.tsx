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
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { MemoryRecords } from '../components/memory-records'
import { directory, renderRisk } from './fixtures'

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
