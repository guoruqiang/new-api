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

import { PolicySettings } from '../components/policy-settings'
import { ProviderCard } from '../components/provider-card'
import { policySchema } from '../lib/schema'
import type { Policy } from '../types'
import { config, defaults, policy, row, renderRisk } from './fixtures'

it('requires a configured provider before an enabled model can be saved', async () => {
  const write = vi.spyOn(api, 'request')
  const user = userEvent.setup()
  renderRisk(
    <PolicySettings
      config={config}
      row={row}
      initial={policy}
      defaults={defaults}
      providers={[]}
      view='model'
      canWrite
      onSaved={() => {}}
    />
  )
  await user.click(
    screen.getByRole('switch', { name: 'Enable decision model' })
  )
  await user.click(screen.getByRole('button', { name: 'Save settings' }))
  expect(await screen.findByRole('alert')).toHaveTextContent(
    'Invalid risk audit settings'
  )
  expect(write).not.toHaveBeenCalled()
})

it('saves settings in one request while preserving the global switch and zero cache duration', async () => {
  const write = vi.spyOn(api, 'request').mockResolvedValue({
    data: { success: true, data: { ...row, revision: 2, config_revision: 4 } },
  })
  const user = userEvent.setup()
  const { client } = renderRisk(
    <PolicySettings
      config={config}
      row={row}
      initial={policy}
      defaults={defaults}
      providers={[]}
      view='model'
      canWrite
      onSaved={() => {}}
    />
  )
  await user.clear(
    screen.getByRole('spinbutton', { name: 'Cache duration (seconds)' })
  )
  await user.type(
    screen.getByRole('spinbutton', { name: 'Cache duration (seconds)' }),
    '0'
  )
  await user.click(screen.getByRole('button', { name: 'Save settings' }))
  await waitFor(() =>
    expect(write).toHaveBeenCalledWith(
      expect.objectContaining({
        url: '/api/risk/policies',
        data: expect.objectContaining({
          base_revision: 1,
          activation: { expected_config_revision: 3 },
          payload: expect.objectContaining({
            model: expect.objectContaining({ dedup_sec: 0 }),
          }),
        }),
      })
    )
  )
  expect(write).toHaveBeenCalledTimes(1)
  await waitFor(() =>
    expect(client.getQueryData(['risk', 'config'])).toMatchObject({
      revision: 4,
      payload: { enabled: false },
    })
  )
})

it('preserves the draft on a configuration conflict and only reloads on request', async () => {
  const write = vi.spyOn(api, 'request').mockRejectedValue({
    response: { status: 409, data: { message: 'Conflict' } },
  })
  const reload = vi.fn()
  const user = userEvent.setup()
  renderRisk(
    <PolicySettings
      config={config}
      row={row}
      initial={policy}
      defaults={defaults}
      providers={[]}
      view='model'
      canWrite
      onSaved={() => {}}
      onReload={reload}
    />
  )
  const input = screen.getByRole('textbox', {
    name: 'Additional safety question',
  })
  await user.type(input, 'Keep this question')
  await user.click(screen.getByRole('button', { name: 'Save settings' }))
  expect(await screen.findByText('Conflict')).toBeVisible()
  expect(screen.getByText(/The risk audit settings changed/)).toBeVisible()
  expect(input).toHaveValue('Keep this question')
  expect(write).toHaveBeenCalledTimes(1)
  expect(reload).not.toHaveBeenCalled()
  expect(
    screen.queryByText(/A newer revision is saved/)
  ).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Reload settings' }))
  expect(reload).toHaveBeenCalledTimes(1)
  expect(input).toHaveValue('Keep this question')
})

