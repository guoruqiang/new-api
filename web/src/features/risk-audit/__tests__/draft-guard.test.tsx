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
import type { QueryClient } from '@tanstack/react-query'
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import {
  cleanup,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { afterEach, expect, it, vi } from 'vitest'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { RiskAudit } from '..'
import { DraftGuard } from '../hooks/draft-guard'
import { useConfirmRiskDiscard, useRiskDraft } from '../hooks/use-risk-draft'
import { riskSearchSchema } from '../lib/schema'
import {
  config,
  defaults,
  directory,
  policy,
  renderRisk,
  row,
} from './fixtures'

const clients: QueryClient[] = []

afterEach(() => {
  cleanup()
  for (const client of clients.splice(0)) client.clear()
  useAuthStore.setState(useAuthStore.getInitialState(), true)
})

async function renderRiskPage(latestRevision = 1) {
  vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
  useAuthStore.getState().auth.setUser({
    id: 1,
    username: 'risk-reviewer',
    role: ROLE.SUPER_ADMIN,
  })
  const server = {
    failProviders: false,
    createdPolicy: false,
    config: structuredClone(config),
  }
  const created = { ...row, id: 2, policy_key: 'review-policy', name: 'Review' }
  const get = vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/risk/providers' && server.failProviders) {
      throw new Error('Provider list unavailable')
    }
    const responses: Record<string, unknown> = {
      '/api/risk/defaults': defaults,
      '/api/risk/config': server.config,
      '/api/risk/groups': directory,
      '/api/risk/policies': server.createdPolicy
        ? [{ ...row, revision: latestRevision }, created]
        : [{ ...row, revision: latestRevision }],
      '/api/risk/providers': [],
      '/api/risk/policies/baseline/revisions':
        latestRevision === 1
          ? [row]
          : [{ ...row, revision: latestRevision }, row],
      '/api/risk/policies/baseline/revisions/1': { ...row, decoded: policy },
      '/api/risk/policies/baseline/revisions/2': {
        ...row,
        revision: 2,
        decoded: policy,
      },
      '/api/risk/policies/review-policy/revisions': [created],
      '/api/risk/policies/review-policy/revisions/1': {
        ...created,
        decoded: policy,
      },
      '/api/risk/overview': {
        enabled: false,
        fresh: true,
        available: true,
        config_revision: config.revision,
        requests: 0,
        attempts: 0,
        outcomes: {},
        passes: [],
        blocks: [],
        keywords: [],
        stages: {},
        pending_reviews: 0,
        uncovered_routes: [],
      },
      '/api/risk/events': { items: [], total: 0, page: 1, page_size: 20 },
    }
    if (!(url in responses)) throw new Error(`Unexpected risk request: ${url}`)
    return { data: { success: true, data: structuredClone(responses[url]) } }
  })
  const root = createRootRoute()
  const authenticated = createRoute({
    getParentRoute: () => root,
    id: '_authenticated',
  })
  const risk = createRoute({
    getParentRoute: () => authenticated,
    path: '/risk-audit/',
    validateSearch: riskSearchSchema,
    component: RiskAudit,
  })
  const router = createRouter({
    routeTree: root.addChildren([authenticated.addChildren([risk])]),
    history: createMemoryHistory({
      initialEntries: ['/risk-audit/?view=policy&section=distill'],
    }),
  })
  const result = renderRisk(<RouterProvider router={router} />)
  clients.push(result.client)
  await screen.findByRole('textbox', { name: 'Contest regex' })
  return { ...result, server, get, created }
}

function DraftEditor() {
  const [draft, setDraft] = useState('')
  const [policy, setPolicy] = useState('A')
  useRiskDraft(!!draft)
  const confirmDiscard = useConfirmRiskDiscard()
  return (
    <>
      <Input
        aria-label='Draft'
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
      />
      <p>{policy}</p>
      <Button
        onClick={() =>
          confirmDiscard(() => {
            setDraft('')
            setPolicy('B')
          })
        }
      >
        Switch policy
      </Button>
    </>
  )
}

