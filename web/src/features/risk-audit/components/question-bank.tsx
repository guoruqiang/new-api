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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import {
  Accordion,
  AccordionContent,
  AccordionItem,
  AccordionTrigger,
} from '@/components/ui/accordion'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { FieldGroup, FieldLegend, FieldSet } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import type { Question } from '../types'
import { Field, Toggle } from './fields'

export function QuestionBank(props: {
  disabled?: boolean
  value: Question[] | null
  defaults: Question[]
  onChange: (questions: Question[]) => void
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const questions = props.value ?? props.defaults
  const [expanded, setExpanded] = useState<string[]>([])
  return (
    <FieldSet className='min-w-0 gap-3'>
      <FieldLegend variant='label'>{t('Question bank')}</FieldLegend>
      <div className='flex flex-wrap items-start justify-between gap-3'>
        <p className='text-muted-foreground text-sm'>
          {t(
            'Higher scores mean safer content. If all questions are disabled, the general safety question is used.'
          )}
        </p>
        <Badge variant='secondary'>
          {t('{{enabled}} of {{total}} enabled', {
            enabled: formatNumber(
              questions.filter((question) => question.enabled).length,
              locale
            ),
            total: formatNumber(questions.length, locale),
          })}
        </Badge>
      </div>
      <Accordion
        multiple
        value={expanded}
        onValueChange={setExpanded}
        className='min-w-0 rounded-lg border px-3'
      >
        {questions.map((question, index) => (
          <AccordionItem key={question.id} value={question.id}>
            <div className='flex items-center gap-4'>
              <AccordionTrigger
                className='min-w-0 flex-1 items-center gap-3 py-3 hover:no-underline'
                aria-label={question.label || question.id}
              >
                <span className='min-w-0 space-y-1'>
                  <span className='flex flex-wrap items-center gap-2 break-words'>
                    {question.label || question.id}
                    {question.builtin && (
                      <Badge variant='outline'>{t('Built-in')}</Badge>
                    )}
                  </span>
                  {question.summary && (
                    <span className='text-muted-foreground block text-xs font-normal break-words'>
                      {question.summary}
                    </span>
                  )}
                </span>
              </AccordionTrigger>
              <Toggle
                compact
                disabled={props.disabled}
                label='Enabled'
                checked={question.enabled}
                onChange={(enabled) =>
                  props.onChange(
                    questions.map((q, i) =>
                      i === index ? { ...q, enabled } : q
                    )
                  )
                }
              />
            </div>
            <AccordionContent className='space-y-3 pb-4'>
              <FieldGroup className='grid gap-3 sm:grid-cols-2'>
                <Field label='Question ID'>
                  <Input
                    aria-label={t('Question ID')}
                    className='font-mono'
                    defaultValue={question.id}
                    readOnly={question.builtin}
                    disabled={props.disabled}
                    onBlur={(e) => {
                      const id = e.target.value.trim()
                      if (
                        !/^[a-z][a-z0-9_-]{0,31}$/.test(id) ||
                        id === 'custom' ||
                        questions.some((q, i) => i !== index && q.id === id)
                      ) {
                        e.target.value = question.id
                        return
                      }
                      setExpanded((open) =>
                        open.map((old) => (old === question.id ? id : old))
                      )
                      props.onChange(
                        questions.map((q, i) =>
                          i === index ? { ...q, id } : q
                        )
                      )
                    }}
                  />
                </Field>
                <Field label='Question name'>
                  <Input
                    aria-label={t('Question name')}
                    disabled={props.disabled}
                    value={question.label}
                    onChange={(e) =>
                      props.onChange(
                        questions.map((q, i) =>
                          i === index ? { ...q, label: e.target.value } : q
                        )
                      )
                    }
                  />
                </Field>
              </FieldGroup>
              <Field label='Question summary'>
                <Input
                  aria-label={t('Question summary')}
                  disabled={props.disabled}
                  value={question.summary}
                  onChange={(e) =>
                    props.onChange(
                      questions.map((q, i) =>
                        i === index ? { ...q, summary: e.target.value } : q
                      )
                    )
                  }
                />
              </Field>
              <Field label='Question instructions'>
                <Textarea
                  aria-label={t('Question instructions')}
                  value={question.instructions}
                  disabled={props.disabled}
                  className='min-h-24 resize-y'
                  onChange={(e) =>
                    props.onChange(
                      questions.map((q, i) =>
                        i === index ? { ...q, instructions: e.target.value } : q
                      )
                    )
                  }
                />
              </Field>
              {!question.builtin && (
                <Button
                  type='button'
                  variant='ghost'
                  disabled={props.disabled}
                  onClick={() => {
                    setExpanded((open) =>
                      open.filter((id) => id !== question.id)
                    )
                    props.onChange(questions.filter((_, i) => i !== index))
                  }}
                >
                  {t('Delete')}
                </Button>
              )}
            </AccordionContent>
          </AccordionItem>
        ))}
      </Accordion>
      <div className='flex flex-wrap gap-2'>
        <Button
          type='button'
          variant='outline'
          disabled={props.disabled || questions.length >= 24}
          onClick={() => {
            let id = 'question_1'
            for (let n = 2; questions.some((q) => q.id === id); n++) {
              id = `question_${n}`
            }
            setExpanded((open) => [...open, id])
            props.onChange([
              ...questions,
              {
                id,
                label: '',
                summary: '',
                instructions: '',
                enabled: true,
                builtin: false,
              },
            ])
          }}
        >
          {t('Add question')}
        </Button>
        <Button
          type='button'
          variant='outline'
          disabled={props.disabled}
          onClick={() => {
            setExpanded([])
            props.onChange(structuredClone(props.defaults))
          }}
        >
          {t('Restore built-in questions')}
        </Button>
      </div>
    </FieldSet>
  )
}