it('loads the saved character window and submits edits using max_audit_chars', async () => {
  const write = vi.spyOn(api, 'request').mockResolvedValue({
    data: { success: true, data: { ...row, revision: 2, config_revision: 4 } },
  })
  const user = userEvent.setup()
  renderRisk(
    <PolicySettings
      config={config}
      row={row}
      initial={policy}
      defaults={defaults}
      providers={[]}
      view='model'
      canWrite
      onSaved={() => {}}
    />
  )
  const window = screen.getByRole('spinbutton', {
    name: 'Audit window characters',
  })
  expect(window).toHaveValue(16000)
  expect(window).toHaveAttribute('min', '256')
  expect(window).toHaveAttribute('max', '64000')
  expect(window).toHaveAttribute('step', '1')
  await user.clear(window)
  await user.type(window, '32000')
  await user.click(screen.getByRole('button', { name: 'Save settings' }))
  await waitFor(() =>
    expect(write).toHaveBeenCalledWith(
      expect.objectContaining({
        url: '/api/risk/policies',
        data: expect.objectContaining({
          payload: expect.objectContaining({
            model: { ...policy.model, max_audit_chars: 32000 },
          }),
        }),
      })
    )
  )
})

it.each(['button', 'form'])(
  'saves and enables with one atomic request from the %s',
  async (trigger) => {
    let finish!: (value: unknown) => void
    const write = vi.spyOn(api, 'request').mockImplementation(
      () =>
        new Promise((resolve) => {
          finish = resolve
        })
    )
    const saved = vi.fn()
    const user = userEvent.setup()
    const current = {
      ...config,
      payload: {
        ...config.payload,
        exempt_namespaces: ['ns-b'],
        group_bindings: [
          {
            ...config.payload.default_binding,
            namespace: 'ns-a',
            mode: 'enforce' as const,
          },
        ],
      },
    }
    const { client } = renderRisk(
      <PolicySettings
        config={current}
        row={row}
        initial={policy}
        defaults={defaults}
        providers={[]}
        view='model'
        canWrite
        onSaved={saved}
      />
    )
    const input = screen.getByRole('textbox', {
      name: 'Additional safety question',
    })
    await user.type(input, 'Only safe requests')
    if (trigger === 'button') {
      await user.click(screen.getByRole('button', { name: 'Save and enable' }))
    } else {
      fireEvent.submit(
        screen
          .getByRole('button', { name: 'Save and enable' })
          .closest('form') as HTMLFormElement
      )
    }
    await waitFor(() => expect(write).toHaveBeenCalledTimes(1))
    expect(write.mock.calls[0][0]).toEqual({
      method: 'post',
      url: '/api/risk/policies',
      data: {
        policy_key: 'baseline',
        name: 'Baseline',
        base_revision: 1,
        payload: {
          ...policy,
          model: { ...policy.model, custom_instruction: 'Only safe requests' },
        },
        activation: { expected_config_revision: 3, enable: true },
      },
    })
    expect(input).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Save settings' })).toBeDisabled()
    expect(
      screen.getByRole('button', { name: 'Save and enable' })
    ).toBeDisabled()
    finish({
      data: {
        success: true,
        data: { ...row, revision: 2, config_revision: 4 },
      },
    })
    await waitFor(() => expect(saved).toHaveBeenCalledOnce())
    expect(client.getQueryData(['risk', 'config'])).toEqual({
      revision: 4,
      payload: {
        ...current.payload,
        enabled: true,
        default_binding: {
          ...current.payload.default_binding,
          policy_revision: 2,
        },
        group_bindings: [
          { ...current.payload.group_bindings[0], policy_revision: 2 },
        ],
      },
    })
    expect(screen.queryByText(/v1/)).not.toBeInTheDocument()
  }
)