it('retains an unsaved policy on cancel and changes policy only after explicit confirmation', async () => {
  const user = userEvent.setup()
  const router = createRouter({
    routeTree: createRootRoute({
      component: () => (
        <DraftGuard>
          <DraftEditor />
        </DraftGuard>
      ),
    }),
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  render(<RouterProvider router={router} />)
  await user.type(
    await screen.findByRole('textbox', { name: 'Draft' }),
    'unsaved'
  )
  await user.click(screen.getByRole('button', { name: 'Switch policy' }))
  await user.click(screen.getByRole('button', { name: 'Cancel' }))
  expect(screen.getByRole('textbox')).toHaveValue('unsaved')
  expect(screen.getByText('A')).toBeVisible()
  await user.click(screen.getByRole('button', { name: 'Switch policy' }))
  await user.click(screen.getByRole('button', { name: 'Continue' }))
  await waitFor(() => expect(screen.getByText('B')).toBeVisible())
  expect(screen.getByRole('textbox')).toHaveValue('')
})

it('keeps the real policy draft visible when Refresh fails after data was loaded', async () => {
  const user = userEvent.setup()
  const page = await renderRiskPage()
  const input = screen.getByRole('textbox', { name: 'Contest regex' })
  await user.clear(input)
  await user.type(input, 'Keep this unsaved message')

  page.server.failProviders = true
  await user.click(screen.getByRole('button', { name: 'Refresh' }))

  expect(await screen.findByRole('button', { name: 'Retry' })).toBeVisible()
  expect(screen.getByRole('textbox', { name: 'Contest regex' })).toHaveValue(
    'Keep this unsaved message'
  )
  expect(screen.getByRole('button', { name: 'Save settings' })).toBeEnabled()
})

it.each([
  { name: 'policy', policy_key: 'review-policy', policy_revision: 1 },
  { name: 'revision', policy_key: 'baseline', policy_revision: 2 },
])(
  'keeps the selected policy draft when the default $name changes on refresh',
  async (next) => {
    const user = userEvent.setup()
    const page = await renderRiskPage()
    const input = screen.getByRole('textbox', { name: 'Contest regex' })
    await user.clear(input)
    await user.type(input, 'Keep this unsaved policy')
    page.server.createdPolicy = true
    page.server.config = {
      ...config,
      revision: config.revision + 1,
      payload: {
        ...config.payload,
        default_binding: {
          ...config.payload.default_binding,
          policy_key: next.policy_key,
          policy_revision: next.policy_revision,
        },
      },
    }
    await user.click(screen.getByRole('button', { name: 'Refresh' }))
    await waitFor(() =>
      expect(page.get).toHaveBeenCalledWith(
        '/api/risk/config',
        expect.anything()
      )
    )
    expect(screen.getByRole('textbox', { name: 'Contest regex' })).toHaveValue(
      'Keep this unsaved policy'
    )
    expect(screen.getByRole('button', { name: 'Save settings' })).toBeEnabled()
  }
)

it('shows one configuration and saves and enables it with a single request', async () => {
  const user = userEvent.setup()
  await renderRiskPage(2)
  const write = vi.spyOn(api, 'request').mockResolvedValue({
    data: { success: true, data: { ...row, revision: 2, config_revision: 4 } },
  })
  expect(screen.queryByRole('combobox', { name: 'Policy to edit' })).toBeNull()
  expect(screen.queryByRole('combobox', { name: 'Revision' })).toBeNull()
  expect(screen.queryByRole('spinbutton', { name: 'Revision' })).toBeNull()
  expect(screen.queryByRole('button', { name: 'New policy' })).toBeNull()
  expect(screen.queryByText(/Baseline · v/)).toBeNull()
  await user.click(screen.getByRole('button', { name: 'Save and enable' }))
  await waitFor(() =>
    expect(write).toHaveBeenCalledWith(
      expect.objectContaining({
        method: 'post',
        url: '/api/risk/policies',
        data: expect.objectContaining({
          policy_key: 'baseline',
          base_revision: 2,
          activation: {
            expected_config_revision: config.revision,
            enable: true,
          },
        }),
      })
    )
  )
  expect(write).toHaveBeenCalledTimes(1)
})

it('keeps scope and policy drafts across tabs and requires saving the scope first', async () => {
  const user = userEvent.setup()
  await renderRiskPage()
  await user.type(
    screen.getByRole('textbox', { name: 'Contest regex' }),
    ' modified'
  )
  await user.click(screen.getByRole('tab', { name: 'Scope and scoring' }))
  await user.selectOptions(
    await screen.findByRole('combobox', { name: 'Mode' }),
    'enforce'
  )
  expect(
    screen.getByRole('button', { name: 'Save configuration' })
  ).toBeEnabled()
  await user.click(screen.getByRole('tab', { name: 'Policy' }))
  await user.click(screen.getByRole('tab', { name: 'Decision model' }))
  await user.click(screen.getByRole('tab', { name: 'Distillation detection' }))
  expect(
    await screen.findByRole('textbox', { name: 'Contest regex' })
  ).toHaveValue('contest modified')
  expect(screen.getByRole('button', { name: 'Save and enable' })).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Save settings' })).toBeDisabled()
  await user.click(screen.getByRole('tab', { name: 'Scope and scoring' }))
  expect(await screen.findByRole('combobox', { name: 'Mode' })).toHaveValue(
    'enforce'
  )
})

it('keeps a conflicting draft until reload is confirmed and locks editing while reloading', async () => {
  const user = userEvent.setup()
  const page = await renderRiskPage()
  vi.spyOn(api, 'request').mockRejectedValue({
    response: {
      status: 409,
      data: {
        message:
          'The risk audit settings were changed by someone else. Reload and try again.',
      },
    },
  })
  const input = screen.getByRole('textbox', { name: 'Contest regex' })
  await user.type(input, ' keep draft')
  await user.click(screen.getByRole('button', { name: 'Save settings' }))
  await user.click(
    await screen.findByRole('button', { name: 'Reload settings' })
  )
  await user.click(
    within(await screen.findByRole('alertdialog')).getByRole('button', {
      name: 'Cancel',
    })
  )
  expect(input).toHaveValue('contest keep draft')
  let finishLoading!: () => void
  const pending = new Promise<void>((resolve) => {
    finishLoading = resolve
  })
  const originalGet = page.get.getMockImplementation()
  if (!originalGet) throw new Error('Risk API fixture is missing')
  page.get.mockImplementation(async (...args) => {
    await pending
    return originalGet(...args)
  })
  await user.click(screen.getByRole('button', { name: 'Reload settings' }))
  await user.click(
    within(await screen.findByRole('alertdialog')).getByRole('button', {
      name: 'Continue',
    })
  )
  expect(input).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Save and enable' })).toBeDisabled()
  finishLoading()
  await waitFor(() =>
    expect(screen.getByRole('textbox', { name: 'Contest regex' })).toHaveValue(
      'contest'
    )
  )
})
