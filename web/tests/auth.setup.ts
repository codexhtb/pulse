import { expect, test as setup } from '@playwright/test'
import { chmod, mkdir } from 'node:fs/promises'
import process from 'node:process'

const authState = 'test-results/.auth/admin.json'

setup('create authenticated Admin session', async ({ request }) => {
  const username = process.env.PULSE_E2E_USERNAME
  const password = process.env.PULSE_E2E_PASSWORD

  expect(username, 'PULSE_E2E_USERNAME is required for integration tests').toBeTruthy()
  expect(password, 'PULSE_E2E_PASSWORD is required for integration tests').toBeTruthy()

  const login = await request.post('/api/v1/auth/login', {
    data: { username, password },
  })
  expect(login.status(), await login.text()).toBe(200)

  const session = await request.get('/api/v1/auth/session')
  const sessionBody = await session.json()
  expect(session.status(), JSON.stringify(sessionBody)).toBe(200)
  expect(sessionBody).toMatchObject({
    state: 'authenticated',
    user: { username, role: 'Admin', enabled: true },
  })

  await mkdir('test-results/.auth', { recursive: true })
  await request.storageState({ path: authState })
  await chmod(authState, 0o600)
})
