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

import { riskWrite } from '../api'
import { LABELS } from '../constants'
import type { Directory, PreviewResult } from '../types'
import { Choice, NumberField, Toggle } from './fields'

export function PreviewDialog(props: { directory: Directory }) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [group, setGroup] = useState(
    props.directory.groups[0]?.group_name ?? ''
  )
  const [auto, setAuto] = useState(false)
  const [user, setUser] = useState(1)
  const [runModel, setRunModel] = useState(false)
  const [body, setBody] = useState(
    '{"model":"example","messages":[{"role":"user","content":"Hello"}]}'
  )
  const preview = useMutation({
    mutationFn: () =>
      riskWrite<PreviewResult>('post', 'preview', {
        group_name: group,
        via_auto: auto,
        user_id: user,
        body,
        run_model: runModel,
      }),
  })
  return (
    <Dialog
      open={open}
      onOpenChange={setOpen}
      title={t('Preview effective policy')}
      description={t(
        'Preview uses saved settings and does not write audit events, refusal records, blocks or caches.'
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
        <NumberField label='User ID' value={user} min={1} onChange={setUser} />
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
        {preview.data && (
          <div role='status' className='space-y-2 rounded-lg border p-3'>
            <p>
              {t(preview.data.audited ? 'Audited' : 'Not audited')} ·{' '}
              {t(
                LABELS[preview.data.mode || preview.data.binding_source] ??
                  preview.data.mode
              )}
            </p>
            <p>
              {t(
                LABELS[preview.data.decision.outcome] ??
                  preview.data.decision.outcome
              )}{' '}
              ·{' '}
              {t(
                LABELS[preview.data.decision.reason] ??
                  preview.data.decision.reason
              )}
            </p>
            <p>
              {t('Coverage')}:{' '}
              {t(
                LABELS[preview.data.decision.coverage] ??
                  preview.data.decision.coverage
              )}
            </p>
            <p>
              {t(
                preview.data.model_run
                  ? 'Decision model ran'
                  : 'Decision model not run'
              )}
            </p>
            <pre className='break-all whitespace-pre-wrap'>
              {JSON.stringify(preview.data.decision.scores, null, 2)}
            </pre>
          </div>
        )}
      </div>
    </Dialog>
  )
}
