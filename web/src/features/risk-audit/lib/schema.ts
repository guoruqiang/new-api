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

import { POLICY_SECTIONS, VIEWS } from '../constants'

export const riskSearchSchema = z.object({
  view: z.enum(VIEWS).catch('overview'),
  section: z.enum(POLICY_SECTIONS).catch('distill'),
  group: z.string().catch(''),
})
const bindingSchema = z.object({
  policy_key: z.string().min(1),
  policy_revision: z.number().int().positive(),
  mode: z.enum(['observe', 'enforce']),
})
const points = z.number().int().min(1).max(100)
export const configSchema = z
  .object({
    enabled: z.boolean(),
    exempt_namespaces: z.array(z.string()),
    default_binding: bindingSchema,
    group_bindings: z.array(bindingSchema.extend({ namespace: z.string() })),
    retention: z.object({
      events_days: z.number().int().min(1).max(3650),
      counters_days: z.number().int().min(1).max(3650),
      violations_days: z.number().int().min(1).max(3650),
    }),
    score: z.object({
      auto_ban: z.boolean(),
      email_notify: z.boolean(),
      default: points,
      deductions: z.record(z.string(), points),
    }),
  })
  .superRefine((value, ctx) => {
    const exempt = value.exempt_namespaces
    if (
      exempt.includes('auto') ||
      new Set(exempt).size !== exempt.length ||
      new Set(value.group_bindings.map((b) => b.namespace)).size !==
        value.group_bindings.length ||
      value.group_bindings.some((b) => exempt.includes(b.namespace))
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
    model: z.object({
      enabled: z.boolean(),
      provider_id: z.number().int().nonnegative(),
      model: z.string(),
      questions: z.array(questionSchema).max(24).nullable(),
      threshold: z.number().min(0).max(1),
      fail_action: z.enum(['pass', 'block']),
      budget_ms: z.number().int().min(200).max(8000),
      max_attempts: z.number().int().min(1).max(10),
      dedup_sec: z.number().int().min(0).max(3600),
      max_audit_chars: z.number().int().min(256).max(64000),
    }),
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

export const violationNoteSchema = z
  .string()
  .trim()
  .min(1)
  .refine((text) => [...text].length <= 500)
