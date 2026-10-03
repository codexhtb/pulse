import { ArrowLeft } from 'lucide-react'
import { useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { api } from '../api'
import { ErrorState, LoadingState, MetricCard, PageHeader, Panel, RangePicker, StatusBadge } from '../components/Common'
import { formatCompact, formatDateTime, formatLatency } from '../format'
import { useAsync } from '../hooks/useAsync'
import type { TimeRange } from '../types'
import { useI18n } from '../i18n'

export function SourceDetailPage() {
  const { t } = useI18n()
  const { source = '' } = useParams()
  const sourceID = decodeURIComponent(source)
  const [range, setRange] = useState<TimeRange>('6h')
  const state = useAsync((signal) => api.source(sourceID, range, signal), [sourceID, range], 10_000)
  return <div className="page detail-page">
    <Link className="back-link" to="/sources"><ArrowLeft size={14} />{t('All sources')}</Link>
    <PageHeader eyebrow={t('SOURCE')} title={sourceID} description={t('Connection, ingestion, outcomes, and silence state.')} actions={<div className="button-row">{state.data && <StatusBadge value={state.data.state} />}<RangePicker value={range} onChange={setRange} /></div>} />
    {state.loading && !state.data && <LoadingState rows={7} />}
    {state.error && !state.data && <ErrorState message={state.error.message} onRetry={state.refresh} />}
    {state.data && <>
      <section className="metric-grid metric-grid-eight">
        <MetricCard label={t('Configuration')} value={t(state.data.expected ? 'Expected' : 'Unexpected')} tone={state.data.unexpected ? 'warning' : 'default'} />
        <MetricCard label={t('Connection')} value={t(state.data.connected ? 'Established' : 'Disconnected')} />
        <MetricCard label={t('Ingest rate')} value={`${state.data.ingest_rate_per_sec.toFixed(1)}/s`} />
        <MetricCard label={t('Queries')} value={formatCompact(state.data.queries)} />
        <MetricCard label={t('Responses')} value={formatCompact(state.data.responses)} />
        <MetricCard label="NO_RESPONSE" value={formatCompact(state.data.no_response)} tone={state.data.no_response ? 'warning' : 'default'} />
        <MetricCard label="SERVFAIL" value={formatCompact(state.data.servfail)} tone={state.data.servfail ? 'danger' : 'default'} />
        <MetricCard label={t('Avg latency')} value={formatLatency(state.data.avg_latency_us)} />
      </section>
      <Panel title={t('Runtime and persistence')} subtitle={t('Counters since collector start · analytics over {range}', { range })}>
        <dl className="health-list">
          <div><dt>{t('Remote address')}</dt><dd>{state.data.remote_address || '—'}</dd></div>
          <div><dt>{t('Last message')}</dt><dd>{state.data.last_message_at ? formatDateTime(state.data.last_message_at) : '—'}</dd></div>
          <div><dt>{t('Last persisted')}</dt><dd>{state.data.last_persisted_at ? formatDateTime(state.data.last_persisted_at) : '—'}</dd></div>
          <div><dt>{t('Silence age')}</dt><dd>{state.data.age_seconds === null ? '—' : `${state.data.age_seconds}s`}</dd></div>
          <div><dt>{t('Frames / queries / responses')}</dt><dd>{formatCompact(state.data.frames_since_start)} / {formatCompact(state.data.queries_since_start)} / {formatCompact(state.data.responses_since_start)}</dd></div>
          <div><dt>{t('Runtime errors')}</dt><dd>{formatCompact(state.data.errors_since_start)}</dd></div>
          <div><dt>{t('NXDOMAIN / unmatched')}</dt><dd>{formatCompact(state.data.nxdomain)} / {formatCompact(state.data.unmatched_responses)}</dd></div>
        </dl>
      </Panel>
    </>}
  </div>
}
