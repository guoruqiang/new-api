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
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { expect, it, vi } from 'vitest'

import { QuestionBank } from '../components/question-bank'
import type { Question } from '../types'
import { defaults } from './fixtures'

function Editor(props: { initial: Question[] | null }) {
  const [questions, setQuestions] = useState(props.initial)
  return (
    <QuestionBank
      value={questions}
      defaults={defaults.builtin_questions}
      onChange={setQuestions}
    />
  )
}

it('lets operators disable built-in questions, add a custom question and restore defaults', async () => {
  const user = userEvent.setup()
  render(<Editor initial={null} />)
  await user.click(screen.getByRole('switch', { name: 'Enabled' }))
  expect(screen.getByRole('switch')).not.toBeChecked()
  expect(
    screen.getByText(
      'Higher scores mean safer content. If all questions are disabled, the general safety question is used.'
    )
  ).toBeVisible()
  await user.click(screen.getByRole('button', { name: 'Add question' }))
  expect(screen.getByRole('button', { name: 'question_1' })).toHaveAttribute(
    'aria-expanded',
    'true'
  )
  await user.click(screen.getByRole('button', { name: 'Safety' }))
  expect(screen.getAllByRole('textbox', { name: 'Question ID' })).toHaveLength(
    2
  )
  await user.type(
    screen.getAllByRole('textbox', { name: 'Question instructions' })[1],
    'Can this request be processed safely?'
  )
  await user.click(
    screen.getByRole('button', { name: 'Restore built-in questions' })
  )
  expect(screen.getByRole('button', { name: 'Safety' })).toHaveAttribute(
    'aria-expanded',
    'false'
  )
  await user.click(screen.getByRole('button', { name: 'Safety' }))
  expect(screen.getAllByRole('textbox', { name: 'Question ID' })).toHaveLength(
    1
  )
  expect(screen.getByRole('switch')).toBeChecked()
})

it('prevents adding beyond 24 questions and does not accept duplicate identifiers', async () => {
  const user = userEvent.setup()
  const questions = Array.from({ length: 24 }, (_, i) => ({
    ...defaults.builtin_questions[0],
    id: `question_${i}`,
    builtin: false,
  }))
  render(<Editor initial={questions} />)
  expect(screen.getByRole('button', { name: 'Add question' })).toBeDisabled()
  await user.click(screen.getAllByRole('button', { name: 'Safety' })[1])
  const second = screen.getByRole('textbox', { name: 'Question ID' })
  await user.clear(second)
  await user.type(second, 'question_0')
  await user.tab()
  expect(second).toHaveValue('question_1')
})

it('keeps renamed custom questions open and preserves their edited content when collapsed', async () => {
  const user = userEvent.setup()
  render(<Editor initial={[]} />)
  await user.click(screen.getByRole('button', { name: 'Add question' }))
  const id = screen.getByRole('textbox', { name: 'Question ID' })
  await user.clear(id)
  await user.type(id, 'custom_safety')
  await user.tab()
  expect(screen.getByRole('button', { name: 'custom_safety' })).toHaveAttribute(
    'aria-expanded',
    'true'
  )
  await user.type(
    screen.getByRole('textbox', { name: 'Question name' }),
    'Custom safety'
  )
  await user.type(
    screen.getByRole('textbox', { name: 'Question summary' }),
    'Check this request'
  )
  await user.type(
    screen.getByRole('textbox', { name: 'Question instructions' }),
    'Is the request safe?'
  )
  const trigger = screen.getByRole('button', { name: 'Custom safety' })
  await user.click(trigger)
  expect(trigger).toHaveAttribute('aria-expanded', 'false')
  trigger.focus()
  await user.keyboard('{Enter}')
  expect(screen.getByRole('textbox', { name: 'Question ID' })).toHaveValue(
    'custom_safety'
  )
  expect(
    screen.getByRole('textbox', { name: 'Question instructions' })
  ).toHaveValue('Is the request safe?')
  expect(screen.getByText('Check this request')).toBeVisible()
})

it('allows read-only question inspection while disabling every edit action', async () => {
  const user = userEvent.setup()
  const change = vi.fn()
  render(
    <QuestionBank
      disabled
      value={null}
      defaults={defaults.builtin_questions}
      onChange={change}
    />
  )
  const trigger = screen.getByRole('button', { name: 'Safety' })
  trigger.focus()
  await user.keyboard('{Enter}')
  expect(trigger).toHaveAttribute('aria-expanded', 'true')
  expect(
    screen.getByRole('textbox', { name: 'Question instructions' })
  ).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Add question' })).toBeDisabled()
  expect(
    screen.getByRole('button', { name: 'Restore built-in questions' })
  ).toBeDisabled()
  expect(screen.getByRole('switch')).toHaveAttribute('aria-disabled', 'true')
  expect(change).not.toHaveBeenCalled()
})