it('enables unchanged saved settings without requiring a policy edit', async () => {
  const write = vi.spyOn(api, 'request').mockResolvedValue({
    data: { success: true, data: { ...row, config_revision: 4 } },
  })
  const user = userEvent.setup()
  renderRisk(
    <PolicySettings
      config={config}
      row={row}
      initial={policy}
      defaults={defaults}
      providers={[]}
      view='model'
      canWrite
      onSaved={() => {}}
    />
  )
  expect(screen.getByRole('button', { name: 'Save settings' })).toBeDisabled()
  await user.click(screen.getByRole('button', { name: 'Save and enable' }))
  await waitFor(() => expect(write).toHaveBeenCalledOnce())
  expect(write.mock.calls[0][0].data).toMatchObject({
    payload: policy,
    activation: { expected_config_revision: 3, enable: true },
  })
})

it('disables both saves while group configuration is unsaved and preserves policy edits', async () => {
  const write = vi.spyOn(api, 'request')
  const user = userEvent.setup()
  renderRisk(
    <PolicySettings
      config={config}
      activationBlocked
      row={row}
      initial={policy}
      defaults={defaults}
      providers={[]}
      view='model'
      canWrite
      onSaved={() => {}}
    />
  )
  const input = screen.getByRole('textbox', {
    name: 'Additional safety question',
  })
  await user.type(input, 'Keep policy draft')
  expect(screen.getByRole('button', { name: 'Save settings' })).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Save and enable' })).toBeDisabled()
  expect(
    screen.getByText(
      'Save the group configuration above before saving these settings.'
    )
  ).toBeVisible()
  fireEvent.submit(
    screen
      .getByRole('button', { name: 'Save and enable' })
      .closest('form') as HTMLFormElement
  )
  await waitFor(() => expect(input).toHaveValue('Keep policy draft'))
  expect(write).not.toHaveBeenCalled()
})

it('allows confirming unchanged active settings without creating a new revision', async () => {
  const write = vi.spyOn(api, 'request').mockResolvedValue({
    data: { success: true, data: { ...row, config_revision: config.revision } },
  })
  const saved = vi.fn()
  const user = userEvent.setup()
  renderRisk(
    <PolicySettings
      config={{ ...config, payload: { ...config.payload, enabled: true } }}
      row={row}
      initial={policy}
      defaults={defaults}
      providers={[]}
      view='model'
      canWrite
      onSaved={saved}
    />
  )
  expect(screen.getByRole('button', { name: 'Save settings' })).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Save and enable' })).toBeEnabled()
  await user.click(screen.getByRole('button', { name: 'Save and enable' }))
  await waitFor(() =>
    expect(saved).toHaveBeenCalledWith({
      ...row,
      config_revision: config.revision,
    })
  )
  expect(write).toHaveBeenCalledOnce()
  expect(write.mock.calls[0][0].data).toMatchObject({
    payload: policy,
    activation: { expected_config_revision: config.revision, enable: true },
  })
})

it('keeps edits available but prevents saving when the backend does not support atomic activation', async () => {
  const write = vi.spyOn(api, 'request')
  const user = userEvent.setup()
  renderRisk(
    <PolicySettings
      config={config}
      row={row}
      initial={policy}
      defaults={{ ...defaults, policy_save_activation: undefined }}
      providers={[]}
      view='model'
      canWrite
      onSaved={() => {}}
    />
  )
  const input = screen.getByRole('textbox', {
    name: 'Additional safety question',
  })
  await user.type(input, 'Keep this draft for the updated backend')
  expect(screen.getByRole('button', { name: 'Save settings' })).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Save and enable' })).toBeDisabled()
  expect(
    screen.getByText(
      'Restart the updated backend to save and enable risk audit settings.'
    )
  ).toBeVisible()
  expect(input).toHaveValue('Keep this draft for the updated backend')
  expect(write).not.toHaveBeenCalled()
})

