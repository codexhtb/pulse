import { expect, test, type Page } from '@playwright/test'

test.beforeEach(async ({ page }, testInfo) => {
  if (testInfo.title.includes('language defaults')) return
  await page.addInitScript(() => localStorage.setItem('pulse-language', 'en'))
})

async function expectNoConsoleErrors(page: Page) {
  const errors: string[] = []
  page.on('console', (message) => {
    if (message.type() === 'error') errors.push(message.text())
  })
  return () => expect(errors, `browser console errors: ${errors.join('\n')}`).toEqual([])
}

test('overview renders real backend data at desktop sizes', async ({ page }) => {
  const checkConsole = await expectNoConsoleErrors(page)
  await page.setViewportSize({ width: 1920, height: 1080 })
  await page.goto('/')
  await expect(page.getByRole('heading', { name: 'DNS overview' })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Top domains' })).toBeVisible()
  await expect(page.locator('.overview-grid .data-table tbody tr').first()).toBeVisible()
  const resolverComparison = page.locator('.resolver-comparison')
  await expect(resolverComparison.getByText('dns1', { exact: true })).toBeVisible()
  await expect(resolverComparison.getByText('dns2', { exact: true })).toBeVisible()
  await expect(resolverComparison.locator('.resolver-share-legend strong')).toHaveCount(2)
  await expect(page.getByRole('heading', { name: 'DNS traffic' })).toBeVisible()
  await expect(page.getByLabel('Time range').getByRole('button')).toHaveText(['15m', '1h', '3h', '6h', '12h', '24h'])
  await page.screenshot({ path: 'test-results/overview-1920.png', fullPage: true })

  await page.setViewportSize({ width: 1366, height: 768 })
  await page.screenshot({ path: 'test-results/overview-1366.png', fullPage: true })
  checkConsole()
})

test('live stream connects and exposes operational stats', async ({ page }) => {
  const checkConsole = await expectNoConsoleErrors(page)
  await page.setViewportSize({ width: 1366, height: 768 })
  await page.goto('/live')
  await expect(page.getByRole('heading', { name: 'Live DNS events' })).toBeVisible()
  await expect(page.getByText('live', { exact: true })).toBeVisible({ timeout: 10_000 })
  await expect(page.locator('.events-table tbody tr').first()).toBeVisible({ timeout: 10_000 })
  await page.screenshot({ path: 'test-results/live-1366.png', fullPage: true })
  checkConsole()
})

test('search uses cursor results and supports a real empty state', async ({ page }) => {
  const checkConsole = await expectNoConsoleErrors(page)
  await page.goto('/search')
  await expect(page.getByRole('heading', { name: 'Historical search' })).toBeVisible()
  await expect(page.getByLabel('Range').locator('option')).toHaveText(['15m', '1h', '3h', '6h', '12h', '24h'])
  await expect(page.getByText('Raw event retention is limited to 24 hours.')).toBeVisible()
  await expect(page.getByRole('columnheader', { name: 'Outcome' })).toBeVisible()
  await expect(page.locator('.events-table tbody tr').first()).toBeVisible()

  await page.getByLabel('Domain', { exact: true }).fill('definitely-not-present.pulse.invalid')
  await page.getByRole('button', { name: 'Run search' }).click()
  await expect(page.getByText('No matching DNS events')).toBeVisible()
  checkConsole()
})

test('client and domain detail routes load directly', async ({ page, request }) => {
  const checkConsole = await expectNoConsoleErrors(page)
  await page.route('**/api/v1/clients/*/dns-block', (route) => route.fulfill({ json: { ip: '192.0.2.1', blocked: false, desired: 'unblocked', status: 'applied', created_at: null, updated_at: null, expires_at: null, nodes: [] } }))
  const topClientsResponse = await request.get('/api/v1/clients/top?range=6h&limit=1')
  expect(topClientsResponse.ok()).toBeTruthy()
  const [{ client_ip: clientIP }] = await topClientsResponse.json() as Array<{ client_ip: string }>
  expect(clientIP).toBeTruthy()
  await page.goto(`/clients/${encodeURIComponent(clientIP)}`)
  await expect(page.getByRole('heading', { name: clientIP })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Client timeline' })).toBeVisible()

  const topDomainsResponse = await request.get('/api/v1/domains/top?range=6h&limit=1')
  expect(topDomainsResponse.ok()).toBeTruthy()
  const [{ domain }] = await topDomainsResponse.json() as Array<{ domain: string }>
  expect(domain).toBeTruthy()

  await page.goto(`/domains/${encodeURIComponent(domain)}`)
  await expect(page.getByRole('heading', { name: domain })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Query types and sources' })).toBeVisible()
  checkConsole()
})

test('anomalies and sources display current operational state', async ({ page }) => {
  const checkConsole = await expectNoConsoleErrors(page)
  await page.goto('/anomalies')
  await expect(page.getByRole('heading', { name: 'Active anomalies' })).toBeVisible()
  await expect(page.getByText(/^\d+ active · \S+ evaluation window$/)).toBeVisible()

  await page.goto('/sources')
  await expect(page.getByText('dns1', { exact: true })).toBeVisible()
  await expect(page.getByText('dns2', { exact: true })).toBeVisible()
  expect(await page.locator('.status-badge').count()).toBeGreaterThanOrEqual(1)
  checkConsole()
})

test('API unavailable state is explicit and recoverable', async ({ page }) => {
  await page.route('**/api/v1/health', (route) => route.abort('connectionfailed'))
  await page.route('**/api/v1/meta', (route) => route.abort('connectionfailed'))
  await page.goto('/settings')
  await expect(page.getByText('Unable to load data')).toBeVisible()
  await expect(page.getByText('Pulse API is unreachable')).toBeVisible()
  await expect(page.getByRole('button', { name: 'Retry' })).toBeVisible()
})

test('SSE failure moves the viewer into reconnecting state', async ({ page }) => {
  await page.route('**/api/v1/live**', (route) => route.abort('connectionfailed'))
  await page.goto('/live')
  await expect(page.getByText('reconnecting', { exact: true })).toBeVisible({ timeout: 10_000 })
})

test('pipeline page exposes durable and live health separately', async ({ page }) => {
  await page.goto('/pipeline')
  await expect(page.getByRole('heading', { name: 'Pipeline' })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Durable telemetry' })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Live telemetry' })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'DNS control nodes' })).toBeVisible()
  await expect(page.getByText('Durable dropped')).toBeVisible()
  await expect(page.getByText('No client response observed within correlation window')).toBeVisible()
})

