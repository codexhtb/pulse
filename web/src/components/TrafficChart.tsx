import { Area, AreaChart, CartesianGrid, Line, ResponsiveContainer, Tooltip, XAxis, YAxis, type ActiveDotProps, type MouseHandlerDataParam } from 'recharts'
import { formatCompact, formatLatency, formatPercent } from '../format'
import type { TrafficPoint } from '../types'
import { useI18n } from '../i18n'
import { EmptyState } from './Common'

export type TrafficChartMode = 'traffic' | 'errors' | 'latency'
export type TrafficChartBucketClick = (point: TrafficPoint, series: string) => void

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

function drilldownDot(series: string, onBucketClick?: TrafficChartBucketClick, fallback: Partial<ActiveDotProps> | boolean = true) {
  if (!onBucketClick) return fallback
  return ({ cx, cy, payload, fill, stroke }: ActiveDotProps) => {
    if (cx === undefined || cy === undefined) return null
    return <circle
      cx={cx}
      cy={cy}
      r={5}
      fill={fill}
      stroke={stroke}
      strokeWidth={2}
      className="chart-drilldown-dot"
      data-series={series}
      onClick={(event) => {
        event.stopPropagation()
        onBucketClick(payload as TrafficPoint, series)
      }}
    />
  }
}

export function TrafficChart({
  data,
  compact = false,
  investigation = false,
  operational = false,
  mode = 'traffic',
  onBucketClick,
}: {
  data: TrafficPoint[]
  compact?: boolean
  investigation?: boolean
  operational?: boolean
  mode?: TrafficChartMode
  onBucketClick?: TrafficChartBucketClick
}) {
  const { locale, t } = useI18n()
  if (data.length === 0) return <EmptyState />

  const volumeKey = operational ? 'qps' : 'queries'
  const axisFormatter = mode === 'errors'
    ? (value: number) => `${value.toFixed(value >= 10 ? 0 : 1)}%`
    : mode === 'latency'
      ? (value: number) => formatLatency(value)
      : formatCompact
  const chartClick = (state: MouseHandlerDataParam) => {
    if (!onBucketClick || !state.isTooltipActive || state.activeTooltipIndex === undefined) return
    const index = Number(state.activeTooltipIndex)
    if (!Number.isInteger(index) || index < 0 || index >= data.length) return
    const fallbackSeries = mode === 'traffic' ? volumeKey : mode === 'latency' ? 'p95_latency_us' : ''
    const series = typeof state.activeDataKey === 'string' ? state.activeDataKey : fallbackSeries
    if (series) onBucketClick(data[index], series)
  }

  return (
    <div className={`${compact ? 'chart chart-compact' : 'chart'}${onBucketClick ? ' chart-clickable' : ''}`}>
      <ResponsiveContainer width="100%" height="100%">
        <AreaChart data={data} margin={{ top: 8, right: 12, bottom: 0, left: -8 }} cursor={onBucketClick ? 'pointer' : undefined} onClick={chartClick}>
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
                  {onBucketClick && <em>{t('Open events for this interval')}</em>}
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
            activeDot={drilldownDot(volumeKey, onBucketClick, { r: 4, fill: '#78d8ed', stroke: '#0b1117', strokeWidth: 2 })}
          />}
          {mode === 'errors' && <>
            <Line yAxisId="primary" type="monotone" dataKey="nxdomain_pct" name="NXDOMAIN" stroke="#e8ad5a" strokeWidth={1.8} dot={false} activeDot={drilldownDot('nxdomain_pct', onBucketClick)} />
            <Line yAxisId="primary" type="monotone" dataKey="servfail_pct" name="SERVFAIL" stroke="#e26d76" strokeWidth={1.8} dot={false} activeDot={drilldownDot('servfail_pct', onBucketClick)} />
          </>}
          {mode === 'latency' && <>
            <Line yAxisId="primary" type="monotone" dataKey="p50_latency_us" name="P50" stroke="#55bfd6" strokeWidth={1.5} dot={false} activeDot={drilldownDot('p50_latency_us', onBucketClick)} />
            <Line yAxisId="primary" type="monotone" dataKey="p95_latency_us" name="P95" stroke="#9d7ee8" strokeWidth={1.9} dot={false} activeDot={drilldownDot('p95_latency_us', onBucketClick)} />
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