it('preserves the draft without reporting success when the response lacks activation confirmation', async () => {
  vi.spyOn(api, 'request').mockResolvedValue({
    data: { success: true, data: { ...row, revision: 2 } },
  })
  const saved = vi.fn()
  const user = userEvent.setup()
  const { client } = renderRisk(
    <PolicySettings
      config={config}
      row={row}
      initial={policy}
      defaults={defaults}
      providers={[]}
      view='model'
      canWrite
      onSaved={saved}
    />
  )
  const input = screen.getByRole('textbox', {
    name: 'Additional safety question',
  })
  await user.type(input, 'Keep this draft without confirmation')
  await user.click(screen.getByRole('button', { name: 'Save and enable' }))
  expect(
    await screen.findByText(
      'Restart the updated backend to save and enable risk audit settings.'
    )
  ).toBeVisible()
  expect(input).toHaveValue('Keep this draft without confirmation')
  expect(saved).not.toHaveBeenCalled()
  expect(client.getQueryData(['risk', 'config'])).toBeUndefined()
})

it('keeps stored provider keys when saving ordinary settings and only clears on explicit replacement', async () => {
  const provider = {
    id: 1,
    name: 'Moderation',
    provider_type: 'jev',
    base_url: 'https://api.example.test',
    model: 'safe',
    proxy: '',
    api_key_count: 2,
    key_states: [],
  }
  const write = vi
    .spyOn(api, 'request')
    .mockResolvedValue({ data: { success: true, data: provider } })
  const user = userEvent.setup()
  renderRisk(
    <ProviderCard
      provider={provider}
      presets={defaults.providers}
      canWrite
      onSaved={() => {}}
    />
  )
  expect(
    screen.queryByRole('textbox', { name: 'API keys (one per line)' })
  ).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Save provider' }))
  await waitFor(() => expect(write).toHaveBeenCalledTimes(1))
  expect(write.mock.calls[0][0].data).not.toHaveProperty('api_keys')
  await user.click(screen.getByRole('switch', { name: 'Replace stored keys' }))
  await user.click(screen.getByRole('button', { name: 'Save provider' }))
  await waitFor(() => expect(write).toHaveBeenCalledTimes(2))
  expect(write.mock.calls[1][0].data).toHaveProperty('api_keys', [])
})

it('rejects invalid scores and duplicate question identifiers before publication', () => {
  expect(
    policySchema.safeParse({
      ...policy,
      model: { ...policy.model, threshold: 1.1 },
    }).success
  ).toBe(false)
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
})

it('preserves commas in regex patterns entered with the keyboard', async () => {
  const write = vi.spyOn(api, 'request').mockResolvedValue({
    data: { success: true, data: { ...row, revision: 2, config_revision: 4 } },
  })
  const user = userEvent.setup()
  renderRisk(
    <PolicySettings
      config={config}
      row={row}
      initial={policy}
      defaults={defaults}
      providers={[]}
      view='hard_regex'
      canWrite
      onSaved={() => {}}
    />
  )
  await user.type(screen.getByPlaceholderText('Add tags...'), 'a{{1,3}{Enter}')
  await user.click(screen.getByRole('button', { name: 'Save settings' }))
  await waitFor(() =>
    expect(write).toHaveBeenCalledWith(
      expect.objectContaining({
        data: expect.objectContaining({
          payload: expect.objectContaining({
            hard_regex: expect.objectContaining({ patterns: ['a{1,3}'] }),
          }),
        }),
      })
    )
  )
})