test('live deep link initializes investigation controls', async ({ page }) => {
  await page.goto('/live?client_ip=192.0.2.10&outcome=NO_RESPONSE&failures=true&slow_us=500000&source=dns1&qtype=A&rcode=SERVFAIL')
  await expect(page.getByLabel('Client IP')).toHaveValue('192.0.2.10')
  await expect(page.getByLabel('Outcome')).toHaveValue('NO_RESPONSE')
  await expect(page.getByLabel('Source')).toHaveValue('dns1')
  await expect(page.getByLabel('QTYPE')).toHaveValue('A')
  await expect(page.getByLabel('RCODE')).toHaveValue('SERVFAIL')
  await expect(page.getByLabel('Slow threshold (ms)')).toHaveValue('500')
  await expect(page.getByRole('checkbox', { name: 'Failures only' })).toBeChecked()
  await expect(page.getByRole('button', { name: 'Pause' })).toBeVisible()
  await expect(page.getByRole('button', { name: /Auto-scroll on/ })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Clear' })).toBeVisible()
})

test('client inspector renders NO_RESPONSE semantics', async ({ page }) => {
  const now = new Date().toISOString()
	let dnsBlocked = false
  const detail = { range: '6h', client_ip: '192.0.2.10', queries: 2, responses: 1, no_response: 1, no_response_pct: 50, unmatched_responses: 0, unique_domains: 2, nxdomain: 0, nxdomain_pct: 0, servfail: 0, servfail_pct: 0, avg_latency_us: 1200, p50_latency_us: 900, p95_latency_us: 1200, p99_latency_us: 1200, response_bytes: 80, last_seen: now, state: 'active', sources: [{ source_id: 'dns1', last_seen: now }] }
  const noResponse = { event_time: new Date().toISOString(), ingested_at: new Date().toISOString(), query_time: new Date().toISOString(), response_time: null, outcome: 'NO_RESPONSE', source_id: 'dns1', client_ip: '192.0.2.10', client_port: 53000, protocol: 'UDP', qname: 'missing-response.test', qtype: 'A', qclass: 'IN', rcode: '', dns_id: 42, response_bytes: 0, answer_count: 0, latency_us: null, matched_query: true }
  await page.route('**/api/v1/clients/192.0.2.10/domains**', (route) => route.fulfill({ json: [{ domain: 'missing-response.test', queries: 2, responses: 1, no_response: 1, nxdomain: 0, servfail: 0, response_bytes: 80 }] }))
  await page.route('**/api/v1/clients/192.0.2.10/traffic**', (route) => route.fulfill({ json: [] }))
  await page.route('**/api/v1/clients/192.0.2.10?**', (route) => route.fulfill({ json: detail }))
  await page.route('**/api/v1/search**', (route) => route.fulfill({ json: { range: '6h', count: 1, events: [noResponse] } }))
	await page.route('**/api/v1/clients/192.0.2.10/dns-block', async (route) => {
	  if (route.request().method() === 'POST') {
	    expect(route.request().postDataJSON()).toEqual({ duration: '1h', reason: 'e2e investigation' })
	    dnsBlocked = true
	  } else if (route.request().method() === 'DELETE') {
	    dnsBlocked = false
	  }
	  const nodes = [
	    { id: 'dns1', display_name: 'DNS1', source_identity: 'dns1', dns_service_ip: '192.0.2.53', control_enabled: true, control_status: 'enabled', health: { status: 'ok', mode: 'enforcing', last_checked_at: now, last_healthy_at: now }, action: dnsBlocked ? 'blocked' : 'unblocked', status: 'applied', attempts: 1, updated_at: now, agent_result: dnsBlocked ? 'firewall state applied' : 'removed' },
	    { id: 'dns2', display_name: 'DNS2', source_identity: 'dns2', dns_service_ip: '192.0.2.54', control_enabled: false, control_status: 'disabled', health: { status: 'ok', mode: 'dry-run', last_checked_at: now, last_healthy_at: now }, action: dnsBlocked ? 'blocked' : 'unblocked', status: 'disabled', attempts: 0, updated_at: now },
	  ]
	  await route.fulfill({ json: { ip: '192.0.2.10', blocked: dnsBlocked, desired: dnsBlocked ? 'blocked' : 'unblocked', status: 'applied', created_at: now, updated_at: now, expires_at: dnsBlocked ? new Date(Date.now() + 3_600_000).toISOString() : null, duration: dnsBlocked ? '1h' : undefined, reason: 'e2e investigation', nodes } })
	})
  await page.goto('/clients/192.0.2.10')
  await expect(page.getByText('50.0%', { exact: true })).toBeVisible()
  await expect(page.getByText('NO_RESPONSE', { exact: true }).first()).toBeVisible()
  await expect(page.getByRole('link', { name: 'Watch Live' })).toHaveAttribute('href', '/live?client_ip=192.0.2.10')
	await expect(page.getByRole('heading', { name: 'DNS access control' })).toBeVisible()
	await expect(page.getByText('DNS1', { exact: true })).toBeVisible()
	await expect(page.getByText('DNS2', { exact: true })).toBeVisible()
	await page.getByLabel('Reason').fill('e2e investigation')
	await page.getByRole('button', { name: 'Block DNS' }).click()
	await expect(page.getByText(/real firewall action/)).toBeVisible()
	await page.getByRole('button', { name: 'Confirm' }).click()
	await expect(page.getByRole('button', { name: 'Unblock DNS' })).toBeVisible()
	await page.getByRole('button', { name: 'Unblock DNS' }).click()
	await page.getByRole('button', { name: 'Confirm' }).click()
	await expect(page.getByRole('button', { name: 'Block DNS' })).toBeVisible()

  const filtered = page.waitForRequest((request) => request.url().includes('/api/v1/search') && request.url().includes('failures=true'))
  await page.getByRole('button', { name: 'Failures', exact: true }).click()
  await filtered
})

test('domain inspector renders top clients and live deep link', async ({ page }) => {
  const now = new Date().toISOString()
  const detail = { range: '6h', domain: 'example.test', queries: 12, responses: 10, no_response: 2, no_response_pct: 16.7, unmatched_responses: 0, unique_clients: 2, nxdomain: 1, nxdomain_pct: 10, servfail: 1, servfail_pct: 10, avg_latency_us: 2200, p50_latency_us: 1500, p95_latency_us: 4500, p99_latency_us: 6000, response_bytes: 800, last_seen: now, state: 'active', sources: [{ source_id: 'dns1', last_seen: now }], qtypes: [{ qtype: 'A', queries: 12 }] }
  await page.route('**/api/v1/domains/example.test/clients**', (route) => route.fulfill({ json: [{ client_ip: '192.0.2.10', queries: 12, responses: 10, no_response: 2, nxdomain: 1, servfail: 1, response_bytes: 800 }] }))
  await page.route('**/api/v1/domains/example.test/traffic**', (route) => route.fulfill({ json: [] }))
  await page.route('**/api/v1/domains/example.test?**', (route) => route.fulfill({ json: detail }))
  await page.route('**/api/v1/search**', (route) => route.fulfill({ json: { range: '6h', count: 0, events: [] } }))

  await page.goto('/domains/example.test')
  await expect(page.getByRole('heading', { name: 'example.test' })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Top clients' })).toBeVisible()
  await expect(page.getByRole('link', { name: '192.0.2.10' })).toBeVisible()
  await expect(page.getByRole('link', { name: 'Watch Live' })).toHaveAttribute('href', '/live?domain=example.test')
  await expect(page.getByRole('button', { name: 'NXDOMAIN', exact: true })).toBeVisible()
})

test('unified investigation entry routes IP and domain', async ({ page }) => {
  await page.route('**/api/v1/search**', (route) => route.fulfill({ json: { range: '6h', count: 0, events: [] } }))
  await page.goto('/search')
  await page.getByLabel('IP or domain').fill('192.0.2.10')
  await page.getByRole('button', { name: 'Investigate' }).click()
  await expect(page).toHaveURL(/\/clients\/192\.0\.2\.10$/)

  await page.goto('/search')
  await page.getByLabel('IP or domain').fill('example.test')
  await page.getByRole('button', { name: 'Investigate' }).click()
  await expect(page).toHaveURL(/\/domains\/example\.test$/)
})

test('language defaults to Russian and persists an English selection', async ({ page }) => {
  await page.goto('/settings')
  await expect(page.getByRole('heading', { name: 'Настройки' })).toBeVisible()
  await expect(page.getByRole('navigation', { name: 'Основная навигация' }).getByText('Обзор', { exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Русский' })).toHaveClass(/selected/)
  await expect(page.evaluate(() => localStorage.getItem('pulse-language'))).resolves.toBe('ru')

  await page.getByRole('button', { name: 'English', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'Settings' })).toBeVisible()
  await expect(page.evaluate(() => localStorage.getItem('pulse-language'))).resolves.toBe('en')
  await page.reload()
  await expect(page.getByRole('heading', { name: 'Settings' })).toBeVisible()
  await expect(page.getByRole('button', { name: 'English', exact: true })).toHaveClass(/selected/)
})
