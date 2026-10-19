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
import { z } from 'zod'

export const riskSearchSchema = z.object({
  view: z
    .enum(['all', 'distill', 'hard_regex', 'refusal', 'model', 'pass'])
    .catch('all'),
  group: z.string().catch(''),
})
const bindingSchema = z.object({
  policy_key: z.string().min(1),
  policy_revision: z.number().int().positive(),
  mode: z.enum(['observe', 'enforce']),
})
export const configSchema = z
  .object({
    enabled: z.boolean(),
    exempt_namespaces: z.array(z.string()),
    default_binding: bindingSchema,
    group_bindings: z.array(bindingSchema.extend({ namespace: z.string() })),
    retention: z.object({
      events_days: z.number().int().min(1).max(3650),
      counters_days: z.number().int().min(1).max(3650),
    }),
  })
  .superRefine((value, ctx) => {
    if (
      value.exempt_namespaces.includes('auto') ||
      new Set(value.exempt_namespaces).size !==
        value.exempt_namespaces.length ||
      new Set(value.group_bindings.map((b) => b.namespace)).size !==
        value.group_bindings.length ||
      value.group_bindings.some((b) =>
        value.exempt_namespaces.includes(b.namespace)
      )
    ) {
      ctx.addIssue({ code: 'custom', message: 'Invalid risk audit settings' })
    }
  })
function unicodeText(max: number) {
  return z.string().refine((text) => [...text].length <= max)
}

export const questionSchema = z.object({
  id: z
    .string()
    .regex(/^[a-z][a-z0-9_-]{0,31}$/)
    .refine((id) => id !== 'custom'),
  label: z.string(),
  summary: z.string(),
  instructions: unicodeText(500).min(1),
  enabled: z.boolean(),
  builtin: z.boolean(),
})
const listSchema = z.array(unicodeText(400)).max(200)
export const policySchema = z
  .object({
    skip_gpt_models: z.boolean(),
    distill: z.object({
      enabled: z.boolean(),
      skip_official: z.boolean(),
      skip_gpt_models: z.boolean(),
      error: z.object({
        status: z.number().int().min(400).max(599),
        message: unicodeText(200),
      }),
      structure: z.object({
        min_max_tokens: z.number().int().positive(),
        require_no_tools: z.boolean(),
        require_single_turn: z.boolean(),
        contest_pattern: unicodeText(400),
      }),
      hard_needles: listSchema,
      needles: listSchema,
      patterns: listSchema.max(50),
      fingerprints: listSchema,
    }),
    hard_regex: z.object({
      enabled: z.boolean(),
      strip_reminders: z.boolean(),
      expand_base64: z.boolean(),
      rules: z
        .array(
          z.object({
            category: z.string().min(1),
            source: unicodeText(400).min(1),
            enabled: z.boolean(),
          })
        )
        .max(60)
        .nullable(),
      patterns: listSchema.max(50),
    }),
    refusal: z.object({
      enabled: z.boolean(),
      similarity_enabled: z.boolean(),
      similarity: z.union([
        z.literal(80),
        z.literal(85),
        z.literal(90),
        z.literal(95),
      ]),
      subject_block_enabled: z.boolean(),
      block_subject: z.enum(['user', 'user_device']),
      block_ttl_minutes: z.number().int().min(0).max(525600),
      memory_ttl_days: z.number().int().min(0).max(3650),
      max_similar_candidates: z.number().int().min(1).max(10000),
    }),
    model: z.object({
      enabled: z.boolean(),
      provider_id: z.number().int().nonnegative(),
      model: z.string(),
      questions: z.array(questionSchema).max(24).nullable(),
      custom_instruction: unicodeText(500),
      threshold: z.number().min(0).max(1),
      near_miss_band: z.number().min(0).max(1),
      fail_action: z.enum(['pass', 'block']),
      budget_ms: z.number().int().min(200).max(8000),
      max_attempts: z.number().int().min(1).max(10),
      dedup_sec: z.number().int().min(0).max(3600),
      max_audit_chars: z.number().int().min(256).max(64000),
    }),
    evidence: z.object({ store_preview: z.boolean() }),
  })
  .superRefine((value, ctx) => {
    const questions = value.model.questions ?? []
    if (
      new Set(questions.map((q) => q.id)).size !== questions.length ||
      questions.length > 24
    ) {
      ctx.addIssue({
        code: 'custom',
        message:
          'Question IDs must be unique; at most 24 questions are allowed.',
      })
    }
    if (value.model.enabled && value.model.provider_id <= 0) {
      ctx.addIssue({
        code: 'custom',
        path: ['model', 'provider_id'],
        message: 'Select a decision provider.',
      })
    }
  })
