import type {
  AnomaliesResponse,
  ClientDetail,
  DNSBlockStatus,
  ClientRow,
  InvestigationClient,
  InvestigationDomain,
  DomainDetail,
  DomainRow,
  HealthResponse,
  MetaResponse,
  PipelineSystem,
  OverviewData,
  QueryFilters,
  RCodeRow,
  SearchResponse,
  SourceRow,
  SourceDetail,
  TimeRange,
  TrafficPoint,
} from './types'

export class ApiError extends Error {
  constructor(
    message: string,
    readonly status?: number,
  ) {
    super(message)
  }
}

export type AuthState = 'authenticated'

export interface AdminUser {
  username: string
  role: 'Admin'
  enabled: boolean
  created_at: string
  updated_at: string
}

export interface AuthSession {
  state: AuthState
  csrf_token: string
  user: AdminUser
}

let csrfToken = ''

export function setCSRFToken(value: string) {
  csrfToken = value
}

async function request<T>(path: string, signal?: AbortSignal, init: RequestInit = {}): Promise<T> {
  let response: Response

  try {
	response = await fetch(path, {
	  ...init,
      signal,
	  credentials: 'same-origin',
	  headers: {
		Accept: 'application/json',
		...(init.method && !['GET', 'HEAD'].includes(init.method) && csrfToken ? { 'X-CSRF-Token': csrfToken } : {}),
		...init.headers,
	  },
    })
  } catch (error) {
    if (error instanceof DOMException && error.name === 'AbortError') throw error
    throw new ApiError('Pulse API is unreachable')
  }

  const text = await response.text()
  let body: unknown

  if (text) {
    try {
      body = JSON.parse(text)
    } catch {
      throw new ApiError('Pulse API returned invalid JSON', response.status)
    }
  }

  if (!response.ok) {
    const message =
      typeof body === 'object' && body && 'error' in body
        ? String((body as { error: unknown }).error)
        : `Pulse API request failed (${response.status})`
	if (response.status === 401 && !path.startsWith('/api/v1/auth/')) {
	  window.dispatchEvent(new Event('pulse:unauthorized'))
	}
    throw new ApiError(message, response.status)
  }

  if (body === undefined) {
    throw new ApiError('Pulse API returned an empty response', response.status)
  }

  return body as T
}

function query(params: Record<string, string | number | boolean | undefined>): string {
  const search = new URLSearchParams()
  Object.entries(params).forEach(([key, value]) => {
    if (value !== '' && value !== undefined) search.set(key, String(value))
  })
  const result = search.toString()
  return result ? `?${result}` : ''
}

