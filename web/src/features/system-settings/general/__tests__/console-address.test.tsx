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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  createMemoryHistory,
  createRootRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { useState } from 'react'
import { beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { SettingsPageProvider } from '../../components/settings-page-context'
import { isValidConsoleAddress } from '../console-address'
import { SystemInfoSection } from '../system-info-section'

async function renderSettings(consoleAddress = '') {
  function Fixture() {
    const [container, setContainer] = useState<HTMLDivElement | null>(null)
    return (
      <>
        <div ref={setContainer} />
        <SettingsPageProvider actionsContainer={container}>
          <SystemInfoSection
            defaultValues={{
              SystemName: 'New API',
              ServerAddress: 'https://api.example.com',
              ConsoleAddress: consoleAddress,
              TaskPublicAddress: '',
              Logo: '',
              Footer: '',
              About: '',
              HomePageContent: '',
              general_setting: { docs_link: '' },
              legal: { user_agreement: '', privacy_policy: '' },
            }}
          />
        </SettingsPageProvider>
      </>
    )
  }
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const router = createRouter({
    routeTree: createRootRoute({ component: Fixture }),
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
  return screen.findByRole('textbox', { name: 'Console Address' })
}

beforeEach(() => {
  vi.spyOn(api, 'put').mockResolvedValue({ data: { success: true } })
})

test('saving a console address preserves the API address and refreshes cached status', async () => {
  const input = await renderSettings()
  localStorage.setItem('status', JSON.stringify({ console_address: '' }))
  fireEvent.change(input, { target: { value: 'https://console.example.com/' } })
  fireEvent.click(screen.getByRole('button', { name: 'Save Changes' }))
  await waitFor(() =>
    expect(api.put).toHaveBeenCalledWith('/api/option/', {
      key: 'ConsoleAddress',
      value: 'https://console.example.com',
    })
  )
  expect(api.put).toHaveBeenCalledTimes(1)
  expect(screen.getByRole('textbox', { name: 'Server Address' })).toHaveValue(
    'https://api.example.com'
  )
  await waitFor(() => expect(localStorage.getItem('status')).toBeNull())
})

test('clearing a console address saves the empty fallback value', async () => {
  const input = await renderSettings('https://console.example.com')
  fireEvent.change(input, { target: { value: '' } })
  fireEvent.click(screen.getByRole('button', { name: 'Save Changes' }))
  await waitFor(() =>
    expect(api.put).toHaveBeenCalledWith('/api/option/', {
      key: 'ConsoleAddress',
      value: '',
    })
  )
})

test.each([
  'http://console.example.com',
  'https://user:secret@console.example.com',
  'https://console.example.com/path',
  'https://console.example.com?next=evil',
  'https://console.example.com#fragment',
  '//console.example.com',
])('invalid console address %s prevents saving', async (value) => {
  const input = await renderSettings()
  fireEvent.change(input, { target: { value } })
  fireEvent.click(screen.getByRole('button', { name: 'Save Changes' }))
  await waitFor(() => expect(input).toHaveAttribute('aria-invalid', 'true'))
  expect(
    screen.getByText(
      'Console address must be an HTTPS origin, or an HTTP loopback origin for local development'
    )
  ).toBeInTheDocument()
  expect(api.put).not.toHaveBeenCalled()
})

test.each([
  'http://localhost:3000',
  'http://127.0.0.1:3000',
  'http://[::1]:3000',
])('local development origin %s can be saved', async (value) => {
  const input = await renderSettings()
  fireEvent.change(input, { target: { value } })
  fireEvent.click(screen.getByRole('button', { name: 'Save Changes' }))
  await waitFor(() =>
    expect(api.put).toHaveBeenCalledWith('/api/option/', {
      key: 'ConsoleAddress',
      value,
    })
  )
})

test.each([
  'http://127.1',
  'http://0x7f000001',
  'http://2130706433',
  'http://127.000.0.1',
  'http://localhost.',
  'https://console.example.com:0',
  'https://console.example.com:',
  'https://console.example.com:65536',
  'https://console.example.com?',
  'https://console.example.com#',
  'https://@console.example.com',
  'https://console.example.com\\path',
  'https://console.\nexample.com',
])('ambiguous or malformed console origin %s is rejected', (value) => {
  expect(isValidConsoleAddress(value)).toBe(false)
})
