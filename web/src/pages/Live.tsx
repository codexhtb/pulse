import { CirclePause, CirclePlay, Eraser, SlidersHorizontal } from 'lucide-react'
import { FormEvent, useEffect, useRef, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { liveURL } from '../api'
import { EventsTable } from '../components/EventsTable'
import { MetricCard, PageHeader, Panel, StatusBadge } from '../components/Common'
import type { DnsEvent, LiveStats } from '../types'
import { useI18n } from '../i18n'

interface LiveFilters {
  source: string
  client_ip: string
  domain: string
  qtype: string
  rcode: string
  protocol: string
  outcome: string
  failures: boolean
  slow_us?: number
  rate: number
}

const defaults: LiveFilters = {
  source: '', client_ip: '', domain: '', qtype: '', rcode: '', protocol: '', outcome: '', failures: false, slow_us: undefined, rate: 200,
}

const emptyStats: LiveStats = {
  matched_eps: 0, shown_eps: 0, suppressed: 0, queue_dropped: 0,
  rate_limit: 200, subscribers: 0, server_received: 0,
}

export function LivePage() {
  const { statusLabel, t } = useI18n()
  const [searchParams] = useSearchParams()
  const initialFilters = () => ({
    ...defaults,
    client_ip: searchParams.get('client_ip') ?? '', domain: searchParams.get('domain') ?? '', source: searchParams.get('source') ?? '',
    qtype: searchParams.get('qtype') ?? '', rcode: searchParams.get('rcode') ?? '', outcome: searchParams.get('outcome') ?? '',
    failures: searchParams.get('failures') === 'true', slow_us: searchParams.get('slow_us') ? Number(searchParams.get('slow_us')) : undefined,
  })
  const [draft, setDraft] = useState<LiveFilters>(initialFilters)
  const [filters, setFilters] = useState<LiveFilters>(initialFilters)
  const [events, setEvents] = useState<DnsEvent[]>([])
  const [stats, setStats] = useState<LiveStats>(emptyStats)
  const [state, setState] = useState<'connecting' | 'live' | 'reconnecting'>('connecting')
  const [paused, setPaused] = useState(false)
  const [autoScroll, setAutoScroll] = useState(true)
  const pending = useRef<DnsEvent[]>([])
  const pausedRef = useRef(paused)
  const autoScrollRef = useRef(autoScroll)
  const streamTable = useRef<HTMLDivElement>(null)

  useEffect(() => { pausedRef.current = paused }, [paused])
  useEffect(() => { autoScrollRef.current = autoScroll }, [autoScroll])

  useEffect(() => {
    setState('connecting')
    pending.current = []
    const stream = new EventSource(liveURL(filters))

    stream.addEventListener('ready', () => setState('live'))
    stream.addEventListener('dns', (message) => {
      if (pausedRef.current) return
      try {
        pending.current.push(JSON.parse((message as MessageEvent).data) as DnsEvent)
      } catch {
        // A malformed live datagram is ignored; the stream remains usable.
      }
    })
    stream.addEventListener('stats', (message) => {
      try {
        setStats(JSON.parse((message as MessageEvent).data) as LiveStats)
      } catch {
        // Keep the last valid stats sample.
      }
    })
    stream.onopen = () => setState('live')
    stream.onerror = () => setState('reconnecting')

    const flush = window.setInterval(() => {
      if (pending.current.length === 0) return
      const batch = pending.current.splice(0, pending.current.length)
      setEvents((current) => [...batch.reverse(), ...current].slice(0, 1000))
      if (autoScrollRef.current) streamTable.current?.scrollTo({ top: 0 })
    }, 200)

    return () => {
      window.clearInterval(flush)
      stream.close()
    }
  }, [filters])

  const apply = (event: FormEvent) => {
    event.preventDefault()
    setEvents([])
    setFilters({ ...draft, rate: Math.min(1000, Math.max(1, Number(draft.rate) || 200)) })
  }

  return (
    <div className="page live-page">
      <PageHeader
        eyebrow={t('REAL-TIME')}
        title={t('Live DNS events')}
        description={t('Best-effort event stream from the active collector pipeline.')}
        actions={<div className="header-status"><StatusBadge value={state} /><span>{t('{count} buffered', { count: events.length })}</span></div>}
      />

      <section className="metric-grid metric-grid-live">
        <MetricCard label={t('Matched / sec')} value={stats.matched_eps} />
        <MetricCard label={t('Shown / sec')} value={stats.shown_eps} />
        <MetricCard label={t('Suppressed')} value={stats.suppressed} tone={stats.suppressed ? 'warning' : 'default'} />
        <MetricCard label={t('Queue dropped')} value={stats.queue_dropped} tone={stats.queue_dropped ? 'danger' : 'default'} />
        <MetricCard label={t('Rate limit')} value={`${stats.rate_limit}/s`} />
        <MetricCard label={t('Subscribers')} value={stats.subscribers} />
      </section>

      <Panel title={t('Stream filters')} subtitle={t('Filters are applied server-side when the stream reconnects.')} className="filter-panel">
        <form className="filter-grid live-filter-grid" onSubmit={apply}>
          <label><span>{t('Source')}</span><input value={draft.source} onChange={(event) => setDraft({ ...draft, source: event.target.value })} placeholder={t('Any source')} /></label>
          <label><span>{t('Client IP')}</span><input value={draft.client_ip} onChange={(event) => setDraft({ ...draft, client_ip: event.target.value })} placeholder={t('Any client')} /></label>
          <label><span>{t('Domain')}</span><input value={draft.domain} onChange={(event) => setDraft({ ...draft, domain: event.target.value })} placeholder={t('Any domain')} /></label>
          <label><span>QTYPE</span><input value={draft.qtype} onChange={(event) => setDraft({ ...draft, qtype: event.target.value })} placeholder={t('Any type')} /></label>
          <label><span>RCODE</span><input value={draft.rcode} onChange={(event) => setDraft({ ...draft, rcode: event.target.value })} placeholder={t('Any code')} /></label>
          <label><span>{t('Outcome')}</span><select value={draft.outcome} onChange={(event) => setDraft({ ...draft, outcome: event.target.value })}><option value="">{t('Any')}</option><option value="RESPONSE">{statusLabel('RESPONSE')}</option><option value="NO_RESPONSE">{statusLabel('NO_RESPONSE')}</option><option value="UNMATCHED_RESPONSE">{statusLabel('UNMATCHED_RESPONSE')}</option></select></label>
          <label><span>{t('Protocol')}</span><select value={draft.protocol} onChange={(event) => setDraft({ ...draft, protocol: event.target.value })}><option value="">{t('Any')}</option><option>UDP</option><option>TCP</option></select></label>
          <label><span>{t('Slow threshold (ms)')}</span><input type="number" min="0" value={draft.slow_us ? draft.slow_us / 1000 : ''} onChange={(event) => setDraft({ ...draft, slow_us: event.target.value ? Number(event.target.value) * 1000 : undefined })} placeholder={t('Disabled')} /></label>
          <label className="checkbox-label"><span>{t('Investigation')}</span><span><input type="checkbox" checked={draft.failures} onChange={(event) => setDraft({ ...draft, failures: event.target.checked })} />{t('Failures only')}</span></label>
          <label><span>{t('Rate / sec')}</span><input type="number" min="1" max="1000" value={draft.rate} onChange={(event) => setDraft({ ...draft, rate: Number(event.target.value) })} /></label>
          <button className="button primary" type="submit"><SlidersHorizontal size={15} />{t('Apply filters')}</button>
        </form>
      </Panel>

      <Panel
        title={t('Event stream')}
        subtitle={t('Newest first · browser buffer capped at 1,000 events')}
        action={<div className="button-row">
          <button className="button secondary" onClick={() => setPaused((value) => !value)}>{paused ? <CirclePlay size={15} /> : <CirclePause size={15} />}{paused ? t('Resume') : t('Pause')}</button>
          <button className={`button secondary ${autoScroll ? 'selected-control' : ''}`} onClick={() => setAutoScroll((value) => !value)}>{t('Auto-scroll')} {autoScroll ? t('on') : t('off')}</button>
          <button className="button secondary" onClick={() => { pending.current = []; setEvents([]) }}><Eraser size={15} />{t('Clear')}</button>
        </div>}
        className="events-panel"
      >
        {paused && <div className="pause-notice">{t('Display paused — incoming events are temporarily ignored.')}</div>}
        <div ref={streamTable} className="live-table-scroll"><EventsTable events={events} live /></div>
      </Panel>
    </div>
  )
}
