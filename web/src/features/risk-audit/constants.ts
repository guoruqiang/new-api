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
export const VIEWS = [
  'overview',
  'scope',
  'policy',
  'providers',
  'violations',
  'events',
] as const
export type View = (typeof VIEWS)[number]
export const POLICY_SECTIONS = ['distill', 'keyword', 'model'] as const
export type PolicySection = (typeof POLICY_SECTIONS)[number]
export const VIEW_LABELS: Record<View, string> = {
  overview: 'Overview',
  scope: 'Scope and scoring',
  policy: 'Policy',
  providers: 'Decision providers',
  violations: 'Violations',
  events: 'Risk events',
}
export const LABELS: Record<string, string> = {
  distill: 'Distillation detection',
  keyword: 'Keyword screening',
  model: 'Decision model',
  gate: 'Audit gate',
  pass: 'Passed',
  block: 'Blocked',
  would_block: 'Observation hit',
  error_block: 'Blocked on error',
  error_pass: 'Passed on error',
  exempt: 'Exempt group',
  uncovered: 'Not covered',
  no_hit: 'No keyword match',
  model_safe: 'Model judged safe',
  model_violation: 'Model confirmed violation',
  model_disabled: 'Decision model disabled',
  unavailable: 'Decision model unavailable',
  unreadable: 'Request body unreadable',
  exempt_group: 'Exempt group',
  gpt_skipped: 'GPT model skipped',
  nsfw: 'Explicit content',
  jailbreak: 'Jailbreak',
  reverse: 'Reverse engineering',
  pentest: 'Unauthorized penetration testing',
  cyber: 'Cyber abuse',
  safety: 'Safety',
  custom: 'Custom rule',
  crack: 'Software cracking',
  pending: 'Pending review',
  upheld: 'Upheld',
  revoked: 'Revoked',
  observed: 'Observed only',
  rejected: 'Request rejected',
  ban_suggested: 'Ban suggested',
  auto_banned: 'Account banned automatically',
  observe: 'Observe',
  enforce: 'Enforce',
  on: 'Enabled',
  off: 'Disabled',
  unset: 'Provider not configured',
  default: 'Inherited default',
  group: 'Group override',
  ok: 'Available',
  cooling: 'Cooling down',
  invalid: 'Invalid key',
  using_group: 'Selected routing group',
  auto_group: 'Auto-selected group',
  retry_select: 'Group selected on retry',
  pinned: 'Pinned channel group',
  pinned_candidate: 'Pinned channel candidate group',
  auto_unresolved: 'Unresolved auto group',
  ws_select: 'WebSocket-selected group',
  ws_locked: 'WebSocket-locked group',
}
export const NOTIFICATION_LABELS: Record<string, string> = {
  pending: 'Notification pending',
  sent: 'Notification sent',
  failed: 'Notification failed',
  skipped: 'Notification skipped',
}
export const USER_STATUS_LABELS: Record<number, string> = {
  0: 'User not found',
  1: 'Enabled',
  2: 'Disabled',
}
export const EVENT_LABELS: Record<string, string> = {
  id: 'ID',
  created_at: 'Time',
  user_id: 'User ID',
  token_id: 'Token ID',
  effective_group: 'Effective group',
  user_group: 'User group',
  token_group: 'Token group',
  group_source: 'Group source',
  namespace: 'Group namespace',
  attempt: 'Attempt',
  channel_id: 'Channel ID',
  model: 'Model',
  route: 'Route',
  mode: 'Mode',
  config_revision: 'Configuration revision',
  policy_key: 'Policy key',
  policy_revision: 'Policy revision',
  outcome: 'Outcome',
  layer: 'Layer',
  reason: 'Reason',
  category: 'Category',
  keyword: 'Keyword',
  scores: 'Question scores',
  provider_model: 'Decision model',
  latency_ms: 'Latency (ms)',
  error_code: 'Error code',
}
