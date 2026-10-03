import { expect, test, type Page } from '@playwright/test'

const user = { username: 'test-admin', role: 'Admin', enabled: true, created_at: '2026-10-02T00:00:00Z', updated_at: '2026-10-02T00:00:00Z' }

async function serveLoggedOutSession(page: Page) {
  await page.route('**/api/v1/auth/session', (route) => route.fulfill({ status: 401, json: { error: 'authentication required' } }))
}

async function serveEmptyOverview(page: Page) {
  await page.route('**/api/v1/overview?**', (route) => route.fulfill({ json: {
    range: '6h', current_qps: 0, queries: 0, responses: 0, no_response: 0, no_response_pct: 0,
    unmatched_responses: 0, queries_today: 0, unique_clients: 0, unique_domains: 0,
    nxdomain: 0, nxdomain_pct: 0, servfail: 0, servfail_pct: 0,
    avg_latency_us: 0, p50_latency_us: 0, p95_latency_us: 0, p99_latency_us: 0,
    response_bytes: 0, active_sources: 0,
    previous: { current_qps: 0, queries: 0, nxdomain_pct: 0, servfail_pct: 0, p95_latency_us: 0 },
  } }))
  for (const path of ['traffic', 'domains/top', 'clients/top', 'rcodes', 'sources']) {
    await page.route(`**/api/v1/${path}?**`, (route) => route.fulfill({ json: [] }))
  }
  await page.route('**/api/v1/anomalies', (route) => route.fulfill({ json: { window: '15m', count: 0, anomalies: [] } }))
}

test('password login opens Pulse immediately', async ({ page }) => {
  let authenticated = false

  await serveEmptyOverview(page)
  await page.route('**/api/v1/auth/session', (route) => authenticated
    ? route.fulfill({ json: { state: 'authenticated', csrf_token: 'csrf-test', user } })
    : route.fulfill({ status: 401, json: { error: 'authentication required' } }))
  await page.route('**/api/v1/auth/login', async (route) => {
    expect(route.request().postDataJSON()).toEqual({ username: 'test-admin', password: 'test-password' })
    authenticated = true
    await route.fulfill({ json: { state: 'authenticated', csrf_token: 'csrf-test', user } })
  })

  await page.goto('/login')
  await expect(page.getByRole('heading', { name: 'Добро пожаловать в Pulse' })).toBeVisible()
  await page.getByLabel('Имя пользователя').fill('test-admin')
  await page.getByLabel('Пароль').fill('test-password')
  await page.getByRole('button', { name: 'Продолжить' }).click()
  await expect(page).toHaveURL(/\/$/)
  await expect(page.locator('.sidebar-foot')).toContainText('test-admin')
})

test('login branding is responsive, non-blocking, and preserves autofill semantics', async ({ page }) => {
  await serveLoggedOutSession(page)
  await page.setViewportSize({ width: 1920, height: 1080 })
  await page.goto('/login')

  await expect(page.locator('.pulse-word')).toHaveText('PULSE')
  await expect(page.locator('.pulse-signature')).toHaveText('by.Bezhan')
  await expect(page.getByLabel('Имя пользователя')).toHaveAttribute('autocomplete', 'username')
  await expect(page.getByLabel('Пароль')).toHaveAttribute('autocomplete', 'current-password')
  await expect(page.getByLabel('Имя пользователя')).toHaveAttribute('name', 'username')
  await expect(page.getByLabel('Пароль')).toHaveAttribute('name', 'password')

  const initialCardLayout = await page.locator('.login-card').evaluate((element) => ({
    offsetTop: (element as HTMLElement).offsetTop,
    offsetHeight: (element as HTMLElement).offsetHeight,
  }))
  await page.getByLabel('Имя пользователя').fill('test-admin')
  await page.getByLabel('Пароль').fill('available-immediately')
  await expect(page.locator('.pulse-signature')).toHaveCSS('opacity', '0.48', { timeout: 5_000 })
  await expect.poll(() => page.locator('.login-card').evaluate((element) => ({
    offsetTop: (element as HTMLElement).offsetTop,
    offsetHeight: (element as HTMLElement).offsetHeight,
  }))).toEqual(initialCardLayout)

  for (const viewport of [{ width: 1366, height: 768 }, { width: 390, height: 844 }]) {
    await page.setViewportSize(viewport)
    const card = page.locator('.login-card')
    await expect(card).toBeVisible()
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBeTruthy()
    const box = await card.boundingBox()
    expect(box).not.toBeNull()
    expect(box!.x).toBeGreaterThanOrEqual(0)
    expect(box!.x + box!.width).toBeLessThanOrEqual(viewport.width)
  }

  await page.reload()
  await expect(page.locator('.pulse-signature')).toHaveText('by.Bezhan')
  await expect(page.getByLabel('Имя пользователя')).toBeVisible()

  const cdp = await page.context().newCDPSession(page)
  await cdp.send('DOM.enable')
  await cdp.send('CSS.enable')
  const { root } = await cdp.send('DOM.getDocument')
  for (const selector of ['input[name="username"]', 'input[name="password"]']) {
    const { nodeId } = await cdp.send('DOM.querySelector', { nodeId: root.nodeId, selector })
    await cdp.send('CSS.forcePseudoState', { nodeId, forcedPseudoClasses: ['autofill'] })
    const autofillStyle = await page.locator(selector).evaluate((element) => {
      const style = getComputedStyle(element)
      const wrapperStyle = getComputedStyle(element.parentElement!)
      return {
        active: element.matches(':autofill'),
        text: style.webkitTextFillColor,
        caret: style.caretColor,
        shadow: style.boxShadow,
        radius: style.borderRadius,
        wrapperRadius: wrapperStyle.borderRadius,
        wrapperOverflow: wrapperStyle.overflow,
      }
    })
    expect(autofillStyle.active).toBeTruthy()
    expect(autofillStyle.text).toBe('rgb(220, 232, 237)')
    expect(autofillStyle.caret).toBe('rgb(220, 232, 237)')
    expect(autofillStyle.shadow).toContain('8, 13, 18')
    expect(autofillStyle.shadow).toContain('inset')
    expect(autofillStyle.radius).toBe('6px')
    expect(autofillStyle.wrapperRadius).toBe('8px')
    expect(autofillStyle.wrapperOverflow).toBe('hidden')
  }
})

test('reduced motion shows the complete login composition immediately', async ({ page }) => {
  await page.emulateMedia({ reducedMotion: 'reduce' })
  await serveLoggedOutSession(page)
  await page.goto('/login')

  await expect(page.locator('.pulse-word')).toHaveCSS('animation-name', 'none')
  await expect(page.locator('.pulse-signature')).toHaveCSS('animation-name', 'none')
  await expect(page.locator('.login-card')).toHaveCSS('animation-name', 'none')
  await expect(page.locator('.pulse-signature')).toHaveCSS('opacity', '0.48')
  await expect(page.getByLabel('Имя пользователя')).toBeVisible()
})

test('invalid password keeps the branded login and exposes the error state', async ({ page }) => {
  await serveLoggedOutSession(page)
  await page.route('**/api/v1/auth/login', (route) => route.fulfill({ status: 401, json: { error: 'invalid credentials' } }))
  await page.goto('/login')
  await page.getByLabel('Имя пользователя').fill('test-admin')
  await page.getByLabel('Пароль').fill('wrong-password')
  await page.getByRole('button', { name: 'Продолжить' }).click()
  await expect(page.getByRole('alert')).toBeVisible()
  await expect(page.locator('.pulse-signature')).toHaveText('by.Bezhan')
})
