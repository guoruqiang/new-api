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
import { zodResolver } from '@hookform/resolvers/zod'
import { useNavigate } from '@tanstack/react-router'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import type { z } from 'zod'

import { PasswordInput } from '@/components/password-input'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Form,
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { api } from '@/lib/api'
import { handleServerError } from '@/lib/handle-server-error'
import { AuthOperationError } from '@/lib/secure-verification'
import { createServerError } from '@/lib/server-error-message'

import { AuthLayout } from '../auth-layout'
import { resetPasswordFormSchema } from '../constants'

export type ResetPasswordSearchParams = {
  email?: string
  token?: string
}

type ResetPasswordValues = z.infer<typeof resetPasswordFormSchema>

// The user chooses a new password through the emailed link, then signs in
// with it; the reset does not sign the user in.
export function ResetPasswordConfirm(props: ResetPasswordSearchParams) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const form = useForm<ResetPasswordValues>({
    resolver: zodResolver(resetPasswordFormSchema),
    defaultValues: { password: '', confirmPassword: '' },
  })
  const isValidResetLink = Boolean(props.email && props.token)

  async function onSubmit(values: ResetPasswordValues) {
    try {
      const res = await api.post(
        '/api/user/reset',
        { token: props.token, password: values.password },
        { skipBusinessError: true } as Record<string, unknown>
      )
      if (!res.data?.success) {
        handleServerError(createServerError(res.data, t('Request failed')))
        return
      }
      toast.success(
        t('Your password has been updated. Sign in with your new password.')
      )
      navigate({ to: '/sign-in', replace: true })
    } catch (error) {
      handleServerError(AuthOperationError.from(error))
    }
  }

  return (
    <AuthLayout>
      <div className='w-full space-y-8'>
        <div className='space-y-2'>
          <h2 className='text-center text-2xl font-semibold tracking-tight sm:text-left'>
            {t('Reset password')}
          </h2>
          <p className='text-muted-foreground text-left text-sm sm:text-base'>
            {t('Choose a new password for your account.')}
          </p>
        </div>

        {!isValidResetLink && (
          <Alert variant='destructive'>
            <AlertDescription>
              {t('Invalid reset link, please request a new password reset.')}
            </AlertDescription>
          </Alert>
        )}

        <Form {...form}>
          <form onSubmit={form.handleSubmit(onSubmit)} className='space-y-4'>
            <div className='space-y-2'>
              <Label htmlFor='email'>{t('Email')}</Label>
              <Input
                id='email'
                type='email'
                value={props.email ?? ''}
                disabled
              />
            </div>
            <FormField
              control={form.control}
              name='password'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('New password')}</FormLabel>
                  <FormControl>
                    <PasswordInput
                      autoComplete='new-password'
                      placeholder={t('Enter password (8–128 characters)')}
                      {...field}
                    />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name='confirmPassword'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Confirm password')}</FormLabel>
                  <FormControl>
                    <PasswordInput autoComplete='new-password' {...field} />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
            <Button
              type='submit'
              className='w-full'
              disabled={!isValidResetLink || form.formState.isSubmitting}
            >
              {t('Set new password')}
            </Button>
            <Button
              type='button'
              variant='link'
              className='w-full'
              onClick={() => navigate({ to: '/sign-in', replace: true })}
            >
              {t('Back to login')}
            </Button>
          </form>
        </Form>
      </div>
    </AuthLayout>
  )
}