export const api = {
  health: (signal?: AbortSignal) => request<HealthResponse>('/api/v1/health', signal),
  meta: (signal?: AbortSignal) => request<MetaResponse>('/api/v1/meta', signal),
  overview: (range: TimeRange, signal?: AbortSignal) =>
    request<OverviewData>(`/api/v1/overview${query({ range })}`, signal),
  traffic: (range: TimeRange, signal?: AbortSignal) =>
    request<TrafficPoint[]>(`/api/v1/traffic${query({ range })}`, signal),
  topDomains: (range: TimeRange, limit = 10, signal?: AbortSignal) =>
    request<DomainRow[]>(`/api/v1/domains/top${query({ range, limit })}`, signal),
  topClients: (range: TimeRange, limit = 10, signal?: AbortSignal) =>
    request<ClientRow[]>(`/api/v1/clients/top${query({ range, limit })}`, signal),
  rcodes: (range: TimeRange, signal?: AbortSignal) =>
    request<RCodeRow[]>(`/api/v1/rcodes${query({ range })}`, signal),
  sources: (range: TimeRange, signal?: AbortSignal) =>
    request<SourceRow[]>(`/api/v1/sources${query({ range })}`, signal),
  anomalies: (signal?: AbortSignal) => request<AnomaliesResponse>('/api/v1/anomalies', signal),
  system: (signal?: AbortSignal) => request<PipelineSystem>('/api/v1/system', signal),
  search: (
    filters: QueryFilters,
    options: { cursor?: string; limit?: number } = {},
    signal?: AbortSignal,
  ) =>
    request<SearchResponse>(
      `/api/v1/search${query({ ...filters, cursor: options.cursor, limit: options.limit ?? 100 })}`,
      signal,
    ),
  client: (clientIP: string, range: TimeRange, signal?: AbortSignal) =>
    request<ClientDetail>(
      `/api/v1/clients/${encodeURIComponent(clientIP)}${query({ range })}`,
      signal,
    ),
  clientTraffic: (clientIP: string, range: TimeRange, signal?: AbortSignal) =>
    request<TrafficPoint[]>(
      `/api/v1/clients/${encodeURIComponent(clientIP)}/traffic${query({ range })}`,
      signal,
    ),
  clientDomains: (clientIP: string, range: TimeRange, limit = 12, signal?: AbortSignal) =>
    request<InvestigationDomain[]>(
      `/api/v1/clients/${encodeURIComponent(clientIP)}/domains${query({ range, limit })}`,
      signal,
    ),
  clientDNSBlock: (clientIP: string, signal?: AbortSignal) =>
    request<DNSBlockStatus>(`/api/v1/clients/${encodeURIComponent(clientIP)}/dns-block`, signal),
  blockClientDNS: (clientIP: string, duration: '1h' | '24h' | 'permanent', reason: string, signal?: AbortSignal) =>
    request<DNSBlockStatus>(`/api/v1/clients/${encodeURIComponent(clientIP)}/dns-block`, signal, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ duration, reason }),
    }),
  unblockClientDNS: (clientIP: string, signal?: AbortSignal) =>
    request<DNSBlockStatus>(`/api/v1/clients/${encodeURIComponent(clientIP)}/dns-block`, signal, { method: 'DELETE' }),
  domain: (domain: string, range: TimeRange, signal?: AbortSignal) =>
    request<DomainDetail>(
      `/api/v1/domains/${encodeURIComponent(domain)}${query({ range })}`,
      signal,
    ),
  domainTraffic: (domain: string, range: TimeRange, signal?: AbortSignal) =>
    request<TrafficPoint[]>(
      `/api/v1/domains/${encodeURIComponent(domain)}/traffic${query({ range })}`,
      signal,
    ),
  domainClients: (domain: string, range: TimeRange, limit = 12, signal?: AbortSignal) =>
    request<InvestigationClient[]>(
      `/api/v1/domains/${encodeURIComponent(domain)}/clients${query({ range, limit })}`,
      signal,
    ),
  source: (source: string, range: TimeRange, signal?: AbortSignal) =>
    request<SourceDetail>(`/api/v1/sources/${encodeURIComponent(source)}${query({ range })}`, signal),
  authSession: (signal?: AbortSignal) => request<AuthSession>('/api/v1/auth/session', signal),
  login: (username: string, password: string, signal?: AbortSignal) =>
    request<AuthSession>('/api/v1/auth/login', signal, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username, password }),
    }),
  logout: (signal?: AbortSignal) =>
    request<{ status: string }>('/api/v1/auth/logout', signal, { method: 'POST' }),
  adminUsers: (signal?: AbortSignal) =>
    request<{ users: AdminUser[]; count: number }>('/api/v1/admin/users', signal),
  createAdmin: (username: string, password: string, signal?: AbortSignal) =>
    request<AdminUser>('/api/v1/admin/users', signal, {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ username, password }),
    }),
  setAdminEnabled: (username: string, enabled: boolean, signal?: AbortSignal) =>
    request<AdminUser>(`/api/v1/admin/users/${encodeURIComponent(username)}`, signal, {
      method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ enabled }),
    }),
  resetAdminPassword: (username: string, password: string, signal?: AbortSignal) =>
    request<{ status: string }>(`/api/v1/admin/users/${encodeURIComponent(username)}/password`, signal, {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ password }),
    }),
  revokeAdminSessions: (username: string, signal?: AbortSignal) =>
    request<{ status: string }>(`/api/v1/admin/users/${encodeURIComponent(username)}/sessions`, signal, { method: 'POST' }),
  deleteAdmin: (username: string, signal?: AbortSignal) =>
    request<{ status: string }>(`/api/v1/admin/users/${encodeURIComponent(username)}`, signal, { method: 'DELETE' }),
}

export function liveURL(filters: Omit<QueryFilters, 'range'> & { rate: number }): string {
  return `/api/v1/live${query(filters)}`
}
