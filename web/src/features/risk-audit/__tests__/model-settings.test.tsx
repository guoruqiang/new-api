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
import type { ComponentProps } from 'react'
import { expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { PolicySettings } from '../components/policy-settings'
import { ProviderCard } from '../components/provider-card'
import { policySchema } from '../lib/schema'
import type { Policy } from '../types'
import { config, defaults, policy, row, renderRisk } from './fixtures'

const decisionProviders = [
  {
    id: 1,
    name: 'Jev',
    provider_type: 'jev',
    base_url: 'https://api.example.test',
    model: 'jev-latest',
    proxy: '',
    api_key_count: 1,
    key_states: [],
  },
  {
    id: 2,
    name: 'OpenAI Moderation',
    provider_type: 'openai',
    base_url: 'https://api.openai.com',
    model: 'omni-moderation-latest',
    proxy: '',
    api_key_count: 1,
    key_states: [],
  },
]

function renderSettings(
  overrides: Partial<ComponentProps<typeof PolicySettings>> = {}
) {
  return renderRisk(
    <PolicySettings
      config={config}
      row={row}
      initial={policy}
      defaults={defaults}
      providers={[]}
      section='model'
      canWrite
      onSaved={() => {}}
      {...overrides}
    />
  )
}

function mockSave() {
  return vi.spyOn(api, 'request').mockResolvedValue({
    data: { success: true, data: { ...row, revision: 2, config_revision: 4 } },
  })
}

it('offers every model returned by an OpenAI moderation provider without a prefix filter', async () => {
  vi.spyOn(api, 'request').mockResolvedValue({
    data: {
      success: true,
      data: { models: ['omni-moderation-latest', 'custom-moderation'] },
    },
  })
  const user = userEvent.setup()
  renderRisk(
    <ProviderCard
      provider={decisionProviders[1]}
      presets={defaults.providers}
      canWrite
      onSaved={() => {}}
    />
  )
  await user.click(screen.getByRole('button', { name: 'Fetch models' }))
  const models = await screen.findByRole('combobox', {
    name: 'Available models',
  })
  expect(
    within(models)
      .getAllByRole('option')
      .map((option) => option.textContent)
  ).toEqual(['omni-moderation-latest', 'custom-moderation'])
})

it('saves an OpenAI provider with a model override that has no omni-moderation prefix', async () => {
  const write = mockSave()
  const user = userEvent.setup()
  renderSettings({
    providers: decisionProviders,
    initial: {
      ...policy,
      model: { ...policy.model, enabled: true, provider_id: 2, model: 'mine' },
    },
  })
  expect(
    screen.queryByText('OpenAI Moderation requires an omni-moderation model')
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('spinbutton', { name: 'Safety threshold' })
  ).not.toBeInTheDocument()
  expect(screen.queryByText('Question bank')).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Save and enable' }))
  await waitFor(() =>
    expect(write).toHaveBeenCalledWith(
      expect.objectContaining({
        data: expect.objectContaining({
          payload: expect.objectContaining({
            model: expect.objectContaining({ provider_id: 2, model: 'mine' }),
          }),
        }),
      })
    )
  )
})

it('saves edited OpenAI category thresholds and drops a cleared category', async () => {
  const write = mockSave()
  const user = userEvent.setup()
  renderSettings({
    providers: decisionProviders,
    initial: {
      ...policy,
      model: { ...policy.model, enabled: true, provider_id: 2 },
    },
  })
  const sexual = screen.getByRole('spinbutton', { name: 'Sexual' })
  const violence = screen.getByRole('spinbutton', { name: 'Violence' })
  expect(sexual).toHaveValue(0.2)
  expect(violence).toHaveValue(null)
  await user.clear(sexual)
  fireEvent.change(violence, { target: { value: '0.1' } })
  await user.click(screen.getByRole('button', { name: 'Save and enable' }))
  await waitFor(() => expect(write).toHaveBeenCalledOnce())
  expect(
    (write.mock.calls[0][0].data as { payload: Policy }).payload.model
      .moderation_thresholds
  ).toEqual({ 'sexual/minors': 0.4, violence: 0.1 })
})

it('shows no automatic confirmation, near-miss or custom question fields for the decision model', () => {
  renderSettings({ providers: decisionProviders })
  for (const name of [
    'Automatic confirmation safety threshold',
    'Near-miss tolerance',
  ]) {
    expect(screen.queryByRole('spinbutton', { name })).not.toBeInTheDocument()
  }
  expect(
    screen.queryByRole('textbox', { name: 'Additional safety question' })
  ).not.toBeInTheDocument()
  expect(
    screen.getByRole('spinbutton', { name: 'Safety threshold' })
  ).toHaveValue(0.5)
  expect(screen.getByText('Question bank')).toBeVisible()
})

it('requires a configured provider before an enabled model can be saved', async () => {
  const write = vi.spyOn(api, 'request')
  const user = userEvent.setup()
  renderSettings()
  await user.click(
    screen.getByRole('switch', { name: 'Enable decision model' })
  )
  await user.click(screen.getByRole('button', { name: 'Save settings' }))
  expect(await screen.findByRole('alert')).toHaveTextContent(
    'Invalid risk audit settings'
  )
  expect(write).not.toHaveBeenCalled()
})

it('saves the exact policy payload in one atomic request from the form', async () => {
  const write = mockSave()
  const saved = vi.fn()
  const { client } = renderSettings({ onSaved: saved })
  const cache = screen.getByRole('spinbutton', {
    name: 'Cache duration (seconds)',
  })
  fireEvent.change(cache, { target: { value: '0' } })
  fireEvent.submit(
    screen
      .getByRole('button', { name: 'Save and enable' })
      .closest('form') as HTMLFormElement
  )
  await waitFor(() => expect(saved).toHaveBeenCalledOnce())
  expect(write).toHaveBeenCalledTimes(1)
  expect(write.mock.calls[0][0]).toEqual({
    method: 'post',
    url: '/api/risk/policies',
    data: {
      policy_key: 'baseline',
      name: 'Baseline',
      base_revision: 1,
      payload: { ...policy, model: { ...policy.model, dedup_sec: 0 } },
      activation: { expected_config_revision: 3, enable: true },
    },
  })
  expect(client.getQueryData(['risk', 'config'])).toMatchObject({
    revision: 4,
    payload: { enabled: true, default_binding: { policy_revision: 2 } },
  })
})

it('preserves the draft on a configuration conflict and only reloads on request', async () => {
  vi.spyOn(api, 'request').mockRejectedValue({
    response: { status: 409, data: { message: 'Conflict' } },
  })
  const reload = vi.fn()
  const user = userEvent.setup()
  renderSettings({ onReload: reload })
  const input = screen.getByRole('textbox', { name: 'Model' })
  await user.type(input, 'kept-model')
  await user.click(screen.getByRole('button', { name: 'Save settings' }))
  expect(await screen.findByText(/The risk audit settings changed/)).toBeVisible()
  expect(input).toHaveValue('kept-model')
  expect(reload).not.toHaveBeenCalled()
  await user.click(screen.getByRole('button', { name: 'Reload settings' }))
  expect(reload).toHaveBeenCalledTimes(1)
  expect(input).toHaveValue('kept-model')
})

it('disables both saves while the scope configuration has unsaved changes', async () => {
  const write = vi.spyOn(api, 'request')
  const user = userEvent.setup()
  renderSettings({ activationBlocked: true })
  await user.type(screen.getByRole('textbox', { name: 'Model' }), 'draft')
  expect(screen.getByRole('button', { name: 'Save settings' })).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Save and enable' })).toBeDisabled()
  expect(
    screen.getByText(
      'Save the scope and scoring configuration before saving these settings.'
    )
  ).toBeVisible()
  expect(write).not.toHaveBeenCalled()
})

it('prevents saving when the backend does not confirm atomic activation', async () => {
  vi.spyOn(api, 'request').mockResolvedValue({
    data: { success: true, data: { ...row, revision: 2 } },
  })
  const saved = vi.fn()
  const user = userEvent.setup()
  renderSettings({ onSaved: saved })
  await user.click(screen.getByRole('button', { name: 'Save and enable' }))
  expect(
    await screen.findByText(
      'Restart the updated backend to save and enable risk audit settings.'
    )
  ).toBeVisible()
  expect(saved).not.toHaveBeenCalled()
})

it('shows keyword rules without a handling column and saves new rules without an action', async () => {
  const write = mockSave()
  const user = userEvent.setup()
  renderSettings({ section: 'keyword' })
  expect(
    screen.queryByRole('columnheader', { name: 'Handling' })
  ).not.toBeInTheDocument()
  expect(
    screen.getByText(
      'Every rule match selects the request for model review. Without an available decision model, matches alone never confirm a violation or deduct points.'
    )
  ).toBeVisible()
  expect(
    within(screen.getByRole('combobox', { name: 'Category' }))
      .getAllByRole('option')
      .map((option) => option.getAttribute('value'))
  ).toEqual(defaults.rule_categories)
  await user.click(screen.getByRole('button', { name: 'Add rule' }))
  await user.type(screen.getAllByRole('textbox', { name: 'Regex' })[1], 'new')
  await user.type(screen.getByPlaceholderText('Add tags...'), 'a{{1,3}{Enter}')
  await user.click(screen.getByRole('button', { name: 'Save settings' }))
  await waitFor(() => expect(write).toHaveBeenCalledOnce())
  expect(
    (write.mock.calls[0][0].data as { payload: Policy }).payload.hard_regex
  ).toEqual({
    ...policy.hard_regex,
    rules: [
      ...defaults.builtin_rules,
      { category: 'custom', source: 'new', enabled: true },
    ],
    patterns: ['a{1,3}'],
  })
})

it('keeps the distillation view free of client response fields and preserves list edits', async () => {
  const user = userEvent.setup()
  renderSettings({ section: 'distill' })
  expect(
    screen.queryByRole('textbox', { name: 'Block message' })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('spinbutton', { name: 'Block HTTP status' })
  ).not.toBeInTheDocument()
  const rules = screen.getByRole('group', { name: 'Rules' })
  const trigger = within(rules).getByRole('button', {
    name: /Harvest wrappers/,
  })
  expect(trigger).toHaveAttribute('aria-expanded', 'false')
  await user.click(trigger)
  await user.type(
    within(rules).getByPlaceholderText('Add tags...'),
    'harvest-wrapper{Enter}'
  )
  await user.click(trigger)
  await user.click(trigger)
  expect(within(rules).getByText('harvest-wrapper')).toBeVisible()
  expect(screen.getByRole('button', { name: 'Save settings' })).toBeEnabled()
})

it('disables keyword rule mutations for read-only operators', () => {
  renderSettings({ section: 'keyword', canWrite: false })
  expect(screen.getByRole('textbox', { name: 'Regex' })).toBeDisabled()
  expect(screen.getByRole('combobox', { name: 'Category' })).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Delete' })).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Add rule' })).toBeDisabled()
  expect(
    screen.queryByRole('button', { name: 'Save settings' })
  ).not.toBeInTheDocument()
})

