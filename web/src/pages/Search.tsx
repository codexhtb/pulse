import { Search, SlidersHorizontal } from 'lucide-react'
import { FormEvent, useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { api } from '../api'
import { EmptyState, ErrorState, LoadingState, PageHeader, Panel } from '../components/Common'
import { EventsTable } from '../components/EventsTable'
import { ranges } from '../constants'
import type { DnsEvent, SearchFilters, SearchRange, SearchResponse, TimeRange } from '../types'
import { useI18n } from '../i18n'

interface SearchDraft {
  range: SearchRange
  fromLocal: string
  toLocal: string
  source: string
  client_ip: string
  domain: string
  qtype: string
  rcode: string
  protocol: string
  outcome: string
}

const relativeInitialFilters: SearchFilters = {
  range: '6h', source: '', client_ip: '', domain: '', qtype: '', rcode: '', protocol: '', outcome: '',
}

function pad(value: number) {
  return String(value).padStart(2, '0')
}

function formatLocalDateTime(date: Date) {
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`
}

function localDateTimeToRFC3339(value: string) {
  const date = new Date(value)
  if (!value || Number.isNaN(date.getTime())) return null
  const offsetMinutes = -date.getTimezoneOffset()
  const sign = offsetMinutes >= 0 ? '+' : '-'
  const absolute = Math.abs(offsetMinutes)
  return `${formatLocalDateTime(date)}${sign}${pad(Math.floor(absolute / 60))}:${pad(absolute % 60)}`
}

function rfc3339ToLocalDateTime(value: string | null) {
  if (!value) return ''
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '' : formatLocalDateTime(date)
}

function formatOffset(value: string) {
  const date = new Date(value)
  if (!value || Number.isNaN(date.getTime())) return null
  const offsetMinutes = -date.getTimezoneOffset()
  const sign = offsetMinutes >= 0 ? '+' : '-'
  const absolute = Math.abs(offsetMinutes)
  return `UTC${sign}${pad(Math.floor(absolute / 60))}:${pad(absolute % 60)}`
}

function timezoneLabel(fromLocal: string, toLocal: string) {
  const zone = Intl.DateTimeFormat().resolvedOptions().timeZone || 'Browser local time'
  const offsets = [...new Set([formatOffset(fromLocal), formatOffset(toLocal)].filter(Boolean))]
  return offsets.length ? `${zone} (${offsets.join(' → ')})` : zone
}

function retentionMilliseconds(value?: string) {
  const match = value?.trim().match(/^(\d+)([mhd])$/)
  if (!match) return null
  const amount = Number(match[1])
  const unit = match[2] === 'm' ? 60_000 : match[2] === 'h' ? 3_600_000 : 86_400_000
  return amount * unit
}

function createInitialDraft(searchParams: URLSearchParams): SearchDraft {
  const requestedRange = searchParams.get('range')
  const hasCustomBounds = Boolean(searchParams.get('from') || searchParams.get('to'))
  const range: SearchRange = requestedRange === 'custom' || hasCustomBounds
    ? 'custom'
    : ranges.includes(requestedRange as TimeRange) ? requestedRange as TimeRange : relativeInitialFilters.range
  return {
    ...relativeInitialFilters,
    range,
    fromLocal: range === 'custom' ? rfc3339ToLocalDateTime(searchParams.get('from')) : '',
    toLocal: range === 'custom' ? rfc3339ToLocalDateTime(searchParams.get('to')) : '',
    source: searchParams.get('source') ?? '',
    client_ip: searchParams.get('client_ip') ?? '',
    domain: searchParams.get('domain') ?? '',
  }
}

function buildFilters(draft: SearchDraft, retention?: string): { filters?: SearchFilters; error?: string } {
  const { fromLocal, toLocal, ...filters } = draft
  if (draft.range !== 'custom') return { filters }

  const from = localDateTimeToRFC3339(fromLocal)
  const to = localDateTimeToRFC3339(toLocal)
  if (!from || !to) return { error: 'From and To are required.' }
  const fromTime = new Date(from).getTime()
  const toTime = new Date(to).getTime()
  if (fromTime >= toTime) return { error: 'From must be earlier than To.' }
  const retentionMS = retentionMilliseconds(retention)
  if (retentionMS && toTime - fromTime > retentionMS) {
    return { error: 'The selected window is longer than raw event retention ({retention}).' }
  }
  return { filters: { ...filters, range: 'custom', from, to } }
}

function retentionWarning(draft: SearchDraft, retention: string | undefined, now?: number) {
  if (draft.range !== 'custom') return null
  const retentionMS = retentionMilliseconds(retention)
  const from = localDateTimeToRFC3339(draft.fromLocal)
  const to = localDateTimeToRFC3339(draft.toLocal)
  if (!retentionMS || !from || !to || now === undefined) return null
  const cutoff = now - retentionMS
  if (new Date(to).getTime() <= cutoff) return 'This window is outside raw event retention; matching events may already have expired.'
  if (new Date(from).getTime() < cutoff) return 'Part of this window is outside raw event retention and may be incomplete.'
  return null
}

export function SearchPage() {
  const { statusLabel, t } = useI18n()
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()
  const initialDraft = createInitialDraft(searchParams)
  const initialResult = buildFilters(initialDraft)
  const [investigation, setInvestigation] = useState('')
  const [draft, setDraft] = useState<SearchDraft>(initialDraft)
  const [filters, setFilters] = useState<SearchFilters>(initialResult.filters ?? relativeInitialFilters)
  const [events, setEvents] = useState<DnsEvent[]>([])
  const [cursor, setCursor] = useState<string | undefined>()
  const [loading, setLoading] = useState(true)
  const [loadingMore, setLoadingMore] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [formError, setFormError] = useState<string | null>(initialResult.error ?? null)
  const [rawRetention, setRawRetention] = useState<string>()
  const [retentionReferenceMS, setRetentionReferenceMS] = useState<number>()
  const controller = useRef<AbortController | null>(null)

  const load = async (nextFilters: SearchFilters, nextCursor?: string, append = false) => {
    controller.current?.abort()
    const requestController = new AbortController()
    controller.current = requestController
    if (append) setLoadingMore(true)
    else setLoading(true)
    setError(null)

    try {
      const response: SearchResponse = await api.search(nextFilters, { cursor: nextCursor, limit: 100 }, requestController.signal)
      setEvents((current) => append ? [...current, ...response.events] : response.events)
      setCursor(response.next_cursor)
    } catch (requestError) {
      if (requestError instanceof DOMException && requestError.name === 'AbortError') return
      setError(requestError instanceof Error ? requestError.message : String(requestError))
    } finally {
      setLoading(false)
      setLoadingMore(false)
    }
  }

  useEffect(() => {
    void load(filters)
    return () => controller.current?.abort()
  }, [filters])

  useEffect(() => {
    const requestController = new AbortController()
    void api.meta(requestController.signal).then((meta) => {
      setRawRetention(meta.retention.raw)
      const serverTime = Date.parse(meta.time)
      setRetentionReferenceMS(Number.isNaN(serverTime) ? Date.now() : serverTime)
    }).catch(() => undefined)
    return () => requestController.abort()
  }, [])

  const warning = useMemo(() => retentionWarning(draft, rawRetention, retentionReferenceMS), [draft, rawRetention, retentionReferenceMS])
  const displayedFormError = formError ? t(formError, { retention: rawRetention ?? '' }) : null

  const apply = (event: FormEvent) => {
    event.preventDefault()
    const result = buildFilters(draft, rawRetention)
    if (!result.filters) {
      setFormError(result.error ?? 'Invalid custom time range.')
      return
    }
    setFormError(null)
    setFilters(result.filters)
  }

  const selectRange = (range: SearchRange) => {
    if (range !== 'custom' || (draft.fromLocal && draft.toLocal)) {
      setDraft({ ...draft, range })
      setFormError(null)
      return
    }
    const to = new Date()
    const from = new Date(to.getTime() - 15 * 60_000)
    setDraft({ ...draft, range, fromLocal: formatLocalDateTime(from), toLocal: formatLocalDateTime(to) })
    setFormError(null)
  }

  return (
    <div className="page search-page">
      <PageHeader
        eyebrow={t('QUERY EXPLORER')}
        title={t('Historical search')}
        description={t('Inspect retained raw DNS events. Results are ordered newest first.')}
      />

      <Panel title={t('Investigate an IP or domain')} subtitle={t('Open a focused Inspector; raw Query Explorer remains available below.')} className="investigation-entry">
        <form onSubmit={(event) => {
          event.preventDefault()
          const value = investigation.trim()
          if (!value) return
          const looksLikeIP = value.includes(':') || /^\d{1,3}(\.\d{1,3}){3}$/.test(value)
          navigate(`/${looksLikeIP ? 'clients' : 'domains'}/${encodeURIComponent(value)}`)
        }}><input aria-label={t('IP or domain')} value={investigation} onChange={(event) => setInvestigation(event.target.value)} placeholder="192.0.2.10 or example.com" /><button className="button primary" type="submit"><Search size={15} />{t('Investigate')}</button></form>
      </Panel>

      <Panel title={t('Search filters')} subtitle={rawRetention ? t('Raw event retention: {retention}.', { retention: rawRetention }) : t('Raw event retention is reported by backend.')} className="filter-panel">
        <form className="filter-grid search-filter-grid" onSubmit={apply}>
          <label><span>{t('Range')}</span><select aria-label={t('Range')} value={draft.range} onChange={(event) => selectRange(event.target.value as SearchRange)}>{ranges.map((range) => <option key={range}>{range}</option>)}<option value="custom">{t('Custom')}</option></select></label>
          {draft.range === 'custom' && <div className="custom-time-controls">
            <label><span>{t('From')}</span><input aria-label={t('From')} type="datetime-local" step="1" value={draft.fromLocal} onChange={(event) => { setDraft({ ...draft, fromLocal: event.target.value }); setFormError(null) }} /></label>
            <label><span>{t('To')}</span><input aria-label={t('To')} type="datetime-local" step="1" value={draft.toLocal} onChange={(event) => { setDraft({ ...draft, toLocal: event.target.value }); setFormError(null) }} /></label>
            <div className="custom-time-zone"><span>{t('Timezone')}</span><strong>{timezoneLabel(draft.fromLocal, draft.toLocal)}</strong></div>
          </div>}
          <label><span>{t('Source')}</span><input value={draft.source} onChange={(event) => setDraft({ ...draft, source: event.target.value })} placeholder="source-id" /></label>
          <label><span>{t('Client IP')}</span><input value={draft.client_ip} onChange={(event) => setDraft({ ...draft, client_ip: event.target.value })} placeholder="192.0.2.10" /></label>
          <label><span>{t('Domain')}</span><input value={draft.domain} onChange={(event) => setDraft({ ...draft, domain: event.target.value })} placeholder="example.com" /></label>
          <label><span>QTYPE</span><input aria-label="QTYPE" value={draft.qtype} onChange={(event) => setDraft({ ...draft, qtype: event.target.value })} placeholder="A" /></label>
          <label><span>RCODE</span><input aria-label="RCODE" value={draft.rcode} onChange={(event) => setDraft({ ...draft, rcode: event.target.value })} placeholder="NXDOMAIN" /></label>
          <label><span>{t('Outcome')}</span><select value={draft.outcome} onChange={(event) => setDraft({ ...draft, outcome: event.target.value })}><option value="">{t('Any')}</option><option value="RESPONSE">{statusLabel('RESPONSE')}</option><option value="NO_RESPONSE">{statusLabel('NO_RESPONSE')}</option><option value="UNMATCHED_RESPONSE">{statusLabel('UNMATCHED_RESPONSE')}</option></select></label>
          <label><span>{t('Protocol')}</span><select value={draft.protocol} onChange={(event) => setDraft({ ...draft, protocol: event.target.value })}><option value="">{t('Any')}</option><option>UDP</option><option>TCP</option></select></label>
          <button className="button primary" type="submit"><SlidersHorizontal size={15} />{t('Run search')}</button>
          {(displayedFormError || warning) && <div className={`custom-time-feedback ${displayedFormError ? 'error' : 'warning'}`} role={displayedFormError ? 'alert' : 'status'}>{displayedFormError ?? t(warning ?? '')}</div>}
        </form>
      </Panel>

      <Panel
        title={t('DNS events')}
        subtitle={loading ? t('Searching…') : t('{count} events loaded · cursor pagination', { count: events.length })}
        className="events-panel"
      >
        {loading && <LoadingState rows={8} />}
        {error && !loading && <ErrorState message={error} onRetry={() => void load(filters)} />}
        {!loading && !error && events.length === 0 && <EmptyState title={t('No matching DNS events')} />}
        {!loading && events.length > 0 && <EventsTable events={events} />}
        {!loading && cursor && (
          <div className="load-more-row">
            <button className="button secondary" disabled={loadingMore} onClick={() => void load(filters, cursor, true)}>
              <Search size={15} />{loadingMore ? t('Loading…') : t('Load next page')}
            </button>
          </div>
        )}
      </Panel>
    </div>
  )
}