it('saves the chosen rule handling and adds new rules for model review', async () => {
  const write = vi.spyOn(api, 'request').mockResolvedValue({
    data: { success: true, data: { ...row, revision: 2, config_revision: 4 } },
  })
  const user = userEvent.setup()
  renderRisk(
    <PolicySettings
      config={config}
      row={row}
      initial={{
        ...policy,
        hard_regex: {
          ...policy.hard_regex,
          rules: [{ ...defaults.builtin_rules[0], source: 'custom-pattern' }],
        },
      }}
      defaults={defaults}
      providers={[]}
      view='hard_regex'
      canWrite
      onSaved={() => {}}
    />
  )
  const handling = screen.getByRole('combobox', { name: 'Handling' })
  expect(handling).toHaveValue('review')
  await user.selectOptions(handling, 'block')
  await user.click(screen.getByRole('button', { name: 'Add rule' }))
  expect(screen.getAllByRole('combobox', { name: 'Handling' })[1]).toHaveValue(
    'review'
  )
  await user.type(screen.getAllByRole('textbox', { name: 'Regex' })[1], 'new')
  await user.click(screen.getByRole('button', { name: 'Save settings' }))
  await waitFor(() =>
    expect(write).toHaveBeenCalledWith(
      expect.objectContaining({
        data: expect.objectContaining({
          payload: expect.objectContaining({
            hard_regex: expect.objectContaining({
              rules: [
                {
                  ...defaults.builtin_rules[0],
                  source: 'custom-pattern',
                  action: 'block',
                },
                {
                  category: 'custom',
                  source: 'new',
                  enabled: true,
                  action: 'review',
                },
              ],
            }),
          }),
        }),
      })
    )
  )
})

it('locks exact built-in rule handling to its default while custom patterns and categories stay editable', () => {
  const distillRule = {
    category: 'distill',
    source: 'distill-pattern',
    enabled: true,
    action: 'review' as const,
  }
  renderRisk(
    <PolicySettings
      config={config}
      row={row}
      initial={{
        ...policy,
        hard_regex: {
          ...policy.hard_regex,
          rules: [
            { ...distillRule, source: ' distill-pattern ', action: 'block' },
            { ...distillRule, category: 'custom', action: 'block' },
            { ...distillRule, source: 'custom-pattern', action: 'block' },
            { ...defaults.builtin_rules[0], action: 'block' },
          ],
        },
      }}
      defaults={{
        ...defaults,
        builtin_rules: [...defaults.builtin_rules, distillRule],
      }}
      providers={[]}
      view='hard_regex'
      canWrite
      onSaved={() => {}}
    />
  )
  const handling = screen.getAllByRole('combobox', { name: 'Handling' })
  expect(handling).toHaveLength(4)
  expect(handling[0]).toHaveValue('review')
  expect(handling[0]).toBeDisabled()
  expect(handling[1]).toHaveValue('block')
  expect(handling[1]).toBeEnabled()
  expect(handling[2]).toHaveValue('block')
  expect(handling[2]).toBeEnabled()
  expect(handling[3]).toHaveValue('review')
  expect(handling[3]).toBeDisabled()
})

it('rejects behavior score values outside the server bounds and non-integer values', () => {
  for (const patch of [
    { threshold: 0 },
    { threshold: 101 },
    { threshold: 2.5 },
    { window_minutes: 0 },
    { window_minutes: 1441 },
    { block_ttl_minutes: 0 },
    { block_ttl_minutes: 1441 },
    { block_ttl_minutes: Number.NaN },
  ]) {
    expect(
      policySchema.safeParse({
        ...policy,
        behavior: { ...policy.behavior, ...patch },
      }).success
    ).toBe(false)
  }
})

