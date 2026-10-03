import { Copy } from 'lucide-react'
import { Link } from 'react-router-dom'
import { formatBytes, formatLatency, formatTime } from '../format'
import type { DnsEvent } from '../types'
import { useI18n } from '../i18n'
import { EmptyState, StatusBadge } from './Common'

export function EventsTable({ events, live = false }: { events: DnsEvent[]; live?: boolean }) {
  const { t } = useI18n()
  if (events.length === 0) {
    return <EmptyState title={live ? 'Waiting for DNS events' : 'No matching events'} detail={live ? 'The stream is connected. New matching events will appear here.' : 'Try a wider range or remove one or more filters.'} />
  }

  return (
    <div className="table-scroll events-scroll">
      <table className="data-table events-table">
        <thead>
          <tr>
            <th>{t('Time')}</th>
            <th>{t('Source')}</th>
            <th>{t('Client')}</th>
            <th>{t('Protocol')}</th>
            <th>{t('Domain')}</th>
            <th>{t('Type')}</th>
            <th>{t('Outcome')}</th>
            <th>RCODE</th>
            <th className="align-right">{t('Latency')}</th>
            <th className="align-right">{t('Answers')}</th>
            <th className="align-right">{t('Bytes')}</th>
          </tr>
        </thead>
        <tbody>
          {events.map((event, index) => (
            <tr className={`event-${event.outcome.toLowerCase()}`} key={`${event.event_time}-${event.dns_id}-${event.client_port}-${index}`}>
              <td className="mono muted">{formatTime(event.event_time)}</td>
              <td>{event.source_id}</td>
              <td><span className="copy-cell"><Link className="cell-link mono" to={`/clients/${encodeURIComponent(event.client_ip)}`}>{event.client_ip}</Link><button aria-label={`${t('Copy client')} ${event.client_ip}`} title={t('Copy client')} onClick={() => void navigator.clipboard.writeText(event.client_ip)}><Copy size={11} /></button></span></td>
              <td className="muted">{event.protocol}</td>
              <td className="domain-cell"><span className="copy-cell"><Link className="cell-link" to={`/domains/${encodeURIComponent(event.qname)}`}>{event.qname}</Link><button aria-label={`${t('Copy domain')} ${event.qname}`} title={t('Copy domain')} onClick={() => void navigator.clipboard.writeText(event.qname)}><Copy size={11} /></button></span></td>
              <td><span className="query-type">{event.qtype}</span></td>
              <td title={event.outcome === 'NO_RESPONSE' ? t('No client response observed within correlation window') : undefined}><StatusBadge value={event.outcome} /></td>
              <td>{event.rcode ? <StatusBadge value={event.rcode} /> : <span className="muted">—</span>}</td>
              <td className="align-right mono">{formatLatency(event.latency_us)}</td>
              <td className="align-right mono">{event.answer_count}</td>
              <td className="align-right mono">{formatBytes(event.response_bytes)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
