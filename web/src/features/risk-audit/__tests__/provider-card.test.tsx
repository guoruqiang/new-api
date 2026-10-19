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
import { toast } from 'sonner'
import { expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { ProviderCard } from '../components/provider-card'
import { defaults, renderRisk } from './fixtures'

const provider = {
  id: 1,
  name: 'Moderation',
  provider_type: 'jev',
  base_url: 'https://api.example.test',
  model: 'safe',
  proxy: '',
  api_key_count: 1,
  key_states: [],
}

it('keeps a model-fetch failure visible and clears it after a successful retry', async () => {
  vi.spyOn(api, 'request')
    .mockResolvedValueOnce({
      data: {
        success: false,
        message: 'The provider rejected model discovery (HTTP 451).',
      },
    })
    .mockResolvedValueOnce({
      data: { success: true, data: { models: ['safe', 'safe-latest'] } },
    })
  const user = userEvent.setup()
  renderRisk(
    <ProviderCard
      provider={provider}
      presets={defaults.providers}
      canWrite
      onSaved={() => {}}
    />
  )
  await user.click(screen.getByRole('button', { name: 'Fetch models' }))
  expect(await screen.findByRole('alert')).toHaveTextContent(
    'The provider rejected model discovery (HTTP 451).'
  )
  await user.click(screen.getByRole('button', { name: 'Fetch models' }))
  expect(
    await screen.findByRole('combobox', { name: 'Available models' })
  ).toBeVisible()
  expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  await user.selectOptions(
    screen.getByRole('combobox', { name: 'Available models' }),
    'safe-latest'
  )
  expect(screen.getByRole('textbox', { name: 'Model' })).toHaveValue(
    'safe-latest'
  )
})

it.each([
  ['Base URL', 'https://new.example.test'],
  ['Proxy', 'http://127.0.0.1:8080'],
])(
  'requires saving a changed %s before probing with stored credentials',
  async (label, input) => {
    const updated = {
      ...provider,
      ...(label === 'Proxy' ? { proxy: input } : { base_url: input }),
    }
    const write = vi
      .spyOn(api, 'request')
      .mockResolvedValueOnce({ data: { success: true, data: updated } })
      .mockResolvedValueOnce({ data: { success: true, data: { models: [] } } })
    const user = userEvent.setup()
    renderRisk(
      <ProviderCard
        provider={provider}
        presets={defaults.providers}
        canWrite
        onSaved={() => {}}
      />
    )
    await user.clear(screen.getByRole('textbox', { name: label }))
    await user.type(screen.getByRole('textbox', { name: label }), input)
    expect(screen.getByRole('button', { name: 'Fetch models' })).toBeDisabled()
    expect(screen.getByRole('status')).toHaveTextContent(
      'Save the provider before fetching models with stored keys after changing the endpoint or proxy.'
    )
    expect(write).not.toHaveBeenCalled()
    await user.click(screen.getByRole('button', { name: 'Save provider' }))
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Fetch models' })).toBeEnabled()
    )
    await user.click(screen.getByRole('button', { name: 'Fetch models' }))
    expect(await screen.findByRole('status')).toHaveTextContent(
      'No models were returned by this provider.'
    )
    expect(write.mock.calls[1][0].data).toMatchObject({
      base_url: updated.base_url,
      proxy: updated.proxy,
    })
    expect(write.mock.calls[1][0].data).not.toHaveProperty('api_key')
  }
)

