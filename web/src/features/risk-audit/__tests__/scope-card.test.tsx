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

import { ScopeCard } from '../components/scope-card'
import { configSchema } from '../lib/schema'
import { config, directory, renderRisk } from './fixtures'

it('collapses active group configuration and preserves edits when reopened', async () => {
  const user = userEvent.setup()
  renderRisk(
    <ScopeCard
      config={{ ...config, payload: { ...config.payload, enabled: true } }}
      directory={directory}
      canWrite
    />
  )
  const trigger = screen.getByRole('button', {
    name: /Group exemptions and modes/,
  })
  expect(trigger).toHaveAttribute('aria-expanded', 'false')
  await user.click(trigger)
  await user.click(screen.getByRole('switch', { name: 'Enable risk audit' }))
  await user.click(trigger)
  expect(trigger).toHaveAttribute('aria-expanded', 'false')
  await user.click(trigger)
  expect(
    screen.getByRole('switch', { name: 'Enable risk audit' })
  ).not.toBeChecked()
  expect(
    screen.getByRole('button', { name: 'Save configuration' })
  ).toBeEnabled()
})

it('edits only default and group modes while preserving internal bindings and reporting dirty changes', async () => {
  const user = userEvent.setup()
  const dirty = vi.fn()
  const write = vi.spyOn(api, 'request').mockResolvedValue({
    data: { success: true, data: { revision: 4 } },
  })
  const override = { ...config.payload.default_binding, namespace: 'ns-a' }
  renderRisk(
    <ScopeCard
      config={{
        ...config,
        payload: {
          ...config.payload,
          exempt_namespaces: ['ns-b'],
          group_bindings: [override],
        },
      }}
      directory={{
        ...directory,
        groups: [
          ...directory.groups,
          {
            ...directory.groups[0],
            group_name: 'exempt',
            namespace: 'ns-b',
            exempt: true,
          },
        ],
      }}
      canWrite
      onDirtyChange={dirty}
    />
  )
  expect(dirty).toHaveBeenLastCalledWith(false)
  expect(
    screen.queryByRole('combobox', { name: 'Policy' })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('spinbutton', { name: 'Revision' })
  ).not.toBeInTheDocument()
  expect(screen.queryByText(/baseline v1/)).not.toBeInTheDocument()
  const modes = screen.getAllByRole('combobox', { name: 'Mode' })
  await user.selectOptions(modes[0], 'enforce')
  await user.selectOptions(modes[1], 'enforce')
  expect(dirty).toHaveBeenLastCalledWith(true)
  await user.click(screen.getByRole('button', { name: 'Save configuration' }))
  await waitFor(() =>
    expect(write).toHaveBeenCalledWith(
      expect.objectContaining({
        method: 'put',
        url: '/api/risk/config',
        data: {
          ...config.payload,
          expected_revision: 3,
          default_binding: {
            ...config.payload.default_binding,
            mode: 'enforce',
          },
          exempt_namespaces: ['ns-b'],
          group_bindings: [{ ...override, mode: 'enforce' }],
        },
      })
    )
  )
  await waitFor(() => expect(dirty).toHaveBeenLastCalledWith(false))
})

it('saves an empty exemption list with the expected revision and keeps auto out of choices', async () => {
  const write = vi
    .spyOn(api, 'request')
    .mockResolvedValue({ data: { success: true, data: { revision: 4 } } })
  const user = userEvent.setup()
  renderRisk(
    <ScopeCard
      config={config}
      directory={{
        ...directory,
        groups: [
          ...directory.groups,
          { ...directory.groups[0], group_name: 'auto', namespace: 'auto' },
        ],
      }}
      canWrite
    />
  )
  expect(
    screen.getByText(
      'New groups are audited automatically. Auto requests are always audited, even in exempt groups.'
    )
  ).toBeVisible()
  await user.click(screen.getByRole('switch', { name: 'Enable risk audit' }))
  await user.click(screen.getByRole('button', { name: 'Save configuration' }))
  await waitFor(() =>
    expect(write).toHaveBeenCalledWith(
      expect.objectContaining({
        data: expect.objectContaining({
          expected_revision: 3,
          enabled: true,
          exempt_namespaces: [],
          retention: { events_days: 17, counters_days: 123 },
        }),
      })
    )
  )
  expect(screen.queryByText('auto', { exact: true })).not.toBeInTheDocument()
})

it('keeps unsaved values on revision conflict until the operator reloads', async () => {
  const write = vi.spyOn(api, 'request').mockRejectedValue({
    response: { status: 409, data: { message: 'Conflict' } },
  })
  vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: { ...config, revision: 7 } },
  })
  const user = userEvent.setup()
  renderRisk(<ScopeCard config={config} directory={directory} canWrite />)
  await user.click(screen.getByRole('switch', { name: 'Enable risk audit' }))
  await user.click(screen.getByRole('button', { name: 'Save configuration' }))
  expect(
    await screen.findByText(
      'The risk audit settings were changed by someone else. Reload and try again.'
    )
  ).toBeVisible()
  expect(screen.getByRole('switch')).toBeChecked()
  await user.click(screen.getByRole('button', { name: 'Reload' }))
  await waitFor(() => expect(screen.getByRole('switch')).not.toBeChecked())
  write.mockResolvedValue({ data: { success: true, data: { revision: 8 } } })
  await user.click(screen.getByRole('switch', { name: 'Enable risk audit' }))
  await user.click(screen.getByRole('button', { name: 'Save configuration' }))
  await waitFor(() =>
    expect(write).toHaveBeenLastCalledWith(
      expect.objectContaining({
        data: expect.objectContaining({ expected_revision: 7 }),
      })
    )
  )
})

it('shows retired group references and makes configuration read-only without write permission', () => {
  renderRisk(
    <ScopeCard
      config={config}
      directory={{
        ...directory,
        invalid_refs: [
          { namespace: 'old', group_name: 'retired', kind: 'exempt' },
        ],
      }}
      canWrite={false}
    />
  )
  expect(
    screen.getByText(
      'Some group references are inactive. Recreated groups are audited by default.'
    )
  ).toBeVisible()
  expect(
    screen.queryByRole('button', { name: 'Save configuration' })
  ).not.toBeInTheDocument()
  expect(screen.getByRole('switch')).toHaveAttribute('aria-disabled', 'true')
  expect(screen.getByRole('combobox', { name: 'Mode' })).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Override' })).toBeDisabled()
})

it('rejects auto, duplicate namespaces and a group that is both exempt and bound', () => {
  expect(configSchema.safeParse(config.payload).success).toBe(true)
  for (const patch of [
    { exempt_namespaces: ['auto'] },
    { exempt_namespaces: ['a', 'a'] },
    {
      exempt_namespaces: ['a'],
      group_bindings: [{ ...config.payload.default_binding, namespace: 'a' }],
    },
  ]) {
    expect(
      configSchema.safeParse({ ...config.payload, ...patch }).success
    ).toBe(false)
  }
})