it('loads old policies with recording defaults and saves behavior settings independently of legacy hit thresholds', async () => {
  const write = vi.spyOn(api, 'request').mockResolvedValue({
    data: {
      success: true,
      data: { ...row, revision: 2, config_revision: 4 },
    },
  })
  const legacy = structuredClone(policy)
  delete (legacy as Partial<Policy>).behavior
  delete (legacy as Partial<Policy>).screening
  delete (legacy.model as Partial<Policy['model']>).auto_confirm_threshold
  legacy.model.threshold = 0.1
  delete (legacy.evidence as Partial<Policy['evidence']>).store_review_evidence
  const user = userEvent.setup()
  renderRisk(
    <PolicySettings
      config={config}
      row={row}
      initial={legacy}
      defaults={defaults}
      providers={[]}
      view='refusal'
      canWrite
      onSaved={() => {}}
    />
  )
  expect(
    screen.queryByRole('spinbutton', { name: 'Hits before blocking' })
  ).not.toBeInTheDocument()
  expect(
    screen.getByRole('switch', {
      name: 'Automatically suspend the offending API token',
    })
  ).not.toBeChecked()
  expect(
    screen.getByRole('switch', { name: 'Store review evidence' })
  ).toBeChecked()
  expect(
    screen.getByRole('spinbutton', {
      name: 'Screening points required for model review',
    })
  ).toHaveValue(3)
  expect(
    screen.getByRole('spinbutton', { name: 'Screening window (minutes)' })
  ).toHaveValue(10)
  expect(
    screen.getByRole('spinbutton', { name: 'Score window (minutes)' })
  ).toHaveValue(10)
  const threshold = screen.getByRole('spinbutton', {
    name: 'Confirmed violation points for suspension',
  })
  expect(threshold).toHaveValue(6)
  expect(threshold).toHaveAttribute('min', '1')
  expect(threshold).toHaveAttribute('max', '100')
  expect(threshold).toHaveAttribute('step', '1')
  await user.clear(threshold)
  await user.type(threshold, '8')
  await user.click(
    screen.getByRole('switch', { name: 'Store review evidence' })
  )
  await user.click(screen.getByRole('button', { name: 'Save settings' }))
  await waitFor(() =>
    expect(write).toHaveBeenCalledWith(
      expect.objectContaining({
        data: expect.objectContaining({
          payload: expect.objectContaining({
            behavior: { ...policy.behavior, threshold: 8 },
            model: { ...legacy.model, auto_confirm_threshold: 0.1 },
            evidence: { store_preview: true, store_review_evidence: false },
            refusal: policy.refusal,
          }),
        }),
      })
    )
  )
})

it('saves screening and high-confidence confirmation settings separately from penalty points', async () => {
  const write = vi.spyOn(api, 'request').mockResolvedValue({
    data: {
      success: true,
      data: { ...row, revision: 2, config_revision: 4 },
    },
  })
  const user = userEvent.setup()
  renderRisk(
    <PolicySettings
      config={config}
      row={row}
      initial={policy}
      defaults={defaults}
      providers={[]}
      view='model'
      canWrite
      onSaved={() => {}}
    />
  )
  const screening = screen.getByRole('spinbutton', {
    name: 'Screening points required for model review',
  })
  await user.clear(screening)
  await user.type(screening, '5')
  await user.click(
    screen.getByRole('switch', {
      name: 'Automatically confirm high-confidence model violations',
    })
  )
  const threshold = screen.getByRole('spinbutton', {
    name: 'Automatic confirmation safety threshold',
  })
  expect(threshold).toHaveValue(0.15)
  await user.clear(threshold)
  await user.type(threshold, '0.1')
  await user.click(screen.getByRole('button', { name: 'Save settings' }))
  await waitFor(() =>
    expect(write).toHaveBeenCalledWith(
      expect.objectContaining({
        data: expect.objectContaining({
          payload: expect.objectContaining({
            screening: { threshold: 5, window_minutes: 10 },
            behavior: { ...policy.behavior, auto_confirm_enabled: true },
            model: { ...policy.model, auto_confirm_threshold: 0.1 },
          }),
        }),
      })
    )
  )
})

it('validates screening integers, automatic confirmation bounds and the signal rule action', () => {
  for (const patch of [
    { threshold: 0 },
    { threshold: 101 },
    { threshold: 2.5 },
    { window_minutes: 0 },
    { window_minutes: 1441 },
  ]) {
    expect(
      policySchema.safeParse({
        ...policy,
        screening: { ...policy.screening, ...patch },
      }).success
    ).toBe(false)
  }
  for (const auto_confirm_threshold of [-0.1, 1.1, 0.6]) {
    expect(
      policySchema.safeParse({
        ...policy,
        model: { ...policy.model, auto_confirm_threshold },
      }).success
    ).toBe(false)
  }
  expect(
    policySchema.safeParse({
      ...policy,
      hard_regex: {
        ...policy.hard_regex,
        rules: [{ ...defaults.builtin_rules[0], action: 'signal' }],
      },
    }).success
  ).toBe(true)
})

