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
import { useMutation } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/textarea'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import { riskWrite } from '../api'
import { LABELS } from '../constants'
import type { Directory, PreviewResult } from '../types'
import { Choice, Toggle } from './fields'
import { ValidationErrors } from './validation-errors'

export function PreviewDialog(props: { directory: Directory }) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const [open, setOpen] = useState(false)
  const [group, setGroup] = useState(
    props.directory.groups[0]?.group_name ?? ''
  )
  const [auto, setAuto] = useState(false)
  const [runModel, setRunModel] = useState(false)
  const [body, setBody] = useState(
    '{"model":"example","messages":[{"role":"user","content":"Hello"}]}'
  )
  const preview = useMutation({
    meta: { errorToast: false },
    mutationFn: () =>
      riskWrite<PreviewResult>('post', 'preview', {
        group_name: group,
        via_auto: auto,
        body,
        run_model: runModel,
      }),
  })
  const result = preview.data
  const decision = result?.decision
  return (
    <Dialog
      open={open}
      onOpenChange={setOpen}
      title={t('Preview effective policy')}
      description={t(
        'Preview uses saved settings and does not write events, violations, scores or caches.'
      )}
      trigger={
        <Button variant='outline'>{t('Preview effective policy')}</Button>
      }
    >
      <div className='space-y-4'>
        <Choice
          label='Group'
          value={group}
          options={[
            { value: '', label: 'Unresolved auto group' },
            ...props.directory.groups.map((g) => ({
              value: g.group_name,
              label: g.group_name,
            })),
          ]}
          onChange={setGroup}
        />
        <Toggle label='Auto request' checked={auto} onChange={setAuto} />
        <label className='block space-y-2 text-sm'>
          {t('Sample request JSON')}
          <Textarea
            rows={8}
            value={body}
            onChange={(e) => setBody(e.target.value)}
          />
        </label>
        <Toggle
          label='Run decision model'
          checked={runModel}
          onChange={setRunModel}
        />
        <Button
          disabled={preview.isPending || (!group && !auto)}
          onClick={() => preview.mutate()}
        >
          {t('Evaluate preview')}
        </Button>
        <ValidationErrors error={preview.error} />
        {result && (
          <div role='status' className='space-y-2 rounded-lg border p-3'>
            <p>
              {t(result.audited ? 'Audited' : 'Not audited')} ·{' '}
              {t(
                LABELS[result.mode || result.binding_source] ??
                  (result.mode || result.binding_source)
              )}
            </p>
            {result.audited && decision && (
              <>
                <p>
                  {t(LABELS[decision.outcome] ?? decision.outcome)} ·{' '}
                  {t(LABELS[decision.layer] ?? decision.layer)} ·{' '}
                  {t(LABELS[decision.reason] ?? decision.reason)}
                </p>
                {decision.category && (
                  <p>
                    {t('Category')}:{' '}
                    {t(LABELS[decision.category] ?? decision.category)}
                    {decision.keyword && ` · ${t('Keyword')}: ${decision.keyword}`}
                  </p>
                )}
                <p>
                  {t(
                    result.model_run
                      ? 'Decision model ran'
                      : 'Decision model not run'
                  )}
                </p>
                {result.deduction > 0 && (
                  <p className='font-medium'>
                    {t('This violation would deduct {{points}} points.', {
                      points: formatNumber(result.deduction, locale),
                    })}
                  </p>
                )}
                {decision.scores && (
                  <pre className='break-all whitespace-pre-wrap'>
                    {JSON.stringify(decision.scores, null, 2)}
                  </pre>
                )}
              </>
            )}
          </div>
        )}
      </div>
    </Dialog>
  )
}
