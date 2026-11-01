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
import { render } from '@testing-library/react'
import type { ReactNode } from 'react'

import type { ConfigView, Defaults, Directory, Policy } from '../types'

export const policy: Policy = {
  skip_gpt_models: false,
  distill: {
    enabled: true,
    skip_official: true,
    skip_gpt_models: true,
    structure: {
      min_max_tokens: 8192,
      require_no_tools: true,
      require_single_turn: true,
      contest_pattern: 'contest',
    },
    hard_needles: [],
    needles: [],
    patterns: [],
    fingerprints: [],
  },
  hard_regex: {
    enabled: true,
    strip_reminders: true,
    expand_base64: true,
    rules: null,
    patterns: [],
  },
  model: {
    enabled: false,
    provider_id: 0,
    model: '',
    questions: null,
    threshold: 0.5,
    moderation_thresholds: { sexual: 0.2, 'sexual/minors': 0.4 },
    fail_action: 'pass',
    budget_ms: 2000,
    max_attempts: 2,
    dedup_sec: 60,
    max_audit_chars: 16000,
  },
}
export const defaults: Defaults = {
  policy_save_activation: true,
  policy,
  config: {
    enabled: false,
    exempt_namespaces: [],
    default_binding: {
      policy_key: 'baseline',
      policy_revision: 1,
      mode: 'observe',
    },
    group_bindings: [],
    retention: { events_days: 17, counters_days: 123, violations_days: 180 },
    score: {
      auto_ban: false,
      email_notify: true,
      default: 20,
      deductions: { nsfw: 100, jailbreak: 100 },
    },
  },
  rule_categories: ['nsfw', 'distill', 'crack', 'jailbreak', 'custom'],
  score_categories: [
    'nsfw',
    'jailbreak',
    'reverse',
    'pentest',
    'cyber',
    'distill',
    'safety',
    'custom',
  ],
  builtin_questions: [
    {
      id: 'safety',
      label: 'Safety',
      summary: '',
      instructions: 'Is this safe?',
      enabled: true,
      builtin: true,
    },
  ],
  builtin_rules: [
    { category: 'custom', source: 'malicious', enabled: true },
  ],
  providers: [
    {
      id: 'jev',
      label: 'Jev',
      default_base_url: 'https://api.example.test',
      default_model: 'safety-model',
    },
    {
      id: 'openai',
      label: 'OpenAI Moderation',
      default_base_url: 'https://api.openai.com',
      default_model: 'omni-moderation-latest',
    },
  ],
  covered_routes: ['responses'],
  uncovered_routes: ['images'],
}
export const config: ConfigView = { revision: 3, payload: defaults.config }
export const directory: Directory = {
  groups: [
    {
      group_name: 'standard',
      namespace: 'ns-a',
      generation: 1,
      exempt: false,
      binding_source: 'default',
      ...defaults.config.default_binding,
    },
  ],
  invalid_refs: [],
  unmapped: [],
}
export const row = {
  id: 1,
  policy_key: 'baseline',
  revision: 1,
  name: 'Baseline',
  payload: JSON.stringify(policy),
}

export function renderRisk(ui: ReactNode) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return {
    client,
    ...render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>),
  }
}
