import { Search, SlidersHorizontal } from 'lucide-react'
import { FormEvent, useEffect, useRef, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { api } from '../api'
import { EmptyState, ErrorState, LoadingState, PageHeader, Panel } from '../components/Common'
import { EventsTable } from '../components/EventsTable'
import { ranges } from '../constants'
import type { DnsEvent, QueryFilters, SearchResponse, TimeRange } from '../types'
import { useI18n } from '../i18n'

const initialFilters: QueryFilters = {
  range: '6h', source: '', client_ip: '', domain: '', qtype: '', rcode: '', protocol: '', outcome: '',
}

export function SearchPage() {
  const { statusLabel, t } = useI18n()
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()
  const initialFromURL = (): QueryFilters => {
    const requestedRange = searchParams.get('range') as TimeRange | null
    return {
      ...initialFilters,
      range: requestedRange && ranges.includes(requestedRange) ? requestedRange : initialFilters.range,
      source: searchParams.get('source') ?? '',
      client_ip: searchParams.get('client_ip') ?? '',
      domain: searchParams.get('domain') ?? '',
    }
  }
  const [investigation, setInvestigation] = useState('')
  const [draft, setDraft] = useState<QueryFilters>(initialFromURL)
  const [filters, setFilters] = useState<QueryFilters>(initialFromURL)
  const [events, setEvents] = useState<DnsEvent[]>([])
  const [cursor, setCursor] = useState<string | undefined>()
  const [loading, setLoading] = useState(true)
  const [loadingMore, setLoadingMore] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const controller = useRef<AbortController | null>(null)

  const load = async (nextFilters: QueryFilters, nextCursor?: string, append = false) => {
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

  const apply = (event: FormEvent) => {
    event.preventDefault()
    setFilters({ ...draft })
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

      <Panel title={t('Search filters')} subtitle={t('Raw event retention is limited to 24 hours.')} className="filter-panel">
        <form className="filter-grid search-filter-grid" onSubmit={apply}>
          <label><span>{t('Range')}</span><select value={draft.range} onChange={(event) => setDraft({ ...draft, range: event.target.value as TimeRange })}>{ranges.map((range) => <option key={range}>{range}</option>)}</select></label>
          <label><span>{t('Source')}</span><input value={draft.source} onChange={(event) => setDraft({ ...draft, source: event.target.value })} placeholder="source-id" /></label>
          <label><span>{t('Client IP')}</span><input value={draft.client_ip} onChange={(event) => setDraft({ ...draft, client_ip: event.target.value })} placeholder="192.0.2.10" /></label>
          <label><span>{t('Domain')}</span><input value={draft.domain} onChange={(event) => setDraft({ ...draft, domain: event.target.value })} placeholder="example.com" /></label>
          <label><span>QTYPE</span><input value={draft.qtype} onChange={(event) => setDraft({ ...draft, qtype: event.target.value })} placeholder="A" /></label>
          <label><span>RCODE</span><input value={draft.rcode} onChange={(event) => setDraft({ ...draft, rcode: event.target.value })} placeholder="NXDOMAIN" /></label>
          <label><span>{t('Outcome')}</span><select value={draft.outcome} onChange={(event) => setDraft({ ...draft, outcome: event.target.value })}><option value="">{t('Any')}</option><option value="RESPONSE">{statusLabel('RESPONSE')}</option><option value="NO_RESPONSE">{statusLabel('NO_RESPONSE')}</option><option value="UNMATCHED_RESPONSE">{statusLabel('UNMATCHED_RESPONSE')}</option></select></label>
          <label><span>{t('Protocol')}</span><select value={draft.protocol} onChange={(event) => setDraft({ ...draft, protocol: event.target.value })}><option value="">{t('Any')}</option><option>UDP</option><option>TCP</option></select></label>
          <button className="button primary" type="submit"><SlidersHorizontal size={15} />{t('Run search')}</button>
        </form>
      </Panel>

      <Panel
        title={t('DNS events')}
        subtitle={loading ? t('Searching…') : t('{count} events loaded · cursor pagination', { count: events.length })}
        className="events-panel"
      >
        {loading && <LoadingState rows={8} />}
        {error && !loading && <ErrorState message={error} onRetry={() => void load(filters)} />}
        {!loading && !error && events.length === 0 && <EmptyState title="No matching DNS events" />}
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
