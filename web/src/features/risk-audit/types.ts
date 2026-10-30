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
export type ScoreConfig = {
  auto_ban: boolean
  email_notify: boolean
  default: number
  deductions: Record<string, number>
}
export type Config = {
  enabled: boolean
  exempt_namespaces: string[]
  default_binding: Binding
  group_bindings: (Binding & { namespace: string })[]
  retention: {
    events_days: number
    counters_days: number
    violations_days: number
  }
  score: ScoreConfig
}
export type ConfigView = { revision: number; payload: Config }
export type Group = Binding & {
  group_name: string
  namespace: string
  generation: number
  exempt: boolean
  binding_source: 'exempt' | 'group' | 'default'
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
  rule_categories: string[]
  score_categories: string[]
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
  pending_reviews: number
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
  error_code: string
}
export type Page<T> = {
  items: T[]
  total: number
  page: number
  page_size: number
}
export type ViolationStatus = 'pending' | 'upheld' | 'revoked'
export type Violation = {
  id: number
  request_id: string
  content_hash: string
  user_id: number
  token_id: number
  group_name: string
  namespace: string
  mode: Mode
  model: string
  category: string
  keyword: string
  rule: string
  provider_model: string
  scores: string
  deduction: number
  score_before: number
  score_after: number
  action: 'observed' | 'rejected' | 'ban_suggested' | 'auto_banned'
  duplicate_of: number
  status: ViolationStatus
  reviewed_by: number
  reviewed_at: number
  review_note: string
  notification_status: '' | 'pending' | 'sent' | 'failed' | 'skipped'
  request_body?: string
  request_body_bytes: number
  request_body_truncated: boolean
  created_at: number
}
export type ViolationDetail = {
  violation: Violation
  score: number
  user_status: number
}
export type PreviewResult = {
  enabled: boolean
  audited: boolean
  exempt: boolean
  namespace: string
  binding_source: string
  policy_key: string
  policy_revision: number
  mode: string
  model_run: boolean
  deduction: number
  decision: {
    outcome: string
    layer: string
    reason: string
    category: string
    keyword: string
    rule: string
    scores?: Record<string, number>
    policy_key: string
    policy_revision: number
    config_revision: number
    provider_model?: string
    latency_ms: number
  }
}
