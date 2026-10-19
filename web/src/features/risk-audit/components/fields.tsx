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
import { useId, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import {
  Field as FormField,
  FieldDescription,
  FieldLabel,
  FieldTitle,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Switch } from '@/components/ui/switch'

export function Field(props: {
  label: string
  children: ReactNode
  hint?: string
}) {
  const { t } = useTranslation()
  return (
    <FormField className='min-w-0'>
      <FieldTitle>{t(props.label)}</FieldTitle>
      {props.children}
      {props.hint && <FieldDescription>{t(props.hint)}</FieldDescription>}
    </FormField>
  )
}

export function Toggle(props: {
  label: string
  checked: boolean
  onChange: (value: boolean) => void
  disabled?: boolean
  compact?: boolean
}) {
  const { t } = useTranslation()
  const id = useId()
  return (
    <FormField
      orientation='horizontal'
      data-disabled={props.disabled}
      className={
        props.compact ? 'w-auto gap-2' : 'min-w-0 justify-between gap-4'
      }
    >
      <FieldLabel htmlFor={id} className='min-w-0 leading-relaxed'>
        {t(props.label)}
      </FieldLabel>
      <Switch
        id={id}
        checked={props.checked}
        onCheckedChange={props.onChange}
        disabled={props.disabled}
        className='shrink-0'
      />
    </FormField>
  )
}

export function NumberField(props: {
  label: string
  value: number
  onChange: (value: number) => void
  min?: number
  max?: number
  step?: number
  disabled?: boolean
}) {
  const { t } = useTranslation()
  const id = useId()
  return (
    <FormField className='min-w-0'>
      <FieldLabel htmlFor={id}>{t(props.label)}</FieldLabel>
      <Input
        id={id}
        type='number'
        value={Number.isNaN(props.value) ? '' : props.value}
        min={props.min ?? 0}
        max={props.max}
        step={props.step ?? 1}
        disabled={props.disabled}
        onChange={(e) => props.onChange(e.target.valueAsNumber)}
      />
    </FormField>
  )
}

export function Choice(props: {
  label: string
  value: string
  options: { value: string; label: string }[]
  onChange: (value: string) => void
  disabled?: boolean
  hideLabel?: boolean
}) {
  const { t } = useTranslation()
  const id = useId()
  return (
    <FormField className='min-w-0'>
      <FieldLabel
        htmlFor={id}
        className={props.hideLabel ? 'sr-only' : undefined}
      >
        {t(props.label)}
      </FieldLabel>
      <NativeSelect
        id={id}
        value={props.value}
        onChange={(e) => props.onChange(e.target.value)}
        disabled={props.disabled}
        className='w-full'
      >
        {props.options.map((option) => (
          <NativeSelectOption key={option.value} value={option.value}>
            {t(option.label)}
          </NativeSelectOption>
        ))}
      </NativeSelect>
    </FormField>
  )
}
