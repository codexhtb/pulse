import { expect, test as teardown } from '@playwright/test'
import { unlink } from 'node:fs/promises'

const authState = 'test-results/.auth/admin.json'

teardown('revoke integration Admin session', async ({ request }) => {
  const session = await request.get('/api/v1/auth/session')
  const sessionBody = await session.json() as { csrf_token: string }
  expect(session.status(), JSON.stringify(sessionBody)).toBe(200)
  const { csrf_token: csrfToken } = sessionBody

  const logout = await request.post('/api/v1/auth/logout', {
    headers: { 'X-CSRF-Token': csrfToken },
  })
  const logoutBody = await logout.json()
  expect(logout.status(), JSON.stringify(logoutBody)).toBe(200)
  expect(logoutBody).toEqual({ status: 'logged_out' })

  const afterLogout = await request.get('/api/v1/auth/session')
  expect(afterLogout.status()).toBe(401)
  await unlink(authState)
})