it('matches server numeric bounds and counts Unicode code points for question limits', () => {
  for (const patch of [
    { budget_ms: 199 },
    { budget_ms: 8001 },
    { max_attempts: 11 },
    { dedup_sec: 3601 },
    { max_audit_chars: 255 },
    { max_audit_chars: 64001 },
    { max_audit_chars: 256.5 },
    { max_audit_chars: Number.NaN },
    { custom_instruction: 'a'.repeat(501) },
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
      model: { ...policy.model, custom_instruction: '😀'.repeat(500) },
    }).success
  ).toBe(true)
  for (const max_audit_chars of [256, 64000]) {
    expect(
      policySchema.safeParse({
        ...policy,
        model: { ...policy.model, max_audit_chars },
      }).success
    ).toBe(true)
  }
})

it('keeps long distillation lists collapsed and preserves edits after closing and reopening', async () => {
  const user = userEvent.setup()
  renderRisk(
    <PolicySettings
      config={config}
      row={row}
      initial={policy}
      defaults={defaults}
      providers={[]}
      view='distill'
      canWrite
      onSaved={() => {}}
    />
  )
  const rules = screen.getByRole('group', { name: 'Rules' })
  const trigger = within(rules).getByRole('button', {
    name: /Harvest wrappers/,
  })
  expect(trigger).toHaveAttribute('aria-expanded', 'false')
  trigger.focus()
  await user.keyboard('{Enter}')
  expect(trigger).toHaveAttribute('aria-expanded', 'true')
  await user.type(
    within(rules).getByPlaceholderText('Add tags...'),
    'harvest-wrapper{Enter}'
  )
  await user.click(trigger)
  await user.click(trigger)
  expect(within(rules).getByText('harvest-wrapper')).toBeVisible()
  expect(screen.getByRole('button', { name: 'Save settings' })).toBeEnabled()
})

it('allows read-only operators to expand rule lists without editing policy controls', async () => {
  const user = userEvent.setup()
  renderRisk(
    <PolicySettings
      config={config}
      row={row}
      initial={policy}
      defaults={defaults}
      providers={[]}
      view='distill'
      canWrite={false}
      onSaved={() => {}}
    />
  )
  const trigger = screen.getByRole('button', { name: /Harvest wrappers/ })
  await user.click(trigger)
  expect(trigger).toHaveAttribute('aria-expanded', 'true')
  expect(screen.getByPlaceholderText('Add tags...')).toBeDisabled()
  expect(screen.getByRole('textbox', { name: 'Block message' })).toBeDisabled()
  expect(
    screen.getByRole('spinbutton', { name: 'Block HTTP status' })
  ).toBeDisabled()
  expect(
    screen.queryByRole('button', { name: 'Save settings' })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('button', { name: 'Save and enable' })
  ).not.toBeInTheDocument()
})

it('keeps wide regex controls in a scrollable table and disables mutations for read-only operators', () => {
  renderRisk(
    <PolicySettings
      config={config}
      row={row}
      initial={policy}
      defaults={defaults}
      providers={[]}
      view='hard_regex'
      canWrite={false}
      onSaved={() => {}}
    />
  )
  const table = screen.getByRole('table', { name: 'Rules' })
  expect(table).toHaveClass('min-w-[40rem]')
  expect(table.parentElement).toHaveClass('overflow-x-auto')
  expect(screen.getByRole('textbox', { name: 'Regex' })).toBeDisabled()
  expect(screen.getByRole('combobox', { name: 'Category' })).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Delete' })).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Add rule' })).toBeDisabled()
})
