import { AlertCircle, ChevronRight, RefreshCw } from 'lucide-react'
import type { ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { ranges } from '../constants'
import { useI18n } from '../i18n'
import type { TimeRange } from '../types'

export function PageHeader({
  eyebrow,
  title,
  description,
  actions,
}: {
  eyebrow?: string
  title: string
  description?: string
  actions?: ReactNode
}) {
  return (
    <header className="page-header">
      <div>
        {eyebrow && <div className="eyebrow">{eyebrow}</div>}
        <h1>{title}</h1>
        {description && <p>{description}</p>}
      </div>
      {actions && <div className="page-actions">{actions}</div>}
    </header>
  )
}

export function RangePicker({ value, onChange }: {
  value: TimeRange
  onChange: (value: TimeRange) => void
}) {
  const { t } = useI18n()
  return (
    <div className="range-picker" aria-label={t('Time range')}>
      {ranges.map((range) => (
        <button key={range} className={value === range ? 'selected' : ''} onClick={() => onChange(range)}>
          {range}
        </button>
      ))}
    </div>
  )
}

export function Panel({ title, subtitle, action, children, className = '' }: {
  title: string
  subtitle?: string
  action?: ReactNode
  children: ReactNode
  className?: string
}) {
  return (
    <section className={`panel ${className}`}>
      <div className="panel-header">
        <div>
          <h2>{title}</h2>
          {subtitle && <p>{subtitle}</p>}
        </div>
        {action}
      </div>
      <div className="panel-body">{children}</div>
    </section>
  )
}

export function MetricCard({ label, value, detail, tone = 'default' }: {
  label: string
  value: ReactNode
  detail?: ReactNode
  tone?: 'default' | 'warning' | 'danger'
}) {
  return (
    <div className={`metric-card tone-${tone}`}>
      <span>{label}</span>
      <strong>{value}</strong>
      {detail && <small>{detail}</small>}
    </div>
  )
}

export function StatusBadge({ value }: { value: string }) {
  const { statusLabel } = useI18n()
  const normalized = value.toLowerCase()
  const tone = ['active', 'ok', 'noerror', 'connected', 'live', 'healthy', 'response', 'applied', 'enabled', 'unblocked'].includes(normalized)
    ? 'good'
    : ['warning', 'delayed', 'connecting', 'degraded', 'connected_no_events', 'unexpected', 'pending', 'partial'].includes(normalized)
      ? 'warning'
      : ['critical', 'silent', 'error', 'nxdomain', 'servfail', 'offline', 'disconnected', 'never_seen_since_start', 'no_response', 'unmatched_response', 'unavailable', 'blocked'].includes(normalized)
        ? 'danger'
        : 'neutral'
  return <span className={`status-badge status-${tone}`}><i />{statusLabel(value)}</span>
}

export function ErrorState({ message, onRetry }: { message: string; onRetry?: () => void }) {
  const { language, t } = useI18n()
  const localizedMessage = language === 'ru'
    ? message.replace('Pulse API is unreachable', t('Pulse API is unreachable')).replace('Pulse API request failed', 'Запрос к Pulse API завершился ошибкой')
    : message
  return (
    <div className="state-block state-error">
      <AlertCircle size={22} />
      <div>
        <strong>{t('Unable to load data')}</strong>
        <span>{localizedMessage}</span>
      </div>
      {onRetry && <button className="button secondary" onClick={onRetry}><RefreshCw size={14} />{t('Retry')}</button>}
    </div>
  )
}

export function EmptyState({ title, detail }: {
  title?: string
  detail?: string
}) {
  const { t } = useI18n()
  return (
    <div className="state-block state-empty">
      <div className="empty-glyph" />
      <strong>{t(title ?? 'No data in this range')}</strong>
      <span>{t(detail ?? 'Try a wider time range or adjust the filters.')}</span>
    </div>
  )
}

export function LoadingState({ rows = 4 }: { rows?: number }) {
  const { t } = useI18n()
  return (
    <div className="loading-state" aria-label={t('Loading')}>
      {Array.from({ length: rows }).map((_, index) => <i key={index} />)}
    </div>
  )
}

export function ViewAll({ to, label = 'View all' }: { to: string; label?: string }) {
  const { t } = useI18n()
  return <Link className="view-all" to={to}>{t(label)}<ChevronRight size={14} /></Link>
}