it('matches server bounds, strips removed fields and counts Unicode code points', () => {
  for (const patch of [
    { threshold: 1.1 },
    { moderation_thresholds: { sexual: 1.1 } },
    { budget_ms: 199 },
    { max_attempts: 11 },
    { dedup_sec: 3601 },
    { max_audit_chars: 255 },
    { max_audit_chars: Number.NaN },
  ]) {
    expect(
      policySchema.safeParse({
        ...policy,
        model: { ...policy.model, ...patch },
      }).success
    ).toBe(false)
  }
  expect(
    policySchema.safeParse({
      ...policy,
      model: {
        ...policy.model,
        questions: [
          defaults.builtin_questions[0],
          defaults.builtin_questions[0],
        ],
      },
    }).success
  ).toBe(false)
  const parsed = policySchema.parse({
    ...policy,
    refusal: { enabled: true },
    behavior: { enabled: true },
    evidence: { store_preview: true },
    model: { ...policy.model, auto_confirm_threshold: 0.1 },
    hard_regex: {
      ...policy.hard_regex,
      rules: [{ ...defaults.builtin_rules[0], action: 'block' }],
    },
  })
  expect(parsed).toEqual({
    ...policy,
    hard_regex: { ...policy.hard_regex, rules: defaults.builtin_rules },
  })
  expect(
    policySchema.safeParse({
      ...policy,
      hard_regex: {
        ...policy.hard_regex,
        rules: [{ ...defaults.builtin_rules[0], source: '😀'.repeat(400) }],
      },
    }).success
  ).toBe(true)
})
