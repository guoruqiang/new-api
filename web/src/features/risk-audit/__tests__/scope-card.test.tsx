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
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { ScopeCard } from '../components/scope-card'
import { configSchema } from '../lib/schema'
import { config, defaults, directory, renderRisk } from './fixtures'

function mockWrite(revision = 4) {
  return vi
    .spyOn(api, 'request')
    .mockResolvedValue({ data: { success: true, data: { revision } } })
}

it('edits default and group modes, preserves bindings and reports dirty changes', async () => {
  const user = userEvent.setup()
  const dirty = vi.fn()
  const write = mockWrite()
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
            binding_source: 'exempt',
          },
        ],
      }}
      scoreCategories={defaults.score_categories}
      canWrite
      onDirtyChange={dirty}
    />
  )
  expect(dirty).toHaveBeenLastCalledWith(false)
  expect(screen.queryByLabelText('Bypassed groups')).not.toBeInTheDocument()
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

it('saves scoring, notification, per-category deductions and violation retention', async () => {
  const user = userEvent.setup()
  const write = mockWrite()
  renderRisk(
    <ScopeCard
      config={config}
      directory={directory}
      scoreCategories={defaults.score_categories}
      canWrite
    />
  )
  const autoBan = screen.getByRole('switch', {
    name: 'Automatically ban accounts at score 0',
  })
  expect(autoBan).not.toBeChecked()
  await user.click(autoBan)
  await user.click(
    screen.getByRole('switch', { name: 'Email the user about each violation' })
  )
  fireEvent.change(screen.getByRole('spinbutton', { name: 'Default deduction' }), {
    target: { value: '30' },
  })
  fireEvent.change(
    screen.getByRole('spinbutton', {
      name: 'Violation record retention (days)',
    }),
    { target: { value: '365' } }
  )
  const table = screen.getByRole('table', { name: 'Deduction per category' })
  expect(within(table).getByRole('spinbutton', { name: 'Jailbreak' })).toHaveValue(
    100
  )
  expect(
    within(table).getByRole('spinbutton', { name: 'Reverse engineering' })
  ).toHaveValue(30)
  fireEvent.change(within(table).getByRole('spinbutton', { name: 'Cyber abuse' }), {
    target: { value: '50' },
  })
  const nsfw = within(table).getByRole('row', { name: /Explicit content/ })
  await user.click(within(nsfw).getByRole('button', { name: 'Use default' }))
  await user.click(screen.getByRole('button', { name: 'Save configuration' }))
  await waitFor(() => expect(write).toHaveBeenCalledOnce())
  expect(write.mock.calls[0][0].data).toMatchObject({
    retention: { events_days: 17, counters_days: 123, violations_days: 365 },
    score: {
      auto_ban: true,
      email_notify: false,
      default: 30,
      deductions: { jailbreak: 100, cyber: 50 },
    },
  })
})

it('blocks saving a deduction outside 1 to 100 points', async () => {
  const user = userEvent.setup()
  const write = mockWrite()
  renderRisk(
    <ScopeCard
      config={config}
      directory={directory}
      scoreCategories={defaults.score_categories}
      canWrite
    />
  )
  fireEvent.change(screen.getByRole('spinbutton', { name: 'Default deduction' }), {
    target: { value: '101' },
  })
  await user.click(screen.getByRole('button', { name: 'Save configuration' }))
  expect(await screen.findByText('Invalid risk audit settings')).toBeVisible()
  expect(write).not.toHaveBeenCalled()
})

it('keeps unsaved values on revision conflict until the operator reloads', async () => {
  const write = vi.spyOn(api, 'request').mockRejectedValue({
    response: { status: 409, data: { message: 'Conflict' } },
  })
  vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: { ...config, revision: 7 } },
  })
  const user = userEvent.setup()
  renderRisk(
    <ScopeCard
      config={config}
      directory={directory}
      scoreCategories={defaults.score_categories}
      canWrite
    />
  )
  const enable = screen.getByRole('switch', { name: 'Enable risk audit' })
  await user.click(enable)
  await user.click(screen.getByRole('button', { name: 'Save configuration' }))
  expect(
    await screen.findByText(
      'The risk audit settings were changed by someone else. Reload and try again.'
    )
  ).toBeVisible()
  expect(enable).toBeChecked()
  await user.click(screen.getByRole('button', { name: 'Reload' }))
  await waitFor(() =>
    expect(
      screen.getByRole('switch', { name: 'Enable risk audit' })
    ).not.toBeChecked()
  )
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
      scoreCategories={defaults.score_categories}
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
  expect(
    screen.getByRole('switch', { name: 'Enable risk audit' })
  ).toHaveAttribute('aria-disabled', 'true')
  expect(screen.getByRole('combobox', { name: 'Mode' })).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Override' })).toBeDisabled()
  expect(
    screen.getByRole('spinbutton', { name: 'Default deduction' })
  ).toBeDisabled()
})

it('rejects auto, duplicate namespaces and an exempt group with a binding', () => {
  expect(configSchema.safeParse(config.payload).success).toBe(true)
  const binding = { ...config.payload.default_binding, namespace: 'a' }
  for (const patch of [
    { exempt_namespaces: ['auto'] },
    { exempt_namespaces: ['a', 'a'] },
    { exempt_namespaces: ['a'], group_bindings: [binding] },
    { retention: { ...config.payload.retention, violations_days: 0 } },
    { score: { ...config.payload.score, deductions: { nsfw: 0 } } },
  ]) {
    expect(
      configSchema.safeParse({ ...config.payload, ...patch }).success
    ).toBe(false)
  }
})
