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
export type Mode = 'observe' | 'enforce'
export type Binding = {
  policy_key: string
  policy_revision: number
  mode: Mode
}
export type Config = {
  enabled: boolean
  exempt_namespaces: string[]
  bypass_namespaces: string[]
  default_binding: Binding
  group_bindings: (Binding & { namespace: string })[]
  retention: { events_days: number; counters_days: number }
}
export type ConfigView = { revision: number; payload: Config }
export type Group = Binding & {
  group_name: string
  namespace: string
  generation: number
  exempt: boolean
  bypassed: boolean
  binding_source: string
}
export type Directory = {
  groups: Group[]
  invalid_refs: { namespace: string; group_name: string; kind: string }[]
  unmapped: string[]
}
export type Question = {
  id: string
  label: string
  summary: string
  instructions: string
  enabled: boolean
  builtin: boolean
}
export type HardRule = {
  category: string
  source: string
  enabled: boolean
  action?: 'block' | 'review'
}
export type Policy = import('zod').infer<
  typeof import('./lib/schema').policySchema
>
export type PolicyRow = {
  config_revision?: number
  id: number
  policy_key: string
  revision: number
  name: string
  payload: string
}
export type ProviderInput = {
  name: string
  provider_type: string
  base_url: string
  model: string
  proxy: string
  api_keys?: string[]
}
export type Provider = Omit<ProviderInput, 'api_keys'> & {
  id: number
  api_key_count: number
  key_states: { index: number; state: string; cooldown_until: number }[]
}
export type Defaults = {
  policy_save_activation?: boolean
  policy: Policy
  config: Config
  builtin_rules: HardRule[]
  builtin_questions: Question[]
  providers: {
    id: string
    label: string
    default_base_url: string
    default_model: string
  }[]
  covered_routes: string[]
  uncovered_routes: string[]
}
export type Counter = {
  outcome: string
  layer: string
  reason: string
  count: number
}
export type Overview = {
  start?: number
  end?: number
  enabled: boolean
  fresh: boolean
  available: boolean
  config_revision: number
  requests: number
  attempts: number
  outcomes: Record<string, number>
  blocks: Counter[]
  passes: Counter[]
  keywords: { layer: string; keyword: string; count: number }[]
  stages: Record<string, string>
  uncovered_routes: string[]
}
export type RiskEvent = {
  id: number
  request_id: string
  created_at: number
  user_id: number
  token_id: number
  effective_group: string
  user_group: string
  token_group: string
  group_source: string
  namespace: string
  attempt: number
  channel_id: number
  model: string
  route: string
  mode: string
  config_revision: number
  policy_key: string
  policy_revision: number
  outcome: string
  layer: string
  reason: string
  category: string
  keyword: string
  rule: string
  scores: string
  provider_model: string
  latency_ms: number
  coverage: string
  error_code: string
  preview: string
}
export type MemoryRow = {
  id: number
  namespace: string
  user_id: number
  model: string
  preview: string
  error_message: string
  subject_type: string
  subject_value: string
  reason: string
  hit_count: number
  expires_at: number
  released_at: number
  source_request_id: string
}
export type Page<T> = {
  items: T[]
  total: number
  page: number
  page_size: number
}
export type RiskReview = {
  id: number
  request_id: string
  created_at: number
  namespace: string
  group_name: string
  user_id: number
  mode: Mode
  layer: string
  category: string
  reason: string
  model: string
  provider_model: string
  scores: string
  policy_key: string
  policy_revision: number
  fingerprint: string
  evidence: string
  evidence_truncated: boolean
  coverage: string
  status: 'pending' | 'confirmed' | 'dismissed' | 'revoked'
  points: number
  revision: number
  reviewed_at: number
  reviewed_by: number
  review_note: string
  history: string
}
export type RiskReviewDetail = {
  review: RiskReview
  score: {
    score: number
    confirmed_events: number
    window_minutes: number
    threshold: number
    block_ttl_minutes: number
  }
  block?: MemoryRow | null
  behavior_enabled: boolean
  auto_block_enabled: boolean
  mode: Mode
}
export type PreviewResult = {
  enabled: boolean
  audited: boolean
  exempt: boolean
  bypassed: boolean
  namespace: string
  binding_source: string
  policy_key: string
  policy_revision: number
  mode: string
  model_run: boolean
  decision: {
    outcome: string
    layer: string
    reason: string
    coverage: string
    scores: Record<string, number>
    keyword: string
    rule: string
  }
}
