import { Area, AreaChart, CartesianGrid, Line, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'
import { formatCompact, formatLatency, formatPercent } from '../format'
import type { TrafficPoint } from '../types'
import { useI18n } from '../i18n'
import { EmptyState } from './Common'

export type TrafficChartMode = 'traffic' | 'errors' | 'latency'

function axisTime(value: string, locale: string): string {
  const date = new Date(value)
  return new Intl.DateTimeFormat(locale, {
    hour: '2-digit', minute: '2-digit', month: 'short', day: '2-digit', hour12: false,
  }).format(date)
}

function bucketLabel(seconds: number): string {
  if (seconds < 60) return `${seconds}s`
  if (seconds % 3600 === 0) return `${seconds / 3600}h`
  return `${seconds / 60}m`
}

export function TrafficChart({
  data,
  compact = false,
  investigation = false,
  operational = false,
  mode = 'traffic',
}: {
  data: TrafficPoint[]
  compact?: boolean
  investigation?: boolean
  operational?: boolean
  mode?: TrafficChartMode
}) {
  const { locale, t } = useI18n()
  if (data.length === 0) return <EmptyState />

  const volumeKey = operational ? 'qps' : 'queries'
  const axisFormatter = mode === 'errors'
    ? (value: number) => `${value.toFixed(value >= 10 ? 0 : 1)}%`
    : mode === 'latency'
      ? (value: number) => formatLatency(value)
      : formatCompact

  return (
    <div className={compact ? 'chart chart-compact' : 'chart'}>
      <ResponsiveContainer width="100%" height="100%">
        <AreaChart data={data} margin={{ top: 8, right: 12, bottom: 0, left: -8 }}>
          <defs>
            <linearGradient id="queryFill" x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor="#32b8d8" stopOpacity={0.28} />
              <stop offset="92%" stopColor="#32b8d8" stopOpacity={0} />
            </linearGradient>
          </defs>
          <CartesianGrid stroke="#202832" strokeDasharray="3 5" vertical={false} />
          <XAxis
            dataKey="time"
            tickFormatter={(value) => axisTime(value, locale)}
            tick={{ fill: '#718092', fontSize: 11 }}
            tickLine={false}
            axisLine={false}
            minTickGap={38}
          />
          <YAxis
            yAxisId="primary"
            allowDecimals={mode !== 'traffic' || operational}
            tickFormatter={axisFormatter}
            tick={{ fill: '#718092', fontSize: 11 }}
            tickLine={false}
            axisLine={false}
            width={58}
          />
          {investigation && <YAxis yAxisId="latency" orientation="right" hide />}
          <Tooltip
            content={({ active, payload, label }) => {
              if (!active || !payload?.length) return null
              const point = payload[0].payload as TrafficPoint
              if (!operational) return (
                <div className="chart-tooltip">
                  <span>{axisTime(String(label), locale)}</span>
                  <strong>{formatCompact(point.queries)} {t('queries')}</strong>
                  <small>{formatCompact(point.nxdomain)} NXDOMAIN · {formatCompact(point.servfail)} SERVFAIL</small>
                  <small>{formatLatency(point.avg_latency_us)} {t('avg latency')}</small>
                </div>
              )
              return (
                <div className="chart-tooltip operational-tooltip">
                  <span>{axisTime(String(label), locale)}</span>
                  <strong>{formatCompact(point.qps)} QPS {t('avg')}</strong>
                  <small>{formatCompact(point.queries)} {t('queries')} / {bucketLabel(point.bucket_seconds)}</small>
                  <small>{formatCompact(point.nxdomain)} NXDOMAIN · {formatPercent(point.nxdomain_pct)}</small>
                  <small>{formatCompact(point.servfail)} SERVFAIL · {formatPercent(point.servfail_pct)}</small>
                  <small>P95 {t('latency').toLowerCase()} {point.latency_samples ? formatLatency(point.p95_latency_us) : '—'}</small>
                </div>
              )
            }}
          />
          {mode === 'traffic' && <Area
            type="monotone"
            dataKey={volumeKey}
            yAxisId="primary"
            stroke="#38b9d8"
            strokeWidth={2}
            fill="url(#queryFill)"
            activeDot={{ r: 4, fill: '#78d8ed', stroke: '#0b1117', strokeWidth: 2 }}
          />}
          {mode === 'errors' && <>
            <Line yAxisId="primary" type="monotone" dataKey="nxdomain_pct" name="NXDOMAIN" stroke="#e8ad5a" strokeWidth={1.8} dot={false} />
            <Line yAxisId="primary" type="monotone" dataKey="servfail_pct" name="SERVFAIL" stroke="#e26d76" strokeWidth={1.8} dot={false} />
          </>}
          {mode === 'latency' && <>
            <Line yAxisId="primary" type="monotone" dataKey="p50_latency_us" name="P50" stroke="#55bfd6" strokeWidth={1.5} dot={false} />
            <Line yAxisId="primary" type="monotone" dataKey="p95_latency_us" name="P95" stroke="#9d7ee8" strokeWidth={1.9} dot={false} />
          </>}
          {investigation && <>
            <Line yAxisId="primary" type="monotone" dataKey="no_response" stroke="#e8ad5a" strokeWidth={1.4} dot={false} />
            <Line yAxisId="primary" type="monotone" dataKey="nxdomain" stroke="#8293a5" strokeWidth={1.1} dot={false} />
            <Line yAxisId="primary" type="monotone" dataKey="servfail" stroke="#e26d76" strokeWidth={1.4} dot={false} />
            <Line yAxisId="latency" type="monotone" dataKey="avg_latency_us" stroke="#9d7ee8" strokeWidth={1.2} strokeDasharray="4 3" dot={false} />
          </>}
        </AreaChart>
      </ResponsiveContainer>
    </div>
  )
}
