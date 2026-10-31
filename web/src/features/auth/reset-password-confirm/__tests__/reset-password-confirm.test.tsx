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
  createRoute,
  createRouter,
  Outlet,
  RouterProvider,
} from '@tanstack/react-router'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { ResetPasswordConfirm, type ResetPasswordSearchParams } from '..'

function renderResetPage(search: ResetPasswordSearchParams) {
  const root = createRootRoute({ component: Outlet })
  const router = createRouter({
    routeTree: root.addChildren([
      createRoute({
        getParentRoute: () => root,
        path: '/user/reset',
        component: () => <ResetPasswordConfirm {...search} />,
      }),
      createRoute({
        getParentRoute: () => root,
        path: '/sign-in',
        component: () => <div>Sign-in page</div>,
      }),
    ]),
    history: createMemoryHistory({ initialEntries: ['/user/reset'] }),
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
}

async function fillPasswords(password: string, confirmation: string) {
  const user = userEvent.setup()
  await user.type(await screen.findByLabelText('New password'), password)
  await user.type(screen.getByLabelText('Confirm password'), confirmation)
  await user.click(screen.getByRole('button', { name: 'Set new password' }))
}

beforeEach(() => {
  vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
  // The layout loads the site name and logo; the page does not depend on them.
  vi.spyOn(api, 'get').mockResolvedValue({ data: { success: true, data: {} } })
})

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

it('sends the chosen password with the link token and opens sign-in on success', async () => {
  const post = vi
    .spyOn(api, 'post')
    .mockResolvedValue({ data: { success: true, message: '' } })
  renderResetPage({ email: 'user@example.com', token: 'link-token' })
  await fillPasswords('Chosen-password-1', 'Chosen-password-1')
  await waitFor(() =>
    expect(post).toHaveBeenCalledWith(
      '/api/user/reset',
      { token: 'link-token', password: 'Chosen-password-1' },
      expect.anything()
    )
  )
  expect(await screen.findByText('Sign-in page')).toBeInTheDocument()
})

it('rejects mismatched passwords without contacting the server', async () => {
  const post = vi.spyOn(api, 'post')
  renderResetPage({ email: 'user@example.com', token: 'link-token' })
  await fillPasswords('Chosen-password-1', 'Different-password-1')
  expect(await screen.findByText("Passwords don't match.")).toBeVisible()
  expect(post).not.toHaveBeenCalled()
})

it('stays on the page when the server rejects the link', async () => {
  vi.spyOn(api, 'post').mockResolvedValue({
    data: {
      success: false,
      message: 'Password reset link is invalid or has expired',
    },
  })
  renderResetPage({ email: 'user@example.com', token: 'used-token' })
  await fillPasswords('Chosen-password-1', 'Chosen-password-1')
  await waitFor(() =>
    expect(
      screen.getByRole('button', { name: 'Set new password' })
    ).toBeEnabled()
  )
  expect(screen.queryByText('Sign-in page')).not.toBeInTheDocument()
})

it('disables submission for a link without a token', async () => {
  renderResetPage({ email: 'user@example.com' })
  expect(
    await screen.findByText(
      'Invalid reset link, please request a new password reset.'
    )
  ).toBeVisible()
  expect(
    screen.getByRole('button', { name: 'Set new password' })
  ).toBeDisabled()
})