it('allows probing a changed connection with the first non-empty explicitly entered key', async () => {
  const write = vi.spyOn(api, 'request').mockResolvedValue({
    data: { success: true, data: { models: ['safe'] } },
  })
  const user = userEvent.setup()
  renderRisk(
    <ProviderCard
      provider={provider}
      presets={defaults.providers}
      canWrite
      onSaved={() => {}}
    />
  )
  await user.type(
    screen.getByRole('textbox', { name: 'Proxy' }),
    'http://127.0.0.1:8080'
  )
  await user.click(screen.getByRole('switch', { name: 'Replace stored keys' }))
  await user.type(
    screen.getByRole('textbox', { name: 'API keys (one per line)' }),
    '\nexplicit-test-key\nsecond-test-key'
  )
  await user.click(screen.getByRole('button', { name: 'Fetch models' }))
  await waitFor(() =>
    expect(write).toHaveBeenCalledWith(
      expect.objectContaining({
        data: expect.objectContaining({
          api_key: 'explicit-test-key',
          proxy: 'http://127.0.0.1:8080',
        }),
      })
    )
  )
})

it('disables connection editing and key replacement for read-only operators', () => {
  renderRisk(
    <ProviderCard
      provider={provider}
      presets={defaults.providers}
      canWrite={false}
      onSaved={() => {}}
    />
  )
  expect(screen.getByRole('textbox', { name: 'Base URL' })).toBeDisabled()
  expect(screen.getByRole('textbox', { name: 'Proxy' })).toBeDisabled()
  expect(
    screen.getByRole('switch', { name: 'Replace stored keys' })
  ).toHaveAttribute('aria-disabled', 'true')
  expect(screen.getByRole('button', { name: 'Fetch models' })).toBeDisabled()
  expect(
    screen.queryByRole('button', { name: 'Save provider' })
  ).not.toBeInTheDocument()
})

it('saves the selected discovered model and confirms success without replacing stored keys', async () => {
  const savedProvider = { ...provider, model: 'safe-latest' }
  const write = vi
    .spyOn(api, 'request')
    .mockResolvedValueOnce({
      data: { success: true, data: { models: ['safe-latest'] } },
    })
    .mockResolvedValueOnce({ data: { success: true, data: savedProvider } })
  const success = vi.spyOn(toast, 'success')
  const user = userEvent.setup()
  renderRisk(
    <ProviderCard
      provider={provider}
      presets={defaults.providers}
      canWrite
      onSaved={() => {}}
    />
  )
  await user.click(screen.getByRole('button', { name: 'Fetch models' }))
  await user.selectOptions(
    await screen.findByRole('combobox', { name: 'Available models' }),
    'safe-latest'
  )
  await user.click(screen.getByRole('button', { name: 'Save provider' }))
  await waitFor(() =>
    expect(write).toHaveBeenCalledWith({
      method: 'put',
      url: '/api/risk/providers/1',
      data: {
        name: provider.name,
        provider_type: provider.provider_type,
        base_url: provider.base_url,
        model: 'safe-latest',
        proxy: provider.proxy,
      },
    })
  )
  await waitFor(() =>
    expect(success).toHaveBeenCalledWith('Saved successfully')
  )
  expect(screen.getByRole('textbox', { name: 'Model' })).toHaveValue(
    'safe-latest'
  )
  expect(screen.getByRole('button', { name: 'Save provider' })).toBeEnabled()
})

it('keeps a provider save failure visible and preserves the entered settings', async () => {
  vi.spyOn(api, 'request').mockResolvedValue({
    data: {
      success: false,
      message: 'Provider storage is temporarily unavailable',
    },
  })
  const user = userEvent.setup()
  renderRisk(
    <ProviderCard
      provider={provider}
      presets={defaults.providers}
      canWrite
      onSaved={() => {}}
    />
  )
  await user.clear(screen.getByRole('textbox', { name: 'Model' }))
  await user.type(screen.getByRole('textbox', { name: 'Model' }), 'next-model')
  await user.click(screen.getByRole('button', { name: 'Save provider' }))
  expect(await screen.findByRole('alert')).toHaveTextContent(
    'Provider storage is temporarily unavailable'
  )
  expect(screen.getByRole('textbox', { name: 'Model' })).toHaveValue(
    'next-model'
  )
  expect(screen.getByRole('button', { name: 'Save provider' })).toBeEnabled()
})
